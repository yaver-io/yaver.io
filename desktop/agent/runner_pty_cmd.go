package main

// runner_pty_cmd.go — `yaver claude|codex|opencode|glm [runner args…] [--machine=<device>]`:
// run a coding agent's own interactive TUI, locally or on a remote yaver
// machine, with yaver contributing ONLY device resolution (alias / id / name /
// primary / secondary), the tunnel, and tmux persistence on the far end.
//
//	yaver claude --dangerously-skip-permissions --machine=myhetzner
//	yaver codex --dangerously-bypass-approvals-and-sandbox --machine=mypi
//	yaver opencode --machine primary
//	yaver codex remote                # sugar for --machine=primary
//	yaver claude remote               # same, any runner
//	yaver codex                       # no machine: plain local exec passthrough
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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// errTransportUnreach marks a preflight failure caused by the network dial
// itself (dead route) rather than an auth/install problem — the caller falls
// through to the next transport candidate instead of aborting.
var errTransportUnreach = errors.New("transport unreachable")

func isTransportReachError(err error) bool {
	return errors.Is(err, errTransportUnreach)
}

// runnerPTYMinAgentVersion is the first agent build that serves /ws/runner +
// /runner-auth/status (the remote runner TUI, shipped 1.99.274). A remote older
// than this can't host a runner PTY at all; we detect it from /health.version
// and tell the user to update, instead of letting the WS upgrade 404 opaquely.
const runnerPTYMinAgentVersion = "1.99.274"

// remoteReachResult is what the /health preflight learned about a box.
type remoteReachResult struct {
	candidate   RemoteAgentCandidate
	version     string
	authExpired bool
}

// preflightRemoteRunner runs the layered reachability diagnostic and returns
// the first candidate whose agent answered /health (with its version). The
// layers, in order:
//
//  1. yaver: probe /health across every transport candidate. A hit here means
//     everything below is fine — return immediately (the common path).
//  2. internet: /health missed everywhere → is THIS machine even online? A dead
//     local uplink is diagnosed here so we don't blame the box.
//  3. tailscale: the box is only reachable over the tailnet but our tailnet is
//     down → tell the user to `tailscale up`, not to restart the box.
//  4. auto-repair: internet + route look fine but the remote agent is down →
//     best-effort restart it over SSH and re-probe once before giving up.
//
// quiet (the zero-chrome default) suppresses the progress narration; only the
// final diagnosis and any repair ACTION ever print.
func preflightRemoteRunner(candidates []RemoteAgentCandidate, token, machine string, quiet bool) (*remoteReachResult, error) {
	// Layer 1 — yaver /health.
	if r, _ := probeRemoteAgentHealth(candidates, token); r != nil {
		return r, nil
	}

	// Layer 2 — this machine's internet. If we can't reach the public internet
	// at all, the box is almost certainly fine and the problem is local.
	if !localInternetUp() {
		return nil, fmt.Errorf("%s unreachable — but THIS machine has no internet (can't reach 1.1.1.1/8.8.8.8). Fix your connection (Wi-Fi / captive portal / VPN) and retry; the box is probably fine.", machine)
	}

	// Layer 3 — tailnet. When every candidate route rides Tailscale CGNAT
	// (100.64/10) and our own tailnet is down, restarting the box won't help —
	// the tunnel is the broken link.
	if candidatesAreTailscaleOnly(candidates) && !localTailscaleUp() {
		return nil, fmt.Errorf("%s is only reachable over Tailscale, but your tailnet is down here — run `tailscale up` (or `yaver mesh up`), then retry.", machine)
	}

	// Layer 4 — remote agent looks down while the network is fine. Try to bring
	// it back over SSH, then re-probe. Best-effort: SSH may not be reachable
	// (no key, box firewalled), in which case we fall through to the diagnosis.
	if !quiet {
		fmt.Fprintf(os.Stderr, "→ %s: agent not answering on any transport — attempting SSH restart…\r\n", machine)
	}
	if repairRemoteAgentOverSSH(machine, quiet) {
		for attempt := 0; attempt < 4; attempt++ {
			time.Sleep(2 * time.Second)
			if r, _ := probeRemoteAgentHealth(candidates, token); r != nil {
				fmt.Fprintf(os.Stderr, "→ %s: agent is back up after SSH restart\r\n", machine)
				return r, nil
			}
		}
	}

	// Nothing worked — emit the per-transport diagnosis.
	_, fails := probeRemoteAgentHealthDetailed(candidates, token)
	var b strings.Builder
	fmt.Fprintf(&b, "%s unreachable — its yaver agent did not answer on any transport:", machine)
	for _, f := range fails {
		kind := strings.TrimSpace(f.kind)
		if kind == "" {
			kind = "transport"
		}
		fmt.Fprintf(&b, "\n  %-10s %s", kind, f.cause)
	}
	b.WriteString("\n\nInternet + tailnet look fine here, so the box's agent is down (or bound to a different port than its stale device row).")
	b.WriteString("\nBring it back: `yaver ssh " + machine + "` (auto-recovers the agent) · refresh the tunnel: `yaver primary auth`.")
	return nil, errors.New(b.String())
}

