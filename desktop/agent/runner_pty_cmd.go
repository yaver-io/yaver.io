package main

// runner_pty_cmd.go — `yaver claude|codex|opencode|glm [runner args…] [--machine=<device>]`:
// run a coding agent's own interactive TUI, locally or on a remote yaver
// machine, with yaver contributing ONLY device resolution (alias / id / name /
// primary / secondary), the tunnel, and tmux persistence on the far end.
//
//	yaver claude --dangerously-skip-permissions --machine=myhetzner
//	yaver codex --dangerously-bypass-approvals-and-sandbox --machine=mypi
//	yaver opencode --machine primary
//	yaver codex                       # no --machine: plain local exec passthrough
//
// Everything that is not a yaver-owned flag (--machine/-m, --chrome,
// --yaver-cwd, --yaver-session, --yaver-help) is shipped VERBATIM to the
// runner's argv — yaver does not model or validate runner flags, which is
// what keeps this generic across runners and future-proof across their
// releases. Remote sessions are wrapped in tmux by the agent (see
// runner_pty.go): drop the connection and the runner keeps running; rerun
// the same command to land back in the same TUI. `tmux_adopt_session` can
// adopt the same pane from the phone.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func runRunnerPassthrough(runnerName string, args []string) {
	machine := ""
	chrome := false
	cwd := ""
	session := ""
	yaverSafe := false
	passthrough := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--machine" || a == "-m":
			if i+1 < len(args) {
				i++
				machine = args[i]
			}
		case strings.HasPrefix(a, "--machine="):
			machine = strings.TrimPrefix(a, "--machine=")
		case a == "--chrome":
			chrome = true
		case strings.HasPrefix(a, "--yaver-cwd="):
			cwd = strings.TrimPrefix(a, "--yaver-cwd=")
		case strings.HasPrefix(a, "--yaver-session="):
			session = strings.TrimPrefix(a, "--yaver-session=")
		case a == "--yaver-safe":
			yaverSafe = true
		case a == "--yaver-help":
			printRunnerPassthroughUsage(runnerName)
			return
		default:
			passthrough = append(passthrough, a)
		}
	}

	runnerID := normalizeRunnerID(runnerName)
	if !IsSupportedRunner(runnerID) {
		fmt.Fprintf(os.Stderr, "%s: unsupported runner (expected one of: %s)\n",
			runnerName, strings.Join(supportedRunnerIDs, ", "))
		os.Exit(1)
	}
	if !yaverSafe {
		passthrough = applyRunnerYoloDefaults(runnerID, passthrough)
	}

	if strings.TrimSpace(machine) == "" {
		runLocalRunnerPassthrough(runnerID, passthrough)
		return
	}
	if err := runRemoteRunnerPTY(machine, runnerID, passthrough, cwd, session, chrome); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", runnerName, err)
		os.Exit(1)
	}
}

// applyRunnerYoloDefaults prepends each runner's no-approvals flag when the
// caller didn't say otherwise — yolo is the default per
// feedback_runners_always_dangerous; `--yaver-safe` opts out.
//
//	claude/glm → --dangerously-skip-permissions
//	codex      → --dangerously-bypass-approvals-and-sandbox
//	opencode   → nothing (its TUI takes no permission flag; config-driven)
//
// Injection is skipped when the args already carry a permission/sandbox
// stance, and when the first arg is a management subcommand (codex login,
// claude mcp, …) where a root-level flag would be rejected. A quoted prompt
// as first positional is fine — flags-before-positional parses on both CLIs.
func applyRunnerYoloDefaults(runnerID string, args []string) []string {
	var yolo string
	var conflicts []string
	var subcommands map[string]bool
	switch runnerID {
	case "claude", "glm":
		yolo = "--dangerously-skip-permissions"
		conflicts = []string{"--dangerously-skip-permissions", "--permission-mode"}
		subcommands = map[string]bool{
			"mcp": true, "config": true, "doctor": true, "update": true,
			"install": true, "migrate-installer": true, "setup-token": true,
			"auth": true, "plugin": true, "agents": true, "help": true,
		}
	case "codex":
		yolo = "--dangerously-bypass-approvals-and-sandbox"
		conflicts = []string{
			"--dangerously-bypass-approvals-and-sandbox", "--full-auto",
			"--sandbox", "-s", "--ask-for-approval", "-a",
		}
		subcommands = map[string]bool{
			"exec": true, "e": true, "login": true, "logout": true,
			"mcp": true, "app-server": true, "completion": true,
			"debug": true, "apply": true, "a": true, "resume": true,
			"sandbox": true, "proto": true, "help": true,
		}
	default:
		return args
	}
	for _, a := range args {
		flag := a
		if idx := strings.Index(flag, "="); idx > 0 {
			flag = flag[:idx]
		}
		for _, c := range conflicts {
			if flag == c {
				return args
			}
		}
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && subcommands[args[0]] {
		return args
	}
	return append([]string{yolo}, args...)
}

// runLocalRunnerPassthrough execs the runner binary in-place with inherited
// stdio — the degenerate "no yaver at all" case, so `yaver codex` without
// --machine behaves exactly like `codex`. GLM gets its provider env overlay
// (claude binary pointed at z.ai) when the local vault can supply it.
func runLocalRunnerPassthrough(runnerID string, args []string) {
	rc := builtinRunners[runnerID]
	if err := CheckRunnerBinary(rc.Command); err != nil {
		fmt.Fprintf(os.Stderr, "%s (%s) is not installed — `yaver runner-auth setup %s` installs it\n",
			rc.Name, rc.Command, runnerID)
		os.Exit(1)
	}
	cmd := exec.Command(rc.Command, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), runnerProviderEnv(runnerID)...)
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", rc.Command, err)
		os.Exit(1)
	}
}

