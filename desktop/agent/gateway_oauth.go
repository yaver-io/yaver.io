package main

// gateway_oauth.go — the oauth_code AuthMethod handler.
//
// Implements OAuth2 **authorization code + PKCE** with automatic refresh. The
// token exchange is hand-rolled with net/http to match the rest of the repo
// (auth_dev.go / oauth_provider.go hand-roll OAuth — we do NOT pull in
// golang.org/x/oauth2).
//
// In this slice the broker's job is the *runtime* half: given a connector whose
// tokens were already consented + stored in the vault, return a valid bearer
// session, refreshing the access token with the refresh token when it has
// expired. The one-time interactive consent (open browser → exchange code) is
// the authoring funnel's job (docs §15) and is exercised here only via
// ExchangeCode, which is also what the test drives end-to-end.
//
// Policy Guard (CLAUDE.md / docs §9): honest User-Agent; on a token endpoint
// 401 we refresh at most once then fail clean; we never spam retries or rotate
// identity. There is NO captcha/bot-detection logic here.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// gatewayContactUA is Yaver's honest client identity for gateway HTTP — it
// names itself and gives a contact URL, the opposite of browser-spoofing.
const gatewayContactUA = "Yaver/1.0 (+https://yaver.io; personal agent gateway)"

// oauthRefreshSkew refreshes a token a little before its real expiry so an
// in-flight request never races the boundary.
const oauthRefreshSkew = 60 * time.Second

// oauthCodeHandler implements AuthMethod for connector.Auth.Method == "oauth_code".
type oauthCodeHandler struct {
	store      CredStore
	httpClient *http.Client
}

func newOAuthCodeHandler(store CredStore) *oauthCodeHandler {
	return &oauthCodeHandler{
		store:      store,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *oauthCodeHandler) Method() string { return "oauth_code" }

// NeedsHuman is false: oauth_code only refreshes silently at runtime. The
// one-time consent already happened at authoring time; a revoked refresh token
// surfaces as a clean error the caller turns into a re-consent prompt — it does
// not block the broker on a human here.
func (h *oauthCodeHandler) NeedsHuman(_ *Connector) bool { return false }

// Ensure returns a valid bearer session for the connector, refreshing the
// access token if it has expired.
func (h *oauthCodeHandler) Ensure(ctx context.Context, c *Connector) (Session, error) {
	ref := c.Auth.CredRef
	if strings.TrimSpace(ref) == "" {
		return Session{}, fmt.Errorf("gateway: connector %q has no credRef — run the consent flow first", c.ID)
	}
	creds, err := loadOAuthCreds(h.store, ref)
	if err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q not authorized (no stored creds) — run the consent flow first: %w", c.ID, err)
	}

	if !oauthExpired(creds) && creds.AccessToken != "" {
		return bearerSession(creds), nil
	}

	// Access token stale/missing → refresh with the refresh token.
	if strings.TrimSpace(creds.RefreshToken) == "" {
		return Session{}, fmt.Errorf("gateway: connector %q access token expired and no refresh token — re-consent required", c.ID)
	}
	refreshed, err := h.refresh(ctx, c, creds)
	if err != nil {
		return Session{}, err
	}
	if err := saveOAuthCreds(h.store, ref, refreshed); err != nil {
		return Session{}, fmt.Errorf("gateway: persist refreshed creds: %w", err)
	}
	return bearerSession(refreshed), nil
}

// Refresh forces a token refresh regardless of clock expiry. It's used on the
// 401-retry path in gatewayInvoke: the resource server rejected a token that is
// still valid by our clock (revoked/rotated server-side), so Ensure would hand
// back the same rejected token. Refresh skips the expiry check and exchanges the
// refresh token exactly once.
func (h *oauthCodeHandler) Refresh(ctx context.Context, c *Connector) (Session, error) {
	ref := c.Auth.CredRef
	if strings.TrimSpace(ref) == "" {
		return Session{}, fmt.Errorf("gateway: connector %q has no credRef — run the consent flow first", c.ID)
	}
	creds, err := loadOAuthCreds(h.store, ref)
	if err != nil {
		return Session{}, fmt.Errorf("gateway: connector %q not authorized (no stored creds) — run the consent flow first: %w", c.ID, err)
	}
	if strings.TrimSpace(creds.RefreshToken) == "" {
		return Session{}, fmt.Errorf("gateway: connector %q rejected (401) and no refresh token — re-consent required", c.ID)
	}
	refreshed, err := h.refresh(ctx, c, creds)
	if err != nil {
		return Session{}, err
	}
	if err := saveOAuthCreds(h.store, ref, refreshed); err != nil {
		return Session{}, fmt.Errorf("gateway: persist refreshed creds: %w", err)
	}
	return bearerSession(refreshed), nil
}

// oauthExpired reports whether the access token is at/after its refresh skew.
// A zero expiry is treated as expired so we refresh before first use.
func oauthExpired(c *OAuthCreds) bool {
	if c.ExpiryUnix == 0 {
		return true
	}
	return time.Now().Add(oauthRefreshSkew).Unix() >= c.ExpiryUnix
}

func bearerSession(c *OAuthCreds) Session {
	exp := time.Time{}
	if c.ExpiryUnix > 0 {
		exp = time.Unix(c.ExpiryUnix, 0)
	}
	return Session{Kind: SessionBearer, Token: c.AccessToken, Expiry: exp}
}

