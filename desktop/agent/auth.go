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
	"sort"
	"strings"
	"sync"
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

// SdkTokenInfo contains validation results for an SDK token.
type SdkTokenInfo struct {
	UserID               string   `json:"userId"`
	Scopes               []string `json:"scopes"`
	AllowedCIDRs         []string `json:"allowedCIDRs"`
	DelegatedGuestUserID string   `json:"delegatedGuestUserId,omitempty"`
	DelegatedGuestScope  string   `json:"delegatedGuestScope,omitempty"`
	SourceSurface        string   `json:"sourceSurface,omitempty"`
	TargetDeviceID       string   `json:"targetDeviceId,omitempty"`
	AllowedProjects      []string `json:"allowedProjects,omitempty"`
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
			UserID               string   `json:"userId"`
			Scopes               []string `json:"scopes"`
			AllowedCIDRs         []string `json:"allowedCIDRs"`
			DelegatedGuestUserID string   `json:"delegatedGuestUserId"`
			DelegatedGuestScope  string   `json:"delegatedGuestScope"`
			SourceSurface        string   `json:"sourceSurface"`
			TargetDeviceID       string   `json:"targetDeviceId"`
			AllowedProjects      []string `json:"allowedProjects"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode sdk token validate response: %w", err)
	}
	return &SdkTokenInfo{
		UserID:               result.User.UserID,
		Scopes:               result.User.Scopes,
		AllowedCIDRs:         result.User.AllowedCIDRs,
		DelegatedGuestUserID: result.User.DelegatedGuestUserID,
		DelegatedGuestScope:  result.User.DelegatedGuestScope,
		SourceSurface:        result.User.SourceSurface,
		TargetDeviceID:       result.User.TargetDeviceID,
		AllowedProjects:      result.User.AllowedProjects,
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
	// Scope is the access tier the server recorded for this invitation.
	// Mirrors GuestConfig.Scope — "full" or "feedback-only".
	Scope string `json:"scope,omitempty"`
}

// InviteGuestOpts controls the destination and optional scoping of an invitation.
// Exactly one of Email / UserID should be set. ProposedDeviceIDs narrows the
// invitation to a subset of the host's devices; the guest can trim this
// further on accept. Scope picks the access tier:
//
//	""              — server default ("feedback-only" for new invites)
//	"feedback-only" — hardened end-user tier (feedback / blackbox / voice only)
//	"full"          — classic teammate tier
type InviteGuestOpts struct {
	Email             string
	UserID            string
	ProposedDeviceIDs []string
	Scope             string
	// AllowedProjects narrows a guest's grant to one or more project names/slugs
	// on the host. Empty = all projects (current behavior). Most useful paired
	// with Scope == "feedback-only" when a host wants end-users of Project A to
	// file feedback without exposing Projects B / C.
	AllowedProjects []string
	// CanVibe opts a tester (Scope == "sdk-project") into the AI-improve surface
	// (/vibing). Ignored by the server for any other scope. Default false.
	CanVibe bool
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
	if s := strings.TrimSpace(opts.Scope); s != "" {
		payload["scope"] = s
	}
	if projs := cleanProjectList(opts.AllowedProjects); len(projs) > 0 {
		payload["allowedProjects"] = projs
	}
	if opts.CanVibe {
		payload["canVibe"] = true
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

// DeleteGuest hides a guest row from host-facing lists after revoking any live access.
func DeleteGuest(baseURL, token, inviteID, email, userID string) error {
	payload := map[string]string{}
	if id := strings.TrimSpace(inviteID); id != "" {
		payload["inviteId"] = id
	}
	if e := strings.TrimSpace(email); e != "" {
		payload["email"] = e
	}
	if u := strings.TrimSpace(userID); u != "" {
		payload["userId"] = u
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal delete guest: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/guests/delete", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create delete guest request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete guest request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

// LeaveSharedAccessResult is returned from the guest-side leave endpoint.
type LeaveSharedAccessResult struct {
	OK          bool   `json:"ok"`
	AlreadyGone bool   `json:"alreadyGone"`
	HostName    string `json:"hostName"`
	HostUserID  string `json:"hostUserId"`
}

// LeaveSharedAccess drops the CALLER's own guest access to a host's shared
// infra, identified by the host's public userId or email. The mirror of
// RevokeGuestWith: that one is the host pushing a guest out, this one is the
// guest walking out.
//
// Not a block — the host can invite again and the guest can accept again.
func LeaveSharedAccess(baseURL, token, hostUserID, hostEmail string) (*LeaveSharedAccessResult, error) {
	payload := map[string]string{}
	if u := strings.TrimSpace(hostUserID); u != "" {
		payload["hostUserId"] = u
	}
	if e := strings.TrimSpace(hostEmail); e != "" {
		payload["hostEmail"] = strings.ToLower(e)
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("host userId or email is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal leave: %w", err)
	}

	req, err := newBearerRequest("POST", baseURL+"/guests/leave", token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create leave request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("leave request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(respBody)))
	}
	var result LeaveSharedAccessResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode leave response: %w", err)
	}
	return &result, nil
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
	InviteID   string `json:"inviteId,omitempty"`
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
	GuestUserID string `json:"guestUserId"`
	GuestEmail  string `json:"guestEmail"`
	GuestName   string `json:"guestName"`
	// Scope is the access tier this grant was created with:
	//   "full"          — classic teammate scope (tasks, vibing, dev, builds, projects, ...).
	//   "feedback-only" — hardened end-user scope (feedback / blackbox / voice / health / info).
	// Legacy rows without scope come back as "full" (backward-compat).
	Scope string `json:"scope,omitempty"`
	// AllowedProjects narrows this grant to specific project slugs on the host.
	// Empty = all projects. Enforced on /feedback list filtering, /feedback fix
	// triggering, and /tasks workDir gating.
	AllowedProjects []string `json:"allowedProjects,omitempty"`
	// ProjectRoles carries per-project collaboration permissions materialized
	// from projectShares (backend/convex/projectShares.ts). See
	// guest_project_role.go for how they are enforced — and for why the agent
	// reads FLAGS rather than mapping role names to permissions itself.
	ProjectRoles []GuestProjectRole `json:"projectRoles,omitempty"`
	// CanVibe opts an sdk-project (tester) grant into the AI-improve surface
	// (/vibing). Default nil/false — vibe is explicit opt-in at invite time.
	// A tester's vibe task is always force-isolated + routed to a GLM/BYO
	// runner (handleVibingExecute), never the owner's Claude/Codex plan.
	CanVibe                   *bool    `json:"canVibe,omitempty"`
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

type HostShareCreateOpts struct {
	GuestEmail         string
	GuestUserID        string
	Label              string
	HostDeviceID       string
	InviteTTLMinutes   int
	SessionTTLMinutes  int
	IdleTimeoutMinutes int
	ToolingPreset      string
	ResourcePreset     string
	AllowInfra         *bool
	AllowTerminal      *bool
	AllowTunnel        *bool
	UseHostAgentTools  *bool
	UseHostInfra       *bool
	AllowedRunners     []string
	AllowedProjects    []string
}

type HostSharePolicy struct {
	ToolingPreset      string   `json:"toolingPreset,omitempty"`
	ResourcePreset     string   `json:"resourcePreset,omitempty"`
	AllowInfra         bool     `json:"allowInfra"`
	AllowTerminal      bool     `json:"allowTerminal"`
	AllowTunnel        bool     `json:"allowTunnel"`
	UseHostAgentTools  bool     `json:"useHostAgentTools"`
	UseHostInfra       bool     `json:"useHostInfra"`
	AllowedRunners     []string `json:"allowedRunners,omitempty"`
	AllowedProjects    []string `json:"allowedProjects,omitempty"`
	SessionTTLMinutes  int      `json:"sessionTtlMinutes,omitempty"`
	IdleTimeoutMinutes int      `json:"idleTimeoutMinutes,omitempty"`
}

type HostShareCreateResult struct {
	OK              bool            `json:"ok"`
	InviteID        string          `json:"inviteId"`
	InviteCode      string          `json:"inviteCode"`
	InviteExpiresAt int64           `json:"inviteExpiresAt"`
	HostName        string          `json:"hostName"`
	HostEmail       string          `json:"hostEmail"`
	GuestRegistered bool            `json:"guestRegistered"`
	GuestEmail      string          `json:"guestEmail,omitempty"`
	GuestUserID     string          `json:"guestUserId,omitempty"`
	Policy          HostSharePolicy `json:"policy"`
}

type HostShareInvitePreview struct {
	InviteCode         string   `json:"inviteCode"`
	Status             string   `json:"status"`
	Label              string   `json:"label,omitempty"`
	HostUserID         string   `json:"hostUserId"`
	HostUserIDString   string   `json:"hostUserIdString"`
	HostName           string   `json:"hostName"`
	HostEmail          string   `json:"hostEmail"`
	GuestEmail         string   `json:"guestEmail,omitempty"`
	GuestUserID        string   `json:"guestUserId,omitempty"`
	HostDeviceID       string   `json:"hostDeviceId,omitempty"`
	InviteExpiresAt    int64    `json:"inviteExpiresAt"`
	SessionTTLMinutes  int      `json:"sessionTtlMinutes"`
	IdleTimeoutMinutes int      `json:"idleTimeoutMinutes"`
	ToolingPreset      string   `json:"toolingPreset,omitempty"`
	ResourcePreset     string   `json:"resourcePreset,omitempty"`
	AllowInfra         bool     `json:"allowInfra"`
	AllowTerminal      bool     `json:"allowTerminal"`
	AllowTunnel        bool     `json:"allowTunnel"`
	UseHostAgentTools  bool     `json:"useHostAgentTools"`
	UseHostInfra       bool     `json:"useHostInfra"`
	AllowedRunners     []string `json:"allowedRunners,omitempty"`
	AllowedProjects    []string `json:"allowedProjects,omitempty"`
	Targeted           bool     `json:"targeted"`
	CreatedAt          int64    `json:"createdAt"`
}

type HostShareJoinResult struct {
	OK        bool            `json:"ok"`
	SessionID string          `json:"sessionId"`
	HostName  string          `json:"hostName"`
	HostEmail string          `json:"hostEmail"`
	ExpiresAt int64           `json:"expiresAt"`
	Policy    HostSharePolicy `json:"policy"`
}

type HostShareInviteInfo struct {
	InviteCode         string `json:"inviteCode"`
	Status             string `json:"status"`
	Label              string `json:"label,omitempty"`
	GuestEmail         string `json:"guestEmail,omitempty"`
	GuestUserID        string `json:"guestUserId,omitempty"`
	GuestName          string `json:"guestName,omitempty"`
	HostName           string `json:"hostName,omitempty"`
	HostEmail          string `json:"hostEmail,omitempty"`
	HostDeviceID       string `json:"hostDeviceId,omitempty"`
	GuestDeviceID      string `json:"guestDeviceId,omitempty"`
	InviteExpiresAt    int64  `json:"inviteExpiresAt"`
	SessionTTLMinutes  int    `json:"sessionTtlMinutes"`
	IdleTimeoutMinutes int    `json:"idleTimeoutMinutes"`
	ToolingPreset      string `json:"toolingPreset,omitempty"`
	ResourcePreset     string `json:"resourcePreset,omitempty"`
	CreatedAt          int64  `json:"createdAt,omitempty"`
	AcceptedAt         int64  `json:"acceptedAt,omitempty"`
	RevokedAt          int64  `json:"revokedAt,omitempty"`
}

type HostShareSessionInfo struct {
	SessionID          string          `json:"sessionId"`
	InviteID           string          `json:"inviteId"`
	Status             string          `json:"status"`
	Label              string          `json:"label,omitempty"`
	HostName           string          `json:"hostName"`
	HostEmail          string          `json:"hostEmail"`
	GuestName          string          `json:"guestName"`
	GuestEmail         string          `json:"guestEmail"`
	HostDeviceID       string          `json:"hostDeviceId,omitempty"`
	GuestDeviceID      string          `json:"guestDeviceId,omitempty"`
	Policy             HostSharePolicy `json:"policy"`
	StartedAt          int64           `json:"startedAt"`
	ExpiresAt          int64           `json:"expiresAt"`
	IdleTimeoutMinutes int             `json:"idleTimeoutMinutes"`
	LastActivityAt     int64           `json:"lastActivityAt"`
}

type HostShareAccessInfo struct {
	SessionID          string          `json:"sessionId"`
	InviteID           string          `json:"inviteId"`
	Label              string          `json:"label,omitempty"`
	HostDeviceID       string          `json:"hostDeviceId,omitempty"`
	GuestDeviceID      string          `json:"guestDeviceId,omitempty"`
	GuestUserID        string          `json:"guestUserId"`
	GuestEmail         string          `json:"guestEmail"`
	GuestName          string          `json:"guestName"`
	Policy             HostSharePolicy `json:"policy"`
	ExpiresAt          int64           `json:"expiresAt"`
	IdleTimeoutMinutes int             `json:"idleTimeoutMinutes"`
	LastActivityAt     int64           `json:"lastActivityAt"`
}

func CreateHostShareInvite(baseURL, token string, opts HostShareCreateOpts) (*HostShareCreateResult, error) {
	payload := map[string]interface{}{}
	if v := strings.TrimSpace(opts.GuestEmail); v != "" {
		payload["guestEmail"] = v
	}
	if v := strings.TrimSpace(opts.GuestUserID); v != "" {
		payload["guestUserId"] = v
	}
	if v := strings.TrimSpace(opts.Label); v != "" {
		payload["label"] = v
	}
	if v := strings.TrimSpace(opts.HostDeviceID); v != "" {
		payload["hostDeviceId"] = v
	}
	if opts.InviteTTLMinutes > 0 {
		payload["inviteTtlMinutes"] = opts.InviteTTLMinutes
	}
	if opts.SessionTTLMinutes > 0 {
		payload["sessionTtlMinutes"] = opts.SessionTTLMinutes
	}
	if opts.IdleTimeoutMinutes > 0 {
		payload["idleTimeoutMinutes"] = opts.IdleTimeoutMinutes
	}
	if v := strings.TrimSpace(opts.ToolingPreset); v != "" {
		payload["toolingPreset"] = v
	}
	if v := strings.TrimSpace(opts.ResourcePreset); v != "" {
		payload["resourcePreset"] = v
	}
	if opts.AllowInfra != nil {
		payload["allowInfra"] = *opts.AllowInfra
	}
	if opts.AllowTerminal != nil {
		payload["allowTerminal"] = *opts.AllowTerminal
	}
	if opts.AllowTunnel != nil {
		payload["allowTunnel"] = *opts.AllowTunnel
	}
	if opts.UseHostAgentTools != nil {
		payload["useHostAgentTools"] = *opts.UseHostAgentTools
	}
	if opts.UseHostInfra != nil {
		payload["useHostInfra"] = *opts.UseHostInfra
	}
	if v := cleanProjectList(opts.AllowedProjects); len(v) > 0 {
		payload["allowedProjects"] = v
	}
	if len(opts.AllowedRunners) > 0 {
		payload["allowedRunners"] = opts.AllowedRunners
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host-share invite: %w", err)
	}
	req, err := newBearerRequest("POST", baseURL+"/host-share/create", token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create host-share request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share create request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result HostShareCreateResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share create response: %w", err)
	}
	return &result, nil
}

func FindHostShareInvite(baseURL, token, code string) (*HostShareInvitePreview, error) {
	url := baseURL + "/host-share/invite?code=" + urlpkg.QueryEscape(strings.ToUpper(strings.TrimSpace(code)))
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create host-share preview request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share preview request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result HostShareInvitePreview
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share preview response: %w", err)
	}
	return &result, nil
}

func JoinHostShareByCode(baseURL, token, code string) (*HostShareJoinResult, error) {
	cfg, _ := LoadConfig()
	bodyMap := map[string]string{"code": strings.ToUpper(strings.TrimSpace(code))}
	if cfg != nil && strings.TrimSpace(cfg.DeviceID) != "" {
		bodyMap["guestDeviceId"] = strings.TrimSpace(cfg.DeviceID)
	}
	body, _ := json.Marshal(bodyMap)
	req, err := newBearerRequest("POST", baseURL+"/host-share/join", token, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create host-share join request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share join request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result HostShareJoinResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share join response: %w", err)
	}
	return &result, nil
}

func RevokeHostShareInvite(baseURL, token, code string) error {
	body, _ := json.Marshal(map[string]string{"code": strings.ToUpper(strings.TrimSpace(code))})
	req, err := newBearerRequest("POST", baseURL+"/host-share/revoke", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create host-share revoke request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("host-share revoke request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

func FetchHostShareInvites(baseURL, token, role string) ([]HostShareInviteInfo, error) {
	url := baseURL + "/host-share/list"
	if strings.TrimSpace(role) != "" {
		url += "?role=" + urlpkg.QueryEscape(role)
	}
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create host-share list request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share list request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result struct {
		Invites []HostShareInviteInfo `json:"invites"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share list response: %w", err)
	}
	return result.Invites, nil
}

