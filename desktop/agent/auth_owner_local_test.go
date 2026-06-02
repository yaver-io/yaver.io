package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// writeOwnerConfig drops a config.json with the given auth token under a temp
// HOME so LoadConfig() (which resolves ~/.yaver/config.json via os.UserHomeDir)
// returns it. The t.Setenv keeps HOME pointed at the temp dir for the test.
func writeOwnerConfig(t *testing.T, authToken string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	data, _ := json.Marshal(&Config{AuthToken: authToken})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// countingConvex returns an httptest server that answers every token-validation
// request with the given status, and a pointer to a hit counter. We use it to
// assert that owner-token requests NEVER touch Convex, and that a reachable
// Convex still rejects a genuinely-unknown token.
func countingConvex(t *testing.T, status int) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// The owner's on-disk token must be recognized with NO Convex round-trip, even
// when the daemon's in-memory s.token has drifted (the cold-restart / upgrade /
// rotation window). This is the case that was wrongly returning 403
// "invalid token" and blocking local builds — online OR offline.
func TestAuthSDKOwnerOnDiskTokenSkipsConvex(t *testing.T) {
	const diskToken = "owner-disk-token"
	writeOwnerConfig(t, diskToken)
	// Convex would 401 if ever dialed — proves we don't dial it.
	backend, hits := countingConvex(t, http.StatusUnauthorized)

	srv := &HTTPServer{
		token:       "stale-in-memory-token", // intentionally != diskToken
		ownerUserID: "owner-user",
		convexURL:   backend.URL,
	}

	called := false
	h := srv.authSDK(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/builds", nil)
	req.Header.Set("Authorization", "Bearer "+diskToken)
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("owner on-disk token should be accepted, got called=%v code=%d body=%q", called, rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(hits); n != 0 {
		t.Fatalf("owner token must NOT hit Convex, but backend was called %d time(s)", n)
	}
}

// Same guarantee for the broader auth() middleware (covers /exec/* used by
// `yaver build status` / log streaming).
func TestAuthOwnerOnDiskTokenSkipsConvex(t *testing.T) {
	const diskToken = "owner-disk-token"
	writeOwnerConfig(t, diskToken)
	backend, hits := countingConvex(t, http.StatusUnauthorized)

	srv := &HTTPServer{
		token:       "stale-in-memory-token",
		ownerUserID: "owner-user",
		convexURL:   backend.URL,
	}

	called := false
	h := srv.auth(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/exec/abc", nil)
	req.Header.Set("Authorization", "Bearer "+diskToken)
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("owner on-disk token should be accepted by auth(), got called=%v code=%d body=%q", called, rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(hits); n != 0 {
		t.Fatalf("owner token must NOT hit Convex via auth(), but backend was called %d time(s)", n)
	}
}

// The in-memory fast path still works and likewise never dials Convex.
func TestAuthSDKOwnerInMemoryFastPathSkipsConvex(t *testing.T) {
	backend, hits := countingConvex(t, http.StatusUnauthorized)
	srv := &HTTPServer{
		token:       "owner-live-token",
		ownerUserID: "owner-user",
		convexURL:   backend.URL,
	}

	called := false
	h := srv.authSDK(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/builds", nil)
	req.Header.Set("Authorization", "Bearer owner-live-token")
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("owner live token should fast-path, got called=%v code=%d", called, rec.Code)
	}
	if n := atomic.LoadInt64(hits); n != 0 {
		t.Fatalf("in-memory owner token must NOT hit Convex, backend called %d time(s)", n)
	}
}

// A genuinely-unknown token is NOT the owner's, so it must still be validated
// against Convex and rejected. The owner shortcut must not become a blanket
// bypass.
func TestAuthSDKUnknownTokenStillRejected(t *testing.T) {
	writeOwnerConfig(t, "owner-disk-token")
	backend, hits := countingConvex(t, http.StatusUnauthorized) // Convex rejects

	srv := &HTTPServer{
		token:       "owner-disk-token",
		ownerUserID: "owner-user",
		convexURL:   backend.URL,
	}

	h := srv.authSDK(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("an unknown (non-owner) token must not reach the handler")
	})

	req := httptest.NewRequest(http.MethodPost, "/builds", nil)
	req.Header.Set("Authorization", "Bearer some-other-users-token")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unknown token, got %d (%q)", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt64(hits); n == 0 {
		t.Fatalf("unknown token should have been validated against Convex (got 0 backend hits)")
	}
}

// An unauthenticated daemon (empty token, no config) must grant nothing — the
// empty-string guard in secretEqual prevents an empty token from matching.
func TestAuthSDKEmptyOwnerTokenGrantsNothing(t *testing.T) {
	writeOwnerConfig(t, "") // empty on-disk token
	backend, _ := countingConvex(t, http.StatusUnauthorized)

	srv := &HTTPServer{
		token:       "", // empty in-memory token
		ownerUserID: "owner-user",
		convexURL:   backend.URL,
	}

	h := srv.authSDK(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("empty owner token must not authorize anything")
	})

	req := httptest.NewRequest(http.MethodPost, "/builds", nil)
	req.Header.Set("Authorization", "Bearer ") // empty bearer
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("empty token must not be accepted, got %d", rec.Code)
	}
}
