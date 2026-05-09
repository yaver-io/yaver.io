package main

// git_oauth_device.go — RFC 8628 Device Authorization Grant for GitHub
// and GitLab. Lets a fresh remote box bootstrap a working git PAT
// without anyone pasting one: the user gets a short code + URL, opens
// it in any browser, approves once, and the resulting OAuth access
// token lands in the agent's vault + ~/.yaver/git-credentials.json
// using the exact same persistence shape as `git/provider/setup`.
//
// The flow lives entirely on the target agent. Mobile/web/CLI just
// hit /git-provider/oauth/start and poll /git-provider/oauth/status,
// optionally peer-routed via /peer/<id>/. Tokens never reach Convex.
//
// Client ID resolution (in priority order):
//
//  1. Vault entry `github-oauth-client-id` / `gitlab-oauth-client-id`
//     in project `oauth` — for users who want full BYO control.
//  2. Env var YAVER_GITHUB_OAUTH_CLIENT_ID / YAVER_GITLAB_OAUTH_CLIENT_ID
//     — useful for CI / managed images.
//  3. Compiled-in defaultYaverGitHubOAuthClientID /
//     defaultYaverGitLabOAuthClientID — Yaver-owned public Device Flow
//     OAuth Apps. Fill these in once registered at github.com/settings/
//     developers and gitlab.com/oauth/applications.
//
// Device Flow apps are public clients (no secret), so embedding the
// client_id in the binary is normal and doesn't grant anyone repo
// access on its own — every login still requires a fresh user
// approval at github.com/login/device.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Yaver-owned Device Flow OAuth App client IDs. Empty by default; fill
// in once the Apps are registered. Until they are, users hit BYO mode
// (vault entry or env var) — the privacy story is the same either way.
const (
	defaultYaverGitHubOAuthClientID = ""
	defaultYaverGitLabOAuthClientID = ""
)

