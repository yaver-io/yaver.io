package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestCompanionHTTPVerbFires proves the last link in the chain: when a companion
// cron fires, the companion_http ops verb actually performs the HTTP request to
// the (interpolated) endpoint. Combined with
// TestCompanionEngineUpArmsCronAndSkipsProposed (which proves the cron is armed
// as a Verb-mode companion_http schedule carrying the interpolated URL), this
// closes the loop: arm -> fire -> endpoint hit. Uses a local stub, never a real
// Supabase project.
func TestCompanionHTTPVerbFires(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath, gotToken string
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.URL.Query().Get("token")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// This is exactly the OpsPayload the engine builds for a cron.
	payload, _ := json.Marshal(map[string]interface{}{
		"url":    srv.URL + "/rest/autoMailSenderDirect?token=secret-token-123",
		"method": "POST",
	})

	res := companionHTTPHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("companion_http verb failed: code=%s err=%s", res.Code, res.Error)
	}

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("stub received %d requests, want 1", hits)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/rest/autoMailSenderDirect" {
		t.Fatalf("path = %q, want /rest/autoMailSenderDirect", gotPath)
	}
	if gotToken != "secret-token-123" {
		t.Fatalf("token = %q, want secret-token-123", gotToken)
	}
}

// TestCompanionHTTPVerbReportsFailure asserts a non-2xx endpoint surfaces as a
// failed routine run (so the UI/last-outcome shows "failed", not silent ok).
func TestCompanionHTTPVerbReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	payload, _ := json.Marshal(map[string]interface{}{"url": srv.URL + "/x", "method": "POST"})
	res := companionHTTPHandler(OpsContext{}, payload)
	if res.OK {
		t.Fatalf("expected failure on HTTP 500, got OK")
	}
}
