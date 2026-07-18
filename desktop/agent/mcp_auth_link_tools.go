package main

// mcp_auth_link_tools.go — account-linking MCP tools. Lets a coding agent
// manage the currently signed-in Yaver account's OAuth providers + trigger
// a manual merge of another Yaver account into this one, all without a
// browser on the host.
//
// Flows covered:
//
//   yaver_auth_list_identities   GET  /auth/providers
//   yaver_auth_capabilities      GET  /auth/config
//   yaver_auth_link_start        POST /auth/oauth-link/start → returns URL + QR
//   yaver_auth_link_wait         polls listAuthIdentities until the new provider appears
//   yaver_auth_unlink            DELETE /auth/oauth-link/{provider}
//   yaver_auth_merge_start       POST /auth/account/merge/start → returns approval URL
//   yaver_auth_merge_wait        GET  /auth/account/merge/status until completed
//
// The caller must already be signed in (have a token in ~/.yaver/config.json).
// If not, the tool surfaces the "run yaver_auth_start first" hint so the
// coding agent can chain flows together without the user switching context.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// loadAuthedConfig returns (convexURL, token) for the logged-in user, or a
// human-readable error if we're not signed in yet. Every tool in this file
// starts with this call.
func loadAuthedConfig() (string, string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return "", "", fmt.Errorf("no Yaver config on disk — run yaver_auth_start to sign in first")
	}
	token := strings.TrimSpace(cfg.AuthToken)
	if token == "" {
		return "", "", fmt.Errorf("not signed in — run yaver_auth_start first")
	}
	convexURL := strings.TrimSpace(cfg.ConvexSiteURL)
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}
	return convexURL, token, nil
}

func webBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("YAVER_WEB_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("NEXT_PUBLIC_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	cfg, err := LoadConfig()
	if err == nil && cfg != nil {
		if value := strings.TrimSpace(cfg.WebBaseURL); value != "" {
			return strings.TrimRight(value, "/")
		}
	}
	return "https://yaver.io"
}

func authedRequest(ctx context.Context, method, url, token string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return httpClient.Do(req)
}

func decodeAuthedJSONBody[T any](resp *http.Response) (T, string, error) {
	var zero T
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return zero, strings.TrimSpace(string(raw)), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if len(raw) == 0 {
		return zero, "", nil
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, string(raw), err
	}
	return out, "", nil
}

// ---------------------------------------------------------------------------
// List identities
// ---------------------------------------------------------------------------

type AuthCapabilitiesResult struct {
	EmailPasswordEnabled bool     `json:"email_password_enabled"`
	RequiresAllowlist    bool     `json:"requires_allowlist"`
	PasswordMinLength    int      `json:"password_min_length"`
	PasswordStorage      string   `json:"password_storage,omitempty"`
	RawPasswordStorage   string   `json:"raw_password_storage"`
	RecommendedSecrets   []string `json:"recommended_secrets"`
	Message              string   `json:"message"`
}

