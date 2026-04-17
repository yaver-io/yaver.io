package main

// auth_convex_path_test.go — exercises the *slow path* in
// HTTPServer.auth: a request presents a token that isn't the agent's
// own, isn't a paired token, isn't a support bearer, and isn't in
// the cache. The middleware calls ValidateTokenUser against Convex.
//
// The other integration tests only cover the fast path (exact match
// with s.token). Without this test we'd never catch a regression in
// the Convex round-trip or the cache-hydrate logic.
//
// Uses an httptest.Server as a stand-in Convex — deterministic, no
// real credentials, no network outside loopback.

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

// mockConvex returns an httptest.Server that behaves just enough like
// Convex for the agent's auth middleware:
//   - POST /auth/validate → returns {userId: <mapped>} for known
//     tokens, 401 for unknowns.
//   - Counts how many times /auth/validate was hit so we can assert
//     the agent is actually caching on repeat calls.
type mockConvex struct {
	srv        *httptest.Server
	tokens     map[string]string // token → userId
	validateN  int64
}

func newMockConvex(tokens map[string]string) *mockConvex {
	mc := &mockConvex{tokens: tokens}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/validate", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&mc.validateN, 1)
		authHeader := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(authHeader) <= len(prefix) || authHeader[:len(prefix)] != prefix {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		tok := authHeader[len(prefix):]
		uid, ok := mc.tokens[tok]
		if !ok {
			http.Error(w, "unknown token", http.StatusUnauthorized)
			return
		}
		// Matches the shape Convex returns and the agent's
		// ValidateTokenInfo expects.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user": map[string]string{"userId": uid, "email": uid + "@test"},
		})
	})
	// Anything else the agent might call during startup (config,
	// device registration, settings) — return harmless empty JSON
	// so the agent doesn't explode.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{})
	})
	mc.srv = httptest.NewServer(mux)
	return mc
}

func (m *mockConvex) validateCalls() int64 {
	return atomic.LoadInt64(&m.validateN)
}

func (m *mockConvex) close() { m.srv.Close() }

// We can't use startTestServer here because that hardcodes an empty
// convexURL. Build a server directly so we can point it at the mock.
func startAgentAgainstMockConvex(t *testing.T, convexURL, ownerToken, ownerUserID string) (string, context.CancelFunc) {
	t.Helper()
	port := getFreePort(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	srv := NewHTTPServer(port, ownerToken, ownerUserID, "test-device", convexURL, "test-host", tm)
	srv.execMgr = NewExecManager(tm.workDir, nil)
	srv.agentGraphMgr = NewAgentGraphManager(tm)
	currentTestHTTPServer = srv

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Start(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return baseURL, cancel
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	cancel()
	t.Fatal("agent didn't come up")
	return "", nil
}

func TestAgentAuthConvexValidationPath(t *testing.T) {
	const (
		ownerUserID   = "user-owner"
		guestUserID   = "user-guest"
		otherUserID   = "user-other"
		ownerSession  = "owner-session-12345"
		otherSession  = "other-user-session-98765"
	)

	mc := newMockConvex(map[string]string{
		// A session token that resolves to the OWNER's userId —
		// should open owner-only endpoints.
		ownerSession: ownerUserID,
		// A session token that resolves to a *different* userId —
		// auth() must reject because they're not the owner and not
		// an approved guest.
		otherSession: otherUserID,
	})
	defer mc.close()

	// Agent's own token is "agent-own-token"; owner userId matches
	// ownerUserID so sessions that resolve to ownerUserID are
	// accepted on second hop.
	baseURL, cancel := startAgentAgainstMockConvex(t, mc.srv.URL, "agent-own-token", ownerUserID)
	defer cancel()

	// 1. Agent's own token — fast path. Convex should NOT be called.
	before := mc.validateCalls()
	status, _ := doRequest(t, "GET", baseURL+"/info", "agent-own-token", "")
	if status != 200 {
		t.Fatalf("fast path /info with agent token: got %d", status)
	}
	if mc.validateCalls() != before {
		t.Errorf("agent's own token should NOT hit Convex (call count changed %d → %d)", before, mc.validateCalls())
	}

	// 2. Owner-equivalent session token — SLOW path. Validates via
	//    Convex, resolves to ownerUserID, allowed through.
	before = mc.validateCalls()
	status, _ = doRequest(t, "GET", baseURL+"/info", ownerSession, "")
	if status != 200 {
		t.Fatalf("slow path /info with owner session: got %d", status)
	}
	if mc.validateCalls() != before+1 {
		t.Errorf("owner session should have triggered one Convex call, got %d", mc.validateCalls()-before)
	}

	// 3. Same session — cache hit. Convex NOT called again.
	before = mc.validateCalls()
	status, _ = doRequest(t, "GET", baseURL+"/info", ownerSession, "")
	if status != 200 {
		t.Fatalf("cached owner session: got %d", status)
	}
	if mc.validateCalls() != before {
		t.Errorf("second hit should come from cache, Convex calls went %d → %d", before, mc.validateCalls())
	}

	// 4. Other-user session — validated, but rejected because it's
	//    not the owner's userId and not on the guest allowlist.
	status, _ = doRequest(t, "GET", baseURL+"/info", otherSession, "")
	if status != 403 {
		t.Errorf("other-user session: expected 403, got %d", status)
	}

	// 5. Bogus token — Convex says 401, agent returns 403.
	status, _ = doRequest(t, "GET", baseURL+"/info", "never-issued-token", "")
	if status != 403 {
		t.Errorf("bogus token: expected 403, got %d", status)
	}

	// 6. Missing Authorization header — agent never even calls Convex.
	before = mc.validateCalls()
	status, _ = doRequest(t, "GET", baseURL+"/info", "", "")
	if status != 401 {
		t.Errorf("missing auth header: expected 401, got %d", status)
	}
	if mc.validateCalls() != before {
		t.Errorf("missing-header request should short-circuit without calling Convex")
	}
}
