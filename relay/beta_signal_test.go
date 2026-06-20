package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeConvexAuthorize stands in for Convex /gateway/authorize: it authorizes
// EXACTLY the magic beta token and rejects everything else — the relay's cost
// gate must defer to this.
func fakeConvexAuthorize(t *testing.T, magicToken, userID string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/gateway/authorize", func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.Header().Set("Content-Type", "application/json")
		if bearer != magicToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "Unauthorized"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"userId": userID, "allow": true, "balanceCents": 500})
	})
	return httptest.NewServer(mux)
}

func betaTestServer(t *testing.T, convexURL string) (*RelayServer, *httptest.Server) {
	t.Helper()
	rs := NewRelayServer(0, 0, "", convexURL, "")
	mux := http.NewServeMux()
	mux.HandleFunc("/beta/wake", rs.handleBetaWake)
	mux.HandleFunc("/beta/state", rs.handleBetaState)
	return rs, httptest.NewServer(mux)
}

// COST SAFETY: an unauthenticated / garbage-token caller must NEVER move the
// pool out of "down" (no provision → no spend).
func TestBetaWake_AttackerCannotWake(t *testing.T) {
	convex := fakeConvexAuthorize(t, "ygw_valid", "user_beta_1")
	defer convex.Close()
	rs, srv := betaTestServer(t, convex.URL)
	defer srv.Close()

	for _, tok := range []string{"", "garbage", "ygw_wrong"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/beta/wake", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("wake req: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: expected 401, got %d", tok, resp.StatusCode)
		}
	}
	if got := rs.betaPool.phase; got != "down" {
		t.Fatalf("attacker moved pool phase to %q — should stay down", got)
	}
	if rs.betaPool.wakeCount != 0 {
		t.Fatalf("attacker incremented wakeCount=%d — should be 0", rs.betaPool.wakeCount)
	}
}

// A verified beta user flips the pool to "waking" exactly once; a rapid second
// wake is cooled down (still one box).
func TestBetaWake_BetaUserWakesAndCooldown(t *testing.T) {
	convex := fakeConvexAuthorize(t, "ygw_valid", "user_beta_1")
	defer convex.Close()
	rs, srv := betaTestServer(t, convex.URL)
	defer srv.Close()

	wake := func() int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/beta/wake", nil)
		req.Header.Set("Authorization", "Bearer ygw_valid")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("wake: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := wake(); code != http.StatusOK {
		t.Fatalf("first wake: expected 200, got %d", code)
	}
	if rs.betaPool.phase != "waking" {
		t.Fatalf("after wake phase=%q, want waking", rs.betaPool.phase)
	}
	if code := wake(); code != http.StatusTooManyRequests {
		t.Fatalf("rapid second wake: expected 429 cooldown, got %d", code)
	}
	if rs.betaPool.wakeCount != 1 {
		t.Fatalf("wakeCount=%d, want 1 (cooldown should block the 2nd)", rs.betaPool.wakeCount)
	}
}

// The controller (admin) can flip the phase to up/down; GET reflects it.
func TestBetaState_ControllerUpdates(t *testing.T) {
	convex := fakeConvexAuthorize(t, "ygw_valid", "u1")
	defer convex.Close()
	rs, srv := betaTestServer(t, convex.URL)
	defer srv.Close()
	rs.adminToken = "admintok"

	body := strings.NewReader(`{"phase":"up","boxAddr":"tenant-box","activity":true}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/beta/state", body)
	req.Header.Set("Authorization", "Bearer admintok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("state post: %v", err)
	}
	resp.Body.Close()
	if rs.betaPool.phase != "up" || rs.betaPool.boxAddr != "tenant-box" {
		t.Fatalf("controller update failed: phase=%q boxAddr=%q", rs.betaPool.phase, rs.betaPool.boxAddr)
	}

	// Non-admin POST must be rejected.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/beta/state", strings.NewReader(`{"phase":"down"}`))
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("non-admin moved phase to down — must be rejected")
	}
	if rs.betaPool.phase != "up" {
		t.Fatalf("non-admin changed phase to %q", rs.betaPool.phase)
	}
}
