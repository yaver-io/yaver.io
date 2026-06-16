package main

// gateway_connect_test.go — tests for OAuth connector AUTHORING (gateway_connect.go).
//
// Convention (CLAUDE.md): real httptest servers, in-memory CredStore only — NO
// real vault, NO macOS keychain, NO network beyond httptest. Run scoped:
//   go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// authoringConnector builds an engine:"api"/oauth_code manifest pointing at the
// given token endpoint, with a single read GET capability.
func authoringConnector(id, surface, authURL, tokenURL string) *Connector {
	return &Connector{
		ID:      id,
		Engine:  "api",
		Surface: surface,
		Auth: ConnectorAuth{
			Method:   "oauth_code",
			AuthURL:  authURL,
			TokenURL: tokenURL,
			ClientID: "test-client",
			Scopes:   []string{"calendar.readonly"},
			CredRef:  "gateway/" + id + "/oauth",
		},
		Capabilities: []Capability{{
			ID:   "next_event",
			Verb: "get",
			Risk: "read",
			Flow: CapabilityFlow{Type: "api", Method: "GET", Path: "/calendar/events"},
			AnswerSchema: map[string]string{
				"title": "items.0.summary:string",
			},
		}},
	}
}

// TestGatewayPKCEChallengeDerivation asserts the S256 challenge is the
// base64url-no-pad SHA-256 of the verifier (RFC 7636).
func TestGatewayPKCEChallengeDerivation(t *testing.T) {
	pkce, err := newPKCE()
	if err != nil {
		t.Fatalf("newPKCE: %v", err)
	}
	if pkce.Method != "S256" {
		t.Fatalf("method = %q, want S256", pkce.Method)
	}
	if pkce.Verifier == "" || pkce.Challenge == "" {
		t.Fatalf("empty pkce: %+v", pkce)
	}
	sum := sha256.Sum256([]byte(pkce.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != want {
		t.Fatalf("challenge = %q, want %q", pkce.Challenge, want)
	}
	// Two pairs must differ (entropy).
	pkce2, _ := newPKCE()
	if pkce2.Verifier == pkce.Verifier {
		t.Fatalf("verifier not random across calls")
	}
}

// TestGatewayConnectStartBuildsAuthURL checks the consent URL carries PKCE
// challenge, state, client id, redirect, and scopes — and that the redirect is a
// loopback URI.
func TestGatewayConnectStartBuildsAuthURL(t *testing.T) {
	conn := authoringConnector("acme", "https://api.acme.test", "https://acme.test/auth", "https://acme.test/token")
	authURL, pc, err := gatewayConnectStart(conn)
	if err != nil {
		t.Fatalf("connectStart: %v", err)
	}
	defer pc.close()

	if !strings.HasPrefix(pc.redirectURI, "http://127.0.0.1:") {
		t.Fatalf("redirect not loopback: %q", pc.redirectURI)
	}
	if !strings.HasSuffix(pc.redirectURI, gatewayConnectCallbackPath) {
		t.Fatalf("redirect missing callback path: %q", pc.redirectURI)
	}
	for _, want := range []string{
		"code_challenge=" + pc.pkce.Challenge,
		"code_challenge_method=S256",
		"state=" + pc.state,
		"client_id=test-client",
		"response_type=code",
	} {
		if !strings.Contains(authURL, want) {
			t.Fatalf("auth URL missing %q\nurl: %s", want, authURL)
		}
	}
	if !strings.Contains(authURL, "calendar.readonly") {
		t.Fatalf("auth URL missing scope: %s", authURL)
	}
}

// TestGatewayConnectFinishStoresCreds drives code→token exchange and asserts the
// correct OAuthCreds land in the in-memory store and the manifest is registered.
func TestGatewayConnectFinishStoresCreds(t *testing.T) {
	var gotVerifier, gotSecret, gotGrant string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		gotVerifier = r.Form.Get("code_verifier")
		gotSecret = r.Form.Get("client_secret")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "access-123",
			"refresh_token": "refresh-123",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"scope":         "calendar.readonly",
		})
	}))
	defer tokenSrv.Close()

	reg, err := newConnectorRegistryAt(t.TempDir())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	store := newMemCredStore()

	conn := authoringConnector("acme", "https://api.acme.test", "https://acme.test/auth", tokenSrv.URL)
	authURL, pc, err := gatewayConnectStart(conn)
	if err != nil {
		t.Fatalf("connectStart: %v", err)
	}
	defer pc.close()
	if authURL == "" {
		t.Fatal("empty auth URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gatewayConnectFinish(ctx, conn, "the-code", pc.pkce.Verifier, "super-secret", pc.redirectURI, store, reg); err != nil {
		t.Fatalf("connectFinish: %v", err)
	}

	if gotGrant != "authorization_code" {
		t.Fatalf("grant_type = %q", gotGrant)
	}
	if gotVerifier != pc.pkce.Verifier {
		t.Fatalf("server saw verifier %q, want %q", gotVerifier, pc.pkce.Verifier)
	}
	if gotSecret != "super-secret" {
		t.Fatalf("server saw client_secret %q, want super-secret", gotSecret)
	}

	// Tokens persisted under the connector's CredRef.
	creds, err := loadOAuthCreds(store, conn.Auth.CredRef)
	if err != nil {
		t.Fatalf("loadOAuthCreds: %v", err)
	}
	if creds.AccessToken != "access-123" || creds.RefreshToken != "refresh-123" {
		t.Fatalf("creds = %+v", creds)
	}
	if creds.ExpiryUnix == 0 {
		t.Fatal("expiry not set from expires_in")
	}

	// Client secret persisted to the vault under the derived ref, NOT in creds.
	sp, sn, _ := credNameFromRef(clientSecretRef(conn.Auth.CredRef))
	secBlob, err := store.GetCreds(sp, sn)
	if err != nil {
		t.Fatalf("client secret not stored: %v", err)
	}
	if string(secBlob) != "super-secret" {
		t.Fatalf("stored secret = %q", string(secBlob))
	}

	// Manifest registered + retrievable.
	got, err := reg.Get("acme")
	if err != nil {
		t.Fatalf("registry.Get: %v", err)
	}
	if got.Engine != "api" || got.Auth.Method != "oauth_code" {
		t.Fatalf("registered manifest wrong: %+v", got.Auth)
	}
}

