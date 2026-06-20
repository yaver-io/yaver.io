package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// runnerAuthDebugEnabled returns true when YAVER_RUNNER_AUTH_DEBUG is
// set to a truthy value. Gates verbose per-line logging of subprocess
// stdout/stderr — useful while debugging URL parser regressions, noisy
// in steady-state production. Always log the high-level transitions
// (spawn, terminal, errors) regardless of this flag.
func runnerAuthDebugEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_RUNNER_AUTH_DEBUG"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

type runnerBrowserAuthStartRequest struct {
	Runner string `json:"runner"`
}

type runnerBrowserAuthSession struct {
	ID             string `json:"id"`
	Runner         string `json:"runner"`
	TenantUserID   string `json:"tenantUserId,omitempty"`
	Method         string `json:"method"`
	Status         string `json:"status"`
	OpenURL        string `json:"openUrl,omitempty"`
	Code           string `json:"code,omitempty"`
	Detail         string `json:"detail,omitempty"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Error          string `json:"error,omitempty"`
	StartedAt      int64  `json:"startedAt"`
	UpdatedAt      int64  `json:"updatedAt"`
	CompletedAt    int64  `json:"completedAt,omitempty"`
}

type runnerBrowserAuthSessionState struct {
	mu sync.Mutex
	runnerBrowserAuthSession
	cmd    *exec.Cmd
	cancel context.CancelFunc
	// stdin is the spawned CLI's stdin pipe, captured at start time so
	// the dashboard / mobile / Yaver mobile app can forward a pasted
	// authentication code (`claude auth login --console` flow shows a
	// URL, the user signs in on platform.claude.com, gets a long token,
	// pastes it back). The token is forwarded once and discarded — it
	// never lands in Convex, the bus, or any persistent log. Closing
	// the writer signals EOF so a CLI that wraps a single-shot read
	// terminates cleanly.
	stdin io.WriteCloser
}

var (
	runnerBrowserAuthSessions sync.Map
	urlPattern                = regexp.MustCompile(`https://[^\s]+`)
	codexCodePattern          = regexp.MustCompile(`\b[A-Z0-9]{4,}-[A-Z0-9]{4,}\b`)
	ansiPattern               = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

func normalizeBrowserAuthLine(line string) string {
	line = strings.ReplaceAll(line, "\r", "")
	line = ansiPattern.ReplaceAllString(line, "")
	line = strings.TrimSpace(line)
	if strings.Contains(line, "\x1b]8;") {
		line = strings.ReplaceAll(line, "\x1b]8;;", "")
		line = strings.ReplaceAll(line, "\x1b]8;", "")
		line = strings.ReplaceAll(line, "\a", "")
	}
	return strings.TrimSpace(line)
}

func newRunnerBrowserAuthSession(runner string, tr tenantRuntime) *runnerBrowserAuthSessionState {
	now := time.Now().UnixMilli()
	idPrefix := runner
	if tr.Enabled {
		idPrefix = runner + "-" + betaSanitizeRef(tr.UserID)
	}
	id := fmt.Sprintf("%s-%d", idPrefix, now)
	return &runnerBrowserAuthSessionState{
		runnerBrowserAuthSession: runnerBrowserAuthSession{
			ID:           id,
			Runner:       runner,
			TenantUserID: tr.UserID,
			Status:       "starting",
			StartedAt:    now,
			UpdatedAt:    now,
		},
	}
}

func (s *runnerBrowserAuthSessionState) snapshot() runnerBrowserAuthSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.runnerBrowserAuthSession
	refreshRunnerBrowserAuthSnapshot(&out)
	return out
}

func refreshRunnerBrowserAuthSnapshot(out *runnerBrowserAuthSession) {
	rows, err := collectRunnerAuthStatusRows()
	if err != nil {
		return
	}
	row := runnerStatusRowFor(rows, out.Runner)
	out.AuthConfigured = row.AuthConfigured
	out.AuthSource = row.AuthSource
	if out.Status == "completed" && strings.TrimSpace(out.Detail) == "" {
		if row.AuthConfigured && row.AuthSource != "" {
			out.Detail = "Authenticated via " + row.AuthSource
		} else {
			out.Detail = "Authentication completed on the remote machine."
		}
	}
}

func (s *runnerBrowserAuthSessionState) update(fn func(*runnerBrowserAuthSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.runnerBrowserAuthSession)
	s.runnerBrowserAuthSession.UpdatedAt = time.Now().UnixMilli()
}

