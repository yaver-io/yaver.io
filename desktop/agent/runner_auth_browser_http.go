package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	// Trigger says WHO asked. "" is treated as an EXPLICIT user tap, because
	// that is what every pre-2026-07-27 caller was: the automatic callers
	// (launch gates, health loops, modals that start on open) must now say
	// "auto" and accept being refused. Defaulting the other way would silence
	// the user's own button.
	Trigger string `json:"trigger,omitempty"`
	// Confirm is the user's second, deliberate tap after being told the runner
	// already looks signed in — e.g. because they are switching accounts. This
	// is the ONLY way to reap a healthy session.
	Confirm bool `json:"confirm,omitempty"`
}

func runnerAuthStartTriggerFromRequest(r *http.Request, trigger string) RunnerAuthStartTrigger {
	switch strings.ToLower(strings.TrimSpace(trigger)) {
	case "auto", "automatic", "background":
		return RunnerAuthTriggerAuto
	case "confirmed", "confirm", "reauth", "switch-account":
		return RunnerAuthTriggerConfirmed
	}
	if r != nil {
		switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("trigger"))) {
		case "auto", "automatic", "background":
			return RunnerAuthTriggerAuto
		case "confirmed", "confirm":
			return RunnerAuthTriggerConfirmed
		}
	}
	return RunnerAuthTriggerExplicit
}

// existingRunnerBrowserAuthSession returns the live (non-terminal) session for
// this runner+tenant, or nil. Two surfaces asking at once (phone + web) must
// converge on ONE session: reaping and respawning would leave the first surface
// polling a session id that no longer exists, which reads as a sign-in that
// silently died.
func existingRunnerBrowserAuthSession(runner, tenantUserID string) *runnerBrowserAuthSessionState {
	runner = normalizeRunnerAuthName(runner)
	tenantUserID = strings.TrimSpace(tenantUserID)
	var found *runnerBrowserAuthSessionState
	runnerBrowserAuthSessions.Range(func(_, v any) bool {
		st, ok := v.(*runnerBrowserAuthSessionState)
		if !ok {
			return true
		}
		snap := st.snapshot()
		if normalizeRunnerAuthName(snap.Runner) != runner ||
			strings.TrimSpace(snap.TenantUserID) != tenantUserID {
			return true
		}
		switch snap.Status {
		case "completed", "failed", "cancelled":
			return true
		}
		found = st
		return false
	})
	return found
}

type runnerBrowserAuthSession struct {
	ID           string `json:"id"`
	Runner       string `json:"runner"`
	TenantUserID string `json:"tenantUserId,omitempty"`
	Method       string `json:"method"`
	Status       string `json:"status"`
	OpenURL      string `json:"openUrl,omitempty"`
	// CallbackPort is the loopback port the CLI is waiting on for its OAuth
	// redirect. A client that binds the SAME port locally and forwards it here
	// lets the user finish sign-in in their own browser on their own machine:
	// redirect_uri is http://localhost:<this>, which is valid on both ends, and
	// PKCE forbids rewriting it to anything else. 0 means "not found" — never a
	// guess, because forwarding the wrong port sends the browser somewhere
	// silent. See runner_auth_callback_port.go.
	CallbackPort   int    `json:"callbackPort,omitempty"`
	Code           string `json:"code,omitempty"`
	Detail         string `json:"detail,omitempty"`
	AuthConfigured bool   `json:"authConfigured"`
	AuthSource     string `json:"authSource,omitempty"`
	Error          string `json:"error,omitempty"`
	StartedAt      int64  `json:"startedAt"`
	UpdatedAt      int64  `json:"updatedAt"`
	CompletedAt    int64  `json:"completedAt,omitempty"`
	// LastOutputAt is when the spawned CLI last wrote ANY line. Surfaces
	// render "CLI alive, no URL yet — last output 12s ago" from it instead
	// of an undifferentiated spinner; its absence while Status is still
	// "starting" means the CLI has said nothing at all (the silence
	// watchdog's territory). remained.md P0 contract.
	LastOutputAt int64 `json:"lastOutputAt,omitempty"`
	// recentOutput is the tail of the CLI's sanitized stdout/stderr lines,
	// kept so a hard exit can quote the CLI's own words instead of the
	// bare Go "exit status N" (audit CX5 — same verbatim principle as the
	// kimi Login-failed rule). Unexported on purpose: it never serializes
	// into snapshots; only the exit transition reads it.
	recentOutput []string
}

