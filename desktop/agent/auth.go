package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RefreshToken extends the session expiry by 1 year and, if the
// backend supports rotation (yaver.io Convex, Apr 2026+), returns a
// freshly-minted bearer token. Rotation is automatic; the backend bounds
// acceptance of the immediately previous token with its recovery grace.
//
// The caller is expected to persist the returned rotated token to
// ~/.yaver/config.json atomically before considering the refresh
// complete (see persistRotatedAuthToken in main.go).
//
// Returns ("", nil) on success without rotation (old backend).
// Returns (newToken, nil) when the backend rotated.
// Returns ErrAuthExpired (wrapped) on 401 — session is past the
// 1-year grace window or was explicitly revoked from the dashboard.
func RefreshToken(baseURL, token string) (string, error) {
	req, err := newBearerRequest("POST", baseURL+"/auth/refresh", token, nil)
	if err != nil {
		return "", fmt.Errorf("create refresh request: %w", err)
	}
	// Opt in to server-side token rotation. We're 1.99.12+; we know
	// how to persist the returned new token atomically (see
	// persistRotatedAuthToken). Older backends ignore the header.
	req.Header.Set("X-Yaver-Rotate-Token", "1")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("session expired (401)")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("refresh token failed (status %d)", resp.StatusCode)
	}

	// Decode the response to see if the backend rotated the token.
	// If we're talking to an older backend that only returns
	// {ok, expiresAt}, `Token` stays empty and the caller keeps the
	// existing token — fully backwards compatible.
	var body struct {
		Token   string `json:"token"`
		Rotated bool   `json:"rotated"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Rotated && strings.TrimSpace(body.Token) != "" {
		return strings.TrimSpace(body.Token), nil
	}
	return "", nil
}

// Capture the identity before the network request, so a delayed response cannot
// replace a different account signed in while refresh was in flight.
func refreshSavedAuthSession(baseURL string, refresh func(string, string) (string, error)) (string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return "", err
	}
	if cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", fmt.Errorf("no saved session to refresh")
	}
	newToken, err := refresh(baseURL, cfg.AuthToken)
	if err != nil {
		return "", err
	}
	if newToken == "" {
		newToken = cfg.AuthToken
	}
	if err := persistRotatedAuthToken(cfg, newToken); err != nil {
		return "", err
	}
	return cfg.AuthToken, nil
}

func SignupWithEmail(baseURL, fullName, email, password string) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"fullName": fullName,
		"email":    email,
		"password": password,
	})
	resp, err := httpClient.Post(baseURL+"/auth/signup", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("signup request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("signup failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode signup response: %w", err)
	}
	if strings.TrimSpace(result.Token) == "" {
		return "", fmt.Errorf("signup response missing token")
	}
	return result.Token, nil
}

func LoginWithEmail(baseURL, email, password string) (string, error) {
	payload, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	resp, err := httpClient.Post(baseURL+"/auth/login", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Token        string `json:"token"`
		Requires2FA  bool   `json:"requires2fa"`
		PendingToken string `json:"pendingToken"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode login response: %w", err)
	}
	if result.Requires2FA {
		return "", fmt.Errorf("login requires 2FA; CLI email/password shortcut does not support that flow")
	}
	if strings.TrimSpace(result.Token) == "" {
		return "", fmt.Errorf("login response missing token")
	}
	return result.Token, nil
}

// RunnerInfo describes an active runner process for heartbeat reporting.
type RunnerInfo struct {
	TaskID         string `json:"taskId"`
	RunnerID       string `json:"runnerId"`
	Model          string `json:"model,omitempty"`
	PID            int    `json:"pid"`
	Status         string `json:"status"` // "running" or "idle"
	Title          string `json:"title"`
	CheckedAt      int64  `json:"checkedAt,omitempty"`
	Installed      bool   `json:"installed"`
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured"`
	// AuthPresent means the runner's own CLI reports a credential on this
	// machine. Local evidence — it cannot see a server-side revocation.
	AuthPresent bool `json:"authPresent"`
	// AuthVerified means the credential was exercised against the PROVIDER and
	// the provider answered (a completed turn / OAuth exchange), or was
	// explicitly refused by it. The dashboard must not render a green "signed
	// in" chip when this is explicitly false.
	// See RunnerRuntimeStatus.AuthVerified for why these are two fields.
	AuthVerified bool `json:"authVerified"`
	// AuthVerifiedAt is when the PROVIDER last spoke about this credential
	// (epoch ms) — a completed turn, a completed OAuth, or a rejection. It is
	// NOT the same as CheckedAt, which is when the agent last looked at local
	// state. A consumer reading the Convex row needs both: CheckedAt says how
	// stale the row is, AuthVerifiedAt says how stale the VERDICT is.
	//
	// Without this, persisting "authenticated" to Convex would just relocate
	// the false green from the agent's memory into the database.
	AuthVerifiedAt int64  `json:"authVerifiedAt,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
}

// sanitizeRunnerInfosForConvex strips host-identifying detail from runner rows
// before they ride the heartbeat into Convex.
//
// AuthSource is the offender. For claude/codex/opencode the detectors set it to
// the CREDENTIAL FILE PATH they matched — `/home/pokayoke/.codex/auth.json`,
// `/Users/<name>/.claude/.credentials.json`. Those are absolute filesystem
// paths, and the privacy contract forbids them in Convex precisely because they
// leak the user's home-directory username. They have been riding the heartbeat
// since AuthSource was added; carrying MORE per-runner auth state into Convex
// is exactly the wrong moment to leave that in place.
//
// The label a surface needs is "which store is this credential in", not "where
// on this disk". `~/.codex/auth.json` answers the first and not the second.
func sanitizeRunnerInfosForConvex(runners []RunnerInfo) []RunnerInfo {
	if len(runners) == 0 {
		return runners
	}
	out := make([]RunnerInfo, len(runners))
	copy(out, runners)
	for i := range out {
		out[i].AuthSource = sanitizeAuthSourceForConvex(out[i].AuthSource)
		out[i].Warning = redactHomePaths(out[i].Warning)
		out[i].Error = redactHomePaths(out[i].Error)
	}
	return out
}

func sanitizeAuthSourceForConvex(src string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	if !strings.ContainsAny(src, `/\`) {
		return src // a label like "codex login status" / "claude.ai · max"
	}
	if redacted := redactHomePaths(src); redacted != src {
		return redacted
	}
	if filepath.IsAbs(src) {
		// An absolute path outside HOME (CODEX_HOME on a shared volume, a
		// container tenant root). Keep the file identity, drop the location.
		return filepath.Base(src)
	}
	return src
}

// redactHomePaths rewrites this machine's home directory to "~" and neutralizes
// the generic /Users/<name> and /home/<name> shapes, which are what the
// privacy test scans for.
func redactHomePaths(s string) string {
	if s == "" {
		return s
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		s = strings.ReplaceAll(s, home, "~")
	}
	for _, prefix := range []string{"/Users/", "/home/", "/root/"} {
		for {
			idx := strings.Index(s, prefix)
			if idx < 0 {
				break
			}
			rest := s[idx+len(prefix):]
			end := strings.IndexAny(rest, `/ \t"',)`)
			if end < 0 {
				end = len(rest)
			}
			s = s[:idx] + "~" + rest[end:]
		}
	}
	return s
}

