package main

import (
	"testing"
	"time"
)

// M-9 (audit 2026-05-02): rotating the relay password must invalidate
// the cached "yes this password is OK" entries (validatedPw + pwUserIDs)
// AND force-disconnect every existing tunnel. Without this, an attacker
// who knew the OLD password keeps validated-cache hits for up to 5
// minutes, and any agent already registered keeps its tunnel forever.
func TestSetPassword_ClearsCachesOnRotation(t *testing.T) {
	rs := NewRelayServer(0, 0, "old-pw", "https://convex.example.test", "")

	// Pre-populate the caches as if Convex had recently approved
	// some per-user passwords.
	rs.validatedPwMu.Lock()
	rs.validatedPw["per-user-token-A"] = time.Now().Add(5 * time.Minute)
	rs.validatedPw["per-user-token-B"] = time.Now().Add(5 * time.Minute)
	rs.validatedPwMu.Unlock()

	rs.pwUserIDMu.Lock()
	rs.pwUserIDs["per-user-token-A"] = "user-A"
	rs.pwUserIDExp["per-user-token-A"] = time.Now().Add(5 * time.Minute)
	rs.pwUserIDs["per-user-token-B"] = "user-B"
	rs.pwUserIDExp["per-user-token-B"] = time.Now().Add(5 * time.Minute)
	rs.pwUserIDMu.Unlock()

	// Sanity: caches populated.
	rs.validatedPwMu.RLock()
	if len(rs.validatedPw) != 2 {
		rs.validatedPwMu.RUnlock()
		t.Fatalf("expected 2 cached validated entries before rotation, got %d", len(rs.validatedPw))
	}
	rs.validatedPwMu.RUnlock()

	// Rotate the password.
	rs.setPassword("new-pw")

	// Both caches should be empty.
	rs.validatedPwMu.RLock()
	if got := len(rs.validatedPw); got != 0 {
		rs.validatedPwMu.RUnlock()
		t.Fatalf("expected validatedPw cache cleared after rotation, got %d entries", got)
	}
	rs.validatedPwMu.RUnlock()

	rs.pwUserIDMu.RLock()
	if got := len(rs.pwUserIDs); got != 0 {
		rs.pwUserIDMu.RUnlock()
		t.Fatalf("expected pwUserIDs cache cleared after rotation, got %d entries", got)
	}
	if got := len(rs.pwUserIDExp); got != 0 {
		rs.pwUserIDMu.RUnlock()
		t.Fatalf("expected pwUserIDExp cache cleared after rotation, got %d entries", got)
	}
	rs.pwUserIDMu.RUnlock()

	// And the password itself should be the new one.
	if got := rs.getPassword(); got != "new-pw" {
		t.Fatalf("expected password=new-pw after rotation, got %q", got)
	}
}

// M-9: existing tunnels must be torn down on rotation.
//
// We use the same QUIC harness as the C-1 collision test — register a
// real tunnel, rotate the password, and confirm the tunnel's
// connection context fires Done().
func TestSetPassword_ForcesTunnelDisconnectOnRotation(t *testing.T) {
	srv, addr, cleanup := startTestRelayQUIC(t, "old-pw")
	defer cleanup()

	conn, resp, err := dialAndRegister(t, addr, "device-rotate-1", "tok", "old-pw")
	if err != nil || !resp.OK {
		t.Fatalf("register: err=%v resp=%+v", err, resp)
	}
	defer conn.CloseWithError(0, "test cleanup")

	// Tunnel is in the map.
	srv.mu.RLock()
	_, ok := srv.tunnels["device-rotate-1"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatalf("expected tunnel to be registered before rotation")
	}

	// Rotate.
	srv.setPassword("new-pw")

	// The agent's connection context should fire Done() once the
	// CloseWithError propagates. Wait up to 2 seconds.
	select {
	case <-conn.Context().Done():
		// Expected — tunnel was torn down.
	case <-time.After(2 * time.Second):
		t.Fatalf("agent connection still alive 2s after password rotation; expected force-disconnect")
	}
}