type runnerBrowserAuthSessionState struct {
	mu sync.Mutex
	runnerBrowserAuthSession
	cmd    *exec.Cmd
	cancel context.CancelFunc
	// stdin is the spawned CLI's stdin pipe, captured at start time so
	// the dashboard / mobile / Yaver mobile app can forward a pasted
	// authentication code (the `claude auth login --claudeai` flow shows
	// a URL, the user signs in on claude.ai, gets a long token, pastes
	// it back). The token is forwarded once and discarded — it
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
		idPrefix = runner + "-" + sanitizeTenantRef(tr.UserID)
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
	// The whole point of this snapshot is to observe a credential that may
	// have landed a moment ago, so never read it through the status cache.
	// BOTH caches: codex's login-status cache had no invalidation at all,
	// so a completed codex OAuth kept reporting authVerified=false from a
	// mid-sign-in probe for up to ~90s ("verify needed" over a successful
	// login — the false-amber twin of the false-green rule).
	invalidateClaudeAuthStatusCache()
	if out.Runner == "codex" {
		invalidateCodexLoginStatusCache()
	}
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

// watchRunnerBrowserAuthSilence fails a session that produced NO usable output.
//
// ── Why this exists ─────────────────────────────────────────────────────────
//
// `claude auth login --claudeai` on a Mac with a GUI OPENS A BROWSER ON THAT
// MACHINE and prints nothing to stdout. Measured on the Mac mini 2026-07-26:
// 22s with CI=1 NO_COLOR=1 TERM=dumb produced zero bytes, and the process was
// still blocked; under a PTY it emitted only a control character; with
// BROWSER=echo, still nothing. There is no --device-auth for claude the way
// there is for codex, so no stdout strategy can ever capture a URL from it.
//
// The reader above simply waited. Status stayed "starting", OpenURL stayed
// empty, Error stayed empty — so the phone showed "Waiting for the verification
// URL from the remote CLI…" with a spinner, indefinitely, over a flow the user
// had every reason to think was working. That is the worst shape of this
// defect: the operation was IMPOSSIBLE, and the product said nothing at all.
//
// A silence budget converts that into an answer. It does not make claude's
// browser flow work — nothing here can — it makes the failure legible and
// points at the paths that DO work headlessly.
// watchInterceptedBrowserURL promotes a shim-captured URL into the session.
//
// The stdout scanner stays authoritative when a CLI does print (kimi), because
// its line carries the user code alongside the URL. This is the fallback for
// the ones that say nothing at all — and it is the ONLY way those flows ever
// produce something the user can act on.
func watchInterceptedBrowserURL(sess *runnerBrowserAuthSessionState, interceptor *browserInterceptor) {
	defer interceptor.cleanup()
	// Generous: a cold CLI can take a while to reach the browser step, and this
	// costs nothing while it waits. Shorter than the silence watchdog so a
	// captured URL always wins the race against "we gave up".
	url := interceptor.waitForURL(40 * time.Second)
	if url == "" {
		return
	}
	sess.update(func(state *runnerBrowserAuthSession) {
		if state.OpenURL != "" {
			return // the CLI printed one itself; that one is richer, keep it
		}
		state.OpenURL = url
		if state.Status == "starting" {
			state.Status = "awaiting_browser"
		}
		if state.Detail == "" {
			state.Detail = "Captured the sign-in link the CLI tried to open locally — finish it on this device."
		}
		log.Printf("[runner-auth-browser] %s captured openUrl via browser shim: %s", state.Runner, url)
	})
}

// watchRunnerCallbackPort records the CLI's loopback callback port on the
// session once it binds one.
//
// Polled rather than read once: the socket appears AFTER spawn, often behind a
// keychain probe or a network round-trip, so an immediate check finds nothing
// in every real case.
func watchRunnerCallbackPort(sess *runnerBrowserAuthSessionState, pid int) {
	port := waitForRunnerCallbackPort(context.Background(), pid, 45*time.Second)
	if port <= 0 {
		return
	}
	sess.update(func(state *runnerBrowserAuthSession) {
		if state.CallbackPort != 0 {
			return
		}
		state.CallbackPort = port
		log.Printf("[runner-auth-browser] %s waiting for its OAuth callback on localhost:%d — a client forwarding that port can finish the sign-in from its own browser", state.Runner, port)
	})
}

// runnerBrowserAuthTerminal reports whether a session status is final.
// `account_not_eligible` IS terminal: the CLI printed a verbatim
// entitlement rejection ("no active subscription" et al) and no retry,
// code paste, or callback delivery can change the verdict — only a
// different account can. Every guard that means "already ended" must use
// this helper; the hand-rolled completed/failed/cancelled triples it
// replaces were how account_not_eligible sessions kept accepting pastes
// and got clobbered by process exit.
func runnerBrowserAuthTerminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "account_not_eligible":
		return true
	}
	return false
}

// runnerAuthRecentOutputMax bounds the per-session output tail. Five lines is
// enough to skip past a trailing URL/device-code and still find the CLI's
// actual complaint; the pasted token never appears here (it goes to stdin,
// which is never scanned).
const runnerAuthRecentOutputMax = 5

