package main

// remote_status_auth_test.go — locks in the multi-layer auth summary
// emitted by `yaver primary status` / `yaver runner <hint> status`.
// Tests the rendering branch via authStateStringForRunner without
// going through the HTTP fetch path.

import (
	"testing"
)

func TestAuthStateStringForRunnerNotConfigured(t *testing.T) {
	r := &remoteRunnerSummary{ID: "claude-code", Installed: true, AuthConfigured: false}
	got := authStateStringForRunner(r, &remoteAgentStatusReport{})
	if got != "✗ not configured (run: yaver primary auth claude)" {
		t.Errorf("got %q", got)
	}
}

func TestAuthStateStringForRunnerActive(t *testing.T) {
	r := &remoteRunnerSummary{ID: "claude-code", Installed: true, AuthConfigured: true, AuthSource: "keychain"}
	got := authStateStringForRunner(r, &remoteAgentStatusReport{})
	if got != "✓ active (keychain)" {
		t.Errorf("got %q", got)
	}
}

func TestAuthStateStringForRunnerNotInstalled(t *testing.T) {
	r := &remoteRunnerSummary{ID: "codex", Installed: false}
	got := authStateStringForRunner(r, &remoteAgentStatusReport{})
	if got != "✗ not installed" {
		t.Errorf("got %q", got)
	}
}

func TestAuthStateStringForRunnerForbidden(t *testing.T) {
	// /agent/runners returned 403 because the remote box's yaver auth
	// is expired — the runner table is therefore empty. The summary
	// still emits an actionable hint.
	got := authStateStringForRunner(nil, &remoteAgentStatusReport{HTTPStatusRunner: 403})
	if got != "(unknown — fix yaver auth first)" {
		t.Errorf("got %q", got)
	}
	got = authStateStringForRunner(nil, &remoteAgentStatusReport{HTTPStatusRunner: 401})
	if got != "(unknown — fix yaver auth first)" {
		t.Errorf("got %q", got)
	}
}

func TestAuthStateStringForRunnerUnreachable(t *testing.T) {
	// HTTPStatusRunner == 0 means the request never reached the box
	// (DNS / TCP / TLS failure). Different signal than 401/403.
	got := authStateStringForRunner(nil, &remoteAgentStatusReport{HTTPStatusRunner: 0})
	if got != "(unknown — runners endpoint unreachable)" {
		t.Errorf("got %q", got)
	}
}
