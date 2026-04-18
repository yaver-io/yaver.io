package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// deviceCodeResponse is the response from POST /auth/device-code.
type deviceCodeResponse struct {
	UserCode   string `json:"userCode"`
	DeviceCode string `json:"deviceCode"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// pollResponse is the response from GET /auth/device-code/poll.
type pollResponse struct {
	Status string `json:"status"` // "pending", "authorized", "expired"
	Token  string `json:"token,omitempty"`
}

// pendingAuth is the on-disk record of a device-code flow that's been
// started but not yet completed. Persisted so that a short-lived caller
// (an agentic-AI bash tool call that times out after a few minutes, a
// user re-running `yaver auth` manually, …) can resume the same code
// instead of burning a new one — which would force the human to sign
// in on their phone a second time.
//
// Stored at ~/.yaver/pending-auth.json. Cleared on success or when the
// code's 15-minute TTL expires.
type pendingAuth struct {
	DeviceCode string `json:"deviceCode"`
	UserCode   string `json:"userCode"`
	URL        string `json:"url"`
	ConvexURL  string `json:"convexUrl"`
	ExpiresAt  int64  `json:"expiresAt"` // unix ms
	CreatedAt  int64  `json:"createdAt"` // unix ms
}

func pendingAuthPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pending-auth.json"), nil
}

func loadPendingAuth() (*pendingAuth, error) {
	path, err := pendingAuthPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var p pendingAuth
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func savePendingAuth(p *pendingAuth) error {
	path, err := pendingAuthPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func clearPendingAuth() {
	if path, err := pendingAuthPath(); err == nil {
		_ = os.Remove(path)
	}
}

func pendingAuthWatcherPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pending-auth-watcher.pid"), nil
}

func readPendingAuthWatcherPID() int {
	path, err := pendingAuthWatcherPath()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0
	}
	return pid
}

func clearPendingAuthWatcher() {
	if path, err := pendingAuthWatcherPath(); err == nil {
		_ = os.Remove(path)
	}
}

func ensurePendingAuthBackgroundWaiter(convexURL string) {
	if pid := readPendingAuthWatcherPID(); pid > 0 && isProcessAlive(pid) {
		return
	}
	execPath, err := os.Executable()
	if err != nil || strings.TrimSpace(execPath) == "" {
		return
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	cmd := osexec.Command(execPath, "auth", "--headless", "--background-wait", "--convex-url", convexURL)
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return
	}
	if path, err := pendingAuthWatcherPath(); err == nil {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0o600)
	}
	_ = devNull.Close()
}

// perInvocationBlockingAuthBudget caps how long a single `yaver auth`
// invocation will block polling. Agentic-AI bash tools commonly cap
// tool calls at a few minutes; we stay well under the most common
// default (5 min in Claude Code, 2 min in some configs) while still
// giving a quick human plenty of wall time to finish OAuth on their
// phone.
//
// If the caller runs out of budget before the code is used, we exit
// with a clear, machine-readable hint and the pending-auth file still
// on disk — so re-invoking `yaver auth` resumes the same code with no
// second sign-in on the phone.
const perInvocationBlockingAuthBudget = 2*time.Minute + 30*time.Second

// runDeviceCodeAuth performs the device code auth flow for headless machines.
// It resumes any pending-auth record so a second invocation keeps polling
// the same URL the human is already half-way through signing in at.
func runDeviceCodeAuth(convexURL string) (string, error) {
	dcResp, resumed, err := obtainOrResumeDeviceCode(convexURL)
	if err != nil {
		return "", err
	}
	ensurePendingAuthBackgroundWaiter(convexURL)

	authURL := "https://yaver.io/auth/device?code=" + dcResp.UserCode
	meta := buildDeviceCodeRequest()
	machineLabel := strings.TrimSpace(meta.MachineName)
	if machineLabel == "" {
		machineLabel = "this machine"
	}

	// Headline block — line-for-line scannable so an agentic-AI wrapper
	// extracts the URL reliably and can show it to the human without
	// needing to parse the QR art.
	fmt.Println()
	if resumed {
		fmt.Printf("  Resuming sign-in for %s.\n", machineLabel)
		fmt.Println("  (same URL as before — no need to sign in again if you already did)")
	} else {
		fmt.Printf("  Authorize %s from your phone.\n", machineLabel)
	}
	fmt.Println()
	fmt.Println("  Open this URL on your phone and sign in (Apple / GitHub / Google / Microsoft):")
	fmt.Println()
	fmt.Printf("    %s\n", authURL)
	fmt.Println()
	fmt.Printf("  Code: %s\n", dcResp.UserCode)
	fmt.Println()

	// QR code stays for humans at real terminals; if stdout is not a TTY
	// (a bash-tool pipe, a Claude Code call, a log file, …) we skip it so
	// the agent's chat doesn't fill with block-art garbage.
	if isStdoutTTY() {
		qrterminal.GenerateWithConfig(authURL, qrterminal.Config{
			Level:     qrterminal.L,
			Writer:    os.Stdout,
			BlackChar: qrterminal.BLACK,
			WhiteChar: qrterminal.WHITE,
			QuietZone: 2,
		})
		fmt.Println()
	}

	codeTTL := time.Until(time.UnixMilli(dcResp.ExpiresAt))
	if codeTTL < 0 {
		codeTTL = 0
	}
	thisWait := perInvocationBlockingAuthBudget
	if codeTTL < thisWait {
		thisWait = codeTTL
	}
	fmt.Printf("  Waiting for sign-in — up to %s this round (code valid for %s total).\n",
		humanRoundDuration(thisWait), humanRoundDuration(codeTTL))
	fmt.Println()

	token, err := pollUntilAuthorized(convexURL, dcResp.DeviceCode, time.UnixMilli(dcResp.ExpiresAt), thisWait)
	if err != nil {
		return "", err
	}
	return token, nil
}

// obtainOrResumeDeviceCode returns a device code to poll against. If a
// pending-auth record exists and is still within its TTL, it is reused
// (so a re-invocation after a tool timeout keeps the same URL the human
// started signing in at). Otherwise, a fresh code is requested and
// persisted.
func obtainOrResumeDeviceCode(convexURL string) (*deviceCodeResponse, bool, error) {
	if existing, err := loadPendingAuth(); err == nil && existing != nil {
		if existing.ExpiresAt > time.Now().UnixMilli()+5_000 && strings.TrimSpace(existing.DeviceCode) != "" {
			// Honour whichever convex URL started the original flow —
			// mixing convex backends mid-flow will never succeed.
			useURL := strings.TrimSpace(existing.ConvexURL)
			if useURL == "" {
				useURL = convexURL
			}
			return &deviceCodeResponse{
				UserCode:   existing.UserCode,
				DeviceCode: existing.DeviceCode,
				ExpiresAt:  existing.ExpiresAt,
			}, true, nil
		}
		// Stale — clear so we don't loop on an expired code.
		clearPendingAuth()
	}

	payload, _ := json.Marshal(buildDeviceCodeRequest())
	resp, err := httpClient.Post(convexURL+"/auth/device-code", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, false, fmt.Errorf("request device code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("device code request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var dcResp deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dcResp); err != nil {
		return nil, false, fmt.Errorf("decode device code response: %w", err)
	}

	_ = savePendingAuth(&pendingAuth{
		DeviceCode: dcResp.DeviceCode,
		UserCode:   dcResp.UserCode,
		URL:        "https://yaver.io/auth/device?code=" + dcResp.UserCode,
		ConvexURL:  convexURL,
		ExpiresAt:  dcResp.ExpiresAt,
		CreatedAt:  time.Now().UnixMilli(),
	})
	return &dcResp, false, nil
}

// pollUntilAuthorized blocks on a device code, respecting both the
// per-invocation budget and the absolute TTL. Returns:
//
//	token, nil          — authorized, pending-auth cleared by caller
//	"", errResumable    — caller should retry; pending-auth preserved
//	"", other error     — code expired / used; pending-auth cleared
var errResumable = fmt.Errorf("yaver: sign-in still pending — re-run `yaver auth` to keep waiting (same URL, no second sign-in needed)")

func pollUntilAuthorized(convexURL, deviceCode string, absoluteDeadline time.Time, maxWait time.Duration) (string, error) {
	roundDeadline := time.Now().Add(maxWait)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Kick off with an immediate poll so a human who signed in between
	// `yaver auth` invocations finishes on the very first tick.
	for {
		if time.Now().After(absoluteDeadline) {
			clearPendingAuth()
			return "", fmt.Errorf("device code expired — run `yaver auth` to start a fresh sign-in")
		}

		token, done, err := pollDeviceCode(convexURL, deviceCode)
		if err == nil && done {
			if token == "" {
				clearPendingAuth()
				return "", fmt.Errorf("device code expired or already used")
			}
			clearPendingAuth()
			return token, nil
		}
		// err != nil is non-fatal — we just wait for the next tick.
		if time.Now().After(roundDeadline) {
			// Budget blown this round. Leave the pending-auth file in
			// place so re-running the command resumes without burning
			// the code.
			return "", errResumable
		}
		select {
		case <-ticker.C:
		}
	}
}

func runPendingAuthBackgroundWaiter(convexURL string) error {
	defer clearPendingAuthWatcher()
	pending, err := loadPendingAuth()
	if err != nil || pending == nil {
		return err
	}
	if pending.ExpiresAt <= time.Now().UnixMilli() {
		clearPendingAuth()
		return nil
	}
	useURL := strings.TrimSpace(pending.ConvexURL)
	if useURL == "" {
		useURL = convexURL
	}
	token, err := pollUntilAuthorized(
		useURL,
		pending.DeviceCode,
		time.UnixMilli(pending.ExpiresAt),
		time.Until(time.UnixMilli(pending.ExpiresAt)),
	)
	if err != nil {
		if err == errResumable {
			return nil
		}
		return err
	}
	cfg, cfgErr := LoadConfig()
	if cfgErr != nil {
		return cfgErr
	}
	return finalizeAuthConfig(cfg, useURL, token, false, false)
}

func humanRoundDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}

// isStdoutTTY guesses whether stdout is a real terminal. Used to skip
// block-art QR rendering when the caller is piping (a bash tool, an
// MCP host, a log collector).
func isStdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// pollDeviceCode makes a single poll request.
// Returns (token, done, error). done=true means stop polling.
func pollDeviceCode(convexURL, deviceCode string) (string, bool, error) {
	req, err := http.NewRequest("GET", convexURL+"/auth/device-code/poll?device_code="+deviceCode, nil)
	if err != nil {
		return "", false, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("poll failed (status %d)", resp.StatusCode)
	}

	var pr pollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return "", false, err
	}

	switch pr.Status {
	case "authorized":
		return pr.Token, true, nil
	case "expired":
		return "", true, nil
	default:
		return "", false, nil
	}
}

// isHeadless returns true if the environment suggests no display is available
// (or has one the caller can't see). Drives `yaver auth` into the device-code
// flow instead of trying to spawn a local browser.
//
// Headless signals, any one is enough:
//
//	SSH session without display forwarding — the classic remote-server case.
//	WSL with no X/Wayland display — `wslview`/`explorer.exe` would open the
//	  browser on the Windows host, which is the wrong machine when the user
//	  is driving WSL from a phone, a cloud shell, or any non-GUI bridge.
//	YAVER_HEADLESS=1 — explicit opt-in for any other "I can't see a browser"
//	  bridge we haven't thought of.
func isHeadless() bool {
	if envTruthy(os.Getenv("YAVER_HEADLESS")) {
		return true
	}
	noDisplay := os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" {
		if noDisplay {
			return true
		}
	}
	if isWSL() && noDisplay {
		return true
	}
	return false
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
