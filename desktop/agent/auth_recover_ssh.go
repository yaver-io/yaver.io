// auth_recover_ssh.go — SSH as an auth-recovery transport.
//
// The HTTP recovery paths (mcp_auth_recovery.go, auth_recover.go) all
// reach the target agent over LAN / mesh / public endpoint / relay. Each
// of those can be gone exactly when recovery matters most: a box that
// dropped to bootstrap mode holds no valid token, so its relay
// registration is REJECTED ("invalid relay credentials") — the transport
// most likely to survive a NAT is the first one auth loss takes away.
// Off-LAN, that leaves nothing, and mobile's beacon auto-pair can't help
// either (the beacon is same-subnet only). The box then sits there,
// reachable over SSH, waiting for a human to type `yaver auth send`.
//
// SSH is the transport that doesn't depend on Yaver's auth state. This
// file uses it to drive the EXISTING pair window rather than inventing a
// new protocol: a bootstrap agent already serves an unauthenticated
// `/auth/pair/submit?code=<passkey>` on its own loopback, and publishes
// the rotating passkey on `/info`. So we ssh in, read the passkey from
// loopback, and POST this machine's session straight back to loopback.
//
// Two properties fall out of reusing the pair window:
//
//  1. It works against ALREADY-DEPLOYED agents. Recovery that needed a
//     new remote verb would be unshippable by construction — the box we
//     need to fix is the box we cannot deploy to.
//  2. The token never touches argv (`--data-binary @-` reads stdin, and
//     ssh carries it encrypted), so it stays out of the remote `ps`
//     table. Only the short-lived, loopback-scoped passkey is an
//     argument.
//
// `yaver ssh` supplies host resolution (LAN → mesh → Tailscale → public)
// and will bootstrap our pubkey over the same-Convex-user trust channel
// if we have no SSH access yet, so this inherits both.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"
	"strings"
	"time"
)

// agentLoopbackHTTPPort is the agent's default HTTP port. Recovery always
// dials the target's OWN loopback from inside its shell, so this is the
// port as the box sees it — never a routed/forwarded one.
const agentLoopbackHTTPPort = 18080

func agentLoopbackURL(port int) string {
	if port <= 0 {
		port = agentLoopbackHTTPPort
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// sshTargetHint picks the string we hand to `yaver ssh`. Alias first —
// it's what the user's ssh config and device book are most likely keyed
// on — then the device id, then the hostname.
func sshTargetHint(target *DeviceInfo) string {
	if target == nil {
		return ""
	}
	if a := strings.TrimSpace(target.Alias); a != "" {
		return a
	}
	if id := strings.TrimSpace(target.DeviceID); id != "" {
		return id
	}
	return strings.TrimSpace(target.Name)
}

// parseBootstrapPasskey pulls the rotating pair code out of an agent's
// /info payload. An agent that is NOT in bootstrap mode omits the field
// (and requires auth on /info at all), so a missing passkey is a real
// signal — "this box is not waiting to be paired" — not a parse failure.
func parseBootstrapPasskey(raw []byte) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", fmt.Errorf("agent returned an empty /info body")
	}
	var info map[string]interface{}
	if err := json.Unmarshal(trimmed, &info); err != nil {
		// An adopted agent requires auth on /info, so a non-JSON body
		// here usually means the box is already signed in.
		return "", fmt.Errorf("agent /info was not JSON (it may already be signed in): %s", firstErrLine(string(trimmed)))
	}
	// Type-assert rather than anyString(): anyString(nil) stringifies to
	// "<nil>", which is non-empty and would sail past a blank check as a
	// literal passkey of "<NIL>".
	code, _ := info["bootstrapPasskey"].(string)
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("agent is not in bootstrap mode (no pair window open)")
	}
	return strings.ToUpper(code), nil
}

// pairSubmitPayload is the body /auth/pair/submit expects — the same one
// `yaver auth send` posts. Refuses to build a payload from a machine that
// has nothing to give: pushing a blank token would clear the target.
func pairSubmitPayload(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("no local config")
	}
	token := strings.TrimSpace(cfg.AuthToken)
	if token == "" {
		return nil, fmt.Errorf("this machine isn't signed in — run `yaver auth` here first")
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		return nil, fmt.Errorf("this machine has no backend URL to hand over")
	}
	return json.Marshal(map[string]interface{}{
		"token":         token,
		"convexSiteUrl": convex,
	})
}