type probeFail struct{ kind, cause string }

// probeRemoteAgentHealth probes /health across candidates (best-ordered) and
// returns the first that answers 200 with a parseable body, else nil.
func probeRemoteAgentHealth(candidates []RemoteAgentCandidate, token string) (*remoteReachResult, []probeFail) {
	return probeRemoteAgentHealthDetailed(candidates, token)
}

// probeRemoteAgentHealthDetailed is probeRemoteAgentHealth plus the per-
// transport failure list (used to build the final diagnosis).
func probeRemoteAgentHealthDetailed(candidates []RemoteAgentCandidate, token string) (*remoteReachResult, []probeFail) {
	client := &http.Client{Timeout: 6 * time.Second}
	var fails []probeFail
	for i := range candidates {
		c := candidates[i]
		base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
		req, err := http.NewRequest(http.MethodGet, base+"/health", nil)
		if err != nil {
			fails = append(fails, probeFail{c.Kind, err.Error()})
			continue
		}
		ctoken := token
		for k, v := range c.Headers {
			req.Header.Set(k, v)
			if strings.EqualFold(k, "Authorization") {
				ctoken = strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
			}
		}
		if req.Header.Get("Authorization") == "" && ctoken != "" {
			req.Header.Set("Authorization", "Bearer "+ctoken)
		}
		resp, err := client.Do(req)
		if err != nil {
			fails = append(fails, probeFail{c.Kind, condenseTransportError(err)})
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fails = append(fails, probeFail{c.Kind, fmt.Sprintf("HTTP %d", resp.StatusCode)})
			continue
		}
		var h struct {
			Version     string `json:"version"`
			AuthExpired bool   `json:"authExpired"`
		}
		_ = json.Unmarshal(body, &h)
		return &remoteReachResult{candidate: c, version: h.Version, authExpired: h.AuthExpired}, fails
	}
	return nil, fails
}

// localInternetUp is a fast, ICMP-free check that THIS machine can reach the
// public internet — TCP/443 to 1.1.1.1 or 8.8.8.8 (works through networks that
// drop ping). Same probe net_doctor uses.
func localInternetUp() bool {
	if ok, _ := netReachTCP("1.1.1.1", 443, 2500); ok {
		return true
	}
	ok, _ := netReachTCP("8.8.8.8", 443, 2500)
	return ok
}

// candidatesAreTailscaleOnly reports whether every candidate route depends on
// the tailnet (CGNAT 100.64/10 or classified "tailscale") — i.e. there is no
// LAN / public / relay path that could work with Tailscale down.
func candidatesAreTailscaleOnly(candidates []RemoteAgentCandidate) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, c := range candidates {
		if c.Kind == "tailscale" {
			continue
		}
		if u, err := url.Parse(strings.TrimSpace(c.BaseURL)); err == nil {
			if ip := net.ParseIP(u.Hostname()); ip != nil && isCGNATTailscaleIP(u.Hostname()) {
				continue
			}
		}
		return false
	}
	return true
}

