package main

import (
	"errors"
	"fmt"
	"testing"
)

// These tests pin the public surface of projectstore.go so a future
// refactor can't silently break callers that depend on:
//
//   • errors.Is(err, ErrProjectNotFound) detecting wrapped errors
//   • the slug surviving in the error message
//   • ConflictPolicy zero value being "reject" (no string-typo regressions)
//
// No implementation tests yet — those land alongside the
// AgentProjectStore + RepoProjectStore impls in follow-up commits.

func TestNewProjectNotFoundIsDetectedThroughWrapping(t *testing.T) {
	root := NewProjectNotFound("todo-app")
	if !errors.Is(root, ErrProjectNotFound) {
		t.Fatal("NewProjectNotFound result must satisfy errors.Is(_, ErrProjectNotFound)")
	}
	wrapped := fmt.Errorf("agent store: %w", root)
	if !errors.Is(wrapped, ErrProjectNotFound) {
		t.Fatal("wrapping with fmt.Errorf must preserve sentinel; HTTP boundary needs this to map 404")
	}
	if !errMessageContains(wrapped.Error(), "todo-app") {
		t.Fatalf("error must keep the slug for human-facing messages; got %q", wrapped.Error())
	}
}

func TestProjectNotFoundDoesNotMatchUnrelatedErrors(t *testing.T) {
	other := errors.New("dial tcp: connection refused")
	if errors.Is(other, ErrProjectNotFound) {
		t.Fatal("ErrProjectNotFound must not match unrelated errors — HTTP would 404 a network failure otherwise")
	}
}

func TestConflictRejectIsTheZeroValue(t *testing.T) {
	// The agent's existing /phone/projects/receive endpoint defaults
	// to "reject" when the caller doesn't pass a policy. The Go zero
	// value of ConflictPolicy must mean the same thing so callers
	// that omit OnConflict get the safe default automatically.
	var p ConflictPolicy
	if p != ConflictReject {
		t.Fatalf("zero-valued ConflictPolicy must equal ConflictReject; got %q", p)
	}
}

func TestProjectMetaTierIsFreeForm(t *testing.T) {
	// Three known tiers today: agent, repo, phone-sandbox. Nothing in
	// the contract should hardcode them — future stores (e.g. an
	// in-memory test fixture) need to set their own Tier without a
	// type system fight.
	for _, tier := range []string{"agent", "repo", "phone-sandbox", "test-fixture"} {
		m := ProjectMeta{Slug: "x", Tier: tier}
		if m.Tier != tier {
			t.Fatalf("ProjectMeta.Tier should round-trip raw strings; got %q", m.Tier)
		}
	}
}

// errMessageContains is a tiny local substring check kept inside the
// _test file so it doesn't compete with pipeline_cmd.go's `contains`.
func errMessageContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