func runnerBrowserAuthCommand(runner string, tr tenantRuntime) (method string, cmd *exec.Cmd, err error) {
	switch normalizeRunnerAuthName(runner) {
	case "codex":
		bin := resolveRunnerBinary("codex")
		if bin == "" {
			return "", nil, fmt.Errorf("codex CLI not found on this machine (looked in PATH, ~/.npm-global/bin, ~/.local/bin, ~/.bun/bin, /opt/homebrew/bin, /usr/local/bin, and the user login shell). Run `npm i -g @openai/codex` and try again.")
		}
		if tr.Enabled {
			cmd, err = tr.command(context.Background(), tr.Home, bin, []string{"login", "--device-auth"}, append(tr.authEnv(), "CI=1", "NO_COLOR=1", "TERM=dumb"))
			if err != nil {
				return "", nil, err
			}
		} else {
			cmd = exec.Command(bin, "login", "--device-auth")
			cmd.Env = append(cmd.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb")
		}
		return "device-auth", cmd, nil
	case "claude":
		bin := resolveRunnerBinary("claude")
		if bin == "" {
			return "", nil, fmt.Errorf("claude CLI not found on this machine (looked in PATH, ~/.npm-global/bin, ~/.local/bin, ~/.bun/bin, /opt/homebrew/bin, /usr/local/bin, and the user login shell). Run `npm i -g @anthropic-ai/claude-code` and try again.")
		}
		// Use --claudeai (Claude Max / Pro subscription) — the default
		// path. The previous `--console` flag was wrong: it's the
		// "Anthropic Console (API usage billing)" flow, which mints a
		// token that 401s against subscription endpoints. Yaver's
		// no-API-keys constraint means we always want the subscription
		// OAuth, never per-token billing.
		//
		// Pass --claudeai explicitly (not via default) so a future
		// claude release can't silently switch defaults. CLAUDE_CONFIG_DIR
		// nudges claude to also write `~/.claude/.credentials.json` even
		// on macOS — the daemon's spawned-by-launchd / spawned-from-SSH
		// processes can read the file regardless of the GUI security
		// session that gates Keychain access; without this, OAuth saves
		// to Keychain only and subsequent tasks still 401 because the
		// daemon's Keychain access is broken.
		if tr.Enabled {
			cmd, err = tr.command(context.Background(), tr.Home, bin, []string{"auth", "login", "--claudeai"}, append(tr.authEnv(), "CI=1", "NO_COLOR=1", "TERM=dumb"))
			if err != nil {
				return "", nil, err
			}
		} else {
			cmd = exec.Command(bin, "auth", "login", "--claudeai")
			cmd.Env = append(cmd.Environ(),
				"CI=1", "NO_COLOR=1", "TERM=dumb",
				"CLAUDE_CONFIG_DIR="+filepath.Join(os.Getenv("HOME"), ".claude"),
			)
		}
		return "oauth", cmd, nil
	default:
		return "", nil, fmt.Errorf("unsupported runner %q (want claude or codex)", runner)
	}
}

func scanRunnerBrowserAuthOutput(sess *runnerBrowserAuthSessionState, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 512*1024)
	debug := runnerAuthDebugEnabled()
	for scanner.Scan() {
		raw := scanner.Text()
		line := normalizeBrowserAuthLine(raw)
		if debug {
			log.Printf("[runner-auth-browser] %s line=%q raw=%q", sess.Runner, line, raw)
		}
		if line == "" {
			continue
		}
		sess.update(func(state *runnerBrowserAuthSession) {
			state.Detail = line
			if state.OpenURL == "" {
				if url := urlPattern.FindString(line); url != "" {
					state.OpenURL = strings.TrimRight(url, ".)")
					if state.Status == "starting" {
						state.Status = "awaiting_browser"
					}
					// Always log URL capture — it's the moment the mobile
					// pane stops spinning, worth a single line in journalctl.
					log.Printf("[runner-auth-browser] %s captured openUrl=%s", state.Runner, state.OpenURL)
				}
			}
			if state.Runner == "codex" && state.Code == "" {
				if code := codexCodePattern.FindString(line); code != "" {
					state.Code = code
					state.Status = "awaiting_browser"
					log.Printf("[runner-auth-browser] codex captured code=%s", code)
				}
			}
		})
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[runner-auth-browser] %s scanner error: %v", sess.Runner, err)
	} else if debug {
		log.Printf("[runner-auth-browser] %s reader closed (EOF)", sess.Runner)
	}
}

