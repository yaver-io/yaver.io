package main

// Rescue command channel — agent side.
//
// Pairs with backend/convex/agentRescue.ts. The web UI / mobile / CLI
// queue commands into Convex; the agent's heartbeat loop calls
// claimAndExecuteRescueCommand once after each successful heartbeat
// to pull and run any pending command. Convex is reached via plain
// HTTPS, so this works even when the relay tunnel is wedged — which
// is the entire point of the channel.
//
// Strict-enum command field: a compromised dashboard cannot inject
// arbitrary shell. Each command has a hand-written handler below; new
// commands require code in *both* this file and the backend schema.
//
// Audit invariants:
//   - we always call /agent-rescue/report once we've claimed; that's
//     what flips the row from "claimed" → "completed"|"failed" so a
//     wedged claim doesn't trap the queue forever.
//   - if the handler is a hard-restart it has to schedule the report
//     BEFORE exiting, otherwise the queue thinks the claim is still
//     in flight (the next process is a fresh PID and won't recognise
//     this commandId).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// rescueClaim is what /agent-rescue/claim returns. Mirrors the Convex
// row shape — command + params are the bits the agent acts on; the
// rest is stored on the backend.
type rescueClaim struct {
	CommandID string         `json:"_id"`
	Command   string         `json:"command"`
	Params    map[string]any `json:"params"`
}

// rescueClaimResponse is the wrapper the HTTP route returns.
type rescueClaimResponse struct {
	OK      bool         `json:"ok"`
	Command *rescueClaim `json:"command"` // null when nothing pending
	Error   string       `json:"error"`
}

// claimAndExecuteRescueCommand polls for one pending command and runs
// it. Called from the heartbeat loop — silent-no-op when there's
// nothing to do, since the heartbeat fires every ~30 s and most ticks
// will find an empty queue. Errors are logged but never propagated;
// rescue is best-effort and must never wedge the heartbeat itself.
func claimAndExecuteRescueCommand(baseURL, token, deviceID string) {
	if baseURL == "" || token == "" || deviceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	claim, err := claimNextRescue(ctx, baseURL, token, deviceID)
	if err != nil {
		// Don't spam logs on each tick if Convex is briefly down —
		// this fires at heartbeat cadence already.
		if !isQuietRescueError(err) {
			log.Printf("[rescue] claim error: %v", err)
		}
		return
	}
	if claim == nil {
		return
	}
	log.Printf("[rescue] claimed command %s = %q", claim.CommandID, claim.Command)

	status, result := dispatchRescue(claim)
	if err := reportRescue(ctx, baseURL, token, claim.CommandID, status, result); err != nil {
		log.Printf("[rescue] report error for %s: %v", claim.CommandID, err)
	}
}

// dispatchRescue runs the claimed command and returns (status,
// result). status is "completed" or "failed"; result is a short
// stdout / error tail capped at ~1 KB so we don't bloat Convex.
//
// Adding a new command = a new case here AND a new literal in the
// schema's `command` union. Anything else is rejected as unknown.
func dispatchRescue(claim *rescueClaim) (string, string) {
	switch claim.Command {
	case "restart":
		return rescueRestart()
	case "reinstall-latest":
		return rescueReinstallLatest(claim.Params)
	case "tunnel-reset":
		return rescueTunnelReset()
	case "auth-reset":
		return rescueAuthReset()
	default:
		return "failed", fmt.Sprintf("unknown rescue command %q", claim.Command)
	}
}

// rescueRestart asks systemd to restart the unit (Linux), or just
// exits with code 2 elsewhere — local watchdogs / launchd / the
// `yaver doctor` watchdog timer will bring the process back up.
//
// The process exits ~1 second after the report is submitted. Without
// the delay the report could lose the race against process death.
func rescueRestart() (string, string) {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		log.Println("[rescue] restart requested — exiting for systemd respawn")
		// exit code 2 (not 0) so plain `Restart=on-failure` units
		// also pick us back up. Restart=always covers exit 0 too.
		os.Exit(2)
	}()
	if runtime.GOOS == "linux" {
		// Best-effort kick of the unit. If it fails we still exit
		// below — systemd or the watchdog timer covers us either way.
		out, err := exec.Command("systemctl", "restart", "yaver-agent").CombinedOutput()
		if err != nil {
			return "completed", fmt.Sprintf("self-exit scheduled (systemctl returned %v: %s)", err, capSnippet(out, 256))
		}
		return "completed", "systemctl restart yaver-agent invoked"
	}
	return "completed", "self-exit scheduled — local watchdog should respawn"
}