// firstErrLine trims a subprocess's stderr down to something that fits in
// an error string. Distinct from firstLine() in runner_agent_session.go,
// which falls back to "agent session" — a title default, not an error one.
func firstErrLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// runYaverSSH shells `yaver ssh <hint> -- <argv…>` with stdin/stdout
// captured. Args are passed as a vector (never a shell string) so nothing
// needs quoting and no remote shell can re-interpret them.
func runYaverSSH(ctx context.Context, hint string, stdin []byte, argv ...string) ([]byte, error) {
	yaverPath := findYaverBinary()
	full := append([]string{"ssh", hint, "--"}, argv...)
	cmd := osexec.CommandContext(ctx, yaverPath, full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		detail := firstErrLine(errBuf.String())
		if detail == "" {
			detail = err.Error()
		}
		return out.Bytes(), fmt.Errorf("ssh %s: %s", hint, detail)
	}
	return out.Bytes(), nil
}

// fetchRemoteBootstrapPasskeyOverSSH reads the target's pair code from
// its own loopback.
// Deliberately no `-f`: an already-adopted agent answers /info with 401,
// and -f would collapse that into a bare "exit 22" while discarding the
// body. Letting the response through lets parseBootstrapPasskey tell
// "already signed in" apart from "nothing is listening" — the difference
// between "nothing to do" and a real fault.
func fetchRemoteBootstrapPasskeyOverSSH(ctx context.Context, hint string, port int) (string, error) {
	raw, err := runYaverSSH(ctx, hint, nil,
		"curl", "-sS", "--max-time", "5", agentLoopbackURL(port)+"/info")
	if err != nil {
		return "", err
	}
	return parseBootstrapPasskey(raw)
}

// pushTokenToRemotePairWindowOverSSH posts our session into the target's
// open pair window. The payload goes over stdin, so the token never
// appears in the remote process table.
func pushTokenToRemotePairWindowOverSSH(ctx context.Context, hint string, port int, code string, payload []byte) error {
	url := fmt.Sprintf("%s/auth/pair/submit?code=%s", agentLoopbackURL(port), urlQueryEscape(code))
	raw, err := runYaverSSH(ctx, hint, payload,
		"curl", "-fsS", "--max-time", "10",
		"-X", "POST",
		"-H", "Content-Type: application/json",
		"--data-binary", "@-",
		url)
	if err != nil {
		return err
	}
	// The endpoint 200s with a small JSON ack; treat an explicit error
	// field as failure even on a 2xx.
	if body := bytes.TrimSpace(raw); len(body) > 0 {
		var ack map[string]interface{}
		if json.Unmarshal(body, &ack) == nil {
			if msg := strings.TrimSpace(anyString(ack["error"])); msg != "" && msg != "<nil>" {
				return fmt.Errorf("target rejected the token: %s", msg)
			}
		}
	}
	return nil
}

// recoverDeviceAuthOverSSH signs a bootstrap-mode box back in by pushing
// this machine's session over SSH. Zero interaction and no browser: the
// local session is already proof of who we are, and the target only
// accepts it while its own pair window is open.
//
// Returns the transport label on success so callers can report which path
// actually did the work.
func recoverDeviceAuthOverSSH(ctx context.Context, cfg *Config, target *DeviceInfo) (string, error) {
	hint := sshTargetHint(target)
	if hint == "" {
		return "", fmt.Errorf("device has no alias, id, or name to ssh to")
	}
	payload, err := pairSubmitPayload(cfg)
	if err != nil {
		return "", err
	}
	code, err := fetchRemoteBootstrapPasskeyOverSSH(ctx, hint, agentLoopbackHTTPPort)
	if err != nil {
		return "", err
	}
	if err := pushTokenToRemotePairWindowOverSSH(ctx, hint, agentLoopbackHTTPPort, code, payload); err != nil {
		return "", err
	}
	return "ssh:" + hint, nil
}

// waitForDeviceAuthHealthy polls until the target reports a signed-in
// lifecycle. The box needs a moment after accepting the token: it has to
// re-register with Convex and re-establish its relay tunnel before any
// other surface will call it healthy.
func waitForDeviceAuthHealthy(cfg *Config, target *DeviceInfo, timeout time.Duration) (deviceReauthProbe, error) {
	deadline := time.Now().Add(timeout)
	var last deviceReauthProbe
	for {
		last = probeOwnedDeviceReauth(cfg, target)
		switch last.State {
		case "healthy", "ready-to-connect":
			return last, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("target still not signed in: %s", describeDeviceReauthProbe(last))
		}
		time.Sleep(3 * time.Second)
	}
}
