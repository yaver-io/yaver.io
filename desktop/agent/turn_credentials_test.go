package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestIceServersForPeer_STUNOnlyByDefault(t *testing.T) {
	t.Setenv("YAVER_STUN_URL", "")
	t.Setenv("YAVER_TURN_URL", "")
	t.Setenv("TURN_AUTH_SECRET", "")
	t.Setenv("RELAY_PASSWORD", "")

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

	servers := iceServersForPeer()
	if len(servers) != 1 {
		t.Fatalf("ice server count = %d, want 1 (STUN-only)", len(servers))
	}
	assertSTUNServer(t, servers[0], "stun:stun.l.google.com:19302")
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
		guestUserIDs: []string{
			"guest-user",
		},
	}
	srv.tokenCache.Store("guest-token", &cachedTokenInfo{userID: "guest-user"})
	srv.tokenCache.Store("stream-token", &cachedTokenInfo{
		userID: "owner-user",
		isSdk:  true,
		scopes: []string{"stream"},
	})

	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "approved guest", token: "guest-token"},
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
