package main

// mcp_auth_tools.go — headless auth MCP tools. Lets a coding agent (Claude
// Code Remote over SSH / Claude Code local / Codex / Cursor / …) sign the
// user into Yaver using Apple / GitHub / Google / Microsoft OAuth without ever
// opening a browser on the machine the daemon is running on.
//
// Flow for a vibe coder SSH'd into a headless Linux/WSL box:
//
//   1. Agent calls `auth_status` → sees not signed in.
//   2. Agent calls `auth_start` → gets { url, user_code, qr_ascii, device_code, expires_at }
//      and renders it inline in the chat. The HUMAN opens the URL on their
//      phone or laptop browser and completes OAuth.
//   3. Agent calls `auth_wait` (or loops `auth_poll`) with the device_code.
//      Once Convex marks the device authorized, the agent receives the
//      token; Yaver saves it to ~/.yaver/config.json, starts the daemon,
//      and registers itself as an MCP server in every installed editor.
//   4. Agent now has a fully configured Yaver on the remote box — no
//      browser, no port forwarding, no copy-paste of tokens.
//
// Design rules:
//
//   - These tools MUST work without the user already being signed in.
//     The MCP stdio server inherits the permissions of the process that
//     spawned it (the coding agent), so no HTTP auth is needed at this
//     layer.
//   - `auth_start` is non-blocking. Polling / waiting is separated into
//     `auth_poll` (single poll) and `auth_wait` (bounded blocking wait)
//     so coding agents with short tool-call timeouts aren't forced into
//     a multi-minute stall.
//   - Every tool returns machine-readable JSON plus a human-readable
//     `message` so the coding agent can either render the message or
//     parse the fields.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mdp/qrterminal/v3"
)

// --- Status -----------------------------------------------------------------