// gitOAuthSession is one in-flight Device Flow attempt. Held in memory
// only — never persisted, never sent to Convex. The agent restarts wipe
// any in-flight sessions; users just retry the flow.
type gitOAuthSession struct {
	ID              string    `json:"session_id"`
	Provider        string    `json:"provider"` // "github" | "gitlab"
	Host            string    `json:"host"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	DeviceCode      string    `json:"-"` // never serialized to clients
	Interval        int       `json:"interval"`
	ExpiresAt       time.Time `json:"expires_at"`
	StartedAt       time.Time `json:"started_at"`
	State           string    `json:"state"` // pending | done | error | expired
	Error           string    `json:"error,omitempty"`
	Username        string    `json:"username,omitempty"`
	BYOClient       bool      `json:"byo_client"` // true if client_id came from vault/env
}

var (
	gitOAuthSessionsMu sync.Mutex
	gitOAuthSessions   = map[string]*gitOAuthSession{}
)

// gitOAuthClientID resolves the client_id for a provider, checking
// vault → env → compiled default in that order. Returns the source so
// the start endpoint can hint to the user when they need to register
// an app themselves.
func gitOAuthClientID(provider string) (clientID string, byo bool, err error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	var (
		vaultKey string
		envKey   string
		compiled string
	)
	switch provider {
	case "github":
		vaultKey = "github-oauth-client-id"
		envKey = "YAVER_GITHUB_OAUTH_CLIENT_ID"
		compiled = defaultYaverGitHubOAuthClientID
	case "gitlab":
		vaultKey = "gitlab-oauth-client-id"
		envKey = "YAVER_GITLAB_OAUTH_CLIENT_ID"
		compiled = defaultYaverGitLabOAuthClientID
	default:
		return "", false, fmt.Errorf("unsupported provider %q (want github|gitlab)", provider)
	}

	if entry, _ := loadVaultEntryOptional(vaultKey); entry != nil {
		v := strings.TrimSpace(entry.Value)
		if v != "" {
			return v, true, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v, true, nil
	}
	if strings.TrimSpace(compiled) != "" {
		return compiled, false, nil
	}
	return "", false, fmt.Errorf(
		"no %s OAuth Client ID configured. Either: (1) register a Device Flow OAuth App at %s and set vault entry %q (`yaver vault add %s --value <client-id> --project oauth`), or (2) set env %s, or (3) wait for a Yaver release with a registered default app.",
		provider, gitOAuthAppDocsURL(provider), vaultKey, vaultKey, envKey,
	)
}

func gitOAuthAppDocsURL(provider string) string {
	switch provider {
	case "github":
		return "https://github.com/settings/developers"
	case "gitlab":
		return "https://gitlab.com/-/user_settings/applications"
	}
	return ""
}

func newGitOAuthSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// extremely unlikely; fall back to time-based to avoid panic
		return fmt.Sprintf("oauth-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// startGitOAuthDevice kicks off a Device Flow attempt. Returns the
// session record (sans DeviceCode) ready to ship to the caller, plus
// starts a background poller that drives the session to a terminal
// state (done | error | expired).
func startGitOAuthDevice(ctx context.Context, provider, host string) (*gitOAuthSession, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if host == "" {
		switch provider {
		case "github":
			host = "github.com"
		case "gitlab":
			host = "gitlab.com"
		}
	}

	clientID, byo, err := gitOAuthClientID(provider)
	if err != nil {
		return nil, err
	}

	deviceURL, scopes := gitOAuthDeviceEndpoints(provider, host)
	if deviceURL == "" {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}

	body := url.Values{
		"client_id": {clientID},
		"scope":     {strings.Join(scopes, " ")},
	}
	resp, err := postOAuthForm(ctx, deviceURL, body)
	if err != nil {
		return nil, fmt.Errorf("device authorization request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s device endpoint returned %d: %s", provider, resp.StatusCode, snippet(raw))
	}

	var parsed struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		// GitHub uses verification_uri, GitLab also returns
		// verification_uri_complete with the code pre-filled.
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse device response: %w (body: %s)", err, snippet(raw))
	}
	if parsed.DeviceCode == "" || parsed.UserCode == "" {
		return nil, fmt.Errorf("provider returned empty device_code/user_code: %s", snippet(raw))
	}
	if parsed.Interval <= 0 {
		parsed.Interval = 5
	}
	if parsed.ExpiresIn <= 0 {
		parsed.ExpiresIn = 900
	}
	verification := parsed.VerificationURI
	if verification == "" && parsed.VerificationURIComplete != "" {
		verification = parsed.VerificationURIComplete
	}

	sess := &gitOAuthSession{
		ID:              newGitOAuthSessionID(),
		Provider:        provider,
		Host:            host,
		UserCode:        parsed.UserCode,
		VerificationURI: verification,
		DeviceCode:      parsed.DeviceCode,
		Interval:        parsed.Interval,
		ExpiresAt:       time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
		StartedAt:       time.Now(),
		State:           "pending",
		BYOClient:       byo,
	}
	gitOAuthSessionsMu.Lock()
	gitOAuthSessions[sess.ID] = sess
	gitOAuthSessionsMu.Unlock()

	go pollGitOAuthSession(sess.ID, clientID)
	go reapGitOAuthSession(sess.ID)
	return sess, nil
}

func gitOAuthDeviceEndpoints(provider, host string) (deviceURL string, scopes []string) {
	switch provider {
	case "github":
		return "https://github.com/login/device/code", []string{"repo", "read:org", "read:user"}
	case "gitlab":
		return fmt.Sprintf("https://%s/oauth/authorize_device", host), []string{"api", "read_user", "read_repository", "write_repository"}
	}
	return "", nil
}

func gitOAuthTokenEndpoint(provider, host string) string {
	switch provider {
	case "github":
		return "https://github.com/login/oauth/access_token"
	case "gitlab":
		return fmt.Sprintf("https://%s/oauth/token", host)
	}
	return ""
}

// pollGitOAuthSession is the per-session polling goroutine. Honors the
// provider's interval, doubles it on slow_down (per RFC 8628), exits on
// any terminal state.
func pollGitOAuthSession(sessionID, clientID string) {
	ticker := time.NewTimer(0) // first tick fires immediately so we
	// don't burn a full interval before the first poll
	defer ticker.Stop()

	for {
		<-ticker.C

		gitOAuthSessionsMu.Lock()
		sess, ok := gitOAuthSessions[sessionID]
		gitOAuthSessionsMu.Unlock()
		if !ok {
			return
		}
		if sess.State != "pending" {
			return
		}
		if time.Now().After(sess.ExpiresAt) {
			markGitOAuthError(sessionID, "expired", "device code expired before approval — start a new flow")
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		token, errCode, retryHint, err := pollGitOAuthOnce(ctx, sess.Provider, sess.Host, clientID, sess.DeviceCode)
		cancel()
		if err != nil {
			log.Printf("[git-oauth] %s poll error: %v", sess.Provider, err)
			ticker.Reset(time.Duration(sess.Interval) * time.Second)
			continue
		}
		switch {
		case token != "":
			finishGitOAuthSession(sessionID, token)
			return
		case errCode == "authorization_pending":
			ticker.Reset(time.Duration(sess.Interval) * time.Second)
		case errCode == "slow_down":
			// RFC 8628 §3.5: double the polling interval going forward.
			gitOAuthSessionsMu.Lock()
			sess.Interval *= 2
			if sess.Interval > 60 {
				sess.Interval = 60
			}
			gitOAuthSessionsMu.Unlock()
			ticker.Reset(time.Duration(sess.Interval) * time.Second)
		case errCode == "expired_token", errCode == "access_denied":
			markGitOAuthError(sessionID, errCode, fmt.Sprintf("%s: %s", errCode, retryHint))
			return
		default:
			// Unknown error — bail rather than spin forever.
			markGitOAuthError(sessionID, "error", fmt.Sprintf("provider returned %q: %s", errCode, retryHint))
			return
		}
	}
}

// pollGitOAuthOnce hits the provider token endpoint once. Returns
// (token, errorCode, errorDescription, transportErr). token != "" means
// approved; errorCode != "" means the provider answered with an OAuth
// error; transportErr signals a network/decode issue worth retrying.
func pollGitOAuthOnce(ctx context.Context, provider, host, clientID, deviceCode string) (string, string, string, error) {
	endpoint := gitOAuthTokenEndpoint(provider, host)
	if endpoint == "" {
		return "", "", "", fmt.Errorf("no token endpoint for provider %q", provider)
	}
	body := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	resp, err := postOAuthForm(ctx, endpoint, body)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var parsed struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if jerr := json.Unmarshal(raw, &parsed); jerr != nil {
		return "", "", "", fmt.Errorf("decode token response: %w (status=%d body=%s)", jerr, resp.StatusCode, snippet(raw))
	}
	if parsed.AccessToken != "" {
		return parsed.AccessToken, "", "", nil
	}
	return "", parsed.Error, parsed.ErrorDescription, nil
}

func postOAuthForm(ctx context.Context, endpoint string, body url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return http.DefaultClient.Do(req)
}

// finishGitOAuthSession persists the token using the same shape as
// handleGitProviderSetup so a subsequent /git/provider/status call
// shows the new login like any PAT-based one.
func finishGitOAuthSession(sessionID, token string) {
	gitOAuthSessionsMu.Lock()
	sess, ok := gitOAuthSessions[sessionID]
	gitOAuthSessionsMu.Unlock()
	if !ok {
		return
	}

	var (
		username  string
		avatarURL string
		err       error
	)
	switch sess.Provider {
	case "github":
		username, avatarURL, err = verifyGitHubToken(token)
	case "gitlab":
		username, avatarURL, err = verifyGitLabToken(sess.Host, token)
	default:
		err = fmt.Errorf("unknown provider %q", sess.Provider)
	}
	if err != nil {
		markGitOAuthError(sessionID, "verify_failed", "token verification failed after device approval: "+err.Error())
		return
	}

	if perr := persistGitProviderTokenForOAuth(sess.Provider, sess.Host, token, username, avatarURL); perr != nil {
		markGitOAuthError(sessionID, "persist_failed", perr.Error())
		return
	}

	gitOAuthSessionsMu.Lock()
	sess.State = "done"
	sess.Username = username
	sess.DeviceCode = "" // scrub: persisted, no need to retain
	gitOAuthSessionsMu.Unlock()
	log.Printf("[git-oauth] %s/%s linked as %s via device flow (byo=%v)", sess.Provider, sess.Host, username, sess.BYOClient)
}

// persistGitProviderTokenForOAuth mirrors the persistence half of
// handleGitProviderSetup so the OAuth path lands in the same on-disk
// stores: ~/.yaver/git-credentials.json (clone-pull) and the provider
// metadata file.
func persistGitProviderTokenForOAuth(provider, host, token, username, avatarURL string) error {
	creds, _ := loadGitCredentials()
	found := false
	for i := range creds {
		if strings.EqualFold(creds[i].Host, host) {
			creds[i].Token = token
			creds[i].Username = username
			found = true
			break
		}
	}
	if !found {
		creds = append(creds, GitCredential{Host: host, Username: username, Token: token})
	}
	if err := saveGitCredentials(creds); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	providers, _ := loadGitProviders()
	pp := GitProvider{
		Host:      host,
		Provider:  provider,
		Username:  username,
		Token:     token,
		AvatarURL: avatarURL,
		SetupAt:   time.Now().UTC().Format(time.RFC3339),
	}
	updated := false
	for i := range providers {
		if strings.EqualFold(providers[i].Host, host) {
			providers[i] = pp
			updated = true
			break
		}
	}
	if !updated {
		providers = append(providers, pp)
	}
	return saveGitProviders(providers)
}

func markGitOAuthError(sessionID, code, msg string) {
	gitOAuthSessionsMu.Lock()
	defer gitOAuthSessionsMu.Unlock()
	if sess, ok := gitOAuthSessions[sessionID]; ok {
		sess.State = "error"
		sess.Error = msg
		if code == "expired" {
			sess.State = "expired"
		}
	}
}

// reapGitOAuthSession purges a session 30 minutes after it terminates
// (or 30 minutes past expiry). Keeps memory bounded without losing the
// completion signal too quickly for slow UI polling.
func reapGitOAuthSession(sessionID string) {
	timer := time.NewTimer(30 * time.Minute)
	<-timer.C
	gitOAuthSessionsMu.Lock()
	delete(gitOAuthSessions, sessionID)
	gitOAuthSessionsMu.Unlock()
}

func getGitOAuthSession(sessionID string) (*gitOAuthSession, bool) {
	gitOAuthSessionsMu.Lock()
	defer gitOAuthSessionsMu.Unlock()
	sess, ok := gitOAuthSessions[sessionID]
	if !ok {
		return nil, false
	}
	// Return a copy with DeviceCode redacted regardless of state — no
	// caller of getGitOAuthSession ever needs the device_code, and
	// returning the underlying pointer would let a future bug leak it.
	cp := *sess
	cp.DeviceCode = ""
	return &cp, true
}

