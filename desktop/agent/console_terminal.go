package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var sudoPromptPattern = regexp.MustCompile(`(?i)(?:\[(?:sudo|SUDO)\]\s*)?password(?:\s+for\s+[^:\r\n]+)?\s*:`)

// ── /ws/terminal frame protocol ──────────────────────────────────────────────
//
// ONE socket carries two unrelated things: the PTY byte stream and this
// session's control plane. The ONLY thing that separates them is the WebSocket
// opcode, so the split has to be absolute, in both directions:
//
//	server → client   BinaryMessage = PTY bytes, verbatim, and nothing else
//	                  TextMessage   = a JSON control object carrying "type"
//	client → server   BinaryMessage = stdin bytes, verbatim, and nothing else
//	                  TextMessage   = a JSON control object
//
// Both halves of that rule have been broken in production — by the SAME frame,
// in opposite directions:
//
//   - 2026-07-26, INBOUND: an unrecognised text frame was TYPED INTO THE PTY,
//     so the web terminal's own 30s keepalive landed inside a live Claude Code
//     TUI as `> /login {"ping":1,"t":1785067684755}` — the one command that
//     fixes an expired login, corrupted by Yaver's own heartbeat.
//   - 2026-07-27, OUTBOUND: the reply to that keepalive was written as a bare
//     `{"pong":1}` TextMessage on the data socket, and the web client painted
//     every text frame into xterm. An idle terminal accumulated
//     `{"pong":1}{"pong":1}` on the user's prompt line, rendered exactly as if
//     they had typed it.
//
// One defect wearing two hats: a control message that a reader cannot tell
// apart from data. Both fixes below are shape fixes, not string filters — a
// client must be able to decide by FRAME TYPE, without inspecting content,
// because any content test is a test the user can type.

// terminalControlFrame builds a server→client control frame. Every
// server→client TextMessage on this socket goes through it, so "text frame ⇒
// never paint" is a rule a client can apply blind.
//
// "type" is mandatory and not optional dressing: every control-frame
// classifier in this repo keys on it (mobile's isTerminalMetaFrame requires
// `type` or `sessionId`; the support console switches on `msg.type`). A frame
// without one is, to all of them, indistinguishable from output — which is
// precisely how the anonymous {"pong":1} ended up on a user's prompt line.
func terminalControlFrame(kind string, fields map[string]any) []byte {
	payload := map[string]any{"type": kind}
	for k, v := range fields {
		if k == "type" {
			continue
		}
		payload[k] = v
	}
	frame, err := json.Marshal(payload)
	if err != nil {
		// A control frame we failed to encode is still a control frame. Emit
		// the minimal well-formed one rather than falling back to a bare
		// string, which a client would paint.
		return []byte(`{"type":"error","error":"control frame encode failed"}`)
	}
	return frame
}

// terminalErrorFrame reports a terminal-level failure (PTY start, session
// mismatch, sudo stdin write) as structured control rather than as loose prose
// on the data channel.
func terminalErrorFrame(msg string) []byte {
	return terminalControlFrame("error", map[string]any{"error": msg})
}

// terminalPongFrame answers a client keepalive.
//
// COMPATIBILITY — both fields are load-bearing, for different clients:
//
//   - `"pong":1` is KEPT because clients already in the field match that exact
//     key: a phone on an older TestFlight build, a browser tab open since
//     before this deploy. Those clients force-close after 60s with no inbound
//     data of ANY kind, so the reply is what keeps an IDLE-BUT-HEALTHY
//     terminal alive. Dropping it would trade a cosmetic bug for a
//     disconnect, which is the worse of the two.
//   - `"type":"pong"` is ADDED because every classifier above keys on `type`.
//     The old anonymous shape matched none of them, so every client fell
//     through to "this must be output". With a type present, a client that
//     already knows the protocol drops it for free — no string matching on
//     "pong" anywhere, which matters because a user can print that word.
//
// It is sent ONLY in reply to a client that sent a JSON ping — i.e. only to a
// client that has identified itself as speaking this dialect. Nothing is ever
// pushed spontaneously, so a client that never pings never sees a pong, and no
// future client is forced to learn about them.
func terminalPongFrame() []byte {
	return []byte(`{"type":"pong","pong":1}`)
}