func FetchHostShareSessions(baseURL, token, role string) ([]HostShareSessionInfo, error) {
	url := baseURL + "/host-share/sessions"
	if strings.TrimSpace(role) != "" {
		url += "?role=" + urlpkg.QueryEscape(role)
	}
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create host-share sessions request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share sessions request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result struct {
		Sessions []HostShareSessionInfo `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share sessions response: %w", err)
	}
	return result.Sessions, nil
}

func FetchHostShareAccess(baseURL, token, guestUserID, deviceID string) (*HostShareAccessInfo, error) {
	url := baseURL + "/host-share/access?guestUserId=" + urlpkg.QueryEscape(strings.TrimSpace(guestUserID))
	if strings.TrimSpace(deviceID) != "" {
		url += "&deviceId=" + urlpkg.QueryEscape(strings.TrimSpace(deviceID))
	}
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create host-share access request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share access request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result struct {
		Access *HostShareAccessInfo `json:"access"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share access response: %w", err)
	}
	return result.Access, nil
}

func TouchHostShareSession(baseURL, token, sessionID string) error {
	body, _ := json.Marshal(map[string]string{"sessionId": strings.TrimSpace(sessionID)})
	req, err := newBearerRequest("POST", baseURL+"/host-share/touch", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create host-share touch request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("host-share touch request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

func EndHostShareSession(baseURL, token, sessionID string) error {
	body, _ := json.Marshal(map[string]string{"sessionId": strings.TrimSpace(sessionID)})
	req, err := newBearerRequest("POST", baseURL+"/host-share/end", token, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create host-share end request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("host-share end request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s", string(respBody))
	}
	return nil
}

func FetchHostSharePeerAccess(baseURL, token, hostUserID, deviceID string) (*HostShareAccessInfo, error) {
	url := baseURL + "/host-share/peer-access?hostUserId=" + urlpkg.QueryEscape(strings.TrimSpace(hostUserID))
	if strings.TrimSpace(deviceID) != "" {
		url += "&deviceId=" + urlpkg.QueryEscape(strings.TrimSpace(deviceID))
	}
	req, err := newBearerRequest("GET", url, token, nil)
	if err != nil {
		return nil, fmt.Errorf("create host-share peer access request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("host-share peer access request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s", string(respBody))
	}
	var result struct {
		Access *HostShareAccessInfo `json:"access"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode host-share peer access response: %w", err)
	}
	return result.Access, nil
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
	payload := map[string]interface{}{
		"deviceId":           deviceID,
		"runners":            runners,
		"installedRunnerIds": installedRunnerIDs,
		"hardwareId":         HardwareID(),
		"agentVersion":       version,
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
	if cs := connStatusForHeartbeat(context.Background()); cs != nil {
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
		return nil, fmt.Errorf("heartbeat failed (status %d): %s", resp.StatusCode, string(respBody))
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
