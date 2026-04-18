package main

// mcp_auth_tools_test.go — unit tests for the headless auth MCP helpers
// that can run without touching Convex. The polling path (authPollDeviceCode
// / authWaitDeviceCode) hits a real Convex endpoint, so we only smoke-test
// argument validation here; a full integration test would need a staged
// backend and is out of scope.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAuthStatusSnapshot_Structure(t *testing.T) {
	t.Parallel()
	// Even without a config file the call must return a populated
	// snapshot (non-fatal fields) that tells the caller to sign in.
	snap := authStatusSnapshot()
	if snap.ConvexURL == "" {
		t.Fatalf("ConvexURL should fall back to the hosted default, got empty")
	}
	// The snapshot's Message field is the primary human-readable hint;
	// make sure something useful is populated in every branch.
	if strings.TrimSpace(snap.Message) == "" {
		t.Fatalf("Message must be populated in every branch, got empty")
	}
	// signed_in and needs_auth must not both be true — they are mutually
	// exclusive by definition.
	if snap.SignedIn && snap.NeedsAuth {
		t.Fatalf("SignedIn and NeedsAuth must not both be true: snap=%+v", snap)
	}
}

func TestAuthPollDeviceCode_EmptyDeviceCode(t *testing.T) {
	t.Parallel()
	_, err := authPollDeviceCode(context.Background(), "", "")
	if err == nil || !strings.Contains(err.Error(), "device_code is required") {
		t.Fatalf("expected 'device_code is required' error, got %v", err)
	}
}

func TestAuthWaitDeviceCode_RespectsContextCancellation(t *testing.T) {
	t.Parallel()
	// If the caller's context is already canceled, the wait must return
	// promptly with ctx.Err rather than blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := authWaitDeviceCode(ctx, "", "fake-device-code", 60, 3)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAuthLogout_NoConfigIsIdempotent(t *testing.T) {
	t.Parallel()
	// When there's no token to clear, logout is a no-op that reports
	// "already logged out" without error.
	res, err := authLogout()
	if err != nil {
		t.Fatalf("authLogout must not error on empty-config input, got %v", err)
	}
	_ = res // LoggedOut may be true or false depending on current real config
}