// terminalTextFrameIsControl reports whether an inbound client text frame
// belongs to the control plane — the INBOUND half of the rule above.
//
// Historically any text frame the switch in handleTerminalWS did not recognise
// was written into the PTY as if the user had typed it; that is how the
// keepalive got appended to `/login`. runner_pty.go had already drawn the line
// ("Unknown text frames are control noise — never inject into the TUI"); this
// twin had not. Same two-implementations drift the cross-surface rule exists
// to catch.
//
// A JSON object is control and is NEVER typed into the PTY, recognised or not.
// Anything else is legacy stdin: an older browser terminal used to send the
// operator's typed line as a TEXT frame, and an already-open tab has to keep
// working. That legacy path is why
// this is a shape test rather than "text frames are control, full stop".
//
// This is also why a user typing `{"type":"ping"}` at their shell prompt is
// delivered untouched: a real terminal client hands each keystroke to the
// socket as BINARY bytes, never as a text frame, so it never reaches this
// function at all.
func terminalTextFrameIsControl(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var probe map[string]json.RawMessage
	return json.Unmarshal(trimmed, &probe) == nil
}

// terminalLaunchCommand builds the shell line WS /ws/terminal types into a
// fresh PTY when the caller passed ?launch=<runner>. This is the path the web
// dashboard's "click Codex on a device card" uses.
//
// The yolo flags match applyRunnerYoloDefaults (runner_pty_cmd.go): yolo is the
// default for a Yaver-launched runner, per the project's runner contract.
//
// IS_SANDBOX=1 IS PART OF THE FLAG, NOT A NICETY. Measured on a live root-owned
// Linux box, 2026-07-27:
//
//	# claude --dangerously-skip-permissions -p 'say hi'
//	--dangerously-skip-permissions cannot be used with root/sudo privileges …
//	# IS_SANDBOX=1 claude --dangerously-skip-permissions -p 'say hi'
//	Hi! 👋 How can I help you today?
//
// The /ws/runner path has carried this since runnerPTYPaneEnv (runner_pty.go),
// whose own comment records losing it once already; /ws/terminal never had it.
// So on any root box — every default Hetzner/VPS install — clicking Claude in
// the web dashboard opened a terminal that instantly refused, and the refusal
// blamed the user's privileges rather than our missing env. Same drift, second
// surface: the fix belongs in both or it is not landed.
func terminalLaunchCommand(runner string) string {
	return terminalLaunchCommandFor(runner, os.Geteuid())
}

// terminalLaunchCommandFor takes euid explicitly so the root behaviour is
// testable without running the suite as root.
func terminalLaunchCommandFor(runner string, euid int) string {
	var session, argv string
	switch strings.ToLower(strings.TrimSpace(runner)) {
	case "claude", "claude-code":
		session, argv = "yaver-claude", "claude --dangerously-skip-permissions"
	case "glm":
		// Rides the claude binary against z.ai; same root restriction applies.
		session, argv = "yaver-glm", "claude --dangerously-skip-permissions"
	case "codex":
		session, argv = "yaver-codex", "codex --dangerously-bypass-approvals-and-sandbox"
	case "opencode":
		session, argv = "yaver-opencode", "opencode --auto"
	default:
		return ""
	}
	argv = runnerYoloEnvPrefix(runner, euid) + argv
	// argv never contains a single quote, so the tmux quoting below is safe.
	return "if command -v tmux >/dev/null 2>&1; then exec tmux new-session -A -s " + session +
		" '" + argv + "'; else exec " + argv + "; fi"
}

// runnerYoloEnvPrefix returns the `VAR=value ` assignments a runner's
// bypass-permissions flag needs to be ACCEPTED on this machine, or "" when the
// flag works bare. Kept beside the command builder so the two can never drift.
func runnerYoloEnvPrefix(runner string, euid int) string {
	if euid != 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(runner)) {
	case "claude", "claude-code", "glm":
		return "IS_SANDBOX=1 "
	}
	return ""
}