// repairRemoteAgentOverSSH resolves an SSH route to the box and runs a POSIX
// restart script that re-launches the agent if it isn't healthy — the non-
// interactive cousin of `yaver ssh <dev>`'s recovery path. Returns true only
// when the ssh command completed (0 exit); the caller then re-probes /health.
// Deliberately conservative: it never runs an interactive auth flow, so it is
// safe to fire automatically. A box that needs a fresh login still surfaces
// that when the runner auth preflight runs afterward.
func repairRemoteAgentOverSSH(machine string, quiet bool) bool {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return false
	}
	dest := resolveSSHHost(machine)
	if strings.TrimSpace(dest) == "" {
		return false
	}
	if !quiet {
		fmt.Fprintf(os.Stderr, "→ ssh %s → restarting yaver agent\r\n", dest)
	}
	args := sshArgsWithSurvivability(dest, []string{"sh", "-lc", remoteAgentRestartScript})
	args = append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=8"}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sshPath, args...)
	out, runErr := cmd.CombinedOutput()
	if s := strings.TrimSpace(string(out)); s != "" && !quiet {
		fmt.Fprintf(os.Stderr, "  %s\r\n", strings.ReplaceAll(s, "\n", "\r\n  "))
	}
	return runErr == nil
}

// remoteAgentRestartScript restarts the far-side agent if it is installed but
// not healthy, preferring a managed service unit and falling back to a
// backgrounded `yaver serve`. No auth prompts — purely a restart.
const remoteAgentRestartScript = `
if ! command -v yaver >/dev/null 2>&1; then
  echo "[yaver] not installed on this box — npm install -g yaver-cli"; exit 0
fi
if yaver status >/dev/null 2>&1; then echo "[yaver] agent already healthy"; exit 0; fi
echo "[yaver] agent down — restarting"
systemctl --user restart yaver 2>/dev/null \
  || sudo -n systemctl restart yaver 2>/dev/null \
  || (pkill -f 'yaver serve' 2>/dev/null; nohup yaver serve >"${TMPDIR:-/tmp}/yaver-serve.log" 2>&1 &)
sleep 1
echo "[yaver] restart issued"
`

// condenseTransportError collapses a net/http dial error to the phrase that
// actually helps ("connection refused", "timeout", "no route to host") so the
// per-transport diagnostic reads cleanly instead of echoing the full
// "dial tcp 100.x.y.z:18090: connect: …" chain for every candidate.
func condenseTransportError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused (agent not listening)"
	case strings.Contains(s, "i/o timeout"), strings.Contains(s, "deadline exceeded"), strings.Contains(s, "Client.Timeout"):
		return "timed out (host down or firewalled)"
	case strings.Contains(s, "no route to host"):
		return "no route to host"
	case strings.Contains(s, "no such host"), strings.Contains(s, "server misbehaving"):
		return "DNS lookup failed"
	case strings.Contains(s, "connection reset"):
		return "connection reset"
	default:
		if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
			return s[i+2:]
		}
		return s
	}
}

// runnerPassthroughOpts is the parsed split of a `yaver <runner> …` invocation
// into yaver-owned knobs and the argv shipped verbatim to the runner.
type runnerPassthroughOpts struct {
	machine     string
	chrome      bool
	cwd         string
	session     string
	yaverSafe   bool
	showHelp    bool
	sync        bool // mirror the CWD project onto the box (implied by `remote`)
	noSync      bool // explicit opt-out
	passthrough []string
}