func authCapabilities(ctx context.Context, convexURLOverride string) (AuthCapabilitiesResult, error) {
	convexURL := strings.TrimRight(strings.TrimSpace(convexURLOverride), "/")
	if convexURL == "" {
		cfg, err := LoadConfig()
		if err == nil && cfg != nil {
			convexURL = strings.TrimRight(strings.TrimSpace(cfg.ConvexSiteURL), "/")
		}
	}
	if convexURL == "" {
		convexURL = defaultConvexSiteURL
	}
	req, err := http.NewRequestWithContext(ctx, "GET", convexURL+"/auth/config", nil)
	if err != nil {
		return AuthCapabilitiesResult{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return AuthCapabilitiesResult{}, err
	}
	type payload struct {
		EmailPasswordEnabled bool   `json:"emailPasswordEnabled"`
		RequiresAllowlist    bool   `json:"emailPasswordRequiresAllowlist"`
		PasswordMinLength    int    `json:"passwordMinLength"`
		PasswordStorage      string `json:"passwordStorage"`
	}
	body, raw, err := decodeAuthedJSONBody[payload](resp)
	if err != nil {
		return AuthCapabilitiesResult{}, fmt.Errorf("auth capabilities: %v (%s)", err, raw)
	}
	minLength := body.PasswordMinLength
	if minLength <= 0 {
		minLength = 8
	}
	status := "closed"
	if body.EmailPasswordEnabled {
		status = "enabled"
	}
	return AuthCapabilitiesResult{
		EmailPasswordEnabled: body.EmailPasswordEnabled,
		RequiresAllowlist:    body.RequiresAllowlist,
		PasswordMinLength:    minLength,
		PasswordStorage:      body.PasswordStorage,
		RawPasswordStorage:   "never in Convex or git; use local keychain/.env for local runs and GitHub Secrets for CI",
		RecommendedSecrets:   []string{"YAVER_TEST_EMAIL", "YAVER_TEST_PASSWORD"},
		Message:              fmt.Sprintf("Email/password sign-in is %s on this deployment and allowlist-gated.", status),
	}, nil
}

type AuthIdentityEntry struct {
	Provider   string `json:"provider"`
	Email      string `json:"email,omitempty"`
	IsPrimary  bool   `json:"is_primary"`
	CreatedAt  int64  `json:"created_at,omitempty"`
	LastUsedAt int64  `json:"last_used_at,omitempty"`
}

type AuthListIdentitiesResult struct {
	Identities []AuthIdentityEntry `json:"identities"`
	Count      int                 `json:"count"`
	Message    string              `json:"message"`
}

func authListIdentities(ctx context.Context) (AuthListIdentitiesResult, error) {
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return AuthListIdentitiesResult{}, err
	}
	resp, err := authedRequest(ctx, "GET", convexURL+"/auth/providers", token, nil)
	if err != nil {
		return AuthListIdentitiesResult{}, err
	}
	type payload struct {
		Identities []struct {
			Provider   string `json:"provider"`
			Email      string `json:"email"`
			IsPrimary  bool   `json:"isPrimary"`
			CreatedAt  int64  `json:"createdAt"`
			LastUsedAt int64  `json:"lastUsedAt"`
		} `json:"identities"`
	}
	body, raw, err := decodeAuthedJSONBody[payload](resp)
	if err != nil {
		return AuthListIdentitiesResult{}, fmt.Errorf("list identities: %v (%s)", err, raw)
	}
	out := AuthListIdentitiesResult{Identities: make([]AuthIdentityEntry, 0, len(body.Identities))}
	for _, id := range body.Identities {
		out.Identities = append(out.Identities, AuthIdentityEntry{
			Provider:   id.Provider,
			Email:      id.Email,
			IsPrimary:  id.IsPrimary,
			CreatedAt:  id.CreatedAt,
			LastUsedAt: id.LastUsedAt,
		})
	}
	out.Count = len(out.Identities)
	if out.Count == 1 {
		out.Message = "1 sign-in method linked — add another with yaver_auth_link_start to avoid lockout if you lose access to this provider."
	} else {
		out.Message = fmt.Sprintf("%d sign-in methods linked to this account.", out.Count)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Start link intent
// ---------------------------------------------------------------------------

type AuthLinkStartResult struct {
	Provider     string   `json:"provider"`
	URL          string   `json:"url"`
	LinkToken    string   `json:"link_token"`
	ExpiresAt    int64    `json:"expires_at_ms,omitempty"`
	QRASCII      string   `json:"qr_ascii"`
	Instructions []string `json:"instructions"`
	Message      string   `json:"message"`
}

func authLinkStart(ctx context.Context, provider string) (AuthLinkStartResult, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "msft" {
		provider = "microsoft"
	}
	switch provider {
	case "apple", "github", "google", "microsoft", "gitlab":
	default:
		return AuthLinkStartResult{}, fmt.Errorf("provider must be apple | github | gitlab | google | microsoft, got %q", provider)
	}
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return AuthLinkStartResult{}, err
	}

	// Ask Convex for a short-lived link intent bound to the current session.
	startBody := map[string]string{
		"provider": provider,
		"client":   "mcp",
		"returnTo": "/dashboard",
	}
	resp, err := authedRequest(ctx, "POST", convexURL+"/auth/oauth-link/start", token, startBody)
	if err != nil {
		return AuthLinkStartResult{}, err
	}
	type startResp struct {
		Token string `json:"token"`
	}
	body, raw, err := decodeAuthedJSONBody[startResp](resp)
	if err != nil {
		return AuthLinkStartResult{}, fmt.Errorf("link start: %v (%s)", err, raw)
	}
	if body.Token == "" {
		return AuthLinkStartResult{}, fmt.Errorf("link start returned empty token")
	}

	// Build the browser-facing URL. This mirrors what web/SettingsView does
	// when the user clicks "Connect google".
	q := url.Values{}
	q.Set("client", "mcp")
	q.Set("intent", "link")
	q.Set("linkToken", body.Token)
	q.Set("return", "/dashboard")
	authURL := fmt.Sprintf("%s/api/auth/oauth/%s?%s", webBaseURL(), provider, q.Encode())

	var qrBuf bytes.Buffer
	qrterminal.GenerateWithConfig(authURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    &qrBuf,
		BlackChar: "##",
		WhiteChar: "  ",
		QuietZone: 1,
	})

	return AuthLinkStartResult{
		Provider:  provider,
		URL:       authURL,
		LinkToken: body.Token,
		QRASCII:   qrBuf.String(),
		Instructions: []string{
			"1. Open the URL on any device with a browser (your phone works).",
			fmt.Sprintf("2. Sign in with %s.", provider),
			"3. Yaver will attach this provider to the account you're currently signed into.",
			"4. Call `yaver_auth_link_wait` with provider=" + provider + " to confirm it landed.",
		},
		Message: fmt.Sprintf("Open %s on any browser, sign in with %s, then call yaver_auth_link_wait.", authURL, provider),
	}, nil
}

