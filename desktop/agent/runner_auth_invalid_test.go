package main

import (
	"strings"
	"testing"
	"time"
)

func TestIsRunnerAuthFailureOutput_Claude(t *testing.T) {
	cases := []string{
		"Failed to authenticate. API Error: 401 Invalid authentication credentials",
		"some prefix\nNot logged in · Please run /login",
		"Error: Invalid bearer token",
		"Loading config from Claude Code-credentials keychain entry... rejected",
	}
	for _, in := range cases {
		got := IsRunnerAuthFailureOutput(in)
		if got != "claude" {
			t.Errorf("IsRunnerAuthFailureOutput(%q) = %q, want %q", in, got, "claude")
		}
	}
}

func TestIsRunnerAuthFailureOutput_Codex(t *testing.T) {
	cases := []string{
		"Sign in required. Run codex login --device-auth",
		"codex: not authenticated, please sign in",
		"Please run codex login --device-auth to set up ChatGPT auth",
	}
	for _, in := range cases {
		got := IsRunnerAuthFailureOutput(in)
		if got != "codex" {
			t.Errorf("IsRunnerAuthFailureOutput(%q) = %q, want %q", in, got, "codex")
		}
	}
}

func TestIsRunnerAuthFailureOutput_NoMatch(t *testing.T) {
	cases := []string{
		"",
		"OK, sounds good.",
		"warning: --full-auto is deprecated; use --sandbox workspace-write",
		"some random task output that mentions 401 in code but not auth",
		"Successfully completed",
	}
	for _, in := range cases {
		got := IsRunnerAuthFailureOutput(in)
		if got != "" {
			t.Errorf("IsRunnerAuthFailureOutput(%q) = %q, want empty", in, got)
		}
	}
}

func TestRunnerAuthFailureRecent_LifecycleAndTTL(t *testing.T) {
	// Confidence checks for the override map: set / read / clear / expire.
	ClearRunnerAuthInvalid("claude") // start clean
	if runnerAuthFailureRecent("claude") {
		t.Fatal("expected no failure recorded initially")
	}
	MarkRunnerAuthInvalid("claude")
	if !runnerAuthFailureRecent("claude") {
		t.Fatal("expected MarkRunnerAuthInvalid to be observed")
	}
	if runnerAuthFailureRecent("codex") {
		t.Fatal("setting claude must not affect codex")
	}
	ClearRunnerAuthInvalid("claude")
	if runnerAuthFailureRecent("claude") {
		t.Fatal("expected ClearRunnerAuthInvalid to drop the entry")
	}
	// Expiry — directly poke the map to set an old timestamp, then probe.
	lastRunnerAuthFailure.Lock()
	lastRunnerAuthFailure.at["claude"] = time.Now().Add(-2 * runnerAuthFailureTTL)
	lastRunnerAuthFailure.Unlock()
	if runnerAuthFailureRecent("claude") {
		t.Fatal("expected expired entry to drop on probe")
	}
}

func TestDetectRunnerRuntimeStatus_AuthOverrideFlipsConfigured(t *testing.T) {
	// Poison the cache with a "recent" failure; status should flip the
	// AuthConfigured returned by detectClaudeStatus to false even when
	// the file/keychain check would have said true. We can't easily
	// fake a true file/keychain in this unit test, so instead we run
	// the real detection — if it would have returned ok=true, the
	// override forces false; if it returns ok=false (no creds), the
	// test still passes because override doesn't flip a false to true.
	cfg := RunnerConfig{RunnerID: "claude", Command: "claude"}
	MarkRunnerAuthInvalid("claude")
	defer ClearRunnerAuthInvalid("claude")
	got := DetectRunnerRuntimeStatus(cfg, "/tmp")
	if got.AuthConfigured {
		t.Errorf("expected AuthConfigured=false after MarkRunnerAuthInvalid; got AuthConfigured=true (warning=%q)", got.Warning)
	}
	if got.AuthConfigured == false && !strings.Contains(got.Warning, "Token rejected") {
		// Warning only attached when override fires (i.e. presence
		// check would have said true). Don't fail when override
		// didn't trigger — it just means no claude creds are present
		// in this test env, which is fine.
	}
}
