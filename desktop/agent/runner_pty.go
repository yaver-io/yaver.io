package main

// runner_pty.go — WS /ws/runner: an exact-TUI PTY wrap of a coding runner
// (claude / codex / opencode / glm) on THIS machine, driven from another
// surface (the `yaver claude|codex|opencode --machine=<device>` CLI, or any
// client that speaks the /ws/terminal frame protocol).
//
// Contract: yaver adds NO chrome. The runner's own interactive TUI owns the
// byte stream; yaver contributes device resolution, the tunnel, and — when
// tmux is present — persistence: the runner is spawned inside
// `tmux new-session -A -s <name>`, so a dropped connection (laptop lid,
// cell handoff) leaves the runner alive and a reconnect lands back in the
// same TUI. The tmux status bar is hidden by default (zero chrome) and
// enabled with ?chrome=1.
//
// Owner-only: spawning a runner with caller-controlled argv is arbitrary
// code execution, so guests are rejected outright (same posture as
// runner_agent_session_http.go). The endpoint is intentionally NOT in
// hostShareAllowedPrefixes.
//
// Frame protocol (identical to /ws/terminal, terminal_session.go):
//   - binary frames: stdin bytes → pty / pty bytes → stdout
//   - text frames in: {"resize":{"cols":N,"rows":M}} | {"type":"terminate_session"}
//   - text frames out: {"type":"terminal_session",...} then {"type":"runner_pty",...}

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func (s *HTTPServer) handleRunnerPTYWS(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		http.Error(w, "runner PTY is owner-only", http.StatusForbidden)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	q := r.URL.Query()

	// Resume a still-live PTY session by id (same semantics as /ws/terminal).
	if sid := strings.TrimSpace(q.Get("session_id")); sid != "" {
		if existing, ok := s.terminalSessionByID(sid); ok {
			s.pumpRunnerPTY(conn, existing, true, nil)
			return
		}
	}

	runnerID := normalizeRunnerID(q.Get("runner"))
	if runnerID == "" || !IsSupportedRunner(runnerID) {
		runnerPTYFail(conn, fmt.Sprintf("unsupported runner %q — expected one of: %s",
			q.Get("runner"), strings.Join(supportedRunnerIDs, ", ")))
		return
	}
	rc := builtinRunners[runnerID]
	if err := CheckRunnerBinary(rc.Command); err != nil {
		runnerPTYFail(conn, fmt.Sprintf("%s (%s) is not installed on this machine — run runner_auth_setup or `yaver runner-auth setup %s` first",
			rc.Name, rc.Command, runnerID))
		return
	}

	args := q["arg"] // verbatim passthrough argv — yaver does not model runner flags
	cwd := strings.TrimSpace(q.Get("cwd"))
	if cwd == "" && s.taskMgr != nil {
		cwd = s.taskMgr.workDir
	}
	if cwd != "" {
		if info, serr := os.Stat(cwd); serr != nil || !info.IsDir() {
			cwd = ""
		}
	}
	chrome := q.Get("chrome") == "1"

	var cmd *exec.Cmd
	tmuxSession := ""
	if _, terr := exec.LookPath("tmux"); terr == nil {
		tmuxSession = sanitizeTmuxSessionName(q.Get("name"))
		if tmuxSession == "" {
			tmuxSession = "yaver-" + runnerID
		}
		// -A: attach if the session already exists. The runner survives
		// disconnects (and agent restarts — the tmux server is independent);
		// a fresh WS with the same name lands back in the same TUI. tmux's
		// shell-command parameter is a single sh string, so quote strictly.
		inner := shellJoin(append([]string{rc.Command}, args...))
		tmuxArgs := []string{"new-session", "-A", "-s", tmuxSession}
		if cwd != "" {
			tmuxArgs = append(tmuxArgs, "-c", cwd)
		}
		tmuxArgs = append(tmuxArgs, inner)
		cmd = exec.Command("tmux", tmuxArgs...)
	} else {
		cmd = exec.Command(rc.Command, args...)
		if cwd != "" {
			cmd.Dir = cwd
		}
	}
	env := append(os.Environ(), "TERM=xterm-256color")
	// GLM rides the claude binary against z.ai; provider env is what selects
	// it. No-op for runners without a configured provider override.
	env = append(env, runnerProviderEnv(runnerID)...)
	cmd.Env = env
	cmd = sandboxWrapCmd(cmd)

	ts, err := s.newTerminalSession(cmd, nil, "", "", cwd)
	if err != nil {
		runnerPTYFail(conn, "pty start failed: "+err.Error())
		return
	}

	if tmuxSession != "" {
		// Zero-chrome default: hide the tmux status bar so the wrap is
		// invisible; ?chrome=1 keeps it as a thin session indicator.
		// Applied after the session exists; idempotent on -A reattach.
		go func(sess string, on bool) {
			status := "off"
			if on {
				status = "on"
			}
			for i := 0; i < 10; i++ {
				if exec.Command("tmux", "has-session", "-t", sess).Run() == nil {
					_ = exec.Command("tmux", "set-option", "-t", sess, "status", status).Run()
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
		}(tmuxSession, chrome)
	}

	meta := map[string]any{
		"type":        "runner_pty",
		"runner":      runnerID,
		"tmuxSession": tmuxSession,
		"cwd":         cwd,
	}
	s.pumpRunnerPTY(conn, ts, false, meta)
}

// pumpRunnerPTY attaches the WS to a terminal session and pumps frames until
// either side closes. Mirrors handleTerminalWS's loop minus the host-share /
// sudo-prompt machinery, which has no place inside a runner TUI.
func (s *HTTPServer) pumpRunnerPTY(conn *websocket.Conn, ts *terminalSession, resumed bool, meta map[string]any) {
	if err := ts.attach(conn, resumed); err != nil {
		_ = conn.Close()
		return
	}
	defer ts.detach(conn)

	if meta != nil {
		if frame, merr := json.Marshal(meta); merr == nil {
			_ = ts.writeWS(websocket.TextMessage, frame)
		}
	}

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.TextMessage {
			var ctl struct {
				Resize *struct {
					Cols uint16 `json:"cols"`
					Rows uint16 `json:"rows"`
				} `json:"resize"`
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &ctl) == nil {
				if ctl.Resize != nil && (ctl.Resize.Cols > 0 || ctl.Resize.Rows > 0) {
					_ = ts.resize(ctl.Resize.Cols, ctl.Resize.Rows)
					continue
				}
				if ctl.Type == "terminate_session" {
					ts.close(true)
					return
				}
			}
			// Unknown text frames are control noise — never inject into the TUI.
			continue
		}
		_ = ts.writeInput(data)
	}
}

func runnerPTYFail(conn *websocket.Conn, msg string) {
	frame, _ := json.Marshal(map[string]any{"type": "runner_pty_error", "error": msg})
	_ = conn.WriteMessage(websocket.TextMessage, frame)
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = conn.Close()
}

// sanitizeTmuxSessionName keeps caller-supplied tmux session names shell- and
// tmux-safe: [A-Za-z0-9_-] only, max 48 chars, empty on anything else.
func sanitizeTmuxSessionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			return ""
		}
		if b.Len() >= 48 {
			break
		}
	}
	return b.String()
}