// parseRunnerPassthrough peels the yaver-owned flags (--machine/-m, the `remote`
// sugar, --chrome, --yaver-*) off the argv and leaves everything else untouched
// for the runner. Kept pure (no os.Exit / no I/O) so the arg contract is unit-
// testable; runRunnerPassthrough wraps it with the runner dispatch.
func parseRunnerPassthrough(args []string) runnerPassthroughOpts {
	opts := runnerPassthroughOpts{passthrough: make([]string, 0, len(args))}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--machine" || a == "-m":
			if i+1 < len(args) {
				i++
				opts.machine = args[i]
			}
		case strings.HasPrefix(a, "--machine="):
			opts.machine = strings.TrimPrefix(a, "--machine=")
		case a == "remote" && i == 0:
			// `yaver codex remote` — sugar for --machine=primary + project
			// mirror, the common "just run this on my main box, in this repo"
			// case. Only the FIRST token counts so a quoted prompt that happens
			// to be the word "remote" (rare) isn't hijacked. An explicit
			// --machine=<device> still wins. For a non-primary box, use
			// --machine=<device> --yaver-sync.
			if strings.TrimSpace(opts.machine) == "" {
				opts.machine = "primary"
			}
			opts.sync = true
		case a == "--yaver-sync":
			opts.sync = true
		case a == "--yaver-no-sync":
			opts.noSync = true
		case a == "--chrome":
			opts.chrome = true
		case strings.HasPrefix(a, "--yaver-cwd="):
			opts.cwd = strings.TrimPrefix(a, "--yaver-cwd=")
		case strings.HasPrefix(a, "--yaver-session="):
			opts.session = strings.TrimPrefix(a, "--yaver-session=")
		case a == "--yaver-safe":
			opts.yaverSafe = true
		case a == "--yaver-help":
			opts.showHelp = true
		default:
			opts.passthrough = append(opts.passthrough, a)
		}
	}
	return opts
}

// runTopLevelRemote backs `yaver remote …`: run the user's DEFAULT runner on
// the primary device, project-aware. It resolves the default runner from config
// (falling back to claude) and forwards to the runner passthrough with `remote`
// prepended, so all the machine-resolution + mirror logic is shared.
func runTopLevelRemote(args []string) {
	runner := "claude"
	if _, code, err := loadCodeConfig(); err == nil && code != nil {
		if r := normalizeRunnerID(strings.TrimSpace(code.Runner)); r != "" && IsSupportedRunner(r) {
			runner = r
		}
	}
	runRunnerPassthrough(runner, append([]string{"remote"}, args...))
}

// remoteMobileSubcommands are the verbs the legacy `yaver remote` (paired-phone
// control) owns. `yaver remote` dispatches to it for these; anything else (a
// coding prompt / runner flags) routes to CWD-aware remote coding.
var remoteMobileSubcommands = map[string]bool{
	"detect": true, "list": true, "ls": true, "phones": true,
	"insert": true, "push": true, "help": true, "-h": true, "--help": true,
}

// runRemoteDispatch splits the overloaded `yaver remote …`: the historical
// paired-phone control verbs keep their behavior; everything else (a bare prompt
// like `yaver remote "fix the bug"`, or runner flags) becomes CWD-aware remote
// coding on the primary device with the default runner. `yaver remote` with no
// args keeps the phone-control help, its long-standing behavior.
func runRemoteDispatch(args []string) {
	if len(args) == 0 || remoteMobileSubcommands[strings.ToLower(strings.TrimSpace(args[0]))] {
		runRemote(args)
		return
	}
	runTopLevelRemote(args)
}

