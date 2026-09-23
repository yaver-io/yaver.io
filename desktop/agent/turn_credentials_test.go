package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// The credential producer shipped without a mux registration once, leaving
// every browser viewer on public STUN even though the handler and its unit
// tests were green. Pin the actual HTTPServer wiring as part of this contract:
// a signal/route with no consumer is not a shipped feature.
func TestRemoteRuntimeTURNCredentialsRouteIsWired(t *testing.T) {
	src, err := os.ReadFile("httpserver.go")
	if err != nil {
		t.Fatalf("read httpserver.go: %v", err)
	}
	const registration = `mux.HandleFunc("/remote-runtime/turn-credentials", s.auth(s.handleRemoteRuntimeTURNCredentials))`
	if strings.Count(string(src), registration) != 1 {
		t.Fatalf("TURN credential route registration count = %d, want exactly 1", strings.Count(string(src), registration))
	}
}

func TestFetchRelayTURNCredentials_UsesScopedPasswordAndAcceptsAllTURNTransports(t *testing.T) {
	const password = "scoped-account-password"
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ice" || r.Header.Get("X-Relay-Password") != password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(turnCredentialResponse{
			TTLSeconds: 60,
			IceServers: []turnIceServer{
				{URLs: []string{"stun:relay.example.test:3478"}},
				{URLs: []string{"turn:relay.example.test:3478?transport=udp", "turn:relay.example.test:3478?transport=tcp", "turns:relay.example.test:5349?transport=tcp"}, Username: "expiry:nonce", Credential: "ephemeral"},
			},
		})
	}))
	defer broker.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := fetchRelayTURNCredentials(ctx, broker.Client(), relayICEEndpoint{URL: broker.URL + "/ice", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.IceServers) != 2 || len(resp.IceServers[1].URLs) != 3 {
		t.Fatalf("unexpected ICE response: %+v", resp)
	}
}