// ---------------------------------------------------------------------------
// Wait for link to complete
// ---------------------------------------------------------------------------

type AuthLinkWaitResult struct {
	Status   string                   `json:"status"` // pending | linked | timeout
	Provider string                   `json:"provider"`
	Snapshot AuthListIdentitiesResult `json:"snapshot"`
	Message  string                   `json:"message"`
}

// authLinkWait polls /auth/providers until an identity with the requested
// provider appears (or timeout). Cheaper than a dedicated poll endpoint and
// keeps the Convex surface minimal.
func authLinkWait(ctx context.Context, provider string, timeoutSec, pollSec int) (AuthLinkWaitResult, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return AuthLinkWaitResult{}, fmt.Errorf("provider is required")
	}
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	if pollSec <= 0 {
		pollSec = 3
	}
	// Snapshot now so we can detect the NEW provider specifically.
	initial, err := authListIdentities(ctx)
	if err != nil {
		return AuthLinkWaitResult{}, err
	}
	already := false
	for _, id := range initial.Identities {
		if id.Provider == provider {
			already = true
			break
		}
	}
	if already {
		return AuthLinkWaitResult{
			Status:   "linked",
			Provider: provider,
			Snapshot: initial,
			Message:  fmt.Sprintf("%s was already linked to this account.", provider),
		}, nil
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		if ctx.Err() != nil {
			return AuthLinkWaitResult{Status: "pending", Provider: provider, Message: "canceled"}, ctx.Err()
		}
		snap, err := authListIdentities(ctx)
		if err != nil {
			return AuthLinkWaitResult{}, err
		}
		for _, id := range snap.Identities {
			if id.Provider == provider {
				return AuthLinkWaitResult{
					Status:   "linked",
					Provider: provider,
					Snapshot: snap,
					Message:  fmt.Sprintf("%s linked successfully (%d sign-in methods total).", provider, snap.Count),
				}, nil
			}
		}
		if time.Now().After(deadline) {
			return AuthLinkWaitResult{
				Status:   "timeout",
				Provider: provider,
				Snapshot: snap,
				Message:  fmt.Sprintf("timed out after %ds — the user may not have finished signing in yet. Call yaver_auth_link_wait again.", timeoutSec),
			}, nil
		}
		select {
		case <-ctx.Done():
			return AuthLinkWaitResult{Status: "pending", Provider: provider, Message: "canceled"}, ctx.Err()
		case <-time.After(time.Duration(pollSec) * time.Second):
		}
	}
}