// cancelStaleRunnerBrowserAuthSessions terminates any non-terminal
// browser-auth session for the given runner. Called before spawning a
// fresh session so we never keep two `claude auth login` processes alive
// at once (each holds its own PKCE verifier — a code pasted into the new
// session would otherwise risk being exchanged against the old verifier).
func cancelStaleRunnerBrowserAuthSessions(runner string, tenantUserID string) {
	runner = normalizeRunnerAuthName(runner)
	tenantUserID = strings.TrimSpace(tenantUserID)
	runnerBrowserAuthSessions.Range(func(_, v any) bool {
		st, ok := v.(*runnerBrowserAuthSessionState)
		if !ok {
			return true
		}
		snap := st.snapshot()
		if normalizeRunnerAuthName(snap.Runner) != runner {
			return true
		}
		if strings.TrimSpace(snap.TenantUserID) != tenantUserID {
			return true
		}
		switch snap.Status {
		case "completed", "failed", "cancelled":
			return true
		}
		log.Printf("[runner-auth-browser] %s reaping stale session id=%s (status=%s) before new spawn", runner, snap.ID, snap.Status)
		if st.cancel != nil {
			st.cancel()
		}
		return true
	})
}

func startRunnerBrowserAuthSession(runner string, tr tenantRuntime, onTerminal func()) (*runnerBrowserAuthSessionState, error) {
	runner = normalizeRunnerAuthName(runner)
	// Reap any still-running auth process for the SAME runner before
	// spawning a new one. A prior session can be orphaned when the
	// user abandons the flow (closes the app, loses connectivity mid
	// browser-auth) — the spawned `claude auth login` then blocks on
	// stdin forever, holding the PKCE verifier for a URL the user has
	// long since discarded. Leaving it alive both leaks a process and
	// causes a code pasted into a FRESH session to be exchanged against
	// the wrong verifier. One live auth session per runner is correct.
	cancelStaleRunnerBrowserAuthSessions(runner, tr.UserID)
	if tr.Enabled {
		if err := tr.prepare(); err != nil {
			return nil, err
		}
	}
	sess := newRunnerBrowserAuthSession(runner, tr)
	method, cmd, err := runnerBrowserAuthCommand(runner, tr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Preserve the env that runnerBrowserAuthCommand set (notably
	// CLAUDE_CONFIG_DIR, which steers `claude` to write the credentials
	// FILE rather than the GUI Keychain — load-bearing for daemon-spawned
	// processes). Rebuilding via exec.CommandContext drops cmd.Env, so we
	// carry it across explicitly. Previously this re-derived from
	// os.Environ() and silently lost CLAUDE_CONFIG_DIR.
	origEnv := cmd.Env
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	if len(origEnv) > 0 {
		cmd.Env = origEnv
	} else {
		cmd.Env = append(cmd.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb")
	}
	sess.Method = method
	sess.cmd = cmd
	sess.cancel = cancel

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	sess.stdin = stdin
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s auth: %w", runner, err)
	}

	log.Printf("[runner-auth-browser] %s spawned: %s %v (id=%s)", runner, cmd.Path, cmd.Args[1:], sess.ID)
	go scanRunnerBrowserAuthOutput(sess, stdout)
	go scanRunnerBrowserAuthOutput(sess, stderr)
	recordRunnerBrowserAuthOperation(sess.snapshot())
	go func() {
		err := cmd.Wait()
		sess.update(func(state *runnerBrowserAuthSession) {
			state.CompletedAt = time.Now().UnixMilli()
			if err == nil {
				state.Status = "completed"
				// Drop any stale auth-failure override for this runner
				// — fresh OAuth just landed, future tasks should use
				// the new token from the keychain/file. Without this,
				// DeviceDetails would keep showing ⚠️ for up to 30 min
				// after a successful re-sign-in.
				ClearRunnerAuthInvalid(state.Runner)
			} else if ctx.Err() == context.Canceled {
				state.Status = "cancelled"
				if state.Detail == "" {
					state.Detail = "Authentication flow cancelled."
				}
			} else {
				state.Status = "failed"
				state.Error = strings.TrimSpace(err.Error())
				if state.Detail == "" {
					state.Detail = state.Error
				}
			}
			refreshRunnerBrowserAuthSnapshot(state)
		})
		snap := sess.snapshot()
		// Always log the terminal outcome — this is the line that was
		// missing when a browser-auth attempt died: agent.log showed the
		// spawn + captured URL, then nothing, leaving no trace of whether
		// the code was ever exchanged. Now the outcome (and any error /
		// resulting auth source) is in the persistent agent log.
		log.Printf("[runner-auth-browser] %s session id=%s terminal status=%s authConfigured=%v authSource=%s error=%q",
			snap.Runner, snap.ID, snap.Status, snap.AuthConfigured, snap.AuthSource, snap.Error)
		recordRunnerBrowserAuthOperation(snap)
		recordRunnerBrowserAuthIncident(snap)
		// Heartbeat-kick on terminal so the pill on yaver.io / mobile
		// flips from "sign in" to "ready" within a second, instead of
		// waiting up to 30 s for the next ticker.
		if onTerminal != nil {
			onTerminal()
		}
	}()

	runnerBrowserAuthSessions.Store(sess.ID, sess)
	return sess, nil
}

func recordRunnerBrowserAuthOperation(sess runnerBrowserAuthSession) {
	GlobalOperationStore().Upsert(OperationState{
		ID:        sess.ID,
		Kind:      "runner_browser_auth",
		Status:    sess.Status,
		Message:   sess.Detail,
		StartedAt: sess.StartedAt,
		DeviceID:  "local",
		Metadata: map[string]interface{}{
			"runner":         sess.Runner,
			"method":         sess.Method,
			"authConfigured": sess.AuthConfigured,
			"authSource":     sess.AuthSource,
			"openUrl":        sess.OpenURL,
			"code":           sess.Code,
			"error":          sess.Error,
		},
	})
}

func recordRunnerBrowserAuthIncident(sess runnerBrowserAuthSession) {
	switch sess.Status {
	case "failed":
		GlobalIncidentStore().Append(IncidentEvent{
			Timestamp:       sess.UpdatedAt,
			Severity:        IncidentSeverityError,
			Category:        "runner_auth",
			Code:            "runner.browser_auth.failed",
			Source:          "runner-auth/browser",
			Title:           "Runner browser auth failed",
			UserMessage:     firstNonEmptyBrowserAuth(sess.Detail, sess.Error, "Authentication failed on the remote machine."),
			TechnicalInfo:   sess.Error,
			OperationID:     sess.ID,
			LogsAvailable:   false,
			Recoverable:     true,
			CorrelationID:   sess.ID,
			SuggestedAction: "Retry the browser auth flow or configure the runner with credentials directly.",
			Metadata: map[string]interface{}{
				"runner": sess.Runner,
				"method": sess.Method,
			},
		})
	case "cancelled":
		GlobalIncidentStore().Append(IncidentEvent{
			Timestamp:       sess.UpdatedAt,
			Severity:        IncidentSeverityWarn,
			Category:        "runner_auth",
			Code:            "runner.browser_auth.cancelled",
			Source:          "runner-auth/browser",
			Title:           "Runner browser auth cancelled",
			UserMessage:     firstNonEmptyBrowserAuth(sess.Detail, "Authentication flow cancelled."),
			OperationID:     sess.ID,
			LogsAvailable:   false,
			Recoverable:     true,
			CorrelationID:   sess.ID,
			SuggestedAction: "Start the browser auth flow again when you are ready to finish sign-in.",
			Metadata: map[string]interface{}{
				"runner": sess.Runner,
				"method": sess.Method,
			},
		})
	}
}

func firstNonEmptyBrowserAuth(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func lookupRunnerBrowserAuthSession(id string) (*runnerBrowserAuthSessionState, bool) {
	v, ok := runnerBrowserAuthSessions.Load(strings.TrimSpace(id))
	if !ok {
		return nil, false
	}
	sess, ok := v.(*runnerBrowserAuthSessionState)
	return sess, ok
}

func (s *HTTPServer) handleRunnerBrowserAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req runnerBrowserAuthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	tr := runnerAuthTenantRuntimeFromRequest(r)
	sess, err := startRunnerBrowserAuthSession(req.Runner, tr, s.TriggerHeartbeat)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": sess.snapshot(),
	})
}

