package main

import (
	"strings"
	"testing"
)

// TestRunnerPreflightUnknownIsFresh: an empty/unknown runner id is treated as
// fresh (the TaskManager resolves the default runner itself; we don't block a
// path we can't assess).
func TestRunnerPreflightUnknownIsFresh(t *testing.T) {
	if r := RunnerPreflightByID("", ""); !r.Fresh {
		t.Fatalf("empty runner id should be fresh, got %+v", r)
	}
	if r := RunnerPreflightByID("totally-unknown-runner", ""); r.NeedsReauth {
		t.Fatalf("unknown runner should not demand reauth, got %+v", r)
	}
}

// TestRunnerPreflightReauthAfterFailure: a runner flagged as recently-failed
// (the codex-expired signal) preflights as needing reauth, with a spoken CTA.
func TestRunnerPreflightReauthAfterFailure(t *testing.T) {
	// detectCodexStatus reports AuthConfigured only when codex is actually
	// signed in on this machine. Regardless of that, a recent auth failure must
	// force NeedsReauth via the lastRunnerAuthFailure override.
	MarkRunnerAuthInvalid("codex")
	defer ClearRunnerAuthInvalid("codex")

	r := RunnerPreflightByID("codex", "")
	if r.Fresh || !r.NeedsReauth {
		t.Fatalf("codex after a failure should need reauth, got %+v", r)
	}
	if !strings.Contains(r.Action, "codex") {
		t.Fatalf("action should mention codex re-login, got %q", r.Action)
	}
	if runnerPreflightSpoken(r) == "" {
		t.Fatalf("a needs-reauth result must produce a spoken CTA")
	}
}
