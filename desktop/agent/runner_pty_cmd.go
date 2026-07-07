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
	"fmt"
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

	if strings.TrimSpace(machine) == "" {
		runLocalRunnerPassthrough(runnerID, passthrough)
		return
	}
	if err := runRemoteRunnerPTY(machine, runnerID, passthrough, cwd, session, chrome); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", runnerName, err)
		os.Exit(1)
	}
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
  --yaver-help             this help

Remote sessions run inside tmux on the target machine: a dropped connection
leaves the runner alive; rerunning the same command reattaches. The same
session can be adopted from the Yaver mobile app (tmux_adopt_session).
`, runnerName)
}
