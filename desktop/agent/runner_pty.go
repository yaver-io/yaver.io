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
		// invisible; ?chrome=1 keeps it as a thin session indicator. Set it
		// immediately (best-effort, before the first frame) so there's no
		// status-bar flash on short-lived sessions, then re-assert briefly in
		// the background to win any race with tmux's own initial render and
		// to cover -A reattach.
		status := "off"
		if chrome {
			status = "on"
		}
		_ = exec.Command("tmux", "set-option", "-t", tmuxSession, "status", status).Run()
		go func(sess, want string) {
			for i := 0; i < 8; i++ {
				time.Sleep(150 * time.Millisecond)
				if exec.Command("tmux", "has-session", "-t", sess).Run() == nil {
					_ = exec.Command("tmux", "set-option", "-t", sess, "status", want).Run()
				}
			}
		}(tmuxSession, status)
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

// RunnerPTYSession describes a live runner PTY wrap on this box (a tmux
// session started by /ws/runner). Used by `yaver attach --machine=<dev>` to
// discover + reattach.
type RunnerPTYSession struct {
	Name     string `json:"name"`
	Runner   string `json:"runner"`
	Command  string `json:"command,omitempty"`
	Created  int64  `json:"created,omitempty"`
	Attached bool   `json:"attached"`
}

// handleRunnerSessions: GET /runner/sessions — owner-only list of live runner
// PTY tmux sessions on this box, so the CLI can reattach or present a picker.
// A session counts as a runner wrap when its tmux start-command's first token
// is a known runner binary (survives custom --yaver-session names) or the
// session name is the yaver-<runner> default.
func (s *HTTPServer) handleRunnerSessions(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "runner sessions are owner-only"})
		return
	}
	sessions := listRunnerPTYSessions()
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "sessions": sessions})
}

func listRunnerPTYSessions() []RunnerPTYSession {
	out := []RunnerPTYSession{}
	if _, err := exec.LookPath("tmux"); err != nil {
		return out
	}
	raw, err := exec.Command("tmux", "list-sessions", "-F",
		"#{session_name}\t#{session_created}\t#{session_attached}").CombinedOutput()
	if err != nil {
		return out // no server / no sessions
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 1 {
			continue
		}
		name := parts[0]
		created := int64(0)
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%d", &created)
		}
		attached := len(parts) > 2 && strings.TrimSpace(parts[2]) == "1"

		startCmd, curCmd := "", ""
		if pc, perr := exec.Command("tmux", "list-panes", "-t", name, "-F", "#{pane_start_command}\x1f#{pane_current_command}").CombinedOutput(); perr == nil {
			first := strings.Split(strings.TrimSpace(string(pc)), "\n")[0]
			cols := strings.SplitN(first, "\x1f", 2)
			startCmd = strings.TrimSpace(cols[0])
			if len(cols) > 1 {
				curCmd = strings.TrimSpace(cols[1])
			}
		}
		runner := runnerFromStartCommand(startCmd)
		if runner == "" {
			runner = runnerFromStartCommand(curCmd)
		}
		if runner == "" && strings.HasPrefix(name, "yaver-") {
			runner = normalizeRunnerID(strings.TrimPrefix(name, "yaver-"))
			if !IsSupportedRunner(runner) {
				runner = ""
			}
		}
		if runner == "" {
			continue // not a runner wrap
		}
		out = append(out, RunnerPTYSession{
			Name: name, Runner: runner, Command: startCmd,
			Created: created, Attached: attached,
		})
	}
	return out
}

// runnerFromStartCommand returns the runner id when the first token of a tmux
// pane command names a known runner binary, else "". Handles shell-quoted
// tokens (tmux reports our `'codex'` verbatim) and process-name suffixes
// (pane_current_command shows the exec'd `codex-aarch64-a`).
func runnerFromStartCommand(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	bin := strings.Trim(fields[0], "'\"")
	if idx := strings.LastIndex(bin, "/"); idx >= 0 {
		bin = bin[idx+1:]
	}
	switch {
	case bin == "claude" || strings.HasPrefix(bin, "claude-"):
		return "claude"
	case bin == "codex" || strings.HasPrefix(bin, "codex-"):
		return "codex"
	case bin == "opencode" || strings.HasPrefix(bin, "opencode-"):
		return "opencode"
	}
	return ""
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
