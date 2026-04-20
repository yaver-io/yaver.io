package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strings"
	"time"
)

// RefreshToken extends the session expiry by 1 year and, if the
// backend supports rotation (yaver.io Convex, Apr 2026+), returns a
// freshly-minted bearer token. A leaked token then only lives until
// the next daily refresh (~24 h max blast radius) — invisible to the
// user, automatic.
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
	TaskID   string `json:"taskId"`
	RunnerID string `json:"runnerId"`
	Model    string `json:"model,omitempty"`
	PID      int    `json:"pid"`
	Status   string `json:"status"` // "running" or "idle"
	Title    string `json:"title"`
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

// SdkTokenInfo contains validation results for an SDK token.
type SdkTokenInfo struct {
	UserID       string   `json:"userId"`
	Scopes       []string `json:"scopes"`
	AllowedCIDRs []string `json:"allowedCIDRs"`
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
			UserID       string   `json:"userId"`
			Scopes       []string `json:"scopes"`
			AllowedCIDRs []string `json:"allowedCIDRs"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sdk token validate response: %w", err)
	}
	return &SdkTokenInfo{
		UserID:       result.User.UserID,
		Scopes:       result.User.Scopes,
		AllowedCIDRs: result.User.AllowedCIDRs,
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
	Label        string
	Scopes       []string
	AllowedCIDRs []string
	ExpiresInMs  int64
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

// FetchGuestUserIds fetches the list of approved guest userIds from Convex.
// The agent scopes this to its concrete device ID so a guest granted to one
// host device is not automatically trusted by every other device on the same account.
func FetchGuestUserIds(baseURL, token string, deviceID ...string) ([]string, error) {
	url := baseURL + "/guests/allowed"
	scopedDeviceID := ""
	if len(deviceID) > 0 {
		scopedDeviceID = deviceID[0]
	}
	if v := strings.TrimSpace(scopedDeviceID); v != "" {
		url += "?deviceId=" + urlpkg.QueryEscape(v)
	}
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create guest list request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch guest list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("guest list failed (status %d)", resp.StatusCode)
	}

	var result struct {
		GuestUserIds []string `json:"guestUserIds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode guest list: %w", err)
	}
	return result.GuestUserIds, nil
}

// InviteResult holds the response from a guest invitation.
type InviteResult struct {
	InviteCode      string `json:"inviteCode"`
	GuestRegistered bool   `json:"guestRegistered"`
	GuestUserID     string `json:"guestUserId,omitempty"`
	GuestEmail      string `json:"guestEmail,omitempty"`
}

// InviteGuestOpts controls the destination and optional scoping of an invitation.
// Exactly one of Email / UserID should be set. ProposedDeviceIDs narrows the
// invitation to a subset of the host's devices; the guest can trim this
// further on accept.
type InviteGuestOpts struct {
	Email             string
	UserID            string
	ProposedDeviceIDs []string
}

// InviteGuest sends a guest invitation via Convex by email.
func InviteGuest(baseURL, token, email string) (*InviteResult, error) {
	return InviteGuestWith(baseURL, token, InviteGuestOpts{Email: email})
}

// InviteGuestWith sends a guest invitation with full options.
func InviteGuestWith(baseURL, token string, opts InviteGuestOpts) (*InviteResult, error) {
	payload := map[string]interface{}{}
	if e := strings.TrimSpace(opts.Email); e != "" {
		payload["email"] = e
	}
	if u := strings.TrimSpace(opts.UserID); u != "" {
		payload["userId"] = u
	}
	if len(opts.ProposedDeviceIDs) > 0 {
		payload["deviceIds"] = opts.ProposedDeviceIDs
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal invite: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/guests/invite", token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create invite request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("invite request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}

	var result InviteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode invite response: %w", err)
	}
	return &result, nil
}

// RevokeGuest revokes guest access via Convex by email.
func RevokeGuest(baseURL, token, email string) error {
	return RevokeGuestWith(baseURL, token, email, "")
}