// runRemoteRunnerPTY resolves the device (alias / id prefix / name / primary /
// secondary — same resolver as `yaver shell` and `yaver ping`), dials the
// agent's owner-only /ws/runner endpoint over the best probed transport
// (LAN → mesh → public → relay), and bridges the local terminal raw.
func runRemoteRunnerPTY(machine, runnerID string, args []string, cwd, session string, chrome bool) error {
	candidates, token, err := resolveRemoteAgentCandidates(machine)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no reachable transport to %q — is the device online?", machine)
	}
	c := candidates[0]
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return fmt.Errorf("unsupported base URL scheme: %s", c.BaseURL)
	}

	headers := http.Header{}
	for k, v := range c.Headers {
		headers.Set(k, v)
		if strings.EqualFold(k, "Authorization") {
			token = strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
		}
	}

	// State-aware preflight: yaver-auth level is already proven (we resolved
	// the device and probed a transport as this user); now check the RUNNER's
	// own OAuth on the target and repair it before opening a TUI that would
	// just show a login screen — mirror from this machine when we hold the
	// credential, else drive the headless device-auth flow inline.
	if err := preflightRemoteRunnerAuth(c.BaseURL, token, headers, machine, runnerID); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("token", token)
	q.Set("runner", runnerID)
	if chrome {
		q.Set("chrome", "1")
	}
	if strings.TrimSpace(cwd) != "" {
		q.Set("cwd", cwd)
	}
	if strings.TrimSpace(session) != "" {
		q.Set("name", session)
	}
	for _, a := range args {
		q.Add("arg", a)
	}
	wsURL := base + "/ws/runner?" + q.Encode()

	dialer := &websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("connect to %s: forbidden — runner PTY is owner-only", machine)
		}
		if resp != nil {
			return fmt.Errorf("connect to %s: %s (%d)", machine, err, resp.StatusCode)
		}
		return fmt.Errorf("connect to %s: %w", machine, err)
	}
	defer conn.Close()

	// The one line of chrome yaver is allowed before handing the screen to
	// the runner's TUI. Disconnecting (or tmux Ctrl-b d) leaves the session
	// running; the same command reattaches.
	fmt.Fprintf(os.Stderr, "→ %s on %s via %s — session persists on disconnect; rerun this command to reattach (tmux detach: Ctrl-b d)\r\n",
		runnerID, machine, c.Kind)
	return bridgeTerminal(conn)
}

