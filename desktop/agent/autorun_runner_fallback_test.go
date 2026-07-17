package main

import (
	"errors"
	"strings"
	"testing"
)

// A requested-but-unready runner must not end the run.
//
// This is the bug that ate 1 of 7 autoruns on the mini in a single morning:
// `runner opencode is not ready: opencode found but not working: signal: killed
// (output: 1.14.41)` — opencode had literally just printed its version, the
// readiness probe SIGKILLed it, and the whole task died rather than handing the
// work to codex. An autorun exists to make progress unattended; a preference for
// one CLI is not a reason to do nothing.
//
// These drive the real selectAutorunRunner against a real temp workDir. On a box
// with no authenticated runner every candidate legitimately fails, so the
// assertions are about WHICH ERROR / WHICH FALLBACK, never about a runner being
// installed here — that keeps the test honest on a bare CI box and on a laptop
// with four CLIs signed in.

func TestSelectAutorunRunnerRejectsUnknownRunnerWithoutFallback(t *testing.T) {
	dir := t.TempDir()

	_, err := selectAutorunRunner(dir, "not-a-real-runner")
	if err == nil {
		t.Fatal("an unsupported runner must fail; it is a caller bug, not an unready box")
	}
	if !strings.Contains(err.Error(), "unsupported runner") {
		t.Fatalf("a typo must fail loudly as unsupported, not silently fall back to another runner; got: %v", err)
	}
}

// The exact mini failure: opencode requested, opencode unready, codex sitting
// there authenticated. The run must proceed ON CODEX, not die.
func TestSelectAutorunRunnerFallsBackToAReadyRunner(t *testing.T) {
	dir := t.TempDir()
	probed := []string{}
	ready := func(r RunnerConfig, _ string) error {
		probed = append(probed, r.RunnerID)
		if r.RunnerID == "opencode" {
			// Verbatim from autorun-1784281562998711000 on the mini.
			return errors.New("opencode found but not working: signal: killed (output: 1.14.41)")
		}
		return nil
	}

	got, err := selectAutorunRunnerWith(dir, "opencode", ready)
	if err != nil {
		t.Fatalf("an unready opencode must fall back to a ready runner, not end the run: %v", err)
	}
	if got.RunnerID == "opencode" {
		t.Fatal("returned the runner that just failed its readiness probe")
	}
	if got.RunnerID != "claude" {
		t.Fatalf("fallback must follow supportedRunnerIDs order (claude first); got %q", got.RunnerID)
	}
	if probed[0] != "opencode" {
		t.Fatalf("the requested runner must be tried FIRST — it is a preference, not a last resort; probed: %v", probed)
	}
}

// Falling back must not re-probe the runner that already failed.
func TestSelectAutorunRunnerDoesNotRetryTheRequestedRunner(t *testing.T) {
	dir := t.TempDir()
	count := 0
	ready := func(r RunnerConfig, _ string) error {
		if r.RunnerID == "opencode" {
			count++
		}
		return errors.New("not ready")
	}

	_, err := selectAutorunRunnerWith(dir, "opencode", ready)
	if err == nil {
		t.Fatal("expected failure when no runner is ready")
	}
	if count != 1 {
		t.Fatalf("requested runner probed %d times; must be exactly 1", count)
	}
	if !strings.Contains(err.Error(), "no authenticated runner is ready") {
		t.Fatalf("expected the sweep's error after trying every runner; got: %v", err)
	}
	if strings.Count(err.Error(), "opencode:") != 1 {
		t.Fatalf("requested runner reported twice in the failure summary; got: %v", err)
	}
	// Every runner's reason must survive — that report is the only thing the user
	// has when nothing is ready.
	for _, id := range supportedRunnerIDs {
		if !strings.Contains(err.Error(), id+":") {
			t.Fatalf("failure summary omits %s, so the user cannot see why it was skipped; got: %v", id, err)
		}
	}
}

// A ready requested runner must be used as-is — fallback must never override an
// explicit, working preference.
func TestSelectAutorunRunnerHonoursAReadyRequestedRunner(t *testing.T) {
	dir := t.TempDir()
	ready := func(RunnerConfig, string) error { return nil }

	got, err := selectAutorunRunnerWith(dir, "opencode", ready)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.RunnerID != "opencode" {
		t.Fatalf("a ready requested runner must win; got %q", got.RunnerID)
	}
}

// The fallback must never widen the cost surface: it may only pick from the
// canonical CLI/subscription runner set, which is what CheckRunnerReady gates on.
func TestSelectAutorunRunnerFallbackStaysWithinSubscriptionRunners(t *testing.T) {
	for _, id := range supportedRunnerIDs {
		if !IsSupportedRunner(id) {
			t.Fatalf("fallback candidate %q is not a supported runner", id)
		}
	}
	if len(supportedRunnerIDs) == 0 {
		t.Fatal("no fallback candidates: an unready runner would always kill the run")
	}
}