// newBearerRequest creates an HTTP request with Authorization: Bearer header.
func newBearerRequest(method, url, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

const (
	httpTimeout = 10 * time.Second
)

var httpClient = &http.Client{Timeout: httpTimeout}

// ValidateToken checks the auth token against the Convex backend.
// Returns nil on success, an error otherwise.
func ValidateToken(baseURL, token string) error {
	req, err := newBearerRequest("GET", baseURL+"/auth/validate", token, nil)
	if err != nil {
		return fmt.Errorf("create validate request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("validate token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validate token failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// UserInfo contains user profile information from Convex.
type UserInfo struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	FullName string `json:"fullName"`
	Provider string `json:"provider"`
	Scope    string `json:"scope,omitempty"`
	// IsOwner is the server-computed ownerAllowlist flag. Gates owner-only
	// experimental hardware-cell MCP tools (mcp_owner_gate.go).
	IsOwner bool `json:"isOwner"`
}

// ValidateTokenInfo checks the auth token against Convex and returns full user info.
func ValidateTokenInfo(baseURL, token string) (*UserInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/auth/validate", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create validate request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("validate token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("validate token failed (status %d)", resp.StatusCode)
	}

	var result struct {
		User UserInfo `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode validate response: %w", err)
	}
	return &result.User, nil
}

// ValidateTokenUser checks the auth token against Convex and returns the userId.
func ValidateTokenUser(baseURL, token string) (string, error) {
	info, err := ValidateTokenInfo(baseURL, token)
	if err != nil {
		return "", err
	}
	return info.UserID, nil
}

func ValidateTokenUserScope(baseURL, token string) (userID, scope string, err error) {
	info, err := ValidateTokenInfo(baseURL, token)
	if err != nil {
		return "", "", err
	}
	return info.UserID, normalizeSessionScope(info.Scope), nil
}

func normalizeSessionScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "machine", "tv", "watch", "vision", "spatial":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "full"
	}
}

// SdkTokenInfo contains validation results for an SDK token.
type SdkTokenInfo struct {
	UserID          string   `json:"userId"`
	Scopes          []string `json:"scopes"`
	AllowedCIDRs    []string `json:"allowedCIDRs"`
	SourceSurface   string   `json:"sourceSurface,omitempty"`
	TargetDeviceID  string   `json:"targetDeviceId,omitempty"`
	AllowedProjects []string `json:"allowedProjects,omitempty"`
}

// ValidateSdkTokenFull checks an SDK token against Convex and returns full info.
func ValidateSdkTokenFull(baseURL, token string) (*SdkTokenInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/sdk/token/validate", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create sdk token validate request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sdk token validate request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sdk token validation failed (status %d)", resp.StatusCode)
	}

	var result struct {
		User struct {
			UserID          string   `json:"userId"`
			Scopes          []string `json:"scopes"`
			AllowedCIDRs    []string `json:"allowedCIDRs"`
			SourceSurface   string   `json:"sourceSurface"`
			TargetDeviceID  string   `json:"targetDeviceId"`
			AllowedProjects []string `json:"allowedProjects"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sdk token validate response: %w", err)
	}
	return &SdkTokenInfo{
		UserID:          result.User.UserID,
		Scopes:          result.User.Scopes,
		AllowedCIDRs:    result.User.AllowedCIDRs,
		SourceSurface:   result.User.SourceSurface,
		TargetDeviceID:  result.User.TargetDeviceID,
		AllowedProjects: result.User.AllowedProjects,
	}, nil
}

// ValidateSdkToken is a convenience wrapper returning just the userId.
func ValidateSdkToken(baseURL, token string) (string, error) {
	info, err := ValidateSdkTokenFull(baseURL, token)
	if err != nil {
		return "", err
	}
	return info.UserID, nil
}

// CreateSdkToken creates a new SDK token via the Convex backend.
func CreateSdkToken(baseURL, sessionToken string, opts SdkTokenCreateOpts) (string, error) {
	payload := map[string]interface{}{}
	if opts.Label != "" {
		payload["label"] = opts.Label
	}
	if len(opts.Scopes) > 0 {
		payload["scopes"] = opts.Scopes
	}
	if len(opts.AllowedCIDRs) > 0 {
		payload["allowedCIDRs"] = opts.AllowedCIDRs
	}
	if len(opts.AllowedProjects) > 0 {
		payload["allowedProjects"] = opts.AllowedProjects
	}
	if opts.ExpiresInMs > 0 {
		payload["expiresInMs"] = opts.ExpiresInMs
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal sdk token request: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/sdk/token", sessionToken, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create sdk token request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sdk token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create sdk token failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode sdk token response: %w", err)
	}
	return result.Token, nil
}

// SdkTokenCreateOpts holds options for creating an SDK token.
type SdkTokenCreateOpts struct {
	Label           string
	Scopes          []string
	AllowedCIDRs    []string
	AllowedProjects []string
	ExpiresInMs     int64
}

// ReportSecurityEvent sends a security event to Convex.
func ReportSecurityEvent(baseURL, token, eventType string, details map[string]interface{}) {
	d, _ := json.Marshal(details)
	payload := map[string]string{
		"eventType": eventType,
		"details":   string(d),
	}
	body, _ := json.Marshal(payload)
	req, err := newBearerRequest("POST", baseURL+"/security/event", token, bytes.NewReader(body))
	if err != nil {
		return
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// RemoveDevice removes one device from the authenticated user's registry.
func RemoveDevice(baseURL, token, deviceID string) error {
	payload := map[string]string{"deviceId": deviceID}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal remove device: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/remove", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create remove device request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("remove device request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

// RequestPasswordReset sends a forgot-password email via Convex.
// Does not require auth — works with just the email address.
func RequestPasswordReset(baseURL, email string) error {
	payload, _ := json.Marshal(map[string]string{"email": email})
	resp, err := httpClient.Post(baseURL+"/auth/forgot-password", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("request password reset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("password reset request failed: %s", string(body))
	}
	return nil
}

// ChangePassword changes the password for an authenticated email user.
func ChangePassword(baseURL, token, currentPassword, newPassword string) error {
	payload, _ := json.Marshal(map[string]string{
		"currentPassword": currentPassword,
		"newPassword":     newPassword,
	})
	req, err := newBearerRequest("POST", baseURL+"/auth/change-password", token, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create change-password request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("change password request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("change password failed (status %d)", resp.StatusCode)
	}
	return nil
}

// RegisterDeviceRequest contains the fields sent when registering a device.
type RegisterDeviceRequest struct {
	Token           string                    `json:"-"`
	DeviceID        string                    `json:"deviceId"`
	Name            string                    `json:"name"`
	Platform        string                    `json:"platform"`
	PublicKey       string                    `json:"publicKey"`
	SignPublicKey   string                    `json:"signPublicKey,omitempty"`
	QuicHost        string                    `json:"quicHost"`
	QuicPort        int                       `json:"quicPort"`
	PublicEndpoints []string                  `json:"publicEndpoints,omitempty"`
	HardwareID      string                    `json:"hardwareId,omitempty"`
	RecoveryPosture *RecoveryTransportPosture `json:"recoveryPosture,omitempty"`
	HardwareProfile *DeviceHardwareProfile    `json:"hardwareProfile,omitempty"`
	// AgentVersion is the `const version` string from main.go. Reported
	// so the dashboard can show which build each machine is running.
	// Convex side gates the actual write to once per 24h + on change.
	AgentVersion string `json:"agentVersion,omitempty"`
}

// RelayServerInfo describes a relay server from platform config.
type RelayServerInfo struct {
	ID       string `json:"id"`
	QuicAddr string `json:"quicAddr"` // e.g. "relay.example.com:4433"
	HttpURL  string `json:"httpUrl"`  // e.g. "https://connect.yaver.io"
	Region   string `json:"region"`
	Priority int    `json:"priority"`
	// SpkiPin: base64 SHA-256 of the relay's SubjectPublicKeyInfo. When set, the
	// agent verifies the relay's self-signed QUIC cert against it (relay_pinning.go),
	// closing the active-MITM gap. camelCase to match the platformConfig payload.
	SpkiPin string `json:"spkiPin,omitempty"`
}

func configuredPublicEndpoints(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	type tunnelWithPriority struct {
		url      string
		priority int
	}
	var items []tunnelWithPriority
	seen := make(map[string]bool)
	for _, tunnel := range cfg.CloudflareTunnels {
		raw := strings.TrimRight(strings.TrimSpace(tunnel.URL), "/")
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		items = append(items, tunnelWithPriority{url: raw, priority: tunnel.Priority})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].priority != items[j].priority {
			if items[i].priority == 0 {
				return false
			}
			if items[j].priority == 0 {
				return true
			}
			return items[i].priority < items[j].priority
		}
		return items[i].url < items[j].url
	})
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.url)
	}
	// Append the relay-auto-provisioned <deviceId>.dev.yaver.io URL
	// (or whatever the relay's expose-domain is) as the last public
	// endpoint, lowest priority. The dashboard prefers it for probes
	// because it's HTTPS-direct (no /d/<id>/ path, no mixed content)
	// even when the user is behind NAT — traffic still routes
	// through the relay's QUIC tunnel under the hood.
	if assigned := getAssignedRelayURL(); assigned != "" && !seen[assigned] {
		seen[assigned] = true
		out = append(out, assigned)
	}
	// Manual list from config.json wins on first-position so
	// `yaver ssh @alias` and the dashboard SSH/Shell tooltip resolve
	// to the operator-provided host even when Cloudflare is wired
	// up too. Preserve the user-supplied order in the prefix.
	manual := make([]string, 0, len(cfg.PublicEndpoints))
	for _, raw := range cfg.PublicEndpoints {
		ep := strings.TrimRight(strings.TrimSpace(raw), "/")
		if ep == "" || seen[ep] {
			continue
		}
		seen[ep] = true
		manual = append(manual, ep)
	}
	if len(manual) > 0 {
		out = append(manual, out...)
	}
	return out
}

// assignedRelayURL is set by the relay-tunnel client after a
// successful register that returned an AssignedURL. Read by
// configuredPublicEndpoints so the heartbeat publishes it.
var (
	assignedRelayURLMu sync.RWMutex
	assignedRelayURL   string
)

func setAssignedRelayURL(url string) {
	assignedRelayURLMu.Lock()
	defer assignedRelayURLMu.Unlock()
	// The relay assigns a per-device subdomain URL like
	// `https://<deviceId>.yaver.io`. Until the wildcard *.yaver.io
	// DNS / Vercel routing is wired through to the relay, every
	// request to that subdomain returns 404 (DEPLOYMENT_NOT_FOUND)
	// — which makes the dashboard's per-device /health and
	// /projects polling fail with CORS preflight 404 on every tick,
	// producing the visible "blinking" between connected/disconnected
	// states. The relay's *path-style* endpoint
	// `public.yaver.io/d/<deviceId>` does work (verified — returns
	// the agent's 401 challenge instead of a Vercel 404). Transform
	// the assigned URL here so the heartbeat publishes the working
	// form to Convex; if/when the wildcard infra is fixed, we can
	// remove this rewrite and revert to subdomain-direct.
	assignedRelayURL = relayURLToPathStyle(url)
}

func getAssignedRelayURL() string {
	assignedRelayURLMu.RLock()
	defer assignedRelayURLMu.RUnlock()
	return assignedRelayURL
}

// relayURLToPathStyle rewrites a relay-assigned `<deviceId>.<apex>`
// subdomain URL into the working `public.<apex>/d/<deviceId>` path
// form. Leaves non-matching URLs unchanged so configured custom-
// relay hostnames keep working. Exported (lowercase still — same
// package) only so tests can hit it directly.
func relayURLToPathStyle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := urlpkg.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	if strings.EqualFold(u.Hostname(), "public.dev.yaver.io") && strings.HasPrefix(strings.ToLower(u.Path), "/d/") {
		out := scheme + "://public.yaver.io" + u.EscapedPath()
		if u.RawQuery != "" {
			out += "?" + u.RawQuery
		}
		return out
	}
	// Only rewrite the relay's canonical subdomain form — the host
	// must have exactly two leading labels (`<sub>.<apex>...`) where
	// the apex looks like yaver.io. Manual / custom-domain endpoints
	// (cloudflared, ngrok, user-configured CNAMEs) stay untouched.
	parts := strings.SplitN(u.Host, ".", 2)
	if len(parts) != 2 {
		return raw
	}
	sub, apex := parts[0], parts[1]
	if sub == "" || apex == "" {
		return raw
	}
	if !strings.HasSuffix(apex, "yaver.io") {
		return raw
	}
	// Already path-style (e.g. `public.yaver.io/d/<id>`) — leave it.
	if strings.HasPrefix(strings.ToLower(u.Path), "/d/") {
		return raw
	}
	gatewayHost := "public." + apex
	if strings.EqualFold(apex, "dev.yaver.io") {
		gatewayHost = "public.yaver.io"
	}
	return scheme + "://" + gatewayHost + "/d/" + sub
}

// PlatformConfig holds all platform-level config fetched from Convex /config.
type PlatformConfig struct {
	RelayServers  []RelayServerInfo             `json:"relayServers"`
	Runners       []backendRunnerFull           `json:"runners"`
	Models        []BackendModel                `json:"models"`
	ModelDefaults map[string]RunnerModelDefault `json:"modelDefaults"`
	ProductPolicy PlatformProductPolicy         `json:"productPolicy"`
}

type PlatformProductPolicy struct {
	DefaultRunner            string   `json:"defaultRunner"`
	PrimaryCloudProvider     string   `json:"primaryCloudProvider"`
	RemoteRunnerOAuthEnabled bool     `json:"remoteRunnerOAuthEnabled"`
	DeviceRoles              []string `json:"deviceRoles"`
}

// BackendModel mirrors the Convex aiModels table.
type BackendModel struct {
	ModelID                  string   `json:"modelId"`
	RunnerID                 string   `json:"runnerId"`
	Name                     string   `json:"name"`
	Description              string   `json:"description,omitempty"`
	ProviderID               string   `json:"providerId,omitempty"`
	ProviderName             string   `json:"providerName,omitempty"`
	Lifecycle                string   `json:"lifecycle,omitempty"`
	DefaultReasoningEffort   string   `json:"defaultReasoningEffort,omitempty"`
	SupportedReasoningEffort []string `json:"supportedReasoningEfforts,omitempty"`
	IsDefault                bool     `json:"isDefault,omitempty"`
	SortOrder                int      `json:"sortOrder"`
}

// FetchPlatformConfig fetches all platform config from Convex (relays, runners, models).
func FetchPlatformConfig(baseURL string) (*PlatformConfig, error) {
	req, err := http.NewRequest("GET", baseURL+"/config", nil)
	if err != nil {
		return nil, fmt.Errorf("create config request: %w", err)
	}
	// Global runner defaults are owner-managed live configuration. Ask shared
	// caches to revalidate so the one-minute refresh loop is not pinned behind
	// /config's five-minute anonymous cache lifetime.
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("config request failed (status %d)", resp.StatusCode)
	}

	var result PlatformConfig
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyProductPolicy(result.ProductPolicy)
	return &result, nil
}