// rescueReinstallLatest fetches the latest .deb from GitHub releases
// (linux-arm64 only for now — that's our ephemeral tier) and dpkg-i's
// it. After the install the existing systemd unit will respawn the
// new binary on its next restart cycle. Caller's `params.version`
// pins a specific version when present; otherwise we resolve "latest".
func rescueReinstallLatest(params map[string]any) (string, string) {
	if runtime.GOOS != "linux" {
		return "failed", "reinstall-latest is linux-only in this build"
	}
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		return "failed", fmt.Sprintf("unsupported arch %q for reinstall-latest", runtime.GOARCH)
	}
	pinned := ""
	if v, ok := params["version"].(string); ok {
		pinned = strings.TrimSpace(v)
	}

	// Resolve URL. With pinned version go straight to the asset URL;
	// otherwise hit /releases/latest first.
	debURL, err := resolveLatestDebURL(pinned)
	if err != nil {
		return "failed", fmt.Sprintf("resolve .deb url: %v", err)
	}

	// Download.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", debURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "failed", fmt.Sprintf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "failed", fmt.Sprintf("download HTTP %d from %s", resp.StatusCode, debURL)
	}
	tmpFile := "/tmp/yaver-rescue.deb"
	out, err := os.Create(tmpFile)
	if err != nil {
		return "failed", fmt.Sprintf("create temp: %v", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmpFile)
		return "failed", fmt.Sprintf("write temp: %v", err)
	}
	out.Close()
	defer os.Remove(tmpFile)

	// Install. dpkg may want sudo; if we're already root it's a no-op.
	dpkgOut, dpkgErr := exec.Command("dpkg", "-i", tmpFile).CombinedOutput()
	if dpkgErr != nil {
		return "failed", fmt.Sprintf("dpkg -i: %v\n%s", dpkgErr, capSnippet(dpkgOut, 768))
	}

	// Restart so the new binary is the one running.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		log.Println("[rescue] reinstall complete — exiting for respawn with new binary")
		os.Exit(2)
	}()
	return "completed", fmt.Sprintf("installed %s; self-exit scheduled", debURL)
}

// rescueTunnelReset is a placeholder — the relay tunnel goroutine
// has no public reset hook today, so the simplest implementation is
// the same self-exit as restart. When the relay client gets a public
// `Reset()` we can do it without dropping the HTTP server too.
func rescueTunnelReset() (string, string) {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		log.Println("[rescue] tunnel-reset requested — exiting for respawn")
		os.Exit(2)
	}()
	return "completed", "self-exit scheduled (tunnel reconnects on respawn)"
}

// rescueAuthReset clears the cached auth artifacts so the agent
// drops back to "needs pairing" on next boot. Caller (the dashboard
// or mobile) is expected to follow up with a pair flow. Deliberately
// only clears tokens — vault entries, project data, and session
// history are untouched.
//
// SAFETY: this is destructive (the box becomes unauthenticated until
// re-paired). The Convex schema's owner-only check is the gate;
// here we just refuse to run if there's nothing to clear.
func rescueAuthReset() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "failed", "no home dir"
	}
	cfgPath := home + "/.yaver/config.json"
	if _, err := os.Stat(cfgPath); err != nil {
		return "failed", "no config to reset"
	}
	if err := os.Rename(cfgPath, cfgPath+".reset-"+time.Now().Format("20060102-150405")); err != nil {
		return "failed", fmt.Sprintf("rename config: %v", err)
	}
	go func() {
		time.Sleep(1500 * time.Millisecond)
		log.Println("[rescue] auth-reset — config moved aside, exiting for respawn")
		os.Exit(2)
	}()
	return "completed", "config moved aside; re-pair required on next boot"
}

// ── HTTP helpers ─────────────────────────────────────────────────────

func claimNextRescue(ctx context.Context, baseURL, token, deviceID string) (*rescueClaim, error) {
	body, _ := json.Marshal(map[string]string{"deviceId": deviceID})
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/agent-rescue/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, ErrAuthExpired
	}
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("claim status %d: %s", resp.StatusCode, capSnippet(respBytes, 256))
	}
	var parsed rescueClaimResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("parse claim response: %w", err)
	}
	if !parsed.OK {
		if parsed.Error != "" {
			return nil, fmt.Errorf("%s", parsed.Error)
		}
		return nil, fmt.Errorf("claim returned ok=false")
	}
	return parsed.Command, nil
}

func reportRescue(ctx context.Context, baseURL, token, commandID, status, result string) error {
	if status != "completed" && status != "failed" {
		return fmt.Errorf("invalid status %q", status)
	}
	if len(result) > 2048 {
		result = result[:2048]
	}
	body, _ := json.Marshal(map[string]string{
		"commandId": commandID,
		"status":    status,
		"result":    result,
	})
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/agent-rescue/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("report status %d: %s", resp.StatusCode, capSnippet(respBytes, 256))
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────

func resolveLatestDebURL(pinnedVersion string) (string, error) {
	if pinnedVersion != "" {
		return fmt.Sprintf(
			"https://github.com/kivanccakmak/yaver.io/releases/download/v%s/yaver_%s_%s.deb",
			pinnedVersion, pinnedVersion, runtime.GOARCH,
		), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/kivanccakmak/yaver.io/releases/latest", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github releases HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var parsed struct {
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	suffix := "_" + runtime.GOARCH + ".deb"
	for _, a := range parsed.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			return a.URL, nil
		}
	}
	return "", fmt.Errorf("no %s asset on latest release", suffix)
}

func capSnippet(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// isQuietRescueError detects "the network was briefly down" so we
// don't spam log lines on each heartbeat tick. Real bugs still log.
func isQuietRescueError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout")
}

// ── Pacing ───────────────────────────────────────────────────────────

// Don't claim more than once per heartbeat tick. The heartbeat loop
// fires every ~30 s; we use a small mutex to guarantee single-flight
// in case heartbeat ticks overlap (kick + scheduled fire on the same
// second).
var rescueClaimMu sync.Mutex

func claimAndExecuteRescueCommandSingleFlight(baseURL, token, deviceID string) {
	if !rescueClaimMu.TryLock() {
		return
	}
	defer rescueClaimMu.Unlock()
	claimAndExecuteRescueCommand(baseURL, token, deviceID)
}
