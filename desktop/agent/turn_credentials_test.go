package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
