package main

// auth_register_retry_test.go — verifies RegisterDevice self-heals a
// transient Convex 5xx by retrying, but fails fast on a 4xx.
//
// Regression target: a freshness test (fresh account, 2nd device) caught a
// transient `registerDevice` 500 right after login. Because RegisterDevice
// gave up after one attempt, the agent stayed permanently half-registered —
// relay-connected but with no Convex device row, so peers couldn't see it and
// every heartbeat 500'd with "Device not found" until a manual restart. A
// bounded retry-on-5xx makes registration ride out the blip.
//
// Real httptest server, loopback only — matches the repo's no-mocks test
// convention.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestRegisterDeviceRetriesTransient5xx: the server 500s twice then 200s.
// RegisterDevice must keep trying and ultimately succeed, returning the
// rotated token from the successful attempt.
func TestRegisterDeviceRetriesTransient5xx(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/devices/register" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		n := atomic.AddInt64(&hits, 1)
		if n < 3 {
			// Generic Convex "Server Error" 500, exactly like the field repro.
			http.Error(w, `{"code":"[Request ID: x] Server Error"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"token": "rotated-tok"})
	}))
	defer srv.Close()

	tok, err := RegisterDevice(srv.URL, RegisterDeviceRequest{
		Token:    "sess-tok",
		DeviceID: "dev-1",
		Name:     "freshtest",
		Platform: "linux",
		QuicHost: "",
		QuicPort: 18080,
	})
	if err != nil {
		t.Fatalf("expected success after transient 5xx, got error: %v", err)
	}
	if tok != "rotated-tok" {
		t.Fatalf("expected rotated token from the successful attempt, got %q", tok)
	}
	if got := atomic.LoadInt64(&hits); got != 3 {
		t.Fatalf("expected 3 attempts (2 failed + 1 success), got %d", got)
	}
}

// TestRegisterDeviceFailsFastOn4xx: a 4xx (e.g. device owned by another user,
// or an expired token) is a client error — RegisterDevice must NOT retry it,
// so the caller's conflict/auth handling runs immediately.
func TestRegisterDeviceFailsFastOn4xx(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "Device belongs to another user", http.StatusConflict)
	}))
	defer srv.Close()

	_, err := RegisterDevice(srv.URL, RegisterDeviceRequest{
		Token:    "sess-tok",
		DeviceID: "dev-1",
		Name:     "freshtest",
		Platform: "linux",
		QuicPort: 18080,
	})
	if err == nil {
		t.Fatal("expected a 4xx to surface as an error")
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("expected exactly 1 attempt (no retry on 4xx), got %d", got)
	}
}

// TestRegisterDeviceGivesUpAfterMaxAttempts: a server that 500s forever must
// exhaust exactly registerDeviceMaxAttempts and then return the last error,
// rather than spinning indefinitely.
func TestRegisterDeviceGivesUpAfterMaxAttempts(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := RegisterDevice(srv.URL, RegisterDeviceRequest{
		Token: "sess-tok", DeviceID: "dev-1", Name: "x", Platform: "linux", QuicPort: 18080,
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := atomic.LoadInt64(&hits); got != int64(registerDeviceMaxAttempts) {
		t.Fatalf("expected exactly %d attempts, got %d", registerDeviceMaxAttempts, got)
	}
}
