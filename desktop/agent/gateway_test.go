package main

// gateway_test.go — end-to-end tests for the Personal Agent Gateway slice.
//
// Convention (CLAUDE.md): real HTTP servers on random ports, no mocks. We spin
// up (a) a fake OAuth token endpoint and (b) a fake resource API on httptest
// servers, build a manifest pointing at them, back the broker with an IN-MEMORY
// CredStore (keeps the suite off the macOS login keychain), and assert the full
// flow: code→token, authed GET, answerSchema projection, refresh-on-expiry, and
// the Policy Guard (401 refresh-once, 403/429 structured block).
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newGatewayTestHarness builds a registry rooted in t.TempDir(), an in-memory
// CredStore, and a deps bundle wiring them with a real http client.
func newGatewayTestHarness(t *testing.T) (*gatewayDeps, *memCredStore, *ConnectorRegistry) {
	t.Helper()
	reg, err := newConnectorRegistryAt(t.TempDir())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	store := newMemCredStore()
	deps := &gatewayDeps{
		registry:   reg,
		broker:     newBroker(store),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	return deps, store, reg
}

// fakeOAuthServer issues access tokens. Each refresh/exchange increments a
// counter and returns a unique access token so the test can prove a refresh
// actually happened. expiresIn controls the token lifetime it advertises.
type fakeOAuthServer struct {
	srv           *httptest.Server
	exchanges     int32
	refreshes     int32
	expiresIn     int64
	rejectRefresh bool // when true, refresh returns 401 (revoked grant)
}

func newFakeOAuthServer(expiresIn int64) *fakeOAuthServer {
	f := &fakeOAuthServer{expiresIn: expiresIn}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant := r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		switch grant {
		case "authorization_code":
			n := atomic.AddInt32(&f.exchanges, 1)
			// PKCE verifier must be present.
			if r.Form.Get("code_verifier") == "" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "missing code_verifier"})
				return
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token":  fmt.Sprintf("access-exch-%d", n),
				"refresh_token": "refresh-token-1",
				"token_type":    "Bearer",
				"expires_in":    f.expiresIn,
				"scope":         "calendar.readonly",
			})
		case "refresh_token":
			if f.rejectRefresh {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			n := atomic.AddInt32(&f.refreshes, 1)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": fmt.Sprintf("access-refresh-%d", n),
				"token_type":   "Bearer",
				"expires_in":   f.expiresIn,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "unsupported_grant_type"})
		}
	}))
	return f
}

func (f *fakeOAuthServer) close() { f.srv.Close() }

// fakeResourceAPI serves a calendar-event-shaped JSON, but only when it sees the
// access token the test expects. It can be told to require a specific bearer
// (to prove refresh), or to return a block / 401.
type fakeResourceAPI struct {
	srv          *httptest.Server
	requireToken atomic.Value // string; "" => any non-empty bearer accepted
	status       atomic.Int32 // override status code (0 => normal 200)
	lastAuth     atomic.Value // string; last Authorization header seen
}