func runRunnerPassthrough(runnerName string, args []string) {
	opts := parseRunnerPassthrough(args)
	if opts.showHelp {
		printRunnerPassthroughUsage(runnerName)
		return
	}
	passthrough := opts.passthrough

	runnerID := normalizeRunnerID(runnerName)
	if !IsSupportedRunner(runnerID) {
		fmt.Fprintf(os.Stderr, "%s: unsupported runner (expected one of: %s)\n",
			runnerName, strings.Join(supportedRunnerIDs, ", "))
		os.Exit(1)
	}
	if !opts.yaverSafe {
		passthrough = applyRunnerYoloDefaults(runnerID, passthrough)
	}

	if strings.TrimSpace(opts.machine) == "" {
		runLocalRunnerPassthrough(runnerID, passthrough)
		return
	}
	// Mirror the CWD project onto the box unless the caller opted out or pinned
	// an explicit remote dir (--yaver-cwd wins over auto-detection).
	mirror := opts.sync && !opts.noSync && strings.TrimSpace(opts.cwd) == ""
	if err := runRemoteRunnerPTY(opts.machine, runnerID, passthrough, opts.cwd, opts.session, opts.chrome, mirror); err != nil {
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
// secondary — same resolver as `yaver shell` and `yaver ping`) and dials the
// agent's owner-only /ws/runner endpoint. It walks EVERY transport candidate
// the resolver built (same-lan → tailscale → direct/public → relay), trying
// the next on any connection failure — so it is tailscale-AWARE (that route
// is preferred when up) but never tailscale-DEPENDENT (a down tailnet falls
// straight through to the public IP or relay). Then bridges the local
// terminal raw.
//
// Zero chrome by default: exactly like `ssh box` + `codex` — no yaver banner,
// no tmux status bar, just the runner's TUI. `--chrome` adds the banner + the
// remote tmux status line.
//
// When mirror is set (the `remote` sugar / --yaver-sync), the CWD's git project
// is first cloned/pulled onto the box and the runner is cd'd into the matching
// subdir — CWD-aware remote coding.
func runRemoteRunnerPTY(machine, runnerID string, args []string, cwd, session string, chrome, mirror bool) error {
	candidates, token, err := resolveRemoteAgentCandidates(machine)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no reachable transport to %q — is the device online?", machine)
	}

	// Layered reachability diagnostic BEFORE any auth work or WS upgrade. A box
	// that heartbeats "online" in Convex can still have a dead / old / wrong-
	// port agent (stale device row), and the failure can live at any layer:
	// this machine's internet, the tailnet, or the remote agent itself. We walk
	// them in order (internet → tailscale → yaver /health), so the message
	// pinpoints the actual broken layer — and, when only the remote agent is
	// down, best-effort restarts it over SSH — instead of leaking a bare
	// "dial tcp …:18090: connection refused" out of the WS dial.
	reach, rerr := preflightRemoteRunner(candidates, token, machine, !chrome)
	if rerr != nil {
		return rerr
	}
	if rv := strings.TrimSpace(reach.version); rv != "" {
		if semverLess(rv, runnerPTYMinAgentVersion) {
			return fmt.Errorf("%s runs yaver agent v%s, which predates the remote runner TUI (needs v%s+) — update the box:\n  yaver exec %s -- npm install -g yaver-cli@latest\nthen restart its agent (`yaver ssh %s` → `yaver serve`)",
				machine, rv, runnerPTYMinAgentVersion, machine, machine)
		}
		if semverLess(rv, version) {
			fmt.Fprintf(os.Stderr, "→ note: %s runs agent v%s while this CLI is v%s — consider `yaver exec %s -- npm install -g yaver-cli@latest`\r\n", machine, rv, version, machine)
		}
	}
	if reach.authExpired {
		fmt.Fprintf(os.Stderr, "→ note: %s reports its yaver auth token expired — run `yaver primary auth` if the runner sign-in stalls\r\n", machine)
	}

	// CWD-aware mirroring: clone/pull this project onto the box and point the
	// runner at the matching subdir. Runs against the candidate that just
	// answered /health (reach.candidate) — the one route we know is live.
	if mirror {
		repo, _ := detectLocalRepoContext()
		if repo != nil {
			warnLocalRepoState(repo, machine)
			c := reach.candidate
			base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
			headers := http.Header{}
			ctoken := token
			for k, v := range c.Headers {
				headers.Set(k, v)
				if strings.EqualFold(k, "Authorization") {
					ctoken = strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
				}
			}
			remoteCwd, perr := prepareRemoteRepo(base, ctoken, headers, repo, c.DeviceID, !chrome)
			if perr != nil {
				return perr
			}
			cwd = remoteCwd
		} else if !chrome {
			fmt.Fprintf(os.Stderr, "→ not inside a git repo — running in %s's default work dir\r\n", machine)
		}
	}

	q := url.Values{}
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

	preflightDone := false
	var lastErr error
	for _, c := range candidates {
		base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
		var wsBase string
		switch {
		case strings.HasPrefix(base, "https://"):
			wsBase = "wss://" + strings.TrimPrefix(base, "https://")
		case strings.HasPrefix(base, "http://"):
			wsBase = "ws://" + strings.TrimPrefix(base, "http://")
		default:
			lastErr = fmt.Errorf("unsupported base URL scheme: %s", c.BaseURL)
			continue
		}

		ctoken := token
		headers := http.Header{}
		for k, v := range c.Headers {
			headers.Set(k, v)
			if strings.EqualFold(k, "Authorization") {
				ctoken = strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
			}
		}

		// State-aware preflight, once, against the first candidate that
		// answers: check the RUNNER's own OAuth on the target and repair it
		// (mirror our local credential, else headless device-auth) before
		// opening a TUI that would just show a login screen. If this
		// candidate is unreachable the preflight errs → try the next route.
		if !preflightDone {
			if perr := preflightRemoteRunnerAuth(base, ctoken, headers, machine, runnerID, !chrome); perr != nil {
				if isTransportReachError(perr) {
					lastErr = perr
					continue // route dead — fall through to the next candidate
				}
				return perr // real auth/install/sandbox problem — surface it
			}
			preflightDone = true
		}

		qv := q
		qv.Set("token", ctoken)
		wsURL := wsBase + "/ws/runner?" + qv.Encode()

		dialer := &websocket.Dialer{HandshakeTimeout: 15 * time.Second}
		conn, resp, derr := dialer.Dial(wsURL, headers)
		if derr != nil {
			if resp != nil && resp.StatusCode == http.StatusForbidden {
				return fmt.Errorf("connect to %s: forbidden — runner PTY is owner-only", machine)
			}
			lastErr = derr
			continue // this transport failed — try the next candidate
		}
		defer conn.Close()

		if chrome {
			fmt.Fprintf(os.Stderr, "→ %s on %s via %s — session persists on disconnect; rerun to reattach (tmux detach: Ctrl-b d)\r\n",
				runnerID, machine, c.Kind)
		}
		return bridgeTerminal(conn)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no reachable transport")
	}
	return fmt.Errorf("connect to %s: %w", machine, lastErr)
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
// When quiet is set (zero-chrome default), informational lines are suppressed;
// only auth ACTIONS (device-auth URL, mirror) and hard errors ever print.
// A network-unreachable target returns an errTransportUnreach-wrapped error so
// the caller can fall through to the next transport candidate; a reachable
// agent that merely lacks the endpoint (older build) proceeds silently.
func preflightRemoteRunnerAuth(baseURL, token string, headers http.Header, machine, runnerID string, quiet bool) error {
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

	// reachErr is non-nil only when the network dial itself failed (dead
	// route); an HTTP error (404/older agent) leaves it nil.
	fetchRow := func() (row *runnerAuthStatusRow, hasEndpoint bool, reachErr error) {
		resp, err := do(http.MethodGet, "/runner-auth/status", nil)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", errTransportUnreach, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, false, nil
		}
		var out struct {
			Runners []runnerAuthStatusRow `json:"runners"`
		}
		if json.NewDecoder(resp.Body).Decode(&out) != nil {
			return nil, true, nil
		}
		for i := range out.Runners {
			if normalizeRunnerID(out.Runners[i].ID) == runnerID {
				return &out.Runners[i], true, nil
			}
		}
		return nil, true, nil
	}

	row, hasEndpoint, reachErr := fetchRow()
	if reachErr != nil {
		return reachErr // dead route — caller tries the next candidate
	}
	if !hasEndpoint {
		if !quiet {
			fmt.Fprintf(os.Stderr, "→ %s: runner preflight unavailable (older agent) — continuing\r\n", machine)
		}
		return nil
	}
	if row == nil {
		if !quiet {
			fmt.Fprintf(os.Stderr, "→ %s: agent does not report runner %s — continuing\r\n", machine, runnerID)
		}
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
				// Auth repair happened — always worth one line even in quiet
				// mode (it explains the brief pause before the TUI).
				fmt.Fprintf(os.Stderr, "→ mirrored %s auth from this machine to %s\r\n", runnerID, machine)
				return nil
			} else if !quiet {
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

// runRemoteAttachPicker lists live runner PTY sessions on the machine and
// reattaches: exactly one match (optionally filtered by runnerFilter) attaches
// silently; several present a numbered picker (like Claude Code's session
// chooser); none prints a hint. Reattach dials /ws/runner with the session's
// tmux name, so `tmux new-session -A` drops back into the exact same TUI.
func runRemoteAttachPicker(machine, runnerFilter string, chrome bool) error {
	candidates, token, err := resolveRemoteAgentCandidates(machine)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no reachable transport to %q — is the device online?", machine)
	}

	// Find the first reachable candidate and list its sessions there.
	var sessions []RunnerPTYSession
	var usable *RemoteAgentCandidate
	var lastErr error
	for i := range candidates {
		c := candidates[i]
		base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
		headers := http.Header{}
		ctoken := token
		for k, v := range c.Headers {
			headers.Set(k, v)
			if strings.EqualFold(k, "Authorization") {
				ctoken = strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
			}
		}
		ss, serr := fetchRunnerSessions(base, ctoken, headers)
		if serr != nil {
			lastErr = serr
			continue
		}
		sessions = ss
		usable = &candidates[i]
		token = ctoken
		break
	}
	if usable == nil {
		if lastErr != nil {
			return fmt.Errorf("list sessions on %s: %w", machine, lastErr)
		}
		return fmt.Errorf("could not reach %s to list sessions", machine)
	}

	if strings.TrimSpace(runnerFilter) != "" {
		filtered := sessions[:0]
		for _, s := range sessions {
			if s.Runner == runnerFilter {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	switch len(sessions) {
	case 0:
		hint := "start one with `yaver <runner> --machine=" + machine + "`"
		if runnerFilter != "" {
			return fmt.Errorf("no live %s session on %s — %s", runnerFilter, machine, hint)
		}
		return fmt.Errorf("no live runner sessions on %s — %s", machine, hint)
	case 1:
		return runRemoteRunnerPTY(machine, sessions[0].Runner, nil, "", sessions[0].Name, chrome, false)
	default:
		chosen, perr := pickRunnerSession(machine, sessions)
		if perr != nil {
			return perr
		}
		return runRemoteRunnerPTY(machine, chosen.Runner, nil, "", chosen.Name, chrome, false)
	}
}

func fetchRunnerSessions(baseURL, token string, headers http.Header) ([]RunnerPTYSession, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/runner/sessions", nil)
	if err != nil {
		return nil, err
	}
	for k := range headers {
		req.Header.Set(k, headers.Get(k))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("remote agent is too old to list runner sessions (update it)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		Sessions []RunnerPTYSession `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

func pickRunnerSession(machine string, sessions []RunnerPTYSession) (RunnerPTYSession, error) {
	fmt.Fprintf(os.Stderr, "Live runner sessions on %s:\n", machine)
	for i, s := range sessions {
		tag := ""
		if s.Attached {
			tag = " (attached elsewhere)"
		}
		fmt.Fprintf(os.Stderr, "  [%d] %-16s %s%s\n", i+1, s.Runner, s.Name, tag)
	}
	fmt.Fprintf(os.Stderr, "Pick a session [1-%d]: ", len(sessions))
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	idx := 0
	if _, err := fmt.Sscanf(line, "%d", &idx); err != nil || idx < 1 || idx > len(sessions) {
		return RunnerPTYSession{}, fmt.Errorf("invalid selection %q", line)
	}
	return sessions[idx-1], nil
}

func printRunnerPassthroughUsage(runnerName string) {
	fmt.Printf(`yaver %[1]s — run %[1]s's own TUI locally or on a remote yaver machine

Usage:
  yaver %[1]s [%[1]s args...]                     local passthrough (exactly like running %[1]s)
  yaver %[1]s remote [%[1]s args...]              same TUI on your primary device, IN THIS PROJECT
  yaver %[1]s [%[1]s args...] --machine=<device>  same TUI, running on a specific remote machine

Project-aware remote (the "remote" word): from inside a git checkout, yaver
mirrors THIS project onto the box (clone if absent, pull if present) and cd's
the runner into the same subdir — so 'yaver %[1]s remote "fix the bug"' picks up
your current repo on the primary box. Sync is pull-from-origin: the box works on
the PUSHED branch, and you're warned if local changes aren't committed+pushed.

Yaver-owned flags (everything else goes verbatim to %[1]s):
  remote                   run on your primary device, in this project — first word only
  --machine, -m <device>   device alias / id / name / primary / secondary
  --yaver-sync             mirror this project onto --machine (implied by "remote")
  --yaver-no-sync          do NOT mirror; run in the box's default work dir
  --chrome                 keep the remote tmux status bar visible (default: hidden)
  --yaver-cwd=<path>       explicit remote working directory (overrides mirroring)
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
