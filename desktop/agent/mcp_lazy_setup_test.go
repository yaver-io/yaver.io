package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestYaverLazySetup_StartsFromCold asserts that with no config on
// disk, lazy_setup requests a fresh device code, persists it, and
// hands the AI a URL + next_action containing the URL.
func TestYaverLazySetup_StartsFromCold(t *testing.T) {
	withTempHome(t)

	calls := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device-code" && r.Method == http.MethodPost {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(deviceCodeResponse{
				UserCode:   "LAZY-4242",
				DeviceCode: "dc-lazy-cold",
				ExpiresAt:  time.Now().Add(15 * time.Minute).UnixMilli(),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// Point the tool at our stub Convex by priming config.
	cfg := &Config{ConvexSiteURL: srv.URL}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	out, err := yaverLazySetup(context.Background(), 0)
	if err != nil {
		t.Fatalf("yaverLazySetup: %v", err)
	}
	if out.Status != "waiting_sign_in" {
		t.Fatalf("Status = %q, want waiting_sign_in — detail=%q", out.Status, out.Detail)
	}
	if out.UserCode != "LAZY-4242" {
		t.Errorf("UserCode = %q, want LAZY-4242", out.UserCode)
	}
	if !strings.Contains(out.SignInURL, "LAZY-4242") {
		t.Errorf("SignInURL = %q, want to contain user code", out.SignInURL)
	}
	if !strings.Contains(out.NextAction, out.SignInURL) {
		t.Errorf("NextAction should quote the URL for the AI to speak verbatim; got %q", out.NextAction)
	}
	if out.MobileApp.IOS == "" || out.MobileApp.Android == "" {
		t.Errorf("MobileApp links must always populate: %+v", out.MobileApp)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("backend device-code hits = %d, want 1", n)
	}
}

// TestYaverLazySetup_ResumesPending asserts the idempotency contract:
// calling the tool a second time with a still-valid pending-auth file
// must return the SAME URL without hitting the backend again.
func TestYaverLazySetup_ResumesPending(t *testing.T) {
	withTempHome(t)

	// Plant a pending-auth record the tool should reuse.
	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "preserved-dc",
		UserCode:   "RESU-9090",
		URL:        "https://yaver.io/auth/device?code=RESU-9090",
		ConvexURL:  "https://must-not-be-called.invalid",
		ExpiresAt:  time.Now().Add(10 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().Add(-2 * time.Minute).UnixMilli(),
	})

	// Backend MUST NOT be hit on the resume path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected backend call on resume: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	cfg := &Config{ConvexSiteURL: srv.URL}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	out, err := yaverLazySetup(context.Background(), 0)
	if err != nil {
		t.Fatalf("yaverLazySetup: %v", err)
	}
	if out.Status != "waiting_sign_in" {
		t.Fatalf("Status = %q, want waiting_sign_in", out.Status)
	}
	if out.UserCode != "RESU-9090" || out.DeviceCode != "preserved-dc" {
		t.Errorf("resume returned different code: %+v", out)
	}
}

// TestYaverLazySetup_ReportsSignedInWithoutTouchingBackend asserts
// that once a user is signed in, lazy_setup is a no-op wrapper that
// reports the "signed_in" state without any device-code flow.
func TestYaverLazySetup_ReportsSignedInWithoutTouchingBackend(t *testing.T) {
	withTempHome(t)

	// Stub Convex that accepts any Bearer and returns a valid user
	// profile — that's all authStatusSnapshot needs to decide "signed in".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/validate":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"user": map[string]string{
					"userId":   "user_test",
					"email":    "cousin@example.com",
					"fullName": "Test Cousin",
					"provider": "apple",
				},
			})
		case "/auth/device-code":
			t.Fatalf("device-code should not be requested when already signed in")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := &Config{
		ConvexSiteURL: srv.URL,
		AuthToken:     "tok_signed_in",
		DeviceID:      "device-already-paired",
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	out, err := yaverLazySetup(context.Background(), 0)
	if err != nil {
		t.Fatalf("yaverLazySetup: %v", err)
	}
	if out.Status != "signed_in" {
		t.Fatalf("Status = %q, want signed_in — detail=%q", out.Status, out.Detail)
	}
	if out.UserEmail != "cousin@example.com" {
		t.Errorf("UserEmail = %q, want cousin@example.com", out.UserEmail)
	}
	if !strings.Contains(out.NextAction, "mobile") {
		t.Errorf("NextAction should mention the mobile app step; got %q", out.NextAction)
	}
}