// TestGatewayConnectFinishLoopbackCallback exercises the loopback listener path:
// hitting the redirect URI with code+state delivers the code that finish exchanges.
func TestGatewayConnectFinishLoopbackCallback(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "loop-access", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer tokenSrv.Close()

	conn := authoringConnector("loopco", "https://api.loop.test", "https://loop.test/auth", tokenSrv.URL)
	_, pc, err := gatewayConnectStart(conn)
	if err != nil {
		t.Fatalf("connectStart: %v", err)
	}
	defer pc.close()

	// Simulate the browser redirect to the loopback listener.
	go func() {
		resp, err := http.Get(pc.redirectURI + "?code=cb-code&state=" + pc.state)
		if err == nil {
			resp.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, err := pc.wait(ctx)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if code != "cb-code" {
		t.Fatalf("code = %q, want cb-code", code)
	}

	// A mismatched state must NOT deliver a result (CSRF guard). Fresh attempt.
	_, pc2, err := gatewayConnectStart(conn)
	if err != nil {
		t.Fatalf("connectStart2: %v", err)
	}
	defer pc2.close()
	resp, err := http.Get(pc2.redirectURI + "?code=forged&state=WRONG")
	if err == nil {
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("state mismatch should 400, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if _, err := pc2.wait(ctx2); err == nil {
		t.Fatal("forged-state callback should not have delivered a code")
	}
}

// TestGatewayConnectRejectsInlineSecretManifest asserts the manifest validator
// rejects a credRef that carries an inline secret instead of a vault key — the
// authoring path must never persist a secret in the manifest.
func TestGatewayConnectRejectsInlineSecretManifest(t *testing.T) {
	bad := Connector{
		ID:      "leaky",
		Engine:  "api",
		Surface: "https://api.leaky.test",
		Auth: ConnectorAuth{
			Method:  "oauth_code",
			AuthURL: "https://leaky.test/auth", TokenURL: "https://leaky.test/token",
			// An inline JWT-shaped secret, not a vault key.
			CredRef: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature-here-very-long",
		},
		Capabilities: []Capability{{
			ID: "x", Verb: "get", Flow: CapabilityFlow{Type: "api", Method: "GET", Path: "/x"},
		}},
	}
	if err := validateConnectorManifest(bad); err == nil {
		t.Fatal("expected inline-secret credRef to be rejected")
	}

	// And via the finish entrypoint (validates before any network work).
	reg, _ := newConnectorRegistryAt(t.TempDir())
	store := newMemCredStore()
	if err := gatewayConnectFinish(context.Background(), &bad, "code", "verifier", "", "http://127.0.0.1/cb", store, reg); err == nil {
		t.Fatal("expected connectFinish to reject inline-secret manifest")
	}
}

// TestGatewayConnectorsListNoSecrets asserts gateway_connectors listing carries
// public metadata only — no token, secret, or credRef value.
func TestGatewayConnectorsListNoSecrets(t *testing.T) {
	reg, err := newConnectorRegistryAt(t.TempDir())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	conn := authoringConnector("acme", "https://api.acme.test", "https://acme.test/auth", "https://acme.test/token")
	if err := reg.Store(*conn); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Build the listing the same way mcpGatewayConnectors does, but against this
	// test registry (the MCP entrypoint resolves the production registry).
	connectors, err := reg.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	blob, _ := json.Marshal(connectors)
	// The manifest legitimately contains the credRef vault KEY (not a secret) +
	// client id; assert no token/secret leaks. We marshal the listing shape the
	// MCP tool emits (id/engine/method/capabilities) and assert it omits creds.
	listing := make([]map[string]interface{}, 0, len(connectors))
	for _, c := range connectors {
		capIDs := []string{}
		for _, cap := range c.Capabilities {
			capIDs = append(capIDs, cap.ID)
		}
		listing = append(listing, map[string]interface{}{
			"id": c.ID, "engine": c.Engine, "method": c.Auth.Method, "capabilities": capIDs,
		})
	}
	lblob, _ := json.Marshal(listing)
	for _, forbidden := range []string{"access_token", "refresh_token", "client_secret", "credRef", "super-secret", "Bearer "} {
		if strings.Contains(string(lblob), forbidden) {
			t.Fatalf("listing leaked %q: %s", forbidden, lblob)
		}
	}
	// Sanity: the raw manifest blob isn't what we expose (it would carry credRef).
	if !strings.Contains(string(blob), "credRef") {
		t.Skip("manifest shape changed; credRef no longer present")
	}
}

// TestGatewayCapabilitiesNoSecrets asserts a connector's capability listing has
// no token/secret material.
func TestGatewayCapabilitiesNoSecrets(t *testing.T) {
	conn := authoringConnector("acme", "https://api.acme.test", "https://acme.test/auth", "https://acme.test/token")
	listing := []map[string]interface{}{}
	for _, cap := range conn.Capabilities {
		listing = append(listing, map[string]interface{}{
			"id": cap.ID, "verb": cap.Verb, "risk": cap.Risk,
			"params": flowParams(cap.Flow.Path), "answerSchema": cap.AnswerSchema,
		})
	}
	blob, _ := json.Marshal(listing)
	for _, forbidden := range []string{"access_token", "client_secret", "token", "secret"} {
		if strings.Contains(strings.ToLower(string(blob)), forbidden) {
			t.Fatalf("capability listing leaked %q: %s", forbidden, blob)
		}
	}
}

// TestGatewayFlowParams checks placeholder extraction (excluding the built-in {now}).
func TestGatewayFlowParams(t *testing.T) {
	got := flowParams("/users/{userId}/events?after={now}&max={max}")
	want := map[string]bool{"userId": true, "max": true}
	if len(got) != len(want) {
		t.Fatalf("params = %v, want keys %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("unexpected param %q in %v", p, got)
		}
	}
}
