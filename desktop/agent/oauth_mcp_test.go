package main

import (
	"crypto/rand"
	"crypto/rsa"
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

// setTestOauthKey installs an in-memory RSA key so tests never touch ~/.yaver.
func setTestOauthKey(t *testing.T) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	oauthMu.Lock()
	oauthKey = k
	oauthMu.Unlock()
}

func TestOauthJWT_AccessVsRefresh(t *testing.T) {
	setTestOauthKey(t)
	access, err := mintAccessToken("user1", "https://host/mcp", "mcp", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := verifyOauthJWT(access); !ok {
		t.Fatal("valid access token rejected")
	}
	refresh, _ := mintAccessToken("user1", "https://host/mcp", "refresh", time.Hour)
	if _, ok := verifyOauthJWT(refresh); ok {
		t.Fatal("refresh token must NOT authenticate /mcp")
	}
	if _, ok := verifyRefreshToken(refresh); !ok {
		t.Fatal("refresh token rejected by verifyRefreshToken")
	}
	// expired
	expired, _ := mintAccessToken("user1", "aud", "mcp", -time.Minute)
	if _, ok := verifyOauthJWT(expired); ok {
		t.Fatal("expired token accepted")
	}
	// tampered signature
	if _, ok := verifyOauthJWT(access + "x"); ok {
		t.Fatal("tampered token accepted")
	}
}

func TestOauthToken_PKCE(t *testing.T) {
	setTestOauthKey(t)
	// register a client with a known secret (hash:salt)
	secret := "test-secret-123"
	h, salt, _ := hashPassword(secret)
	oauthMu.Lock()
	oauthClients = []OAuthClient{{ID: "cli1", Secret: h + ":" + salt, RedirectURIs: []string{"https://cb"}}}
	// seed an auth code with a PKCE challenge
	verifier := "abc123def456ghijklmnopqrstuvwxyz0123456789ABCDEF"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	oauthCodes["code1"] = oauthCode{
		UserID: "user1", ClientID: "cli1", RedirectURI: "https://cb",
		Scope: "mcp", CodeChallenge: challenge, Resource: "https://host/mcp",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	oauthCodes["code2"] = oauthCodes["code1"]
	oauthMu.Unlock()

	post := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		(&HTTPServer{}).handleOauthToken(rec, req)
		return rec
	}

	// wrong verifier -> rejected
	bad := post(url.Values{"grant_type": {"authorization_code"}, "client_id": {"cli1"},
		"client_secret": {secret}, "code": {"code1"}, "redirect_uri": {"https://cb"}, "code_verifier": {"WRONG"}})
	if bad.Code == 200 {
		t.Fatalf("wrong PKCE verifier accepted (got 200)")
	}

	// correct verifier -> access + refresh, aud bound to resource
	good := post(url.Values{"grant_type": {"authorization_code"}, "client_id": {"cli1"},
		"client_secret": {secret}, "code": {"code2"}, "redirect_uri": {"https://cb"}, "code_verifier": {verifier}})
	if good.Code != 200 {
		t.Fatalf("valid PKCE exchange failed: %d %s", good.Code, good.Body.String())
	}
	var out map[string]interface{}
	json.Unmarshal(good.Body.Bytes(), &out)
	at, _ := out["access_token"].(string)
	if at == "" || out["refresh_token"] == nil {
		t.Fatalf("missing tokens: %v", out)
	}
	claims, ok := verifyOauthJWT(at)
	if !ok {
		t.Fatal("issued access token does not verify")
	}
	if claims["aud"] != "https://host/mcp" {
		t.Fatalf("aud not bound to resource: %v", claims["aud"])
	}

	// bad client secret -> rejected
	badcli := post(url.Values{"grant_type": {"authorization_code"}, "client_id": {"cli1"},
		"client_secret": {"nope"}, "code": {"code1"}, "redirect_uri": {"https://cb"}, "code_verifier": {verifier}})
	if badcli.Code == 200 {
		t.Fatal("bad client secret accepted")
	}
}

func TestAuthMCP_ConnectorScopingAndChallenge(t *testing.T) {
	setTestOauthKey(t)
	s := &HTTPServer{}
	var sawAllowed, sawConnector string
	next := func(w http.ResponseWriter, r *http.Request) {
		sawAllowed = r.Header.Get("X-Yaver-AllowedTools")
		sawConnector = r.Header.Get("X-Yaver-Connector")
		w.WriteHeader(200)
	}
	h := s.authMCP(next)

	// 1. no auth -> 401 + WWW-Authenticate discovery
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/mcp", nil))
	if rec.Code != 401 || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("no-auth should 401 with challenge; got %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}

	// 2. valid connector JWT -> default-deny scope stamped, connector flag set
	at, _ := mintAccessToken("user1", "https://host/mcp", "mcp", time.Hour)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+at)
	// a spoofed inbound scope must be overwritten, never trusted
	req.Header.Set("X-Yaver-AllowedTools", "exec_command,write_file")
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 200 {
		t.Fatalf("valid connector rejected: %d", rec.Code)
	}
	if sawConnector != "true" {
		t.Fatal("connector flag not stamped")
	}
	if strings.Contains(sawAllowed, "exec_command") || strings.Contains(sawAllowed, "write_file") {
		t.Fatalf("SECURITY: connector could reach dangerous tools; allowed=%q", sawAllowed)
	}
	if !strings.Contains(sawAllowed, "calculate") {
		t.Fatalf("connector default-deny allowlist not stamped; got %q", sawAllowed)
	}

	// 3. the stamped scope actually DENIES a dangerous tool at dispatch
	if d := mcpToolDeniedByScope(reqWithAllowed(sawAllowed), "exec_command"); d == nil {
		t.Fatal("SECURITY: exec_command not denied by connector scope")
	}
	if d := mcpToolDeniedByScope(reqWithAllowed(sawAllowed), "calculate"); d != nil {
		t.Fatalf("calculate wrongly denied: %v", d.Reason)
	}
}

func reqWithAllowed(v string) *http.Request {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("X-Yaver-AllowedTools", v)
	return r
}

func TestMCPProtectedResourceMetadata(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource", nil)
	req.Host = "yaver.example"
	(&HTTPServer{}).handleMCPProtectedResourceMetadata(rec, req)
	var out map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["resource"] != "http://yaver.example/mcp" {
		t.Fatalf("bad resource: %v", out["resource"])
	}
	as, _ := out["authorization_servers"].([]interface{})
	if len(as) != 1 || as[0] != "http://yaver.example/oauth" {
		t.Fatalf("bad authorization_servers: %v", out["authorization_servers"])
	}
}