// FetchRelayServers fetches just relay servers (convenience wrapper).
func FetchRelayServers(baseURL string) ([]RelayServerInfo, error) {
	cfg, err := FetchPlatformConfig(baseURL)
	if err != nil {
		return nil, err
	}
	return cfg.RelayServers, nil
}

// registerDeviceMaxAttempts bounds how many times RegisterDevice retries a
// transient failure. 4 attempts → 3 backoff sleeps (≈2.8s worst case).
const registerDeviceMaxAttempts = 4

// registerRetryBackoff is the delay before the Nth retry (attempt 1→400ms,
// 2→800ms, 3→1600ms). Pulled out so tests can assert the schedule without
// actually sleeping the wall clock.
func registerRetryBackoff(attempt int) time.Duration {
	return time.Duration(200*(1<<attempt)) * time.Millisecond
}

// RegisterDevice registers this desktop agent with the Convex backend.
//
// Convex mutations can return a transient 5xx — an OCC/write-conflict surfaced
// as a 500, or a cold-start blip. Observed in the field as a registerDevice
// 500 immediately after a fresh login that then succeeds on the very next
// attempt. A single un-retried failure used to leave the agent permanently
// half-registered: it still connects to the relay (so it can reach OUT and
// `yaver ping` works outbound), but with no Convex device row peers can't see
// or reach it and every heartbeat 500s with "Device not found" forever — only
// a manual restart healed it. So we retry transient failures (network error or
// 5xx) with a short backoff, and fail fast on 4xx (401 unauthorized,
// "belongs to another user") which are not retryable.
func RegisterDevice(baseURL string, r RegisterDeviceRequest) (string, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("marshal register request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= registerDeviceMaxAttempts; attempt++ {
		// Fresh request + body reader each attempt — a Reader is consumed
		// once, so it must be rebuilt before any retry.
		req, err := newBearerRequest("POST", baseURL+"/devices/register", r.Token, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("create register request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			// Network/transport error — transient, retry.
			lastErr = fmt.Errorf("register device request: %w", err)
		} else if resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			if r.HardwareProfile != nil {
				markHardwareProfileSent()
			}
			var result struct {
				Token string `json:"token"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return "", fmt.Errorf("decode register device response: %w", err)
			}
			return strings.TrimSpace(result.Token), nil
		} else {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("register device failed (status %d): %s", resp.StatusCode, string(respBody))
			// 4xx is a client error (bad/expired token, device owned by
			// another user) — not retryable, surface immediately so the
			// caller's conflict/auth handling kicks in.
			if resp.StatusCode < 500 {
				return "", lastErr
			}
		}

		if attempt < registerDeviceMaxAttempts {
			log.Printf("[register] transient failure (attempt %d/%d): %v — retrying", attempt, registerDeviceMaxAttempts, lastErr)
			time.Sleep(registerRetryBackoff(attempt))
		}
	}
	return "", lastErr
}

// ErrAuthExpired is returned when a 401 response indicates the token has expired.
var ErrAuthExpired = fmt.Errorf("auth token expired (401)")

// ErrDeviceIDStale is returned when Convex no longer has this device_id and
// could not safely map it to exactly one row for the same owner + hardware.
var ErrDeviceIDStale = fmt.Errorf("device identity stale")

// ErrDeviceIDAmbiguous is returned when more than one owned device row matches
// this hardware fingerprint, so automatic adoption would be a guess.
var ErrDeviceIDAmbiguous = fmt.Errorf("device identity ambiguous")

// DeviceMetricsSample is an optional CPU/RAM snapshot piggybacked onto a
// heartbeat. Folding it into the heartbeat (every 5 min) replaces the old
// standalone metricsLoop that fired /devices/metrics every 60s — ~43.8k
// Convex calls/mo/agent eliminated, with the mobile sparkline dropping from
// 60→12 points/hour (still fine for a coarse resource gauge). nil = don't
// report metrics on this beat.
type DeviceMetricsSample struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsedMB  float64 `json:"memoryUsedMb"`
	MemoryTotalMB float64 `json:"memoryTotalMb"`
	// DiskPercent is the home volume's used-%. omitempty so agents that
	// haven't completed a disk scan yet simply don't report it, rather than
	// writing a misleading 0% into the history.
	DiskPercent float64 `json:"diskPercent,omitempty"`
	// Capture time (Unix ms). The heartbeat batches every ~60s sample taken
	// since the last beat, so the backend records each at its real time
	// instead of collapsing them to one point — 60s sparkline resolution at
	// the same 12 heartbeats/hour (no extra Convex function calls).
	TimestampMs int64 `json:"timestampMs"`
}

// HeartbeatResult is the parsed /devices/heartbeat response. Beyond the
// synced connection preferences it carries pendingRescue/pendingPublish
// flags so the agent only polls the rescue/publish work-queues when Convex
// says there's actually something waiting (see the claim gating in
// heartbeatLoop) instead of firing both claim mutations every beat forever.
//
// GatingSupported records whether the backend actually returned those flags.
// A backend that predates claim-poll gating omits them entirely; the agent
// MUST then fall back to polling the queues every beat (old behavior) — a
// version-skewed new agent that treated "absent" as "false" would let
// short-TTL rescue commands (5 min) expire before the periodic fallback
// sweep (~30 min) ever claimed them, silently breaking remote recovery.
type HeartbeatResult struct {
	ConnectionPreferences []ConnectionPreference
	GatingSupported       bool
	PendingRescue         bool
	PendingPublish        bool
	// CanonicalDeviceID is non-empty when Convex accepted this heartbeat under
	// a surviving row that matches the same authenticated owner + hardwareId,
	// because the agent's persisted device_id pointed at a stale/deleted row.
	// The running process must adopt it and restart so relay/bus/task state all
	// use one identity again.
	CanonicalDeviceID    string
	RepairedDeviceIDFrom string
	// DesiredAgentVersion is non-empty when a surface asked this box to
	// update while it was unreachable. "latest" or a pinned release.
	// Deliberately NOT folded into GatingSupported: that flag means "the
	// backend speaks the rescue/publish gating contract", and an absent
	// desiredAgentVersion is the steady state on a perfectly current
	// backend, so letting it vote would make GatingSupported flap.
	DesiredAgentVersion string
}

// SendHeartbeat sends a heartbeat to the Convex backend so the device stays
// marked as online. Includes active runner info, a minimal installed-runner
// inventory, the preferred outbound IP (quicHost), every reachable
// LAN/Tailscale/Ethernet address the agent has (localIps) so mobile clients
// can race them in parallel during connect, and an optional CPU/RAM sample.
// Returns ErrAuthExpired if the server returns 401.
func SendHeartbeat(baseURL, token, deviceID string, runners []RunnerInfo, installedRunnerIDs []string, quicHost string, localIps []string, publicEndpoints []string, recoveryPosture *RecoveryTransportPosture, connectionPreferences []ConnectionPreference, metrics []DeviceMetricsSample) (*HeartbeatResult, error) {
	// Attempt bracketing for the resource warden's wedge detector
	// (resource_warden.go): the defer runs on EVERY exit — success, fast
	// failure, panic — so "started without finished" can only mean this call
	// is stuck inside (the tailscale exec-hang class), never that the network
	// is merely down.
	noteHeartbeatAttemptStarted()
	defer noteHeartbeatAttemptFinished()
	payload := map[string]interface{}{
		"deviceId":           deviceID,
		"runners":            sanitizeRunnerInfosForConvex(runners),
		"installedRunnerIds": installedRunnerIDs,
		"hardwareId":         HardwareID(),
		"agentVersion":       version,
	}
	// Discovery metadata only. The raw ADB serial/address is stripped by
	// realDevicesForHeartbeat; screen pixels and input never enter Convex.
	if realDevices := realDevicesForHeartbeat(); realDevices != nil {
		payload["realDevices"] = realDevices
	}
	if profile := hardwareProfileForHeartbeat(); profile != nil {
		payload["hardwareProfile"] = profile
	}
	// Live disk gauge. Unlike hardwareProfile (24h-gated, static specs), free
	// space is exactly the thing that changes — so it rides every beat. Reads
	// the diskhealth loop's cached snapshot; never scans on this path.
	if storage := storageSnapshotForHeartbeat(); storage != nil {
		payload["storage"] = storage
	}
	// Always include quicHost + localIps + publicEndpoints in the
	// heartbeat payload — even if empty — so a previously-set
	// Docker-bridge or stale public IP gets cleared on Convex.
	// Pre-fix the omit-on-empty branch left stale values in place: a
	// box that USED to advertise 172.18.0.1 (Docker bridge) and then
	// upgraded to a binary that filters those out would still see the
	// bridge address in mobile's device list because the field was
	// just never re-sent.
	payload["quicHost"] = quicHost
	// Publish whether this box currently has a LIVE relay tunnel (registered +
	// serving), not just that it heartbeats. Convex stores this in-place on the
	// device row (no history) so the phone/dashboard can show "online · no relay
	// path" instead of a bare "online" that 502s when off-LAN. Decoupled from
	// the relay's own presenceUpdate, which is opt-in and only fires on connect.
	// …and only while that tunnel can still CARRY a request. A tunnel that stays
	// registered but has stopped forwarding (relayDataPathUsable) used to keep
	// publishing relayConnected=true, so the phone kept choosing a relay path
	// that could only ever time out.
	payload["relayConnected"] = relayDataPathUsable()
	// Can this agent actually reboot its host? Verified (root, or passwordless
	// sudo), never inferred from the OS — so the phone and the dashboard can say
	// "no permission on this machine" and offer the opt-in grant, instead of
	// showing a Reboot button that can only fail when tapped.
	payload["canReboot"] = canRebootHost()
	// The rescue code this box is currently offering, if any.
	//
	// beginSelfNomination creates a device code when the relay refuses this box
	// with reason=dead_token, so an owner who is already signed in somewhere can
	// sign it back in with one tap. It used to only LOG the code — which is
	// circular, because the whole premise is that nobody can reach this machine
	// to read its log. currentSelfNominatedCode() had zero consumers: a correct
	// producer that shipped to nothing.
	//
	// The heartbeat is the one channel that still works in exactly this state.
	// It is outbound HTTPS to Convex and needs no relay, no inbound port and no
	// reachability — the same property that lets a dead-session box still pull a
	// queued agent update. So the code rides here, and surfaces render it as an
	// Approve button on the device card.
	//
	// Safe to publish, and not a credential: authorizeDeviceCode derives the
	// account from the APPROVER's bearer token, so holding this code without
	// already being signed in as the owner authorizes nothing. It is an
	// invitation. The minted session token never travels this way — the box
	// polls for it over the same channel `yaver auth` uses. Empty string when
	// nothing is pending, so the field also clears itself.
	payload["pendingAuthCode"] = currentSelfNominatedCode()
	// Coerce nil slices to empty arrays so JSON encodes them as `[]` not
	// `null`. The Convex http wrapper treats Array-valued localIps as
	// "deliberate clear", but `null` short-circuits to `undefined` and
	// skips the clear entirely — leaving stale Docker-bridge IPs frozen
	// on the device row across upgrades.
	if localIps == nil {
		localIps = []string{}
	}
	if publicEndpoints == nil {
		publicEndpoints = []string{}
	}
	payload["localIps"] = localIps
	payload["publicEndpoints"] = publicEndpoints
	if connectionPreferences == nil {
		connectionPreferences = []ConnectionPreference{}
	}
	payload["connectionPreferences"] = connectionPreferences
	// Publish-farm capability: which app stores this box can build for.
	// A non-empty list makes this device a publish-farm node the UI can
	// target. macOS does both (Xcode + Gradle); Linux does Android only;
	// iOS is Mac-only, forever. Static + privacy-safe.
	payload["publishCapabilities"] = computePublishCapabilities()
	// Connectivity shape + intent, for peers to act on when this box drops.
	// Sent ONLY when something actually changed: connStatusForHeartbeat
	// returns nil otherwise and the field is omitted, so a stable device costs
	// exactly what it cost before this existed. See conn_status.go.
	//
	// Bounded from OUTSIDE the probe, and the beat proceeds without it on
	// timeout. connStatus is ADVISORY — a hint about tailnet/mesh topology — and
	// advisory work must never sit in the critical path of the operation it
	// annotates. On 2026-07-25 a wedged `tailscale status` subprocess held this
	// call for 40 minutes and the agent therefore never heartbeated at all: the
	// box looked offline all day BECAUSE of a nice-to-have annotation. The inner
	// exec now has its own deadline+WaitDelay (tailscale_peers.go), but this
	// outer bound is the guarantee that no future probe added to
	// currentConnStatus can re-create the wedge.
	if cs := connStatusForHeartbeatBounded(3 * time.Second); cs != nil {
		payload["connStatus"] = cs
	}
	// Real, PROBED deploy capability — the honest counterpart to the line
	// above. publishCapabilities is a GOOS switch and will happily claim a Mac
	// with no Xcode can ship iOS; this one asked the toolchain. Cached and
	// refreshed off the hot path (see deploy_capabilities_convex.go), so the
	// first beat after boot may carry nothing rather than block. Names only —
	// no paths, versions, secret names or reasons ever reach Convex.
	if caps := deployCapabilitiesForHeartbeat(currentRuntimeVaultStore()); caps != nil {
		payload["deployCapabilities"] = caps.Ready
		payload["deployCapabilitiesBlocked"] = caps.Blocked
		payload["deployCapabilitiesAt"] = caps.Computed.UTC().Format(time.RFC3339)
	}
	// Coarse egress region (eu|us|ap|...) for the device picker — read from the
	// cached egress identity ONLY (no network on the hot path). The egress IP is
	// never sent; only the coarse region, same class as cloudMachines.region.
	// When not yet cached, warm it in the background for the next heartbeat
	// (respecting the disable_auto_public_ip opt-out via the loaded config).
	if region := cachedEgressRegion(); region != "" {
		payload["geoRegion"] = region
	} else {
		go func() {
			cfg, _ := LoadConfig()
			detectEgressIdentity(context.Background(), cfg, false)
		}()
	}
	if recoveryPosture != nil {
		payload["recoveryPosture"] = recoveryPosture
	}
	// Resource envelope from the in-process watchdog (resource_warden.go) —
	// pure-Go sample, zero exec on this path. Level + numbers only: this is
	// how a phone learns "the box is starving" BEFORE the box goes dark
	// (doctrine law 4; both 2026-07-27 box-deaths were invisible until fatal).
	if rp := ResourcePressureNow(); !rp.At.IsZero() {
		payload["resourcePressure"] = map[string]interface{}{
			"level":       rp.Level,
			"canFork":     rp.CanFork,
			"availableMb": rp.AvailableMb,
			"swapUsedMb":  rp.SwapUsedMb,
			"agentRssMb":  rp.AgentRSSMb,
			"children":    rp.Children,
			"reasons":     rp.Reasons,
		}
	}
	// Piggyback the batched CPU/RAM samples onto the heartbeat instead of a
	// separate /devices/metrics call. The backend records + prunes them in
	// the same heartbeat mutation, so this adds zero extra function calls.
	if len(metrics) > 0 {
		payload["metricsSamples"] = metrics
	}
	// Black box piggyback (flightrecorder.go): ship any lifecycle events the box
	// buffered while it was down. Normally nil — these fire on a boot or a
	// shutdown, not on a beat — so a steady-state heartbeat is unchanged. Read
	// here, but confirmed only after the request succeeds, so a failed beat
	// re-sends rather than silently losing the record of why the box died.
	flightPayload, flightEvents := PendingFlightEvents()
	if len(flightPayload) > 0 {
		payload["flightEvents"] = flightPayload
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal heartbeat: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/heartbeat", token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create heartbeat request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("heartbeat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		bodyText := strings.TrimSpace(string(respBody))
		switch {
		case strings.Contains(bodyText, "DEVICE_ID_STALE"):
			return nil, fmt.Errorf("%w: %s", ErrDeviceIDStale, bodyText)
		case strings.Contains(bodyText, "IDENTITY_DRIFT_AMBIGUOUS"):
			return nil, fmt.Errorf("%w: %s", ErrDeviceIDAmbiguous, bodyText)
		default:
			return nil, fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, bodyText)
		}
	}
	if _, ok := payload["hardwareProfile"]; ok {
		markHardwareProfileSent()
	}
	// 200 means Convex durably has them, so the watermark can advance and this
	// box stops re-sending its history on every beat. Same confirm-on-success
	// contract as markHardwareProfileSent above; anything before this line is a
	// failure path where re-sending is the correct behaviour.
	if len(flightEvents) > 0 {
		ConfirmFlightEventsSynced(flightEvents)
	}
	// Pointer bools so an absent field (old backend, no gating) is
	// distinguishable from an explicit false (new backend, nothing queued).
	var heartbeatResp struct {
		ConnectionPreferences []ConnectionPreference `json:"connectionPreferences"`
		PendingRescue         *bool                  `json:"pendingRescue"`
		PendingPublish        *bool                  `json:"pendingPublish"`
		DesiredAgentVersion   *string                `json:"desiredAgentVersion"`
		CanonicalDeviceID     *string                `json:"canonicalDeviceId"`
		RepairedDeviceIDFrom  *string                `json:"repairedDeviceIdFrom"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&heartbeatResp); err != nil {
		// Beat succeeded (200) but the body was unreadable — return a
		// non-nil result with gating unsupported so callers fall back to
		// polling the claim queues rather than silently skipping them.
		return &HeartbeatResult{}, nil
	}
	result := &HeartbeatResult{
		ConnectionPreferences: heartbeatResp.ConnectionPreferences,
		GatingSupported:       heartbeatResp.PendingRescue != nil || heartbeatResp.PendingPublish != nil,
		PendingRescue:         heartbeatResp.PendingRescue != nil && *heartbeatResp.PendingRescue,
		PendingPublish:        heartbeatResp.PendingPublish != nil && *heartbeatResp.PendingPublish,
	}
	if heartbeatResp.DesiredAgentVersion != nil {
		result.DesiredAgentVersion = strings.TrimSpace(*heartbeatResp.DesiredAgentVersion)
	}
	if heartbeatResp.CanonicalDeviceID != nil {
		result.CanonicalDeviceID = strings.TrimSpace(*heartbeatResp.CanonicalDeviceID)
	}
	if heartbeatResp.RepairedDeviceIDFrom != nil {
		result.RepairedDeviceIDFrom = strings.TrimSpace(*heartbeatResp.RepairedDeviceIDFrom)
	}
	return result, nil
}

// ClaimAgentUpdateRequest atomically reads and clears this device's
// pending update request. Returns "" when there was nothing queued —
// which is the steady state, so callers only bother when a heartbeat
// response carried a non-empty DesiredAgentVersion.
//
// The clear happens on claim, not on success: see the rationale on
// claimAgentUpdateRequest in backend/convex/devices.ts. A request that
// can't be satisfied must not re-fire every beat.
func ClaimAgentUpdateRequest(baseURL, token, deviceID string) (string, error) {
	body, err := json.Marshal(map[string]string{"deviceId": deviceID})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/devices/claim-update", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return "", ErrAuthExpired
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("claim-update returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Version *string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Version == nil {
		return "", nil
	}
	return strings.TrimSpace(*out.Version), nil
}

// CPU/RAM metrics are now folded into the heartbeat payload (see
// SendHeartbeat's DeviceMetricsSample) and recorded by the same heartbeat
// mutation. The old standalone ReportMetrics → POST /devices/metrics helper
// was removed to drop a per-60s Convex call; the backend route stays for
// older agents.

// SendDevLog sends a developer log to Convex (only stored for developer emails).
func SendDevLog(baseURL, token, email, tag, message string, data map[string]interface{}) {
	payload := map[string]interface{}{
		"email":   email,
		"source":  "agent",
		"level":   "debug",
		"tag":     tag,
		"message": message,
	}
	if data != nil {
		d, _ := json.Marshal(data)
		payload["data"] = string(d)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := newBearerRequest("POST", baseURL+"/dev/log", token, bytes.NewReader(body))
	if err != nil {
		return
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// ReportDeviceEvent sends a lifecycle event (crash, restart, etc.) to Convex.
func ReportDeviceEvent(baseURL, token, deviceID, event, details string) error {
	payload := map[string]interface{}{
		"deviceId": deviceID,
		"event":    event,
		"details":  details,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/event", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create event request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("event request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("event report failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// SetRunnerDown updates the runnerDown flag on the device in Convex.
func SetRunnerDown(baseURL, token, deviceID string, down bool) error {
	payload := map[string]interface{}{
		"deviceId":   deviceID,
		"runnerDown": down,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal runner-down: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/runner-down", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create runner-down request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("runner-down request: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// ReportRunnerUsage records how long a runner ran for a task.
func ReportRunnerUsage(baseURL, token, deviceID, taskID, runner, model, source string, durationSec float64, startedAt, finishedAt int64) error {
	payload := map[string]interface{}{
		"deviceId":    deviceID,
		"taskId":      taskID,
		"runner":      runner,
		"model":       model,
		"durationSec": durationSec,
		"startedAt":   startedAt,
		"finishedAt":  finishedAt,
		"source":      source,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/usage/record", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create usage request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("usage report failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// UserSettings holds the user's account-level settings from Convex.
type UserSettings struct {
	ForceRelay          bool   `json:"forceRelay"`
	RelayUrl            string `json:"relayUrl"`
	RelayPassword       string `json:"relayPassword"`
	TunnelUrl           string `json:"tunnelUrl"`
	RunnerID            string `json:"runnerId"`
	CustomRunnerCommand string `json:"customRunnerCommand"`
}

// FetchUserSettings fetches the user's settings from Convex GET /settings.
func FetchUserSettings(baseURL, token string) (*UserSettings, error) {
	req, err := newBearerRequest("GET", baseURL+"/settings", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create settings request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("settings request failed (status %d)", resp.StatusCode)
	}

	var result struct {
		Settings UserSettings `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return &result.Settings, nil
}

// shutdownConvexClient is a tight-timeout client for shutdown-path
// notifications. The default httpClient has multi-minute timeouts;
// we don't want a slow Convex to delay process exit by more than a
// couple of seconds. Mobile/web see correct status via the 30 s
// heartbeat freshness gate even if this best-effort call drops.
var shutdownConvexClient = &http.Client{Timeout: 5 * time.Second}

// MarkOffline tells the backend this device is going offline. Used
// for graceful step-down — the device record stays, just isOnline
// flips. Caller can come back online by re-starting the agent.
func MarkOffline(baseURL, token, deviceID string) error {
	payload := map[string]string{"deviceId": deviceID}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal offline: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/offline", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create offline request: %w", err)
	}

	resp, err := shutdownConvexClient.Do(req)
	if err != nil {
		return fmt.Errorf("offline request: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// RemoveDeviceShutdown is RemoveDevice's tight-timeout twin for the
// `yaver clean --including-auth` and npm preuninstall paths. The
// regular RemoveDevice uses httpClient (multi-minute default) which
// would hang process exit if Convex is slow; this one bounds at 5 s
// and logs-only on failure. Mobile / web see the device disappear
// either way thanks to the heartbeat freshness gate.
func RemoveDeviceShutdown(baseURL, token, deviceID string) error {
	payload := map[string]string{"deviceId": deviceID}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal remove: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/remove", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create remove request: %w", err)
	}

	resp, err := shutdownConvexClient.Do(req)
	if err != nil {
		return fmt.Errorf("remove request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("remove request returned HTTP %d", resp.StatusCode)
	}
	return nil
}