func TestManagedICEServersFromConfig_UsesSameBrokerPathAsDoctorAndRuntime(t *testing.T) {
	const password = "doctor-scoped-password"
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ice" || r.Header.Get("X-Relay-Password") != password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(turnCredentialResponse{
			TTLSeconds: 60,
			IceServers: []turnIceServer{
				{URLs: []string{"stun:relay.example.test:3478"}},
				{URLs: []string{"turn:relay.example.test:3478?transport=udp", "turns:relay.example.test:5349?transport=tcp"}, Username: "expiry:nonce", Credential: "ephemeral"},
			},
		})
	}))
	defer broker.Close()

	cfg := &Config{RelayServers: []RelayServerConfig{{HttpURL: broker.URL, Password: password}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	servers, err := managedICEServersFromConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || len(servers[1].URLs) != 2 || servers[1].Username == "" || servers[1].Credential == nil {
		t.Fatalf("managed ICE servers = %+v", servers)
	}
}

func TestRelayICEEndpoints_RejectsPlaintextRemotePasswordTransport(t *testing.T) {
	cfg := &Config{
		RelayPassword: "secret",
		RelayServers: []RelayServerConfig{
			{HttpURL: "http://relay.example.test"},
			{HttpURL: "https://relay.example.test"},
			{HttpURL: "http://127.0.0.1:8080"},
		},
	}
	got := relayICEEndpoints(cfg)
	if len(got) != 2 {
		t.Fatalf("endpoints = %+v, want HTTPS remote + loopback HTTP only", got)
	}
	for _, endpoint := range got {
		if endpoint.URL == "http://relay.example.test/ice" {
			t.Fatal("would send relay password over plaintext remote HTTP")
		}
	}
}

func TestIceServersForPeer_STUNOnlyByDefault(t *testing.T) {
	t.Setenv("YAVER_STUN_URL", "")
	t.Setenv("YAVER_TURN_URL", "")
	t.Setenv("TURN_AUTH_SECRET", "")
	t.Setenv("RELAY_PASSWORD", "")
	t.Setenv("RELAY_URL", "")

	servers := iceServersForPeer()
	if len(servers) != 1 {
		t.Fatalf("ice server count = %d, want 1 (STUN-only)", len(servers))
	}
	assertSTUNServer(t, servers[0], "stun:stun.l.google.com:19302")
}

func TestIceServersForPeer_IncludesTURNWhenConfigured(t *testing.T) {
	t.Setenv("YAVER_STUN_URL", "")
	t.Setenv("YAVER_TURN_URL", "turn:relay.example.com:3478")
	t.Setenv("TURN_AUTH_SECRET", "turn-secret")
	t.Setenv("RELAY_PASSWORD", "")

	servers := iceServersForPeer()
	if len(servers) != 2 {
		t.Fatalf("ice server count = %d, want 2 (STUN + TURN)", len(servers))
	}
	assertSTUNServer(t, servers[0], "stun:stun.l.google.com:19302")
	turn := servers[1]
	if len(turn.URLs) != 1 || turn.URLs[0] != "turn:relay.example.com:3478" {
		t.Fatalf("TURN URLs = %v, want configured relay", turn.URLs)
	}
	if turn.Username == "" || turn.Credential == nil || turn.Credential == "" {
		t.Fatalf("TURN credentials must be non-empty, got %+v", turn)
	}
}

func TestIceServersForPeer_TURNURLWithoutSecretFallsBackToSTUN(t *testing.T) {
	t.Setenv("YAVER_STUN_URL", "")
	t.Setenv("YAVER_TURN_URL", "turn:relay.example.com:3478")
	t.Setenv("TURN_AUTH_SECRET", "")
	t.Setenv("RELAY_PASSWORD", "")
	t.Setenv("RELAY_URL", "")

	servers := iceServersForPeer()
	if len(servers) != 1 {
		t.Fatalf("ice server count = %d, want 1 (STUN-only)", len(servers))
	}
	assertSTUNServer(t, servers[0], "stun:stun.l.google.com:19302")
}

func TestDerivedTurnURLFromRelayURL(t *testing.T) {
	t.Setenv("YAVER_TURN_URL", "")
	t.Setenv("YAVER_TURN_PORT", "")
	for _, tc := range []struct {
		name  string
		relay string
		want  string
	}{
		{name: "https relay → default TURN port", relay: "https://relay.yaver.io", want: "turn:relay.yaver.io:3478"},
		{name: "http relay with path", relay: "http://relay.example.com:8080/", want: "turn:relay.example.com:3478"},
		{name: "unset relay → empty", relay: "", want: ""},
		{name: "garbage relay → empty", relay: "://not a url", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RELAY_URL", tc.relay)
			if got := derivedTurnURLFromRelayURL(); got != tc.want {
				t.Fatalf("derivedTurnURLFromRelayURL() = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("YAVER_TURN_PORT override", func(t *testing.T) {
		t.Setenv("RELAY_URL", "https://relay.yaver.io")
		t.Setenv("YAVER_TURN_PORT", "5349")
		if got := derivedTurnURLFromRelayURL(); got != "turn:relay.yaver.io:5349" {
			t.Fatalf("derivedTurnURLFromRelayURL() = %q, want turn:relay.yaver.io:5349", got)
		}
	})
}

func TestResolveTurnURL_ExplicitWinsOverDerivation(t *testing.T) {
	t.Setenv("YAVER_TURN_URL", "turn:explicit.example.com:3478")
	t.Setenv("RELAY_URL", "https://relay.yaver.io")
	t.Setenv("YAVER_TURN_PORT", "")
	if got := resolveTurnURL(); got != "turn:explicit.example.com:3478" {
		t.Fatalf("resolveTurnURL() = %q, want explicit override to win", got)
	}
	t.Setenv("YAVER_TURN_URL", "")
	if got := resolveTurnURL(); got != "turn:relay.yaver.io:3478" {
		t.Fatalf("resolveTurnURL() with RELAY_URL only = %q, want derived", got)
	}
}

func TestIceServersForPeer_DerivesTURNFromRelayURL(t *testing.T) {
	// F2 guard: a deploy that sets RELAY_URL but not YAVER_TURN_URL must
	// still get relay-backed TURN — this is the "production was STUN-only"
	// regression. Break resolveTurnURL and this test fails.
	t.Setenv("YAVER_STUN_URL", "")
	t.Setenv("YAVER_TURN_URL", "")
	t.Setenv("YAVER_TURN_PORT", "")
	t.Setenv("RELAY_URL", "https://relay.yaver.io")
	t.Setenv("TURN_AUTH_SECRET", "")
	t.Setenv("RELAY_PASSWORD", "shared-secret-1")

	servers := iceServersForPeer()
	if len(servers) != 2 {
		t.Fatalf("ice server count = %d, want 2 (STUN + derived TURN)", len(servers))
	}
	assertSTUNServer(t, servers[0], "stun:stun.l.google.com:19302")
	turn := servers[1]
	if len(turn.URLs) != 1 || turn.URLs[0] != "turn:relay.yaver.io:3478" {
		t.Fatalf("TURN URLs = %v, want derived relay TURN", turn.URLs)
	}
	if turn.Username == "" || turn.Credential == nil || turn.Credential == "" {
		t.Fatalf("TURN credentials must be non-empty, got %+v", turn)
	}
}

func assertSTUNServer(t *testing.T, server webrtc.ICEServer, wantURL string) {
	t.Helper()
	if len(server.URLs) != 1 || server.URLs[0] != wantURL {
		t.Fatalf("STUN URLs = %v, want [%q]", server.URLs, wantURL)
	}
	if server.Username != "" || server.Credential != nil {
		t.Fatalf("STUN server should have no credentials, got %+v", server)
	}
}

func TestHandleRemoteRuntimeTURNCredentials_StunOnlyByDefault(t *testing.T) {
	// With neither YAVER_TURN_URL nor RELAY_PASSWORD set, the agent
	// should still return STUN so the viewer doesn't crash trying
	// to call new RTCPeerConnection({ iceServers: [] }). This is the
	// "I haven't configured a TURN" path — the most common case.
	t.Setenv("YAVER_TURN_URL", "")
	t.Setenv("TURN_AUTH_SECRET", "")
	t.Setenv("RELAY_PASSWORD", "")
	t.Setenv("RELAY_URL", "")

	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/remote-runtime/turn-credentials", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeTURNCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body turnCredentialResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.IceServers) != 1 {
		t.Fatalf("ice server count = %d, want 1 (STUN-only)", len(body.IceServers))
	}
	stun := body.IceServers[0]
	if len(stun.URLs) == 0 || !strings.HasPrefix(stun.URLs[0], "stun:") {
		t.Errorf("first server should be STUN, got %+v", stun)
	}
	if stun.Username != "" || stun.Credential != "" {
		t.Errorf("STUN server should have no creds, got %+v", stun)
	}
	if body.TTLSeconds <= 0 {
		t.Error("ttlSeconds should be > 0")
	}
}

func TestHandleRemoteRuntimeTURNCredentials_IncludesTURNWhenConfigured(t *testing.T) {
	// When the operator has stood up a TURN server and shared its
	// URL via env, every fetch must return a fresh
	// (username, password) pair derived from the relay password.
	t.Setenv("YAVER_TURN_URL", "turn:relay.example.com:3478")
	t.Setenv("TURN_AUTH_SECRET", "")
	t.Setenv("RELAY_PASSWORD", "shared-secret-1")

	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/remote-runtime/turn-credentials", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeTURNCredentials(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body turnCredentialResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.IceServers) != 2 {
		t.Fatalf("ice server count = %d, want 2 (STUN + TURN)", len(body.IceServers))
	}
	turn := body.IceServers[1]
	if len(turn.URLs) == 0 || !strings.HasPrefix(turn.URLs[0], "turn:") {
		t.Errorf("second server should be TURN, got %+v", turn)
	}
	if turn.Username == "" || turn.Credential == "" {
		t.Errorf("TURN server must carry username + credential, got %+v", turn)
	}
}

func TestHandleRemoteRuntimeTURNCredentials_TURNAuthSecretOverridesRelayPassword(t *testing.T) {
	// Operators who want to rotate TURN creds independently from the
	// relay HTTP password can set TURN_AUTH_SECRET. When both are
	// set, TURN_AUTH_SECRET wins.
	t.Setenv("RELAY_PASSWORD", "shared-relay")
	t.Setenv("TURN_AUTH_SECRET", "different-turn-secret")
	if got := turnAuthSecret(); got != "different-turn-secret" {
		t.Errorf("turnAuthSecret() = %q, want override to win", got)
	}
}

func TestHandleRemoteRuntimeTURNCredentials_RejectsNonGet(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/remote-runtime/turn-credentials", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeTURNCredentials(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestStreamWebRTCICECredentialsRouteRejectsNonOwnerTokens(t *testing.T) {
	t.Setenv("YAVER_TURN_URL", "turn:relay.example.com:3478")
	t.Setenv("TURN_AUTH_SECRET", "turn-secret")
	t.Setenv("RELAY_PASSWORD", "")

	srv := &HTTPServer{
		token:       "owner-token",
		ownerUserID: "owner-user",
	}
	srv.tokenCache.Store("foreign-token", &cachedTokenInfo{userID: "foreign-user"})
	srv.tokenCache.Store("stream-token", &cachedTokenInfo{
		userID: "owner-user",
		isSdk:  true,
		scopes: []string{"stream"},
	})

	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "foreign account", token: "foreign-token"},
		{name: "stream scoped SDK", token: "stream-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/stream/webrtc/ice", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()

			srv.auth(srv.handleRemoteRuntimeTURNCredentials)(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "relay.example.com") {
				t.Fatalf("non-owner response leaked TURN config: %s", rec.Body.String())
			}
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/stream/webrtc/ice", nil)
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()
	srv.auth(srv.handleRemoteRuntimeTURNCredentials)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "relay.example.com") {
		t.Fatalf("owner response should include configured TURN server: %s", rec.Body.String())
	}
}