func newFakeResourceAPI() *fakeResourceAPI {
	f := &fakeResourceAPI{}
	f.requireToken.Store("")
	f.lastAuth.Store("")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		f.lastAuth.Store(auth)
		if s := f.status.Load(); s != 0 {
			w.WriteHeader(int(s))
			w.Write([]byte(`{"error":"forced status"}`))
			return
		}
		want := f.requireToken.Load().(string)
		bearer := "Bearer "
		if auth == "" || len(auth) <= len(bearer) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"no token"}`))
			return
		}
		tok := auth[len(bearer):]
		if want != "" && tok != want {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"stale token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// Calendar-style list with one event (terse-manifest array descent).
		w.Write([]byte(`{
			"items": [
				{"summary":"Standup","start":{"dateTime":"2026-06-18T09:00:00Z"},"location":"Room A"}
			]
		}`))
	}))
	return f
}

func (f *fakeResourceAPI) close() { f.srv.Close() }

// buildTestConnector returns a manifest pointing at the fake servers, with an
// answerSchema exercising explicit dotted paths + array descent + an optional.
func buildTestConnector(oauthSrv, apiSrv string) Connector {
	return Connector{
		ID:      "fake",
		Engine:  "api",
		Surface: apiSrv,
		Auth: ConnectorAuth{
			Method:   "oauth_code",
			AuthURL:  oauthSrv + "/auth",
			TokenURL: oauthSrv + "/token",
			ClientID: "test-client",
			Scopes:   []string{"calendar.readonly"},
			CredRef:  "gateway/fake/oauth",
		},
		Capabilities: []Capability{{
			ID:   "next_event",
			Verb: "get",
			Risk: "read",
			Flow: CapabilityFlow{Type: "api", Method: "GET", Path: "/calendar/events"},
			AnswerSchema: map[string]string{
				"title":    "items.0.summary:string",
				"start":    "items.0.start.dateTime:datetime",
				"location": "items.0.location:string?",
				"missing":  "items.0.nonexistent:string?", // optional, absent => omitted
			},
		}},
	}
}

// TestGatewayFullFlow drives code→token→authed GET→answerSchema projection.
func TestGatewayFullFlow(t *testing.T) {
	oauth := newFakeOAuthServer(3600)
	defer oauth.close()
	api := newFakeResourceAPI()
	defer api.close()

	deps, store, reg := newGatewayTestHarness(t)
	conn := buildTestConnector(oauth.srv.URL, api.srv.URL)
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store connector: %v", err)
	}

	// One-time consent: exchange an auth code (PKCE) for tokens.
	h := newOAuthCodeHandler(store)
	pkce, err := newPKCE()
	if err != nil {
		t.Fatalf("pkce: %v", err)
	}
	if pkce.Verifier == "" || pkce.Challenge == "" || pkce.Method != "S256" {
		t.Fatalf("pkce malformed: %+v", pkce)
	}
	if _, err := h.ExchangeCode(context.Background(), &conn, "auth-code-xyz", "http://localhost/cb", pkce.Verifier); err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if atomic.LoadInt32(&oauth.exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", oauth.exchanges)
	}

	// Invoke the read capability.
	res, err := deps.gatewayInvoke(context.Background(), "fake", "next_event", nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %+v", res)
	}
	if got := res.Answer["title"]; got != "Standup" {
		t.Fatalf("title = %v, want Standup", got)
	}
	if got := res.Answer["start"]; got != "2026-06-18T09:00:00Z" {
		t.Fatalf("start = %v", got)
	}
	if got := res.Answer["location"]; got != "Room A" {
		t.Fatalf("location = %v", got)
	}
	if _, present := res.Answer["missing"]; present {
		t.Fatalf("optional absent field should be omitted, got %v", res.Answer["missing"])
	}
	// No refresh should have been needed (token fresh).
	if atomic.LoadInt32(&oauth.refreshes) != 0 {
		t.Fatalf("expected 0 refreshes on fresh token, got %d", oauth.refreshes)
	}
}

// TestGatewayRefreshOnExpiry seeds an EXPIRED token and confirms the broker
// refreshes it (and the API only accepts the refreshed token).
func TestGatewayRefreshOnExpiry(t *testing.T) {
	oauth := newFakeOAuthServer(3600)
	defer oauth.close()
	api := newFakeResourceAPI()
	defer api.close()
	// The API will only accept the token produced by the FIRST refresh, proving
	// the stale seeded token was actually replaced.
	api.requireToken.Store("access-refresh-1")

	deps, store, reg := newGatewayTestHarness(t)
	conn := buildTestConnector(oauth.srv.URL, api.srv.URL)
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store connector: %v", err)
	}

	// Seed an already-expired access token + a valid refresh token.
	seeded := &OAuthCreds{
		AccessToken:  "stale-access",
		RefreshToken: "refresh-token-1",
		ExpiryUnix:   time.Now().Add(-time.Hour).Unix(), // expired
		TokenURL:     oauth.srv.URL + "/token",
		ClientID:     "test-client",
	}
	if err := saveOAuthCreds(store, conn.Auth.CredRef, seeded); err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	res, err := deps.gatewayInvoke(context.Background(), "fake", "next_event", nil)
	if err != nil {
		t.Fatalf("invoke after expiry: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %+v", res)
	}
	if got := res.Answer["title"]; got != "Standup" {
		t.Fatalf("title = %v, want Standup", got)
	}
	if atomic.LoadInt32(&oauth.refreshes) < 1 {
		t.Fatalf("expected at least 1 refresh, got %d", oauth.refreshes)
	}
	// The persisted creds should now carry the refreshed access token.
	got, err := loadOAuthCreds(store, conn.Auth.CredRef)
	if err != nil {
		t.Fatalf("reload creds: %v", err)
	}
	if got.AccessToken != "access-refresh-1" {
		t.Fatalf("persisted access token = %q, want access-refresh-1", got.AccessToken)
	}
	if got.ExpiryUnix <= time.Now().Unix() {
		t.Fatalf("refreshed token should not be already expired")
	}
}

// TestGatewayBlockedBackoff confirms a 429 returns a structured block and STOPS.
func TestGatewayBlockedBackoff(t *testing.T) {
	oauth := newFakeOAuthServer(3600)
	defer oauth.close()
	api := newFakeResourceAPI()
	defer api.close()
	api.status.Store(int32(http.StatusTooManyRequests))

	deps, store, reg := newGatewayTestHarness(t)
	conn := buildTestConnector(oauth.srv.URL, api.srv.URL)
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store connector: %v", err)
	}
	if err := saveOAuthCreds(store, conn.Auth.CredRef, &OAuthCreds{
		AccessToken: "tok", ExpiryUnix: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	res, err := deps.gatewayInvoke(context.Background(), "fake", "next_event", nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !res.Blocked || res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected structured block 429, got %+v", res)
	}
}

// TestGatewayRefreshOnceThen401 confirms a server-side 401 triggers exactly one
// refresh + retry, then fails clean (no retry-spam).
func TestGatewayRefreshOnceThen401(t *testing.T) {
	oauth := newFakeOAuthServer(3600)
	defer oauth.close()
	api := newFakeResourceAPI()
	defer api.close()
	// Force a permanent 401 from the API regardless of token.
	api.status.Store(int32(http.StatusUnauthorized))

	deps, store, reg := newGatewayTestHarness(t)
	conn := buildTestConnector(oauth.srv.URL, api.srv.URL)
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store connector: %v", err)
	}
	if err := saveOAuthCreds(store, conn.Auth.CredRef, &OAuthCreds{
		AccessToken:  "tok",
		RefreshToken: "refresh-token-1",
		ExpiryUnix:   time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	_, err := deps.gatewayInvoke(context.Background(), "fake", "next_event", nil)
	if err == nil {
		t.Fatalf("expected a clean error after refresh+retry 401")
	}
	// Exactly one refresh attempt (the single retry path), not a spam loop.
	if n := atomic.LoadInt32(&oauth.refreshes); n != 1 {
		t.Fatalf("expected exactly 1 refresh on 401 retry, got %d", n)
	}
}

// TestGatewayRevokedRefresh confirms a revoked refresh token (token endpoint
// 401) fails clean with a re-consent message.
func TestGatewayRevokedRefresh(t *testing.T) {
	oauth := newFakeOAuthServer(3600)
	oauth.rejectRefresh = true
	defer oauth.close()
	api := newFakeResourceAPI()
	defer api.close()

	deps, store, reg := newGatewayTestHarness(t)
	conn := buildTestConnector(oauth.srv.URL, api.srv.URL)
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store connector: %v", err)
	}
	if err := saveOAuthCreds(store, conn.Auth.CredRef, &OAuthCreds{
		AccessToken:  "stale",
		RefreshToken: "refresh-token-1",
		ExpiryUnix:   time.Now().Add(-time.Hour).Unix(), // expired => triggers refresh
	}); err != nil {
		t.Fatalf("seed creds: %v", err)
	}

	_, err := deps.gatewayInvoke(context.Background(), "fake", "next_event", nil)
	if err == nil {
		t.Fatalf("expected error on revoked refresh token")
	}
}

// TestGatewayRegistry covers store/get/list/CapabilitiesForMCP + the inline-
// secret + engine guards.
func TestGatewayRegistry(t *testing.T) {
	reg, err := newConnectorRegistryAt(t.TempDir())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	conn := buildTestConnector("https://oauth.example", "https://api.example")
	if err := reg.Store(conn); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := reg.Get("fake")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Surface != "https://api.example" || len(got.Capabilities) != 1 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	list, err := reg.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	caps, err := reg.CapabilitiesForMCP()
	if err != nil || len(caps) != 1 || caps[0].Connector != "fake" || caps[0].Capability != "next_event" {
		t.Fatalf("CapabilitiesForMCP: %v %+v", err, caps)
	}

	// Inline-secret credRef must be rejected.
	bad := conn
	bad.ID = "badsecret"
	bad.Auth.CredRef = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signaturehere"
	if err := reg.Store(bad); err == nil {
		t.Fatalf("expected rejection of inline-secret credRef")
	}

	// Unsupported engine must be rejected ("api" + "redroid" are the supported
	// engines; anything else is refused loudly).
	badEngine := conn
	badEngine.ID = "badengine"
	badEngine.Engine = "ftp"
	if err := reg.Store(badEngine); err == nil {
		t.Fatalf("expected rejection of unsupported engine")
	}

	// An ACT verb paired with a non-mutating GET method is a contradiction and
	// must be rejected (act capabilities require POST/PUT/PATCH/DELETE — see
	// validateCapabilityFlow / gateway_act.go).
	badVerb := conn
	badVerb.ID = "badverb"
	badVerb.Capabilities = []Capability{{
		ID: "place", Verb: "add", Risk: "low",
		Flow: CapabilityFlow{Type: "api", Method: "GET", Path: "/x"},
	}}
	if err := reg.Store(badVerb); err == nil {
		t.Fatalf("expected rejection of act verb paired with GET method")
	}

	// A well-shaped ACT capability (mutating method + declared risk) is accepted.
	goodAct := conn
	goodAct.ID = "goodact"
	goodAct.Capabilities = []Capability{{
		ID: "place", Verb: "add", Risk: "low",
		Flow: CapabilityFlow{Type: "api", Method: "POST", Path: "/orders", Body: `{"item":"{item}"}`},
	}}
	if err := reg.Store(goodAct); err != nil {
		t.Fatalf("expected well-shaped act capability to be accepted, got %v", err)
	}
}

// TestGatewayProjectAnswer unit-tests the deterministic projection helper.
func TestGatewayProjectAnswer(t *testing.T) {
	raw := []byte(`{"data":{"balance":42.5,"currency":"EUR"},"list":[{"v":"first"}]}`)
	schema := map[string]string{
		"balance":  "data.balance:number",
		"currency": "data.currency:string",
		"first":    "list.0.v:string",
		"opt":      "data.missing:string?",
	}
	out, err := projectAnswer(raw, schema)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if out["balance"].(float64) != 42.5 {
		t.Fatalf("balance = %v", out["balance"])
	}
	if out["currency"] != "EUR" || out["first"] != "first" {
		t.Fatalf("unexpected: %+v", out)
	}
	if _, ok := out["opt"]; ok {
		t.Fatalf("optional-absent should be omitted")
	}

	// A missing REQUIRED field must error loudly.
	if _, err := projectAnswer(raw, map[string]string{"x": "data.nope:string"}); err == nil {
		t.Fatalf("expected error for missing required field")
	}
}