// appendRunnerAuthRecentOutput keeps the LAST runnerAuthRecentOutputMax lines.
func appendRunnerAuthRecentOutput(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > runnerAuthRecentOutputMax {
		lines = lines[len(lines)-runnerAuthRecentOutputMax:]
	}
	return lines
}

// lastMeaningfulRunnerAuthOutput returns the most recent CLI line worth
// quoting in an exit error. Lines carrying the sign-in URL or a bare
// device code are flow narration, not a failure reason — quoting them
// would tell the user to re-open the link that already didn't work.
func lastMeaningfulRunnerAuthOutput(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if urlPattern.MatchString(line) {
			continue
		}
		if codexCodePattern.FindString(line) == line {
			continue
		}
		return line
	}
	return ""
}

// applyRunnerBrowserAuthExit encodes the process-exit transition
// (pure — pinned by runner_auth_browser_state_test.go). The rule that
// was violated in production: a process exit NEVER downgrades a session
// that already carries the account_not_eligible diagnosis. kimi prints
// "Login failed: …subscription…" and then exits; overwriting that with
// failed/"exit status 1" (or completed, on a zero exit) demoted the only
// status that tells the user a retry cannot work.
func applyRunnerBrowserAuthExit(state *runnerBrowserAuthSession, exitErr error, cancelled bool) {
	state.CompletedAt = time.Now().UnixMilli()
	if state.Status == "account_not_eligible" {
		if exitErr != nil && strings.TrimSpace(state.Error) == "" {
			state.Error = strings.TrimSpace(exitErr.Error())
		}
		return
	}
	switch {
	case exitErr == nil:
		state.Status = "completed"
	case cancelled:
		state.Status = "cancelled"
		if state.Detail == "" {
			state.Detail = "Authentication flow cancelled."
		}
	default:
		state.Status = "failed"
		msg := strings.TrimSpace(exitErr.Error())
		// "exit status 1" alone gives the user nothing to act on. The CLI
		// almost always said WHY on its way down — carry its last
		// meaningful line verbatim (audit CX5; the codex twin of the kimi
		// Login-failed rule).
		if quote := lastMeaningfulRunnerAuthOutput(state.recentOutput); quote != "" && !strings.Contains(msg, quote) {
			msg = msg + " — " + state.Runner + " said: " + quote
		}
		state.Error = msg
		if state.Detail == "" {
			state.Detail = state.Error
		}
	}
}

// runnerBrowserAuthDeadline bounds the WHOLE session, not just its first
// output. The silence watchdog (45s) catches a CLI that never speaks; this
// catches the other eternal wait: a CLI that printed its URL and then sits
// on a localhost callback that will never arrive (user closed the tab,
// browser landed on an unreachable localhost, code never pasted). Before
// this, such a session stayed "awaiting_browser" forever — it was only
// reaped when a NEW sign-in for the same runner arrived.
const runnerBrowserAuthDeadline = 15 * time.Minute

// applyRunnerBrowserAuthDeadline fails an ACTIVE session that outlived the
// deadline, with a remedy that names both recovery lanes. Terminal sessions
// are never touched. Pure — pinned by runner_auth_browser_state_test.go.
func applyRunnerBrowserAuthDeadline(state *runnerBrowserAuthSession) bool {
	if runnerBrowserAuthTerminal(state.Status) {
		return false
	}
	state.Status = "failed"
	state.Error = fmt.Sprintf("No OAuth callback or code arrived within %d minutes — the sign-in never completed on the provider side. Restart the sign-in; if the browser DID finish but ended on a localhost page, paste that full callback URL (or the code) into the sign-in panel next time.", int(runnerBrowserAuthDeadline.Minutes()))
	if state.Detail == "" {
		state.Detail = state.Error
	}
	state.CompletedAt = time.Now().UnixMilli()
	return true
}

func watchRunnerBrowserAuthDeadline(sess *runnerBrowserAuthSessionState) {
	time.Sleep(runnerBrowserAuthDeadline)
	changed := false
	sess.update(func(state *runnerBrowserAuthSession) {
		changed = applyRunnerBrowserAuthDeadline(state)
	})
	if !changed {
		return
	}
	if sess.cancel != nil {
		sess.cancel() // reap the orphaned CLI (and free its PKCE verifier)
	}
	snap := sess.snapshot()
	log.Printf("[runner-auth-browser] %s session id=%s exceeded %s without completing — failing it with a remedy instead of waiting forever", snap.Runner, snap.ID, runnerBrowserAuthDeadline)
	recordRunnerBrowserAuthOperation(snap)
	recordRunnerBrowserAuthIncident(snap)
}