// RevokeGuestWith revokes guest access by email or by public userId.
func RevokeGuestWith(baseURL, token, email, userID string) error {
	payload := map[string]string{}
	if e := strings.TrimSpace(email); e != "" {
		payload["email"] = e
	}
	if u := strings.TrimSpace(userID); u != "" {
		payload["userId"] = u
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal revoke: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/guests/revoke", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create revoke request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

// AcceptInviteResult is returned from the accept-code endpoint.
type AcceptInviteResult struct {
	OK        bool   `json:"ok"`
	HostName  string `json:"hostName"`
	HostEmail string `json:"hostEmail"`
}

// AcceptGuestByCode accepts a pending invitation by 6-char code.
// When approvedDeviceIDs is non-empty, the grant is scoped to those devices.
func AcceptGuestByCode(baseURL, token, code string, approvedDeviceIDs []string) (*AcceptInviteResult, error) {
	payload := map[string]interface{}{"code": strings.ToUpper(strings.TrimSpace(code))}
	if len(approvedDeviceIDs) > 0 {
		payload["approvedDeviceIds"] = approvedDeviceIDs
	}
	body, _ := json.Marshal(payload)
	req, err := newBearerRequest("POST", baseURL+"/guests/accept-code", token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create accept request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("accept request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result AcceptInviteResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode accept response: %w", err)
	}
	return &result, nil
}

// InvitationHostDevice describes one of the host's devices from the guest's
// preview payload.
type InvitationHostDevice struct {
	DeviceID      string `json:"deviceId"`
	Name          string `json:"name"`
	Platform      string `json:"platform"`
	LastHeartbeat int64  `json:"lastHeartbeat"`
	Proposed      bool   `json:"proposed"`
}

// InvitationPreview is the response from /guests/find-by-code.
type InvitationPreview struct {
	InviteCode        string                 `json:"inviteCode"`
	HostUserID        string                 `json:"hostUserId"`
	HostName          string                 `json:"hostName"`
	HostEmail         string                 `json:"hostEmail"`
	HostUserIDString  string                 `json:"hostUserIdString"`
	ProposedDeviceIDs []string               `json:"proposedDeviceIds"`
	HostDevices       []InvitationHostDevice `json:"hostDevices"`
	InvitedByUserID   bool                   `json:"invitedByUserId"`
	ExpiresAt         int64                  `json:"expiresAt"`
	CreatedAt         int64                  `json:"createdAt"`
}

// FindInviteByCode previews an invitation before accepting.
func FindInviteByCode(baseURL, token, code string) (*InvitationPreview, error) {
	url := baseURL + "/guests/find-by-code?code=" + urlpkg.QueryEscape(strings.ToUpper(strings.TrimSpace(code)))
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create find request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("find request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var preview InvitationPreview
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		return nil, fmt.Errorf("decode preview: %w", err)
	}
	return &preview, nil
}

// PublicUser is a redacted user profile from /users/lookup.
type PublicUser struct {
	UserID   string `json:"userId"`
	FullName string `json:"fullName"`
	Email    string `json:"email"`
}

// LookupPublicUser resolves a userId string to a user profile.
func LookupPublicUser(baseURL, token, userID string) (*PublicUser, error) {
	url := baseURL + "/users/lookup?userId=" + urlpkg.QueryEscape(strings.TrimSpace(userID))
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create lookup request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var user PublicUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode lookup: %w", err)
	}
	return &user, nil
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

// GuestInfo describes a guest from the Convex /guests/list endpoint.
type GuestInfo struct {
	Email      string `json:"email"`
	Status     string `json:"status"`
	FullName   string `json:"fullName,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	AcceptedAt int64  `json:"acceptedAt,omitempty"`
	RevokedAt  int64  `json:"revokedAt,omitempty"`
}

// FetchGuestList fetches the full guest list (with status) from Convex.
func FetchGuestList(baseURL, token string) ([]GuestInfo, error) {
	req, err := newBearerRequest("GET", baseURL+"/guests/list", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create guest list request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch guest list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("guest list failed (status %d)", resp.StatusCode)
	}

	var result struct {
		Guests []GuestInfo `json:"guests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode guest list: %w", err)
	}
	return result.Guests, nil
}

// GuestConfig describes the config for a single guest from Convex.
type GuestConfig struct {
	GuestUserID               string   `json:"guestUserId"`
	GuestEmail                string   `json:"guestEmail"`
	GuestName                 string   `json:"guestName"`
	DailyTokenLimit           *int     `json:"dailyTokenLimit,omitempty"`
	AllowedRunners            []string `json:"allowedRunners,omitempty"`
	UsageMode                 string   `json:"usageMode,omitempty"`
	ShareAllDevices           *bool    `json:"shareAllDevices,omitempty"`
	DeviceIDs                 []string `json:"deviceIds,omitempty"`
	ShareAllMachines          *bool    `json:"shareAllMachines,omitempty"`
	MachineIDs                []string `json:"machineIds,omitempty"`
	ResourcePreset            string   `json:"resourcePreset,omitempty"`
	UseHostAPIKeys            *bool    `json:"useHostApiKeys,omitempty"`
	AllowGuestProvidedAPIKeys *bool    `json:"allowGuestProvidedApiKeys,omitempty"`
	AllowDesktopControl       *bool    `json:"allowDesktopControl,omitempty"`
	AllowBrowserControl       *bool    `json:"allowBrowserControl,omitempty"`
	AllowTunnelForward        *bool    `json:"allowTunnelForward,omitempty"`
	RequireIsolation          *bool    `json:"requireIsolation,omitempty"`
	CPULimitPercent           *int     `json:"cpuLimitPercent,omitempty"`
	RAMLimitMB                *int     `json:"ramLimitMb,omitempty"`
	PriorityMode              string   `json:"priorityMode,omitempty"`
	Schedule                  *struct {
		StartHour int    `json:"startHour"`
		EndHour   int    `json:"endHour"`
		Timezone  string `json:"timezone,omitempty"`
	} `json:"schedule,omitempty"`
}