// AuthStatusSnapshot is the payload returned by the `auth_status` MCP tool.
// It describes everything a coding agent needs to decide whether to kick
// off an auth flow.
type AuthStatusSnapshot struct {
	SignedIn     bool   `json:"signed_in"`
	NeedsAuth    bool   `json:"needs_auth"`
	ConvexURL    string `json:"convex_url"`
	DeviceID     string `json:"device_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	UserEmail    string `json:"user_email,omitempty"`
	UserFullName string `json:"user_full_name,omitempty"`
	Provider     string `json:"provider,omitempty"`
	HasToken     bool   `json:"has_token"`
	Headless     bool   `json:"headless"`
	// PendingAuth is populated when a device-code flow has been started
	// but the human hasn't finished signing in on their phone yet. The
	// coding agent should surface PendingAuth.URL to the human and then
	// call auth_wait (or loop auth_poll) on PendingAuth.DeviceCode. It
	// is the single source of truth for "is an auth in flight right
	// now" across bash and MCP surfaces — both paths write the same
	// ~/.yaver/pending-auth.json.
	PendingAuth *AuthStatusPending `json:"pending_auth,omitempty"`
	Message     string             `json:"message"`
}

// AuthStatusPending describes an in-flight device-code sign-in.
type AuthStatusPending struct {
	URL               string `json:"url"`
	UserCode          string `json:"user_code"`
	DeviceCode        string `json:"device_code"`
	ExpiresAtMs       int64  `json:"expires_at_ms"`
	ExpiresInSeconds  int    `json:"expires_in_seconds"`
}

// authStatusSnapshot inspects the on-disk config and (if a token is present)
// validates it against Convex. Validation failures simply mean needs_auth=true
// — we don't error the tool call on an expired token.
func authStatusSnapshot() AuthStatusSnapshot {
	snap := AuthStatusSnapshot{
		Headless:  isHeadless(),
		ConvexURL: defaultConvexSiteURL,
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		snap.NeedsAuth = true
		snap.Message = "no Yaver config found — run auth_start to sign in"
		return snap
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) != "" {
		snap.ConvexURL = cfg.ConvexSiteURL
	}
	snap.DeviceID = cfg.DeviceID
	snap.HasToken = strings.TrimSpace(cfg.AuthToken) != ""

	// Surface any in-flight device-code sign-in regardless of
	// whether we already have a (separate, possibly expired) token
	// on disk — the caller can see both facts and decide whether
	// to resume-poll or start fresh.
	if pend, _ := loadPendingAuth(); pend != nil &&
		pend.ExpiresAt > time.Now().UnixMilli() &&
		strings.TrimSpace(pend.DeviceCode) != "" {
		snap.PendingAuth = &AuthStatusPending{
			URL:              pend.URL,
			UserCode:         pend.UserCode,
			DeviceCode:       pend.DeviceCode,
			ExpiresAtMs:      pend.ExpiresAt,
			ExpiresInSeconds: int(time.Until(time.UnixMilli(pend.ExpiresAt)).Seconds()),
		}
	}

	if !snap.HasToken {
		snap.NeedsAuth = true
		if snap.PendingAuth != nil {
			snap.Message = "sign-in is in flight — show the pending_auth.url to the human, then call auth_wait with pending_auth.device_code"
		} else {
			snap.Message = "no auth token on disk — run auth_start to sign in"
		}
		return snap
	}
	// Validate the token so we don't lie to the caller about being signed in.
	info, err := ValidateTokenInfo(snap.ConvexURL, cfg.AuthToken)
	if err != nil {
		snap.NeedsAuth = true
		snap.Message = "auth token rejected by Convex — run auth_start to sign in again (" + err.Error() + ")"
		return snap
	}
	snap.SignedIn = true
	snap.NeedsAuth = false
	snap.UserID = info.UserID
	snap.UserEmail = info.Email
	snap.UserFullName = info.FullName
	snap.Provider = info.Provider
	snap.Message = fmt.Sprintf("signed in as %s via %s", firstNonEmpty(info.Email, info.UserID), firstNonEmpty(info.Provider, "oauth"))
	return snap
}

// --- Start (non-blocking) ---------------------------------------------------

// AuthStartResult is the payload returned by `auth_start`. The human opens
// URL on any browser (their phone, their laptop, whatever), finishes OAuth,
// and the coding agent then calls `auth_poll` / `auth_wait` with DeviceCode.
type AuthStartResult struct {
	URL         string   `json:"url"`
	UserCode    string   `json:"user_code"`
	DeviceCode  string   `json:"device_code"`
	ExpiresAt   int64    `json:"expires_at_ms"`
	ExpiresIn   int      `json:"expires_in_seconds"`
	QRASCII     string   `json:"qr_ascii"`
	ConvexURL   string   `json:"convex_url"`
	Instructions []string `json:"instructions"`
	Message     string   `json:"message"`
}

// authStartDeviceCode requests a new device code from Convex and renders
// the user-facing URL + QR. Non-blocking — poll separately.
func authStartDeviceCode(ctx context.Context, convexURLOverride string) (AuthStartResult, error) {
	convexURL := strings.TrimSpace(convexURLOverride)
	if convexURL == "" {
		if cfg, _ := LoadConfig(); cfg != nil && strings.TrimSpace(cfg.ConvexSiteURL) != "" {
			convexURL = cfg.ConvexSiteURL
		}
	}
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	// Reuse any still-valid pending-auth record so an MCP flow and a
	// plain `yaver auth` share the same URL. Prevents the user from
	// being asked to sign in twice if the caller switches paths.
	var dc deviceCodeResponse
	if existing, _ := loadPendingAuth(); existing != nil &&
		existing.ExpiresAt > time.Now().UnixMilli()+5_000 &&
		strings.TrimSpace(existing.DeviceCode) != "" {
		dc = deviceCodeResponse{
			UserCode:   existing.UserCode,
			DeviceCode: existing.DeviceCode,
			ExpiresAt:  existing.ExpiresAt,
		}
	} else {
		payload, _ := json.Marshal(buildDeviceCodeRequest())
		req, err := http.NewRequestWithContext(ctx, "POST", convexURL+"/auth/device-code", bytes.NewReader(payload))
		if err != nil {
			return AuthStartResult{}, fmt.Errorf("create device-code request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return AuthStartResult{}, fmt.Errorf("request device code: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return AuthStartResult{}, fmt.Errorf("device-code request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
			return AuthStartResult{}, fmt.Errorf("decode device-code response: %w", err)
		}
		_ = savePendingAuth(&pendingAuth{
			DeviceCode: dc.DeviceCode,
			UserCode:   dc.UserCode,
			URL:        "https://yaver.io/auth/device?code=" + dc.UserCode,
			ConvexURL:  convexURL,
			ExpiresAt:  dc.ExpiresAt,
			CreatedAt:  time.Now().UnixMilli(),
		})
	}

	authURL := "https://yaver.io/auth/device?code=" + dc.UserCode
	ttl := time.Until(time.UnixMilli(dc.ExpiresAt))

	// Render a compact ASCII QR so coding agents can paint it inline in
	// their chat. Users with iPhones can point their camera at the block
	// art to jump straight into auth.
	var qrBuf bytes.Buffer
	qrterminal.GenerateWithConfig(authURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    &qrBuf,
		BlackChar: "##",
		WhiteChar: "  ",
		QuietZone: 1,
	})

	return AuthStartResult{
		URL:         authURL,
		UserCode:    dc.UserCode,
		DeviceCode:  dc.DeviceCode,
		ExpiresAt:   dc.ExpiresAt,
		ExpiresIn:   int(ttl.Seconds()),
		QRASCII:     qrBuf.String(),
		ConvexURL:   convexURL,
		Instructions: []string{
			"1. Open the URL on any device with a browser (your phone works).",
			"2. Sign in with Apple, Google, or Microsoft.",
			"3. The page will confirm this machine's code matches.",
			"4. Come back here and call `auth_wait` (or poll `auth_poll`) with the device_code to finish.",
		},
		Message: fmt.Sprintf("Open %s and sign in. Then call auth_wait with device_code=%s.", authURL, dc.DeviceCode),
	}, nil
}

// --- Poll (single) ----------------------------------------------------------

// AuthPollResult is the payload returned by `auth_poll` and `auth_wait`.
type AuthPollResult struct {
	Status        string             `json:"status"` // pending | authorized | expired
	TokenSaved    bool               `json:"token_saved"`
	DaemonStarted bool               `json:"daemon_started"`
	MCPRegistered bool               `json:"mcp_registered"`
	Snapshot      AuthStatusSnapshot `json:"snapshot,omitempty"`
	Message       string             `json:"message"`
}

// authPollDeviceCode runs one poll against Convex and, if the device code
// became authorized, persists the returned token to ~/.yaver/config.json,
// starts the daemon in the background, and auto-registers Yaver as an MCP
// server in every installed editor. The caller decides whether to keep
// polling.
func authPollDeviceCode(ctx context.Context, convexURLOverride, deviceCode string) (AuthPollResult, error) {
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return AuthPollResult{}, fmt.Errorf("device_code is required")
	}
	convexURL := strings.TrimSpace(convexURLOverride)
	if convexURL == "" {
		if cfg, _ := LoadConfig(); cfg != nil && strings.TrimSpace(cfg.ConvexSiteURL) != "" {
			convexURL = cfg.ConvexSiteURL
		}
	}
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}

	token, done, err := pollDeviceCode(convexURL, deviceCode)
	if err != nil {
		return AuthPollResult{Status: "pending", Message: "poll failed (transient, keep trying): " + err.Error()}, nil
	}
	if !done {
		return AuthPollResult{Status: "pending", Message: "waiting for the user to finish OAuth in the browser"}, nil
	}
	if token == "" {
		return AuthPollResult{Status: "expired", Message: "device code expired or was already used — call auth_start again"}, nil
	}

	return authFinalizeToken(convexURL, token)
}

// --- Wait (bounded blocking) -----------------------------------------------

// authWaitDeviceCode polls every pollIntervalSec seconds up to
// timeoutSec. Returns as soon as the code is authorized, expires, or the
// caller's context is canceled. Keeps the call under a typical MCP tool
// timeout unless the caller explicitly overrides timeoutSec.
func authWaitDeviceCode(ctx context.Context, convexURLOverride, deviceCode string, timeoutSec, pollIntervalSec int) (AuthPollResult, error) {
	if timeoutSec <= 0 {
		timeoutSec = 120 // fits inside Claude Code's default 2-minute tool timeout
	}
	if pollIntervalSec <= 0 {
		pollIntervalSec = 3
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)

	for {
		if ctx.Err() != nil {
			return AuthPollResult{Status: "pending", Message: "canceled before auth completed"}, ctx.Err()
		}
		result, err := authPollDeviceCode(ctx, convexURLOverride, deviceCode)
		if err != nil {
			return result, err
		}
		if result.Status != "pending" {
			return result, nil
		}
		if time.Now().After(deadline) {
			result.Message = fmt.Sprintf("auth_wait timed out after %ds — keep calling auth_poll or re-invoke auth_wait", timeoutSec)
			return result, nil
		}
		select {
		case <-ctx.Done():
			return AuthPollResult{Status: "pending", Message: "canceled before auth completed"}, ctx.Err()
		case <-time.After(time.Duration(pollIntervalSec) * time.Second):
		}
	}
}

// --- Finalize --------------------------------------------------------------

// authFinalizeToken persists token to disk, validates it for the snapshot,
// starts the daemon if it isn't running, and auto-registers MCP. Returns
// a fully populated AuthPollResult with status=authorized.
func authFinalizeToken(convexURL, token string) (AuthPollResult, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	cfg.AuthToken = strings.TrimSpace(token)
	cfg.ConvexSiteURL = convexURL
	if strings.TrimSpace(cfg.DeviceID) == "" {
		cfg.DeviceID = uuid.New().String()
	}
	// Clear any stale manually-configured relay so the per-user backend
	// relay config takes effect — matches the existing `yaver auth` CLI path.
	cfg.RelayServers = nil
	cfg.RelayPassword = ""
	if err := SaveConfig(cfg); err != nil {
		return AuthPollResult{
			Status:  "authorized",
			Message: "token received but saving config failed: " + err.Error(),
		}, err
	}
	// Sign-in complete — clear the pending-auth resume record so a
	// later `yaver auth` invocation starts a fresh code if needed.
	clearPendingAuth()

	// Belt + braces: validate the token we just saved so the snapshot is
	// honest about signed-in status.
	snap := authStatusSnapshot()

	// Best-effort daemon start + MCP registration. Failures here do not
	// fail the tool call — the token is already persisted.
	daemonStarted := safeStartDaemon()
	mcpRegistered := safeAutoSetupMCP()

	msg := "signed in"
	if snap.UserEmail != "" {
		msg = fmt.Sprintf("signed in as %s", snap.UserEmail)
	}
	if daemonStarted {
		msg += "; daemon started"
	}
	if mcpRegistered {
		msg += "; MCP registered"
	}

	return AuthPollResult{
		Status:        "authorized",
		TokenSaved:    true,
		DaemonStarted: daemonStarted,
		MCPRegistered: mcpRegistered,
		Snapshot:      snap,
		Message:       msg,
	}, nil
}

// --- Logout -----------------------------------------------------------------

type AuthLogoutResult struct {
	LoggedOut bool   `json:"logged_out"`
	Message   string `json:"message"`
}

// authLogout clears the on-disk token. The daemon is left running — callers
// that want to stop it should call `agent_shutdown` separately.
func authLogout() (AuthLogoutResult, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return AuthLogoutResult{LoggedOut: false, Message: "no config found — already logged out"}, nil
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return AuthLogoutResult{LoggedOut: false, Message: "no token on disk — already logged out"}, nil
	}
	cfg.AuthToken = ""
	if err := SaveConfigClearingAuth(cfg); err != nil {
		return AuthLogoutResult{}, fmt.Errorf("save config: %w", err)
	}
	return AuthLogoutResult{LoggedOut: true, Message: "token cleared from ~/.yaver/config.json (daemon not stopped — call agent_shutdown if desired)"}, nil
}

// --- Helpers ----------------------------------------------------------------

// safeStartDaemon tries to start the daemon if it isn't already running,
// swallowing any errors. Returns true if we think it's running after the
// call. The existing startServeIfStopped() panics on some paths so we
// defer-recover to keep the MCP tool from crashing the stdio server.
func safeStartDaemon() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	startServeIfStopped()
	return true
}

// safeAutoSetupMCP invokes autoSetupMCP() with a panic recover, since it
// writes to third-party editor config files and we'd rather the tool call
// report partial success than abort.
func safeAutoSetupMCP() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	// autoSetupMCP prints to stdout; we want the stdio MCP channel clean.
	// Capture stdout during the call and discard — the tool response
	// already reports registration status to the caller.
	origStdout := os.Stdout
	devnull, err := os.Open(os.DevNull)
	if err == nil {
		defer devnull.Close()
		os.Stdout = devnull
		defer func() { os.Stdout = origStdout }()
	}
	autoSetupMCP()
	return true
}