// ---------------------------------------------------------------------------
// Unlink
// ---------------------------------------------------------------------------

type AuthUnlinkResult struct {
	OK        bool   `json:"ok"`
	Provider  string `json:"provider"`
	Remaining int    `json:"remaining"`
	Message   string `json:"message"`
}

func authUnlink(ctx context.Context, provider, totpCode string) (AuthUnlinkResult, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "msft" {
		provider = "microsoft"
	}
	if provider == "" {
		return AuthUnlinkResult{}, fmt.Errorf("provider is required")
	}
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return AuthUnlinkResult{}, err
	}
	var requestBody any
	if strings.TrimSpace(totpCode) != "" {
		requestBody = map[string]string{"totpCode": strings.TrimSpace(totpCode)}
	}
	resp, err := authedRequest(ctx, "DELETE", convexURL+"/auth/oauth-link/"+url.PathEscape(provider), token, requestBody)
	if err != nil {
		return AuthUnlinkResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 409 {
		return AuthUnlinkResult{OK: false, Provider: provider, Message: "Refusing to unlink the only sign-in method — add another provider first."}, nil
	}
	if resp.StatusCode == 404 {
		return AuthUnlinkResult{OK: false, Provider: provider, Message: fmt.Sprintf("%s is not linked to this account.", provider)}, nil
	}
	if resp.StatusCode == 412 {
		return AuthUnlinkResult{OK: false, Provider: provider, Message: "2FA is enabled on this account. Retry with a current 6-digit code."}, nil
	}
	if resp.StatusCode == 403 {
		return AuthUnlinkResult{OK: false, Provider: provider, Message: "Invalid 2FA code."}, nil
	}
	if resp.StatusCode >= 400 {
		return AuthUnlinkResult{}, fmt.Errorf("unlink %s: HTTP %d: %s", provider, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	type body struct {
		OK        bool `json:"ok"`
		Remaining int  `json:"remaining"`
	}
	var b body
	_ = json.Unmarshal(raw, &b)
	return AuthUnlinkResult{
		OK:        b.OK,
		Provider:  provider,
		Remaining: b.Remaining,
		Message:   fmt.Sprintf("%s unlinked. %d sign-in method(s) remaining.", provider, b.Remaining),
	}, nil
}

// ---------------------------------------------------------------------------
// Merge — start
// ---------------------------------------------------------------------------

type AuthMergeStartResult struct {
	MergeToken   string   `json:"merge_token"`
	ApprovalURL  string   `json:"approval_url"`
	ExpiresAtMs  int64    `json:"expires_at_ms"`
	TargetEmail  string   `json:"target_email"`
	QRASCII      string   `json:"qr_ascii"`
	Instructions []string `json:"instructions"`
	Message      string   `json:"message"`
}

func authMergeStart(ctx context.Context, totpCode string) (AuthMergeStartResult, error) {
	convexURL, token, err := loadAuthedConfig()
	if err != nil {
		return AuthMergeStartResult{}, err
	}
	requestBody := map[string]string{"client": "mcp"}
	if strings.TrimSpace(totpCode) != "" {
		requestBody["totpCode"] = strings.TrimSpace(totpCode)
	}
	resp, err := authedRequest(ctx, "POST", convexURL+"/auth/account/merge/start", token, requestBody)
	if err != nil {
		return AuthMergeStartResult{}, err
	}
	type payload struct {
		MergeToken  string `json:"mergeToken"`
		ExpiresAt   int64  `json:"expiresAt"`
		TargetEmail string `json:"targetEmail"`
	}
	data, raw, err := decodeAuthedJSONBody[payload](resp)
	if err != nil {
		return AuthMergeStartResult{}, fmt.Errorf("merge start: %v (%s)", err, raw)
	}
	approvalURL := webBaseURL() + "/account/merge?token=" + url.QueryEscape(data.MergeToken)

	var qrBuf bytes.Buffer
	qrterminal.GenerateWithConfig(approvalURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    &qrBuf,
		BlackChar: "##",
		WhiteChar: "  ",
		QuietZone: 1,
	})

	return AuthMergeStartResult{
		MergeToken:  data.MergeToken,
		ApprovalURL: approvalURL,
		ExpiresAtMs: data.ExpiresAt,
		TargetEmail: data.TargetEmail,
		QRASCII:     qrBuf.String(),
		Instructions: []string{
			"1. Open the approval URL on ANY browser.",
			"2. Sign into the OTHER Yaver account — the one you want to merge AWAY (its data lands on this account afterwards).",
			"3. Confirm the merge when prompted.",
			"4. Call yaver_auth_merge_wait here to watch for completion.",
		},
		Message: fmt.Sprintf("Merge intent created. Open %s on a browser where the other Yaver account is (or can be) signed in, then confirm. This account (%s) will receive the merged data.", approvalURL, data.TargetEmail),
	}, nil
}

// ---------------------------------------------------------------------------
// Merge — wait / status
// ---------------------------------------------------------------------------

type AuthMergeWaitResult struct {
	Status      string `json:"status"` // pending | completed | cancelled | expired | unknown
	TargetEmail string `json:"target_email,omitempty"`
	CompletedAt int64  `json:"completed_at_ms,omitempty"`
	Message     string `json:"message"`
}

func authMergeStatus(ctx context.Context, mergeToken string) (AuthMergeWaitResult, error) {
	mergeToken = strings.TrimSpace(mergeToken)
	if mergeToken == "" {
		return AuthMergeWaitResult{}, fmt.Errorf("merge_token is required")
	}
	convexURL, _, err := loadAuthedConfig()
	if err != nil {
		return AuthMergeWaitResult{}, err
	}
	u := convexURL + "/auth/account/merge/status?token=" + url.QueryEscape(mergeToken)
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return AuthMergeWaitResult{}, err
	}
	type payload struct {
		Status      string `json:"status"`
		TargetEmail string `json:"targetEmail"`
		CompletedAt int64  `json:"completedAt"`
	}
	body, raw, err := decodeAuthedJSONBody[payload](resp)
	if err != nil {
		return AuthMergeWaitResult{}, fmt.Errorf("merge status: %v (%s)", err, raw)
	}
	msg := ""
	switch body.Status {
	case "pending":
		msg = "merge waiting for the source account to approve"
	case "completed":
		msg = fmt.Sprintf("merge completed — source account's data is now on %s", body.TargetEmail)
	case "cancelled":
		msg = "merge was cancelled"
	case "expired":
		msg = "merge token expired — call yaver_auth_merge_start again"
	default:
		msg = "merge token unknown or already cleaned up"
	}
	return AuthMergeWaitResult{
		Status:      body.Status,
		TargetEmail: body.TargetEmail,
		CompletedAt: body.CompletedAt,
		Message:     msg,
	}, nil
}

func authMergeWait(ctx context.Context, mergeToken string, timeoutSec, pollSec int) (AuthMergeWaitResult, error) {
	if timeoutSec <= 0 {
		timeoutSec = 180
	}
	if pollSec <= 0 {
		pollSec = 3
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for {
		if ctx.Err() != nil {
			return AuthMergeWaitResult{Status: "pending", Message: "canceled"}, ctx.Err()
		}
		status, err := authMergeStatus(ctx, mergeToken)
		if err != nil {
			return AuthMergeWaitResult{}, err
		}
		if status.Status != "pending" {
			return status, nil
		}
		if time.Now().After(deadline) {
			status.Message = fmt.Sprintf("timed out after %ds — call yaver_auth_merge_wait again", timeoutSec)
			return status, nil
		}
		select {
		case <-ctx.Done():
			return AuthMergeWaitResult{Status: "pending", Message: "canceled"}, ctx.Err()
		case <-time.After(time.Duration(pollSec) * time.Second):
		}
	}
}
