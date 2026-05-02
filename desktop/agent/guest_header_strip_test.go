package main

// guest_header_strip_test.go — H-13 / M-1 regression: a guest cannot
// elevate their scope by pre-stamping X-Yaver-Guest* headers on their
// inbound request. allowGuest and applyDelegatedGuestSDKHeaders must
// strip every guest-shaped header before re-stamping from the
// server-resolved guest config.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStripGuestRequestHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/info", nil)
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "spoofed-uid")
	req.Header.Set("X-Yaver-GuestScope", "full")
	req.Header.Set("X-Yaver-GuestAllowedProjects", "every-project,*")
	req.Header.Set("X-Yaver-GuestAllowedRunners", "claude-code,opus-4-7")
	req.Header.Set("X-Yaver-Support", "true")
	req.Header.Set("X-Yaver-Proxied-By", "primary")

	stripGuestRequestHeaders(req)

	for _, h := range []string{
		"X-Yaver-Guest",
		"X-Yaver-GuestUserID",
		"X-Yaver-GuestScope",
		"X-Yaver-GuestAllowedProjects",
		"X-Yaver-GuestAllowedRunners",
		"X-Yaver-Support",
		"X-Yaver-Proxied-By",
	} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("header %q must be stripped, still present: %q", h, got)
		}
	}
}

func TestApplyDelegatedSDKHeadersOverridesInbound(t *testing.T) {
	// Even if the SDK token's caller pre-stamps a broader scope,
	// applyDelegatedGuestSDKHeaders must overwrite from the
	// cached token info — not honor inbound values.
	srv := &HTTPServer{deviceID: "dev-A"}
	req := httptest.NewRequest("GET", "/info", nil)
	req.Header.Set("X-Yaver-GuestScope", "full")                   // attacker spoofed
	req.Header.Set("X-Yaver-GuestAllowedProjects", "every-thing") // attacker spoofed

	rec := httptest.NewRecorder()
	info := &cachedTokenInfo{
		delegatedGuestUserID: "user-X",
		delegatedGuestScope:  GuestScopeFeedbackOnly,
		allowedProjects:      []string{"only-this-app"},
		targetDeviceID:       "dev-A",
	}
	if !srv.applyDelegatedGuestSDKHeaders(rec, req, info) {
		t.Fatalf("expected apply to succeed, got rejection")
	}
	if got := req.Header.Get("X-Yaver-GuestScope"); got != GuestScopeFeedbackOnly {
		t.Errorf("scope spoofed: got %q, want %q", got, GuestScopeFeedbackOnly)
	}
	if got := req.Header.Get("X-Yaver-GuestAllowedProjects"); got != "only-this-app" {
		t.Errorf("allowedProjects spoofed: got %q, want %q", got, "only-this-app")
	}
	if got := req.Header.Get("X-Yaver-GuestUserID"); got != "user-X" {
		t.Errorf("guest userID wrong: got %q", got)
	}
	if !strings.EqualFold(req.Header.Get("X-Yaver-Guest"), "true") {
		t.Error("X-Yaver-Guest must be re-stamped to true")
	}
}

func TestApplyDelegatedSDKHeadersDefaultsToFeedbackOnly(t *testing.T) {
	// When the SDK token doesn't carry an explicit scope, default to
	// feedback-only (the safer choice). Pre-fix this collapsed to "full"
	// via guestScopeOrDefault.
	srv := &HTTPServer{deviceID: "dev-A"}
	req := httptest.NewRequest("GET", "/info", nil)
	rec := httptest.NewRecorder()
	info := &cachedTokenInfo{
		delegatedGuestUserID: "user-X",
		delegatedGuestScope:  "", // empty
		targetDeviceID:       "dev-A",
	}
	if !srv.applyDelegatedGuestSDKHeaders(rec, req, info) {
		t.Fatalf("expected apply to succeed, got rejection")
	}
	if got := req.Header.Get("X-Yaver-GuestScope"); got != GuestScopeFeedbackOnly {
		t.Errorf("default scope: got %q, want %q", got, GuestScopeFeedbackOnly)
	}
}