// tokenResponse is the standard OAuth2 token endpoint reply.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// refresh exchanges the refresh token for a fresh access token. Per the Policy
// Guard, a 401 here means the grant was revoked — we fail clean (the caller
// turns it into a re-consent prompt), we do NOT retry-spam or rotate identity.
func (h *oauthCodeHandler) refresh(ctx context.Context, c *Connector, creds *OAuthCreds) (*OAuthCreds, error) {
	tokenURL := gatewayFirstNonEmpty(creds.TokenURL, c.Auth.TokenURL)
	if tokenURL == "" {
		return nil, fmt.Errorf("gateway: connector %q has no token_url for refresh", c.ID)
	}
	clientID := gatewayFirstNonEmpty(creds.ClientID, c.Auth.ClientID)

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", creds.RefreshToken)
	if clientID != "" {
		form.Set("client_id", clientID)
	}

	tr, status, err := h.postToken(ctx, tokenURL, form)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		// Revoked / forbidden — a block is a "no". Fail clean, no retry.
		return nil, fmt.Errorf("gateway: refresh for connector %q rejected (status %d) — refresh token revoked, re-consent required", c.ID, status)
	}
	if status != http.StatusOK || tr.Error != "" {
		return nil, fmt.Errorf("gateway: refresh for connector %q failed (status %d): %s %s", c.ID, status, tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("gateway: refresh for connector %q returned no access_token", c.ID)
	}

	out := *creds
	out.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		// Some providers rotate the refresh token; keep the old one otherwise.
		out.RefreshToken = tr.RefreshToken
	}
	if tr.TokenType != "" {
		out.TokenType = tr.TokenType
	}
	if tr.Scope != "" {
		out.Scope = tr.Scope
	}
	out.ExpiryUnix = expiryFromExpiresIn(tr.ExpiresIn)
	out.TokenURL = tokenURL
	if clientID != "" {
		out.ClientID = clientID
	}
	return &out, nil
}

// postToken POSTs an x-www-form-urlencoded body to a token endpoint and decodes
// the JSON reply. Returns (parsed, statusCode, transportErr).
func (h *oauthCodeHandler) postToken(ctx context.Context, tokenURL string, form url.Values) (*tokenResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", gatewayContactUA)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tr tokenResponse
	// Token endpoints reply JSON; tolerate a decode miss by surfacing status.
	_ = json.Unmarshal(body, &tr)
	return &tr, resp.StatusCode, nil
}

// ── PKCE + authorization-code exchange (authoring/consent side) ──────────────
//
// These are used by the one-time consent flow (and by the test) to obtain the
// first set of tokens. Runtime calls go through Ensure (refresh only).

// PKCE holds a generated PKCE verifier/challenge pair (S256).
type PKCE struct {
	Verifier  string
	Challenge string
	Method    string // always "S256"
}

// newPKCE generates a high-entropy PKCE pair per RFC 7636 (S256).
func newPKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, fmt.Errorf("pkce entropy: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCE{Verifier: verifier, Challenge: challenge, Method: "S256"}, nil
}

// AuthCodeURL builds the authorization-code consent URL with PKCE for a
// connector. The caller opens this in a browser (or device grant) during the
// one-time consent.
func (h *oauthCodeHandler) AuthCodeURL(c *Connector, redirectURI, state string, pkce PKCE) (string, error) {
	if c.Auth.AuthURL == "" {
		return "", fmt.Errorf("gateway: connector %q has no authUrl", c.ID)
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.Auth.ClientID)
	q.Set("redirect_uri", redirectURI)
	if len(c.Auth.Scopes) > 0 {
		q.Set("scope", strings.Join(c.Auth.Scopes, " "))
	}
	q.Set("state", state)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", pkce.Method)
	q.Set("access_type", "offline") // request a refresh token where supported
	sep := "?"
	if strings.Contains(c.Auth.AuthURL, "?") {
		sep = "&"
	}
	return c.Auth.AuthURL + sep + q.Encode(), nil
}

// ExchangeCode exchanges an authorization code (+ PKCE verifier) for tokens and
// persists them in the vault under the connector's CredRef. This is the
// one-time consent completion; afterwards Ensure() handles refresh.
func (h *oauthCodeHandler) ExchangeCode(ctx context.Context, c *Connector, code, redirectURI, codeVerifier string) (*OAuthCreds, error) {
	tokenURL := c.Auth.TokenURL
	if tokenURL == "" {
		return nil, fmt.Errorf("gateway: connector %q has no tokenUrl", c.ID)
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if c.Auth.ClientID != "" {
		form.Set("client_id", c.Auth.ClientID)
	}
	form.Set("code_verifier", codeVerifier)

	tr, status, err := h.postToken(ctx, tokenURL, form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || tr.Error != "" || tr.AccessToken == "" {
		return nil, fmt.Errorf("gateway: code exchange for connector %q failed (status %d): %s %s", c.ID, status, tr.Error, tr.ErrorDesc)
	}
	creds := &OAuthCreds{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
		ExpiryUnix:   expiryFromExpiresIn(tr.ExpiresIn),
		TokenURL:     tokenURL,
		ClientID:     c.Auth.ClientID,
	}
	if err := saveOAuthCreds(h.store, c.Auth.CredRef, creds); err != nil {
		return nil, fmt.Errorf("gateway: persist consent creds: %w", err)
	}
	return creds, nil
}

// expiryFromExpiresIn converts an OAuth expires_in (seconds) into a Unix expiry.
// 0 ⇒ 0 (unknown → treated as expired by oauthExpired).
func expiryFromExpiresIn(expiresIn int64) int64 {
	if expiresIn <= 0 {
		return 0
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second).Unix()
}

func gatewayFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
