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
	"log"
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

	// Claude Code runs its first-run wizard — theme picker, then "Select login
	// method", then a browser OAuth URL — whenever ~/.claude.json lacks
	// hasCompletedOnboarding, no matter how valid the credential is. A box
	// provisioned by Yaver has never run `claude` by hand, so without this
	// every remote TUI opens on a sign-in screen the box cannot complete.
	// Gated on the runner actually being authenticated: on a signed-out box
	// that wizard is the only thing that can still help its owner.
	if runnerID == "claude" || runnerID == "glm" {
		if DetectRunnerRuntimeStatus(rc, cwd).AuthConfigured {
			if home, herr := os.UserHomeDir(); herr == nil && home != "" {
				if err := ensureClaudeOnboardingComplete(home); err != nil {
					log.Printf("[runner-pty] onboarding marker for %s: %v", runnerID, err)
				}
			}
		}
	}

	var cmd *exec.Cmd
	tmuxSession := ""
	if tmuxAvailable() {
		tmuxSession = sanitizeTmuxSessionName(q.Get("name"))
		if tmuxSession == "" {
			tmuxSession = "yaver-" + runnerID
		}
		// Persistence is the point of tmux here, but it also means a session
		// left sitting on the runner's own login screen outlives the auth
		// repair that fixed it — `new-session -A` would reattach the dead
		// screen forever. Retire such a session before starting.
		//   ?fresh=1   caller knows auth just changed (see runner_pty_cmd.go)
		//   otherwise  only when the pane is visibly parked on a login prompt
		//
		// The pane reports the BINARY it runs, so GLM (which drives the claude
		// binary against z.ai) must be matched as "claude", not "glm".
		if q.Get("fresh") == "1" || runnerPaneAwaitingLogin(tmuxSession, runnerFromStartCommand(rc.Command)) {
			killStaleRunnerTmuxSession(tmuxSession)
		}
		// -A: attach if the session already exists. The runner survives
		// disconnects (and agent restarts — the tmux server is independent);
		// a fresh WS with the same name lands back in the same TUI. tmux's
		// shell-command parameter is a single sh string, so quote strictly.
		//
		// cmd.Env below reaches the tmux CLIENT, not the pane. tmux runs pane
		// commands from the environment the SERVER was started with, and that
		// server outlives every agent restart — so a variable the agent only
		// learned about later (a z.ai key for GLM, IS_SANDBOX for root claude)
		// silently never arrives. Carry them on the command line instead,
		// where no tmux version can drop them.
		inner := shellJoin(append(append([]string{"env"}, runnerPTYPaneEnv(runnerID, q.Get("term"))...),
			append([]string{rc.Command}, args...)...))
		tmuxArgs := []string{"new-session", "-A", "-s", tmuxSession}
		if cwd != "" {
			tmuxArgs = append(tmuxArgs, "-c", cwd)
		}
		tmuxArgs = append(tmuxArgs, inner)
		cmd = exec.Command(tmuxCmdName(), tmuxArgs...)
	} else {
		cmd = exec.Command(rc.Command, args...)
		if cwd != "" {
			cmd.Dir = cwd
		}
	}
	// Still set the process env: it is what the non-tmux path uses, and it
	// seeds the tmux SERVER's environment on the very first new-session.
	cmd.Env = append(os.Environ(), runnerPTYPaneEnv(runnerID, q.Get("term"))...)
	cmd = sandboxWrapCmd(cmd)

	ts, err := s.newTerminalSession(cmd, nil, "", "", cwd)
	if err != nil {
		runnerPTYFail(conn, "pty start failed: "+err.Error())
		return
	}
	ts.runnerID = runnerID

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
		_ = exec.Command(tmuxCmdName(), "set-option", "-t", tmuxSession, "status", status).Run()
		go func(sess, want string) {
			for i := 0; i < 8; i++ {
				time.Sleep(150 * time.Millisecond)
				if exec.Command(tmuxCmdName(), "has-session", "-t", sess).Run() == nil {
					_ = exec.Command(tmuxCmdName(), "set-option", "-t", sess, "status", want).Run()
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

// runnerPTYPaneEnv is the environment the runner process itself must see —
// as `KEY=VALUE` assignments, so it can either be appended to cmd.Env or
// prefixed to a tmux pane command via `env`.
//
// Keep this the single source of truth for both paths. When it lived only in
// cmd.Env, everything here was silently lost on any box whose tmux server was
// already running: GLM fell back to Anthropic instead of z.ai, and root-owned
// claude lost the IS_SANDBOX=1 that lets it accept --dangerously-skip-permissions.
func runnerPTYPaneEnv(runnerID, requestedTerm string) []string {
	env := []string{"TERM=" + safePTYTermName(requestedTerm)}
	if os.Geteuid() == 0 && (runnerID == "claude" || runnerID == "glm") {
		env = append(env, "IS_SANDBOX=1")
	}
	// GLM rides the claude binary against z.ai; provider env is what selects
	// it. No-op for runners without a configured provider override.
	return append(env, runnerProviderEnv(runnerID)...)
}

// runnerLoginScreenMarkers are phrases a coding-agent TUI renders only while
// it is blocking on sign-in. Matched case-insensitively against the BOTTOM of
// the visible pane — where a prompt lives — so the same words appearing inside
// a scrolled-back transcript don't trip it.
//
// Every entry names the login UI specifically. Generic lines that a healthy
// session could plausibly print ("press enter to retry", "not logged in") are
// deliberately absent: killing a live session costs the user their context,
// and the login screen always carries one of these alongside them anyway.
var runnerLoginScreenMarkers = []string{
	"select login method",
	"claude account with subscription",
	"anthropic console account",
	"oauth error:",
	"paste code here if prompted",
	// The wizard's first step. A session parked here is just as dead as one
	// parked on the login step, and it precedes it.
	"choose the text style that looks best",
}

// runnerLoginScreenTailLines is how many trailing non-empty pane lines are
// searched for a marker.
const runnerLoginScreenTailLines = 8

// runnerPaneAwaitingLogin reports whether the named tmux session is stuck on
// the runner's login screen *even though the runner is signed in*. That
// contradiction is the tell: a session started before credentials landed keeps
// showing its login prompt forever, and `new-session -A` reattaches it, so the
// user re-runs the command and concludes the auth repair did nothing.
//
// Every clause is a guard against killing live work. The session must exist,
// its pane must still be running this runner, local auth must be VERIFIED good
// (so the login screen cannot be legitimate), and a marker must appear in the
// pane's trailing lines. Anything less and we leave the session alone.
func runnerPaneAwaitingLogin(session, runnerBinaryID string) bool {
	if strings.TrimSpace(session) == "" || runnerBinaryID == "" {
		return false
	}
	if exec.Command(tmuxCmdName(), "has-session", "-t", session).Run() != nil {
		return false
	}
	// If the runner really is signed out, a login screen is correct and the
	// user is about to be handed the headless flow anyway. Only a *verified*
	// sign-in makes the screen provably stale.
	auth := DetectRunnerRuntimeStatus(GetRunnerConfig(runnerBinaryID), "")
	if !auth.AuthConfigured || !auth.AuthVerified {
		return false
	}
	current, err := exec.Command(tmuxCmdName(), "list-panes", "-t", session, "-F", "#{pane_current_command}").Output()
	if err != nil || runnerFromStartCommand(strings.TrimSpace(string(current))) != runnerBinaryID {
		return false
	}
	pane, err := exec.Command(tmuxCmdName(), "capture-pane", "-p", "-t", session).Output()
	if err != nil {
		return false
	}
	for _, marker := range runnerLoginScreenMarkers {
		if strings.Contains(paneTailLower(string(pane), runnerLoginScreenTailLines), marker) {
			log.Printf("[runner-pty] %s session %q is parked on a login screen (%q) while auth is healthy — starting fresh",
				runnerBinaryID, session, marker)
			return true
		}
	}
	return false
}

// paneTailLower returns the last n non-empty lines of a captured pane,
// lowercased and rejoined.
func paneTailLower(pane string, n int) string {
	lines := make([]string, 0, n)
	all := strings.Split(pane, "\n")
	for i := len(all) - 1; i >= 0 && len(lines) < n; i-- {
		if line := strings.TrimSpace(all[i]); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.ToLower(strings.Join(lines, "\n"))
}

func killStaleRunnerTmuxSession(session string) {
	if strings.TrimSpace(session) == "" {
		return
	}
	if exec.Command(tmuxCmdName(), "has-session", "-t", session).Run() != nil {
		return
	}
	if out, err := exec.Command(tmuxCmdName(), "kill-session", "-t", session).CombinedOutput(); err != nil {
		log.Printf("[runner-pty] could not kill stale session %q: %v (%s)", session, err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("[runner-pty] killed stale session %q before restart", session)
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
	// Confirmed is true only when a runner process was actually OBSERVED for this
	// pane — its start command, its current command, or its process tree. It is
	// false when the session was classified by the weak signals: a `yaver-` name
	// prefix, or the pane text merely containing the word "codex"/"claude code".
	//
	// The difference is not cosmetic. A tmux session named `yaver-codex` whose
	// runner has since exited is a plain interactive SHELL — and a "prompt" typed
	// into a shell is not a prompt, it is a COMMAND, submitted with Enter. Sending
	// dictated text there executes it. Verified on a live box: a turn aimed at a
	// bare `yaver-codex` session ran the text and came back `zsh: command not
	// found`. Sessions survive their runner (systemd `KillMode=process` keeps the
	// tmux server up across restarts), so this is the normal end state, not a
	// corner case. `executeRunnerSessionTurn` refuses to type into an unconfirmed
	// session; listing them is fine, driving them is not.
	Confirmed bool `json:"confirmed"`
}

type RunnerSessionCloseResult struct {
	Name   string `json:"name"`
	Runner string `json:"runner,omitempty"`
	Error  string `json:"error,omitempty"`
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

func (s *HTTPServer) handleRunnerSessionsClose(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Yaver-Guest") == "true" {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "runner sessions are owner-only"})
		return
	}
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	results := closeTmuxSessions()
	failed := 0
	for _, r := range results {
		if r.Error != "" {
			failed++
		}
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":     failed == 0,
		"killed": results,
		"failed": failed,
	})
}

func closeTmuxSessions() []RunnerSessionCloseResult {
	if !tmuxAvailable() {
		return nil
	}
	raw, err := exec.Command(tmuxCmdName(), "list-sessions", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		return nil
	}
	var results []RunnerSessionCloseResult
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		res := RunnerSessionCloseResult{Name: name, Runner: detectRunnerFromTmuxSession(name)}
		if out, kerr := exec.Command(tmuxCmdName(), "kill-session", "-t", name).CombinedOutput(); kerr != nil {
			res.Error = strings.TrimSpace(string(out))
			if res.Error == "" {
				res.Error = kerr.Error()
			}
		}
		results = append(results, res)
	}
	return results
}

func listRunnerPTYSessions() []RunnerPTYSession {
	out := []RunnerPTYSession{}
	if !tmuxAvailable() {
		return out
	}
	raw, err := exec.Command(tmuxCmdName(), "list-sessions", "-F",
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
		if pc, perr := exec.Command(tmuxCmdName(), "list-panes", "-t", name, "-F", "#{pane_start_command}\x1f#{pane_current_command}").CombinedOutput(); perr == nil {
			first := strings.Split(strings.TrimSpace(string(pc)), "\n")[0]
			cols := strings.SplitN(first, "\x1f", 2)
			startCmd = strings.TrimSpace(cols[0])
			if len(cols) > 1 {
				curCmd = strings.TrimSpace(cols[1])
			}
		}
		// A runner process we can actually see: the pane's start command or its
		// current foreground command. These are the only two signals that prove a
		// runner is there to receive a prompt.
		confirmed := true
		runner := runnerFromStartCommand(startCmd)
		if runner == "" {
			runner = runnerFromStartCommand(curCmd)
		}
		// Below here we are GUESSING. A name prefix and a word in the scrollback
		// both survive the runner's death — the session stays, the agent is gone,
		// and what is left listening on the pane is a shell. Keep listing these
		// (they are still the user's sessions) but mark them unconfirmed so no
		// caller types into one. See RunnerPTYSession.Confirmed.
		if runner == "" && strings.HasPrefix(name, "yaver-") {
			runner = normalizeRunnerID(strings.TrimPrefix(name, "yaver-"))
			if !IsSupportedRunner(runner) {
				runner = ""
			}
			confirmed = false
		}
		if runner == "" {
			// Process-tree detection is real observation; the pane-text fallback
			// inside it is not. detectRunnerFromTmuxSession reports which it used.
			var byProcess bool
			runner, byProcess = detectRunnerFromTmuxSessionDetailed(name)
			confirmed = byProcess
		}
		if runner == "" {
			continue // not a runner wrap
		}
		out = append(out, RunnerPTYSession{
			Name: name, Runner: runner, Command: startCmd,
			Created: created, Attached: attached, Confirmed: confirmed,
		})
	}
	return out
}

func detectRunnerFromTmuxSession(name string) string {
	runner, _ := detectRunnerFromTmuxSessionDetailed(name)
	return runner
}

// detectRunnerFromTmuxSessionDetailed also reports whether the runner was found
// by OBSERVING a process (byProcess=true) or merely inferred from pane text
// (byProcess=false).
//
// The text fallback is a guess and must be labelled as one: it matches any pane
// whose scrollback happens to contain "codex" or "openai" — a shell where someone
// ran `git log`, or read this very file — and a caller that types a prompt into
// that shell has executed a command. See RunnerPTYSession.Confirmed.
func detectRunnerFromTmuxSessionDetailed(name string) (string, bool) {
	if pid := getPanePID(name); pid > 0 {
		if runner := normalizeRunnerID(detectAgentType(pid)); IsSupportedRunner(runner) {
			return runner, true
		}
	}
	preview := strings.ToLower(capturePanePreview(name, 80))
	switch {
	case strings.Contains(preview, "claude code") || strings.Contains(preview, "claude.ai") || strings.Contains(preview, "claude /login"):
		return "claude", false
	case strings.Contains(preview, "codex") || strings.Contains(preview, "openai"):
		return "codex", false
	case strings.Contains(preview, "opencode"):
		return "opencode", false
	}
	return "", false
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