// handleTerminalWS: WS /ws/terminal — starts or resumes a PTY-backed shell.
// See the frame-protocol block at the top of this file: binary is the PTY byte
// stream in both directions, text is the control plane in both directions, and
// neither is ever allowed to leak into the other.
func (s *HTTPServer) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	touchSession := func(bool) {}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	terminalSessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	var ts *terminalSession
	resumed := false

	if terminalSessionID != "" {
		if existing, ok := s.terminalSessionByID(terminalSessionID); ok {
			ts = existing
			resumed = true
		}
	}

	if ts == nil {
		shell := r.URL.Query().Get("shell")
		if shell == "" {
			shell = os.Getenv("SHELL")
		}
		if shell == "" {
			shell = "/bin/bash"
		}

		cwd := r.URL.Query().Get("cwd")

		if ts == nil {
			cmd := exec.Command(shell)
			cmd.Env = append(os.Environ(), "TERM="+safePTYTermName(r.URL.Query().Get("term")))
			if cwd != "" {
				cmd.Dir = cwd
			}
			// On Android the agent runs native but the shell must execute inside
			// the proot Alpine rootfs so claude/codex/node resolve. No-op on every
			// other platform (gated on YAVER_ANDROID_* env). See sandbox_proot.go.
			cmd = sandboxWrapCmd(cmd)
			ts, err = s.newTerminalSession(cmd, touchSession, "")
			if err != nil {
				_ = conn.WriteMessage(websocket.TextMessage, terminalErrorFrame("pty start failed: "+err.Error()))
				_ = conn.Close()
				return
			}
			touchSession(true)
		}
	}

	if err := ts.attach(conn, resumed); err != nil {
		_ = conn.Close()
		return
	}
	if launchCommand := terminalLaunchCommand(r.URL.Query().Get("launch")); launchCommand != "" && !resumed {
		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = ts.writeInput([]byte(launchCommand + "\n"))
		}()
	} else if !resumed {
		profile := &SSHProfile{
			Shell:       r.URL.Query().Get("profile_shell"),
			Tmux:        strings.TrimSpace(r.URL.Query().Get("profile_tmux")) != "",
			TmuxSession: r.URL.Query().Get("profile_tmux"),
		}
		if profileCommand := sshProfileLaunchCommand(profile); profileCommand != "" {
			go func() {
				time.Sleep(150 * time.Millisecond)
				_ = ts.writeInput([]byte(profileCommand + "\n"))
			}()
		}
	}
	defer ts.detach(conn)

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage {
			touchSession(false)
			// A JSON object is CONTROL, recognised or not — see
			// terminalTextFrameIsControl. Anything else is legacy stdin from
			// the support console, which still types raw lines as text.
			isControl := terminalTextFrameIsControl(data)
			if isControl {
				var ctl struct {
					Resize *struct {
						Cols uint16 `json:"cols"`
						Rows uint16 `json:"rows"`
					} `json:"resize"`
					Type     string `json:"type"`
					Password string `json:"password"`
					// Ping is the web terminal's 30s keepalive
					// ({"ping":1,"t":<ms>}).
					Ping *json.RawMessage `json:"ping"`
				}
				if json.Unmarshal(data, &ctl) == nil {
					if ctl.Resize != nil && (ctl.Resize.Cols > 0 || ctl.Resize.Rows > 0) {
						_ = ts.resize(ctl.Resize.Cols, ctl.Resize.Rows)
						continue
					}
					if ctl.Type == "sudo_response" {
						if err := ts.writeInput([]byte(ctl.Password + "\n")); err != nil {
							_ = ts.writeWS(websocket.TextMessage, terminalErrorFrame("sudo stdin write failed: "+err.Error()))
						}
						ctl.Password = ""
						continue
					}
					if ctl.Type == "cancel_sudo" {
						_ = ts.writeInput([]byte{3})
						continue
					}
					if ctl.Type == "terminate_session" {
						ts.close(true)
						return
					}
					if ctl.Ping != nil || ctl.Type == "ping" {
						// Answer it, and answer it as CONTROL. The client
						// force-closes after 60s with no inbound data of ANY
						// kind, so an unanswered keepalive makes an
						// IDLE-BUT-HEALTHY terminal disconnect itself — the
						// reply is load-bearing and must not simply be
						// dropped. What changed on 2026-07-27 is its SHAPE:
						// see terminalPongFrame for why it now carries a type
						// and why the legacy field stays.
						_ = ts.writeWS(websocket.TextMessage, terminalPongFrame())
						continue
					}
				}
				// Unrecognised control frame — a keepalive variant, a field a
				// newer client added, a frame from a future protocol version.
				// It is NOT stdin and must never be typed into the PTY: that
				// fallback is exactly what put `{"ping":1,…}` on the end of a
				// user's `/login`. Drop it, like runner_pty.go already does.
				continue
			}
			_ = ts.writeInput(data)
			continue
		}
		touchSession(false)
		_ = ts.writeInput(data)
	}
}
