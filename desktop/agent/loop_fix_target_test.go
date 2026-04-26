package main

// loop_fix_target_test.go — small unit tests for the FixTarget routing
// helper. The phaseCommit integration is exercised by the existing
// loop tests; these tests just lock the branch-name generator + fallback
// behaviour so a future refactor can't silently regress the contract.

import (
	"strings"
	"testing"
	"time"
)

func TestAutoDevBranchName_safeChars(t *testing.T) {
	got := autoDevBranchName("sfmg/onboarding loop!")
	if !strings.HasPrefix(got, "autodev/sfmg/onboarding-loop-") {
		t.Fatalf("name should preserve safe chars + dashify the rest, got %q", got)
	}
	// Timestamp suffix is YYYYMMDD-HHMMSS — 15 chars after the dash.
	parts := strings.Split(got, "-")
	if len(parts) < 2 {
		t.Fatalf("name should have a timestamp suffix, got %q", got)
	}
	ts := parts[len(parts)-2] + "-" + parts[len(parts)-1]
	if _, err := time.Parse("20060102-150405", ts); err != nil {
		t.Fatalf("timestamp suffix %q does not parse: %v", ts, err)
	}
}

func TestAutoDevBranchName_emptySpec(t *testing.T) {
	got := autoDevBranchName("")
	if !strings.HasPrefix(got, "autodev/loop-") {
		t.Fatalf("empty spec name should fall back to autodev/loop-..., got %q", got)
	}
}

func TestAutoDevBranchName_uniquePerCall(t *testing.T) {
	a := autoDevBranchName("p")
	time.Sleep(1100 * time.Millisecond) // timestamp granularity is seconds
	b := autoDevBranchName("p")
	if a == b {
		t.Fatalf("two consecutive calls should produce distinct branch names; both = %q", a)
	}
}