func runnerAuthTenantRuntimeFromRequest(r *http.Request) tenantRuntime {
	if r == nil {
		return tenantRuntime{}
	}
	guestID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestID == "" {
		return tenantRuntime{}
	}
	return tenantRuntimeForGuest(guestID)
}

func (s *HTTPServer) handleRunnerBrowserAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing id")
		return
	}
	sess, ok := lookupRunnerBrowserAuthSession(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "auth session not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok": true,
		"session": func() runnerBrowserAuthSession {
			snap := sess.snapshot()
			recordRunnerBrowserAuthOperation(snap)
			return snap
		}(),
	})
}

// handleRunnerBrowserAuthSubmitCode forwards a user-pasted token to the
// running CLI's stdin. Used by the Claude device-auth flow: the user
// signs in on platform.claude.com, copies the long authentication code,
// pastes it into the Yaver dashboard / mobile UI, and we feed it through
// to `claude auth login --console` (which is blocked on its read).
//
// Privacy contract: the code is forwarded once to the spawned CLI's
// stdin and immediately discarded. It is NEVER persisted, never logged,
// never sent to Convex, never stored on the bus. The activity log only
// records that *some* code was submitted — never the value.
func (s *HTTPServer) handleRunnerBrowserAuthSubmitCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing id")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		jsonError(w, http.StatusBadRequest, "missing code")
		return
	}
	sess, ok := lookupRunnerBrowserAuthSession(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "auth session not found")
		return
	}
	// Log that a code arrived (never the value — privacy contract above).
	// Pairs with the terminal-status line so agent.log shows the full arc:
	// spawn → captured URL → code received → exchange outcome. Its absence
	// is itself diagnostic: a 401 with no "code received" line means the
	// request never reached the agent (e.g. relay rejected it upstream).
	log.Printf("[runner-auth-browser] %s code received for session id=%s (%d chars) — forwarding to CLI stdin", sess.Runner, id, len(code))
	sess.mu.Lock()
	stdin := sess.stdin
	status := sess.Status
	sess.mu.Unlock()
	if stdin == nil {
		jsonError(w, http.StatusConflict, "session has no stdin pipe")
		return
	}
	if status == "completed" || status == "failed" || status == "cancelled" {
		jsonError(w, http.StatusConflict, fmt.Sprintf("session already %s", status))
		return
	}
	// Append a newline so the CLI's blocked `read` resolves. Closing
	// the writer afterwards signals EOF so single-shot prompts (the
	// `claude` flow) terminate cleanly without us having to track
	// whether more lines are expected.
	if _, err := io.WriteString(stdin, code+"\n"); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("write stdin: %s", err.Error()))
		return
	}
	// Close the writer fire-and-forget — some CLIs keep stdin open for
	// re-prompts, in which case Close() is a no-op-after-EOF and the
	// CLI ignores it. Best-effort.
	_ = stdin.Close()
	// Mark progress on the session so polling clients see motion. We
	// intentionally do NOT include the code in any field.
	sess.update(func(state *runnerBrowserAuthSession) {
		if state.Status == "starting" || state.Status == "awaiting_browser" {
			state.Status = "verifying"
		}
		state.Detail = "Authentication code submitted; waiting for the CLI to confirm."
	})
	snap := sess.snapshot()
	recordRunnerBrowserAuthOperation(snap)
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": snap,
	})
}

