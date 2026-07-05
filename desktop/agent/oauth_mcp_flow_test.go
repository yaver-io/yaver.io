package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestOauthMCPFullFlow drives the whole connector handshake through the real HTTP
// handlers in sequence: authorize form → login → token (PKCE) → /mcp with the JWT,
// and asserts the connector is hard default-deny (safe tool ok, exec denied).
func TestOauthMCPFullFlow(t *testing.T) {
	setTestOauthKey(t)

	// Seed one user + one client directly in the stores (owner-API would need a
	// running owner token; the handlers read these globals).
	const pw = "reviewer-pw-123"
	uh, us, _ := hashPassword(pw)
	const csecret = "connector-secret-xyz"
	ch, cs, _ := hashPassword(csecret)
	oauthMu.Lock()
	oauthUsers = []OAuthUser{{ID: "u1", Email: "reviewer@yaver.test", Hash: uh, Salt: us}}
	oauthClients = []OAuthClient{{ID: "claude", Secret: ch + ":" + cs, RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}}}
	oauthCodes = map[string]oauthCode{}
	oauthMu.Unlock()

	s := &HTTPServer{}
	verifier := "verifier-abcdefghijklmnopqrstuvwxyz-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	resource := "https://yaver.test/mcp"
	redirect := "https://claude.ai/api/mcp/auth_callback"

	// 1. GET /oauth/authorize → login form carrying the PKCE challenge + resource.
	areq := httptest.NewRequest("GET", "/oauth/authorize?"+url.Values{
		"client_id": {"claude"}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {resource},
	}.Encode(), nil)
	arec := httptest.NewRecorder()
	s.handleOauthAuthorize(arec, areq)
	if arec.Code != 200 || !strings.Contains(arec.Body.String(), "password") ||
		!strings.Contains(arec.Body.String(), challenge) {
		t.Fatalf("authorize did not render a login form with PKCE: %d", arec.Code)
	}

	// PKCE is mandatory — authorize without a challenge must 400.
	nreq := httptest.NewRequest("GET", "/oauth/authorize?"+url.Values{
		"client_id": {"claude"}, "redirect_uri": {redirect}, "response_type": {"code"},
	}.Encode(), nil)
	nrec := httptest.NewRecorder()
	s.handleOauthAuthorize(nrec, nreq)
	if nrec.Code != 400 {
		t.Fatalf("authorize without PKCE should 400, got %d", nrec.Code)
	}

	// 2. POST /oauth/login → 303 redirect back with a code.
	form := url.Values{
		"email": {"reviewer@yaver.test"}, "password": {pw}, "client_id": {"claude"},
		"redirect_uri": {redirect}, "code_challenge": {challenge}, "resource": {resource},
	}
	lreq := httptest.NewRequest("POST", "/oauth/login", strings.NewReader(form.Encode()))
	lreq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	lrec := httptest.NewRecorder()
	s.handleOauthLogin(lrec, lreq)
	if lrec.Code != http.StatusSeeOther {
		t.Fatalf("login should redirect (303), got %d %s", lrec.Code, lrec.Body.String())
	}
	loc, _ := url.Parse(lrec.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in login redirect")
	}

	// wrong password → 401, no code
	badform := url.Values{"email": {"reviewer@yaver.test"}, "password": {"WRONG"},
		"client_id": {"claude"}, "redirect_uri": {redirect}, "code_challenge": {challenge}}
	breq := httptest.NewRequest("POST", "/oauth/login", strings.NewReader(badform.Encode()))
	breq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	brec := httptest.NewRecorder()
	s.handleOauthLogin(brec, breq)
	if brec.Code == http.StatusSeeOther {
		t.Fatal("wrong password should not redirect with a code")
	}

	// 3. POST /oauth/token with PKCE verifier → access token bound to the resource.
	treq := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"claude"},
		"client_secret": {csecret}, "redirect_uri": {redirect}, "code_verifier": {verifier},
	}.Encode()))
	treq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	trec := httptest.NewRecorder()
	s.handleOauthToken(trec, treq)
	if trec.Code != 200 {
		t.Fatalf("token exchange failed: %d %s", trec.Code, trec.Body.String())
	}
	var tok map[string]interface{}
	json.Unmarshal(trec.Body.Bytes(), &tok)
	at, _ := tok["access_token"].(string)
	if at == "" {
		t.Fatal("no access token")
	}
	claims, ok := verifyOauthJWT(at)
	if !ok || claims["aud"] != resource || claims["sub"] != "u1" {
		t.Fatalf("token not bound to user+resource: ok=%v claims=%v", ok, claims)
	}

	// 4. Use the token through authMCP → connector default-deny scope; exec denied.
	var allowed string
	next := func(w http.ResponseWriter, r *http.Request) { allowed = r.Header.Get("X-Yaver-AllowedTools"); w.WriteHeader(200) }
	mreq := httptest.NewRequest("POST", "/mcp", nil)
	mreq.Header.Set("Authorization", "Bearer "+at)
	mrec := httptest.NewRecorder()
	s.authMCP(next)(mrec, mreq)
	if mrec.Code != 200 {
		t.Fatalf("connector token rejected at /mcp: %d", mrec.Code)
	}
	if strings.Contains(allowed, "exec_command") {
		t.Fatal("SECURITY: connector could exec")
	}
	if d := mcpToolDeniedByScope(reqWithAllowed(allowed), "exec_command"); d == nil {
		t.Fatal("SECURITY: exec_command not denied for connector")
	}
	if d := mcpToolDeniedByScope(reqWithAllowed(allowed), "calculate"); d != nil {
		t.Fatal("safe tool wrongly denied")
	}
	_ = time.Now
}