// applyRunnerBrowserAuthCancel flips an ACTIVE session to cancelled and
// reports whether it changed anything. A session that already reached a
// terminal state keeps it — a late × on the modal must not un-report a
// completed sign-in, nor erase an entitlement verdict.
func applyRunnerBrowserAuthCancel(state *runnerBrowserAuthSession) bool {
	if runnerBrowserAuthTerminal(state.Status) {
		return false
	}
	state.Status = "cancelled"
	if state.Detail == "" {
		state.Detail = "Authentication flow cancelled."
	}
	state.CompletedAt = time.Now().UnixMilli()
	return true
}

func watchRunnerBrowserAuthSilence(sess *runnerBrowserAuthSessionState, runner string) {
	const silenceBudget = 45 * time.Second
	time.Sleep(silenceBudget)
	sess.update(func(state *runnerBrowserAuthSession) {
		if state.OpenURL != "" || state.Code != "" || runnerBrowserAuthTerminal(state.Status) {
			return // it spoke, or it already ended — nothing to report
		}
		state.Status = "failed"
		if strings.EqualFold(runner, "claude") {
			state.Error = "Claude Code printed no verification URL in 45s. On a machine with a desktop it opens a browser THERE instead of printing a link, and it has no --device-auth flag, so this remote flow cannot capture one. Use `runner_auth_credentials_import` to copy an existing login from a machine you are signed in on, or run `claude auth login` in a terminal on that machine (a tmux session works)."
		} else if strings.EqualFold(runner, "codex") {
			// Codex-specific silence causes, in observed order of likelihood:
			// device-auth can be DISABLED per-workspace by an admin (the flag
			// is accepted but the flow just stalls — openai/codex#9253, the
			// same gating the spawn comment documents), or the CLI is waiting
			// on an interactive prompt a remote flow cannot answer. The scan
			// for "Login failed:" lines can never fire here because codex
			// device-auth is measured SILENT, so this watchdog is the only
			// place the entitlement possibility can be named for codex.
			state.Error = "codex printed no verification URL or code in 45s. Either device-auth is disabled for this workspace by its admin (enable it in the workspace settings, or sign in with a personal account), or the CLI is waiting on an interactive prompt a remote flow cannot answer — run `codex login` once in a terminal on that machine, or import credentials from a machine already signed in."
		} else {
			state.Error = fmt.Sprintf("%s printed no verification URL or code in 45s. The CLI may be waiting on an interactive prompt that a remote flow cannot answer — run its login once in a terminal on that machine, or import credentials from a machine already signed in.", runner)
		}
		log.Printf("[runner-auth-browser] %s produced no URL within %s — failing the session with a remedy instead of spinning", runner, silenceBudget)
	})
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
			// --device-auth IS still supported. It is absent from `codex login
			// --help` on 0.144.4, and I briefly removed it on that basis — wrong.
			// An unknown flag makes codex exit IMMEDIATELY with "unexpected
			// argument"; this HANGS instead, which is what an accepted flag doing
			// a device-code poll looks like. Help output is not the contract.
			//
			// OpenAI's docs still recommend it for headless and note it can be
			// gated per-workspace by an admin (openai/codex#9253) — so a silent
			// flow may be an ENTITLEMENT problem on the account rather than a
			// missing flag, which is what the watchdog now surfaces instead of
			// spinning forever.
			//
			// Deliberately NOT falling back to --with-api-key or
			// --with-access-token: Yaver is subscription-only.
			cmd = exec.Command(bin, "login", "--device-auth")
			cmd.Env = append(cmd.Environ(), "CI=1", "NO_COLOR=1", "TERM=dumb")
		}
		return "device-auth", cmd, nil
	case "kimi":
		bin := resolveRunnerBinary("kimi")
		if bin == "" {
			return "", nil, fmt.Errorf("kimi CLI not found on this machine (looked in PATH, ~/.npm-global/bin, ~/.local/bin, ~/.bun/bin, /opt/homebrew/bin, /usr/local/bin, and the user login shell). Install it with `curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash` or `npm i -g @moonshot-ai/kimi-code`, then try again.")
		}
		// `kimi login` is an RFC 8628 device-code flow and the ONLY runner here
		// that was designed for this. It needs no TUI, prints the verification
		// URL and user code to STDERR, and polls until the browser side
		// completes — so it works identically on a headless box, a phone, a TV
		// or a watch, because the authorising device never has to be the one
		// running the CLI.
		//
		// Observed output (2026-07-26):
		//   Opening browser for Kimi device login: https://www.kimi.com/code/authorize_device?user_code=IY0N-NC7Z
		//   If the browser did not open, paste the URL above and enter code: IY0N-NC7Z
		//   Code expires in 1800s.
		//   Waiting for authorization to complete...
		//
		// The URL is caught by urlPattern and the XXXX-XXXX code by the shared
		// device-code pattern, so no new capture logic is required.
		//
		// Subscription only, per Yaver's no-API-keys rule: `kimi login` is the
		// OAuth path. Kimi also accepts a platform API key, and that belongs in
		// OpenCode's provider config where a key is a deliberate choice — never
		// here.
		if tr.Enabled {
			cmd, err = tr.command(context.Background(), tr.Home, bin, []string{"login"}, append(tr.authEnv(), "CI=1", "NO_COLOR=1", "TERM=dumb"))
			if err != nil {
				return "", nil, err
			}
		} else {
			cmd = exec.Command(bin, "login")
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
	case "opencode":
		// opencode's ChatGPT subscription login, driven headlessly (audit
		// 2026-08-12, verified against opencode 1.18.15 with a real paid
		// account):
		//
		//   OPENCODE_CONFIG_CONTENT='{"enabled_providers":["openai"]}' \
		//     opencode auth login -p openai -m "ChatGPT Pro/Plus (headless)"
		//
		// Two facts make this work where the naive `opencode auth login`
		// hangs in a TUI provider picker:
		//
		//   1. `auth login` FILTERS providers by config.enabled_providers.
		//      The user's real config lists ["zai-coding-plan","deepseek"],
		//      so `openai` (the OAuth credential) silently vanished and every
		//      -p <provider> returned "Unknown provider". OPENCODE_CONFIG_CONTENT
		//      (a JSON config string merged with local priority, verified in
		//      sst/opencode config.ts) injects an enabled set that includes
		//      openai WITHOUT touching the user's on-disk config. It only
		//      affects this login process.
		//
		//   2. The "headless" method is a plain RFC 8628 device-code flow
		//      (clack log.info → process.stdout, no TTY needed):
		//        Go to: https://auth.openai.com/codex/device
		//        Enter code: MWJI-A4WH0
		//      The URL matches urlPattern and the XXXX-XXXX code matches
		//      codexCodePattern, so the shared scanner captures both into
		//      OpenURL + Code with zero new parsing — opencode rides the
		//      exact machine codex/kimi already ride on.
		//
		// Method label match is case-insensitive (providers.ts compares
		// lowercased labels). The "browser" variant opens a browser on the
		// box instead — wrong for headless/remote boxes, hence headless.
		bin := resolveRunnerBinary("opencode")
		if bin == "" {
			return "", nil, fmt.Errorf("opencode CLI not found on this machine (looked in PATH, ~/.npm-global/bin, ~/.local/bin, ~/.bun/bin, /opt/homebrew/bin, /usr/local/bin, and the user login shell). Run `npm i -g opencode-ai` and try again.")
		}
		cmd = exec.Command(bin, "auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)")
		cmd.Env = append(cmd.Environ(),
			"CI=1", "NO_COLOR=1", "TERM=dumb",
			`OPENCODE_CONFIG_CONTENT={"enabled_providers":["openai"]}`,
		)
		return "device-auth", cmd, nil
	default:
		return "", nil, fmt.Errorf("unsupported runner %q (want claude, codex, opencode, or kimi)", runner)
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
			state.LastOutputAt = time.Now().UnixMilli()
			state.recentOutput = appendRunnerAuthRecentOutput(state.recentOutput, line)
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
			// Codex, Kimi and opencode all emit an RFC 8628 style XXXX-XXXX
			// user code. Kimi's looks like "IY0N-NC7Z", opencode's "MWJI-A4WH0"
			// — the same shape, so one pattern serves all three rather than
			// two that can drift apart. opencode's headless ChatGPT login
			// prints "Enter code: MWJI-A4WH0" right after the device URL
			// (verified 2026-08-12), so it must ride the same capture gate.
			if (state.Runner == "codex" || state.Runner == "kimi" || state.Runner == "opencode") && state.Code == "" {
				if code := codexCodePattern.FindString(line); code != "" {
					state.Code = code
					state.Status = "awaiting_browser"
					log.Printf("[runner-auth-browser] %s captured code=%s", state.Runner, code)
				}
			}

			// A CLI that says it FAILED must end the session, not leave the
			// phone waiting on a code that will never be accepted. Kimi reports
			//   Login failed: ... unable to verify your membership benefits ...
			// after a perfectly good device handshake — the flow worked and the
			// ACCOUNT was rejected. Those are different problems and the user
			// can only act on the second if we repeat it verbatim.
			if state.Status != "completed" {
				if idx := strings.Index(line, "Login failed:"); idx >= 0 {
					detail := strings.TrimSpace(line[idx:])
					// AUTHORIZED-BUT-NOT-ENTITLED IS NOT A LOGIN FAILURE.
					//
					// Kimi answers a perfectly good device handshake with
					//   Login failed: ... unable to verify your membership
					//   benefits ... Please ensure your membership is active.
					// The user opened the URL, signed in (Google, in this case)
					// and approved the code — every step they control SUCCEEDED.
					// What is missing is a plan on the account.
					//
					// Rendering that as "login failed" tells them to retry the
					// one thing that already worked, forever. It is its own
					// status so every surface can say: signed in, membership not
					// active yet — and point at the account page instead of the
					// sign-in button.
					lower := strings.ToLower(detail)
					entitlement := strings.Contains(lower, "membership") ||
						strings.Contains(lower, "subscription") ||
						strings.Contains(lower, "benefits") ||
						strings.Contains(lower, "waitlist") ||
						strings.Contains(lower, "not eligible") ||
						strings.Contains(lower, "no active plan")
					if entitlement {
						// NOT "authorized". We never saw authorization confirmed —
						// the CLI printed "Waiting for authorization to complete..."
						// and then this. Claiming a successful sign-in would be the
						// same fabrication this file exists to remove; the honest
						// statement is that the ACCOUNT was rejected, quoting the
						// CLI verbatim rather than paraphrasing it into a claim.
						state.Status = "account_not_eligible"
						state.Error = detail
						state.Detail = "Sign-in did not complete: " + state.Runner + " rejected the account, not the code. Verbatim: " + detail + " — retrying the sign-in will not change this; activate the plan (or clear the waitlist) on the account first."
						log.Printf("[runner-auth-browser] %s rejected the ACCOUNT (not the device code): %s", state.Runner, detail)
					} else {
						state.Status = "failed"
						state.Error = detail
						log.Printf("[runner-auth-browser] %s login failed: %s", state.Runner, detail)
					}
				}
			}
		})
	}
	if err := scanner.Err(); err != nil {
		// "file already closed" is the EXPECTED shape of shutdown: cmd.Wait
		// closes the pipes when the CLI exits, and both scanner goroutines
		// then see os.ErrClosed on their blocked read. Logging that as
		// "scanner error" made every successful sign-in end with two error
		// lines in the journal (observed live 2026-07-27, right after a
		// callback replay completed). Only a read failure on a LIVE pipe is
		// worth an error line.
		if errors.Is(err, os.ErrClosed) || strings.Contains(err.Error(), "file already closed") {
			if debug {
				log.Printf("[runner-auth-browser] %s reader closed after process exit", sess.Runner)
			}
		} else {
			log.Printf("[runner-auth-browser] %s scanner error: %v", sess.Runner, err)
		}
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

	// Route the child's browser-open through a shim so a CLI that prints NOTHING
	// still yields its URL. claude and codex both open a browser and emit no
	// text; without this there is no URL to send to the phone, and the whole
	// remote flow has nothing to show. See runner_auth_browser_intercept.go.
	if interceptor, ierr := newBrowserInterceptor(sess.ID); ierr == nil {
		cmd.Env = interceptor.env(cmd.Env)
		go watchInterceptedBrowserURL(sess, interceptor)
	} else {
		log.Printf("[runner-auth-browser] %s: browser interception unavailable (%v) — falling back to stdout scraping only", runner, ierr)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s auth: %w", runner, err)
	}

	log.Printf("[runner-auth-browser] %s spawned: %s %v (id=%s)", runner, cmd.Path, cmd.Args[1:], sess.ID)
	go scanRunnerBrowserAuthOutput(sess, stdout)
	go scanRunnerBrowserAuthOutput(sess, stderr)
	// A CLI that prints nothing must not leave the caller spinning forever.
	go watchRunnerBrowserAuthSilence(sess, runner)
	// …and a CLI that spoke but whose callback never arrives must not
	// either — bound the whole session, not just its first output.
	go watchRunnerBrowserAuthDeadline(sess)
	// Find the loopback port the CLI will wait on, so a client can forward it
	// and complete the flow from the user's own browser.
	if cmd.Process != nil {
		go watchRunnerCallbackPort(sess, cmd.Process.Pid)
	}
	recordRunnerBrowserAuthOperation(sess.snapshot())
	go func() {
		err := cmd.Wait()
		sess.update(func(state *runnerBrowserAuthSession) {
			applyRunnerBrowserAuthExit(state, err, ctx.Err() == context.Canceled)
			if state.Status == "completed" {
				// Drop any stale auth-failure override for this runner
				// — fresh OAuth just landed, future tasks should use
				// the new token from the keychain/file. Without this,
				// DeviceDetails would keep showing ⚠️ for up to 30 min
				// after a successful re-sign-in.
				ClearRunnerAuthInvalid(state.Runner)
				// A completed OAuth exchange IS an operation against the
				// provider: the authorization code was redeemed and the
				// provider handed back a token. That is the one moment we can
				// legitimately claim AuthVerified without spending anything —
				// so record it, rather than leaving a freshly signed-in box
				// reporting "present but unconfirmed" until its first task.
				MarkRunnerAuthProven(state.Runner)
				// `claude auth login` authenticates but does not mark the
				// first-run wizard done, so the TUI would still greet this
				// freshly signed-in box with "Select login method".
				if state.Runner == "claude" && !tr.Enabled {
					ensureClaudeOnboardingForLocalHome()
				}
				// A completed sign-in is the OTHER proof that the blocker
				// cleared — and on a headless box it is the ONLY one, because
				// `codex login --device-auth` establishes a brand-new
				// credential rather than renewing an old one, so the keep-alive
				// refresh path never runs.
				//
				// Without this, the whole parked-turn promise breaks exactly
				// where the user is most likely to see it: they get told "your
				// message will send once you're signed in", they go and sign in
				// on the box, and nothing happens. A recovery path that does
				// not resume the work is not a recovery.
				replayParkedTurnsAfterAuthRecovery(state.Runner + " sign-in completed")
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
	case "account_not_eligible":
		// The one terminal state where "try again" is a lie — surface it
		// to the incident feed with the verbatim CLI rejection so every
		// surface (custodian, phone, web) can render the real verdict.
		GlobalIncidentStore().Append(IncidentEvent{
			Timestamp:       sess.UpdatedAt,
			Severity:        IncidentSeverityError,
			Category:        "runner_auth",
			Code:            "runner.browser_auth.account_not_eligible",
			Source:          "runner-auth/browser",
			Title:           "Runner account not eligible",
			UserMessage:     firstNonEmptyBrowserAuth(sess.Detail, sess.Error, "The signed-in account has no eligible subscription for this runner."),
			TechnicalInfo:   sess.Error,
			OperationID:     sess.ID,
			LogsAvailable:   false,
			Recoverable:     false,
			CorrelationID:   sess.ID,
			SuggestedAction: "Sign in with an account that has an active subscription for this runner — retrying with the same account cannot succeed.",
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
	if !remoteRunnerOAuthEnabled() {
		jsonError(w, http.StatusGone, "Yaver-managed remote runner OAuth is disabled; sign in locally on the runner machine")
		return
	}
	var req runnerBrowserAuthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	tr := runnerAuthTenantRuntimeFromRequest(r)

	// DON'T REAP A HEALTHY SESSION. Starting a browser-auth flow is destructive
	// — cancelStaleRunnerBrowserAuthSessions kills any live session for this
	// runner, a PKCE flow is burned, and for claude the credential can end up
	// REPLACED. On 2026-07-27 the user was shown sign-in dialogs repeatedly for
	// runners that were fine; that is the same "inventory vs operation" defect
	// as the false green, pointing the other way.
	//
	// The decision is centralised in DecideRunnerAuthStart so every caller
	// (launch gate, chip, health loop, dispatch preflight, the modals that used
	// to start a session merely because they opened) gets the same answer.
	decision := DecideRunnerAuthStart(RunnerAuthStartInput{
		Runner: normalizeRunnerAuthName(req.Runner),
		Trigger: func() RunnerAuthStartTrigger {
			if req.Confirm {
				return RunnerAuthTriggerConfirmed
			}
			return runnerAuthStartTriggerFromRequest(r, req.Trigger)
		}(),
		Status:   DetectRunnerRuntimeStatus(GetRunnerConfig(req.Runner), ""),
		InFlight: existingRunnerBrowserAuthSession(req.Runner, tr.UserID) != nil,
	})
	switch decision.Action {
	case RunnerAuthStartNoop:
		jsonReply(w, http.StatusOK, map[string]any{
			"ok":         true,
			"action":     string(decision.Action),
			"reason":     decision.Reason,
			"reauthable": decision.Reauthable,
		})
		return
	case RunnerAuthStartReuse:
		if existing := existingRunnerBrowserAuthSession(req.Runner, tr.UserID); existing != nil {
			jsonReply(w, http.StatusOK, map[string]any{
				"ok":      true,
				"action":  string(decision.Action),
				"reason":  decision.Reason,
				"session": existing.snapshot(),
			})
			return
		}
	}

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
	return tenantRuntime{}
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
// to `claude auth login --claudeai` (which is blocked on its read).
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
	if normalizeRunnerAuthName(sess.Runner) != "claude" {
		jsonError(w, http.StatusBadRequest, "pasted authentication codes/tokens are only supported for Claude Code; use the browser callback/device flow for "+sess.Runner)
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
	if runnerBrowserAuthTerminal(status) {
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

// handleRunnerBrowserAuthSubmitCallback completes localhost-redirect runner
// OAuth from a browser that is NOT on the runner machine.
//
// Some CLIs open a normal OAuth URL whose redirect_uri is
// http://localhost:<ephemeral>/callback. If the user completes that URL on
// their laptop while the CLI is running on a remote Linux box, the browser
// lands on the laptop's localhost and the remote CLI never sees the code. The
// user can paste that final localhost URL here; the agent validates that the
// port is exactly the callback port this session observed, then replays the
// request from the runner machine to its own loopback listener.
//
// Privacy: the callback URL contains an OAuth code. It is never logged,
// persisted, or written to operation metadata. Only the URL shape and matched
// port are validated.
func (s *HTTPServer) handleRunnerBrowserAuthSubmitCallback(w http.ResponseWriter, r *http.Request) {
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
		CallbackURL string `json:"callback_url"`
		CallbackUrl string `json:"callbackUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	callbackURL := strings.TrimSpace(firstNonEmptyBrowserAuth(body.CallbackURL, body.CallbackUrl))
	if callbackURL == "" {
		jsonError(w, http.StatusBadRequest, "missing callback_url")
		return
	}
	sess, ok := lookupRunnerBrowserAuthSession(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "auth session not found")
		return
	}
	sess.mu.Lock()
	callbackPort := sess.CallbackPort
	status := sess.Status
	sess.mu.Unlock()
	if runnerBrowserAuthTerminal(status) {
		jsonError(w, http.StatusConflict, fmt.Sprintf("session already %s", status))
		return
	}
	targetURL, err := validateRunnerBrowserAuthCallbackURL(callbackURL, callbackPort)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "callback URL malformed")
		return
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		jsonError(w, http.StatusBadGateway, "callback did not reach the waiting runner on this machine: "+err.Error())
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		jsonError(w, http.StatusBadGateway, fmt.Sprintf("runner callback returned HTTP %d", resp.StatusCode))
		return
	}
	log.Printf("[runner-auth-browser] %s callback replayed for session id=%s on localhost:%d — waiting for CLI confirmation", sess.Runner, id, callbackPort)
	sess.update(func(state *runnerBrowserAuthSession) {
		if state.Status == "starting" || state.Status == "awaiting_browser" {
			state.Status = "verifying"
		}
		state.Detail = "OAuth callback delivered to the remote runner; waiting for the CLI to confirm."
	})
	snap := sess.snapshot()
	recordRunnerBrowserAuthOperation(snap)
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"session": snap,
	})
}

func validateRunnerBrowserAuthCallbackURL(raw string, callbackPort int) (string, error) {
	if callbackPort <= 0 {
		return "", fmt.Errorf("the runner has not reported a localhost callback port yet — wait a moment, then try again")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("paste the full localhost callback URL from the browser address bar")
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("runner callback URL must be http://localhost")
	}
	host := strings.Trim(strings.ToLower(u.Hostname()), "[]")
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", fmt.Errorf("runner callback URL must point to localhost, not %s", u.Hostname())
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port != callbackPort {
		return "", fmt.Errorf("callback port mismatch: runner is waiting on localhost:%d", callbackPort)
	}
	u.Scheme = "http"
	u.Host = "127.0.0.1:" + strconv.Itoa(callbackPort)
	return u.String(), nil
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
	changed := false
	sess.update(func(state *runnerBrowserAuthSession) {
		changed = applyRunnerBrowserAuthCancel(state)
	})
	snap := sess.snapshot()
	if changed {
		recordRunnerBrowserAuthOperation(snap)
		recordRunnerBrowserAuthIncident(snap)
	}
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
	if runner != "claude" && runner != "codex" && runner != "opencode" {
		jsonError(w, http.StatusBadRequest, "unsupported runner — claude, codex, or opencode")
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
	case "opencode":
		// opencode's canonical store (XDG data dir) — first entry that
		// openCodeAuthPaths() probes for detectOpenCodeStatus.
		dest = filepath.Join(home, ".local", "share", "opencode", "auth.json")
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
	//
	// IMPORTED bytes are PRESENCE, not proof: a mirrored credential can be
	// expired, scoped to another account, or already revoked at the source.
	// Drop any standing proof rather than inheriting the old token's — this is
	// a different credential now, and nobody has exercised it.
	ClearRunnerAuthInvalid(runner)
	ClearRunnerAuthProven(runner)
	invalidateClaudeAuthStatusCache()
	if runner == "claude" && !tr.Enabled {
		ensureClaudeOnboardingForLocalHome()
	}
	// Imported credentials are the third way a blocked runner becomes usable
	// again (browser sign-in and keep-alive renewal are the other two). Every one
	// of them must resume the work the user parked, or the promise only holds on
	// whichever path the last author happened to think about.
	replayParkedTurnsAfterAuthRecovery(runner + " credentials imported")
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