// preflightRemoteRunnerAuth checks the runner's install + OAuth state on the
// target agent and repairs what it can, headlessly:
//
//  1. GET /runner-auth/status — authConfigured? done.
//  2. Not authed but THIS machine holds the credential file → push it via the
//     P2P mirror (never through Convex) and proceed.
//  3. Nothing to mirror → drive the target's device-auth flow inline: the
//     target box runs `codex login --device-auth` / `claude auth login`, we
//     print the URL (+ code for codex; stdin paste-back for claude) in this
//     terminal and poll until signed in. Works SSH-only on both ends.
//
// Never fatal on status-endpoint absence (older agents): warn and proceed —
// /ws/runner surfaces its own errors.
func preflightRemoteRunnerAuth(baseURL, token string, headers http.Header, machine, runnerID string) error {
	client := &http.Client{Timeout: 20 * time.Second}
	do := func(method, path string, body io.Reader) (*http.Response, error) {
		req, err := http.NewRequest(method, strings.TrimRight(baseURL, "/")+path, body)
		if err != nil {
			return nil, err
		}
		for k := range headers {
			req.Header.Set(k, headers.Get(k))
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return client.Do(req)
	}

	fetchRow := func() (row *runnerAuthStatusRow, supported bool) {
		resp, err := do(http.MethodGet, "/runner-auth/status", nil)
		if err != nil {
			return nil, false
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, false
		}
		var out struct {
			Runners []runnerAuthStatusRow `json:"runners"`
		}
		if json.NewDecoder(resp.Body).Decode(&out) != nil {
			return nil, true
		}
		for i := range out.Runners {
			if normalizeRunnerID(out.Runners[i].ID) == runnerID {
				return &out.Runners[i], true
			}
		}
		return nil, true
	}

	row, supported := fetchRow()
	if !supported {
		fmt.Fprintf(os.Stderr, "→ %s: runner preflight unavailable (older agent?) — continuing\r\n", machine)
		return nil
	}
	if row == nil {
		fmt.Fprintf(os.Stderr, "→ %s: agent does not report runner %s — continuing\r\n", machine, runnerID)
		return nil
	}
	if !row.Installed {
		return fmt.Errorf("%s is not installed on %s — run `yaver exec %s -- npm install -g %s` or re-provision (bootstrap installs runners now)",
			runnerID, machine, machine, runnerNpmPackage(runnerID))
	}
	if strings.Contains(strings.ToLower(row.Error), "blocking the sandbox") {
		return fmt.Errorf("%s: Linux is blocking codex's sandbox (userns sysctls) — apply /etc/sysctl.d/99-yaver-runner-sandbox.conf on the box (re-provision does this automatically)", machine)
	}
	if row.AuthConfigured {
		return nil
	}

	// 2) Mirror from this machine when we hold the credential.
	switch runnerID {
	case "claude", "codex", "opencode":
		if _, err := ReadLocalRunnerCredential(runnerID); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, merr := PushMirrorToPeer(ctx, runnerID, baseURL, token, func(req *http.Request) (*http.Response, error) {
				for k := range headers {
					if req.Header.Get(k) == "" {
						req.Header.Set(k, headers.Get(k))
					}
				}
				return client.Do(req)
			}); merr == nil {
				fmt.Fprintf(os.Stderr, "→ mirrored %s auth from this machine to %s\r\n", runnerID, machine)
				return nil
			} else {
				fmt.Fprintf(os.Stderr, "→ %s auth mirror to %s failed (%v) — falling back to device-auth\r\n", runnerID, machine, merr)
			}
		}
	case "glm":
		return fmt.Errorf("GLM on %s needs its z.ai key in the runtime vault — run runner_auth_setup glm (or `yaver runner-auth set glm --zai-api-key …`) against that machine", machine)
	}

	// 3) Headless device-auth on the target, driven from this terminal.
	return runHeadlessRunnerDeviceAuth(do, machine, runnerID)
}

func runnerNpmPackage(runnerID string) string {
	switch runnerID {
	case "claude", "glm":
		return "@anthropic-ai/claude-code"
	case "codex":
		return "@openai/codex"
	case "opencode":
		return "opencode-ai"
	}
	return runnerID
}