// FetchGuestConfigs fetches guest configs from Convex.
func FetchGuestConfigs(baseURL, token string) ([]GuestConfig, error) {
	req, err := newBearerRequest("GET", baseURL+"/guests/config", token, nil)
	if err != nil {
		return nil, fmt.Errorf("create guest config request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch guest config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("guest config failed (status %d)", resp.StatusCode)
	}

	var result struct {
		Configs []GuestConfig `json:"configs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode guest config: %w", err)
	}
	return result.Configs, nil
}

// UpdateGuestConfig updates a guest's config via Convex.
func UpdateGuestConfig(baseURL, token string, cfg map[string]interface{}) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal guest config: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/guests/config", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create guest config request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update guest config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

// RecordGuestUsage reports guest usage (task-seconds) to Convex.
func RecordGuestUsage(baseURL, token, guestUserID string, secondsUsed float64, date string) error {
	payload := map[string]interface{}{
		"guestUserId": guestUserID,
		"secondsUsed": secondsUsed,
		"date":        date,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal guest usage: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/guests/usage", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create guest usage request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("record guest usage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("record guest usage failed: %s", string(respBody))
	}
	return nil
}

// GuestUsageInfo describes daily usage for a guest.
type GuestUsageInfo struct {
	GuestEmail  string  `json:"guestEmail"`
	GuestName   string  `json:"guestName"`
	Date        string  `json:"date"`
	SecondsUsed float64 `json:"secondsUsed"`
}

// FetchGuestUsage fetches guest usage for a date from Convex.
func FetchGuestUsage(baseURL, token, date string) ([]GuestUsageInfo, error) {
	url := baseURL + "/guests/usage"
	if date != "" {
		url += "?date=" + date
	}
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create guest usage request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch guest usage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("guest usage failed (status %d)", resp.StatusCode)
	}

	var result struct {
		Usage []GuestUsageInfo `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode guest usage: %w", err)
	}
	return result.Usage, nil
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
	Token      string `json:"-"`
	DeviceID   string `json:"deviceId"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	PublicKey  string `json:"publicKey"`
	QuicHost   string `json:"quicHost"`
	QuicPort   int    `json:"quicPort"`
	HardwareID string `json:"hardwareId,omitempty"`
}

// RelayServerInfo describes a relay server from platform config.
type RelayServerInfo struct {
	ID       string `json:"id"`
	QuicAddr string `json:"quicAddr"` // e.g. "relay.example.com:4433"
	HttpURL  string `json:"httpUrl"`  // e.g. "https://connect.yaver.io"
	Region   string `json:"region"`
	Priority int    `json:"priority"`
}

// PlatformConfig holds all platform-level config fetched from Convex /config.
type PlatformConfig struct {
	RelayServers []RelayServerInfo   `json:"relayServers"`
	Runners      []backendRunnerFull `json:"runners"`
	Models       []BackendModel      `json:"models"`
}

// BackendModel mirrors the Convex aiModels table.
type BackendModel struct {
	ModelID     string `json:"modelId"`
	RunnerID    string `json:"runnerId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	IsDefault   bool   `json:"isDefault,omitempty"`
	SortOrder   int    `json:"sortOrder"`
}

// FetchPlatformConfig fetches all platform config from Convex (relays, runners, models).
func FetchPlatformConfig(baseURL string) (*PlatformConfig, error) {
	req, err := http.NewRequest("GET", baseURL+"/config", nil)
	if err != nil {
		return nil, fmt.Errorf("create config request: %w", err)
	}

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

// RegisterDevice registers this desktop agent with the Convex backend.
func RegisterDevice(baseURL string, r RegisterDeviceRequest) (string, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("marshal register request: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/register", r.Token, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create register request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("register device request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("register device failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode register device response: %w", err)
	}
	return strings.TrimSpace(result.Token), nil
}

// ErrAuthExpired is returned when a 401 response indicates the token has expired.
var ErrAuthExpired = fmt.Errorf("auth token expired (401)")

// SendHeartbeat sends a heartbeat to the Convex backend so the device stays
// marked as online. Includes active runner info, the preferred outbound IP
// (quicHost), and every reachable LAN/Tailscale/Ethernet address the agent
// has (localIps) so mobile clients can race them in parallel during connect.
// Returns ErrAuthExpired if the server returns 401.
func SendHeartbeat(baseURL, token, deviceID string, runners []RunnerInfo, quicHost string, localIps []string) error {
	payload := map[string]interface{}{
		"deviceId":   deviceID,
		"runners":    runners,
		"hardwareId": HardwareID(),
	}
	if quicHost != "" {
		payload["quicHost"] = quicHost
	}
	if len(localIps) > 0 {
		payload["localIps"] = localIps
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal heartbeat: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/heartbeat", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create heartbeat request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ReportMetrics sends CPU/RAM metrics to Convex.
func ReportMetrics(baseURL, token, deviceID string, cpuPercent, memUsedMB, memTotalMB float64) error {
	payload := map[string]interface{}{
		"deviceId":      deviceID,
		"cpuPercent":    cpuPercent,
		"memoryUsedMb":  memUsedMB,
		"memoryTotalMb": memTotalMB,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/devices/metrics", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create metrics request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("metrics request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrAuthExpired
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("metrics failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	return nil
}

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