func (s *HTTPServer) handleRunnerBrowserAuthCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		jsonError(w, http.StatusBadRequest, "missing id")
		return
	}
	sess, ok := lookupRunnerBrowserAuthSession(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "auth session not found")
		return
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	sess.update(func(state *runnerBrowserAuthSession) {
		state.Status = "cancelled"
		if state.Detail == "" {
			state.Detail = "Authentication flow cancelled."
		}
		state.CompletedAt = time.Now().UnixMilli()
	})
	snap := sess.snapshot()
	recordRunnerBrowserAuthOperation(snap)
	recordRunnerBrowserAuthIncident(snap)
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": snap,
	})
}

// handleRunnerAuthCredentialsImport accepts a runner credentials JSON
// blob (the format claude / codex write to disk on a successful login)
// and persists it where the local runner CLI will read it on next spawn.
// This is the "copy my local token to the remote box" path — yaver is a
// single-user wrapper, so when the user already has working subscription
// auth on one of their devices, we copy that working state instead of
// running fresh OAuth on every box (which hits the SSH-launched-daemon
// Keychain wall on macOS).
//
// Body: {"runner": "claude" | "codex", "credentialsJson": "<full JSON>"}
//
// Storage: claude → $HOME/.claude/.credentials.json (mode 0600),
//
//	codex  → $HOME/.codex/auth.json (mode 0600).
//
// Side effect: clears MarkRunnerAuthInvalid for that runner so DeviceDetails
// flips back to ✓ signed in on the next /runner-auth/status poll without
// waiting for a successful task to vouch for the new creds.
//
// Note: this writes to the **file** path. claude on macOS reads the file
// before falling back to Keychain when CLAUDE_CONFIG_DIR is set or the
// default file path exists. Subsequent task spawns will pick up the new
// creds without needing GUI Keychain access.
func (s *HTTPServer) handleRunnerAuthCredentialsImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Runner          string `json:"runner"`
		CredentialsJSON string `json:"credentialsJson"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	runner := normalizeRunnerAuthName(body.Runner)
	if runner != "claude" && runner != "codex" {
		jsonError(w, http.StatusBadRequest, "unsupported runner — claude or codex only")
		return
	}
	creds := strings.TrimSpace(body.CredentialsJSON)
	if creds == "" {
		jsonError(w, http.StatusBadRequest, "missing credentialsJson")
		return
	}
	// Sanity-check it parses — refuse to write garbage that would break
	// the runner CLI on next spawn.
	var probe map[string]any
	if err := json.Unmarshal([]byte(creds), &probe); err != nil {
		jsonError(w, http.StatusBadRequest, "credentialsJson is not valid JSON: "+err.Error())
		return
	}
	tr := runnerAuthTenantRuntimeFromRequest(r)
	home := ""
	if tr.Enabled {
		if err := tr.prepare(); err != nil {
			jsonError(w, http.StatusInternalServerError, "tenant runtime: "+err.Error())
			return
		}
		home = tr.Home
	} else {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			jsonError(w, http.StatusInternalServerError, "cannot resolve $HOME on this machine")
			return
		}
	}
	var dest string
	switch runner {
	case "claude":
		dest = filepath.Join(home, ".claude", ".credentials.json")
	case "codex":
		dest = filepath.Join(home, ".codex", "auth.json")
	}
	if tr.Enabled {
		if err := writeTenantCredentialFile(tr, dest, []byte(creds)); err != nil {
			jsonError(w, http.StatusInternalServerError, "write tenant credentials: "+err.Error())
			return
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			jsonError(w, http.StatusInternalServerError, "mkdir "+filepath.Dir(dest)+": "+err.Error())
			return
		}
		if err := os.WriteFile(dest, []byte(creds), 0o600); err != nil {
			jsonError(w, http.StatusInternalServerError, "write "+dest+": "+err.Error())
			return
		}
	}
	// Reset the auth-failure override so the runner status pill flips
	// back to ✓ signed in on the next poll instead of waiting for a
	// task to vouch for the new creds.
	ClearRunnerAuthInvalid(runner)
	log.Printf("[runner-auth] imported %s credentials to %s (%d bytes)", runner, dest, len(creds))
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":     true,
		"runner": runner,
		"path":   dest,
		"bytes":  len(creds),
	})
}

func writeTenantCredentialFile(tr tenantRuntime, dest string, data []byte) error {
	if !tr.Enabled {
		return fmt.Errorf("tenant runtime not enabled")
	}
	cleanDest := filepath.Clean(dest)
	if cleanDest != tr.Home && !strings.HasPrefix(cleanDest, filepath.Clean(tr.Home)+string(filepath.Separator)) {
		return fmt.Errorf("credential destination %s is outside tenant home %s", cleanDest, tr.Home)
	}
	tmp, err := os.CreateTemp("", "yaver-tenant-cred-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.MkdirAll(filepath.Dir(cleanDest), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(cleanDest, data, 0o600); err != nil {
			return err
		}
		if out, err := exec.Command("chown", tr.User+":"+tr.User, cleanDest).CombinedOutput(); err != nil {
			return fmt.Errorf("chown: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if out, err := exec.Command("sudo", "-n", "install", "-d", "-o", tr.User, "-g", tr.User, "-m", "0700", filepath.Dir(cleanDest)).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo install dir: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("sudo", "-n", "install", "-o", tr.User, "-g", tr.User, "-m", "0600", tmpPath, cleanDest).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo install credential: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