func runHeadlessRunnerDeviceAuth(do func(string, string, io.Reader) (*http.Response, error), machine, runnerID string) error {
	startBody, _ := json.Marshal(map[string]string{"runner": runnerID})
	resp, err := do(http.MethodPost, "/runner-auth/browser/start", strings.NewReader(string(startBody)))
	if err != nil {
		return fmt.Errorf("start %s device-auth on %s: %w", runnerID, machine, err)
	}
	var started struct {
		Session runnerBrowserAuthSession `json:"session"`
		Error   string                   `json:"error"`
	}
	derr := json.NewDecoder(resp.Body).Decode(&started)
	resp.Body.Close()
	if derr != nil || started.Session.ID == "" {
		if started.Error != "" {
			return fmt.Errorf("%s device-auth on %s: %s", runnerID, machine, started.Error)
		}
		return fmt.Errorf("%s is not signed in on %s and the device-auth flow could not start — run `yaver code auth %s` or sign in on the box once", runnerID, machine, runnerID)
	}
	fmt.Fprintf(os.Stderr, "→ %s is not signed in on %s — starting headless sign-in\r\n", runnerID, machine)

	sawURL, sawCode, promptedPaste := "", "", false
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		sresp, serr := do(http.MethodGet, "/runner-auth/browser/status?id="+url.QueryEscape(started.Session.ID), nil)
		if serr != nil {
			continue
		}
		var out struct {
			Session runnerBrowserAuthSession `json:"session"`
		}
		derr := json.NewDecoder(sresp.Body).Decode(&out)
		sresp.Body.Close()
		if derr != nil {
			continue
		}
		snap := out.Session
		if snap.OpenURL != "" && snap.OpenURL != sawURL {
			sawURL = snap.OpenURL
			fmt.Fprintf(os.Stderr, "\r\n  Open on any device:  %s\r\n", snap.OpenURL)
		}
		if snap.Code != "" && snap.Code != sawCode {
			sawCode = snap.Code
			fmt.Fprintf(os.Stderr, "  Enter this code:     %s\r\n\r\n", snap.Code)
		}
		// Claude's flow hands the user a long code to paste BACK into the
		// CLI. We're that CLI surface — read it from stdin and forward.
		if sawURL != "" && sawCode == "" && !promptedPaste &&
			(runnerID == "claude" || runnerID == "glm") && snap.Status == "awaiting_browser" {
			promptedPaste = true
			fmt.Fprintf(os.Stderr, "  Paste the authentication code here and press Enter:\r\n  > ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line != "" {
				codeBody, _ := json.Marshal(map[string]string{"code": line})
				cresp, cerr := do(http.MethodPost, "/runner-auth/browser/submit-code?id="+url.QueryEscape(started.Session.ID), strings.NewReader(string(codeBody)))
				if cerr == nil {
					cresp.Body.Close()
				}
			}
		}
		switch snap.Status {
		case "completed":
			fmt.Fprintf(os.Stderr, "→ %s signed in on %s\r\n", runnerID, machine)
			return nil
		case "failed", "cancelled":
			msg := snap.Error
			if msg == "" {
				msg = snap.Detail
			}
			return fmt.Errorf("%s sign-in on %s %s: %s", runnerID, machine, snap.Status, msg)
		}
		if snap.AuthConfigured {
			fmt.Fprintf(os.Stderr, "→ %s signed in on %s\r\n", runnerID, machine)
			return nil
		}
	}
	return fmt.Errorf("%s sign-in on %s timed out after 6 minutes — rerun the command to try again", runnerID, machine)
}

func printRunnerPassthroughUsage(runnerName string) {
	fmt.Printf(`yaver %[1]s — run %[1]s's own TUI locally or on a remote yaver machine

Usage:
  yaver %[1]s [%[1]s args...]                     local passthrough (exactly like running %[1]s)
  yaver %[1]s [%[1]s args...] --machine=<device>  same TUI, running on the remote machine

Yaver-owned flags (everything else goes verbatim to %[1]s):
  --machine, -m <device>   device alias / id / name / primary / secondary
  --chrome                 keep the remote tmux status bar visible (default: hidden)
  --yaver-cwd=<path>       remote working directory (default: agent work dir)
  --yaver-session=<name>   remote tmux session name (default: yaver-<runner>)
  --yaver-safe             do NOT inject the default no-approvals flag
  --yaver-help             this help

No approvals by default: claude/glm get --dangerously-skip-permissions and
codex gets --dangerously-bypass-approvals-and-sandbox automatically (skipped
for management subcommands, when you pass your own permission/sandbox flags,
or with --yaver-safe). opencode's TUI has no such flag — configure its
permission settings instead.

Remote sessions run inside tmux on the target machine: a dropped connection
leaves the runner alive; rerunning the same command reattaches. The same
session can be adopted from the Yaver mobile app (tmux_adopt_session).
`, runnerName)
}
