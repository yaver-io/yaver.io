package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// `ship` freezes the fleet, drains it, and pins a SHA so the thing it verified
// is the thing it publishes. Every deploy step then shells from the WORKING
// TREE — so without this check the pin was bookkeeping, not a guarantee.
func newPinRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := exec.Command("sh", "-c", "echo one > "+dir+"/f.txt").Run(); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", dir, "add", "-A").Run()
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "one").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

func TestDeployPinAcceptsTheGatedCommit(t *testing.T) {
	dir, sha := newPinRepo(t)
	if err := verifyDeployPin(context.Background(), dir, sha); err != nil {
		t.Fatalf("the gated commit must be deployable: %v", err)
	}
	// Abbreviated pins are accepted — callers pass whatever their own rev-parse
	// handed them.
	if err := verifyDeployPin(context.Background(), dir, sha[:12]); err != nil {
		t.Fatalf("an abbreviated pin must be accepted: %v", err)
	}
}

// The case this exists for: an autorun lands mid-ship, or a human on the box
// moves HEAD, and the deploy would publish a commit nobody gated.
func TestDeployPinRefusesAMovedTree(t *testing.T) {
	dir, sha := newPinRepo(t)
	exec.Command("sh", "-c", "echo two > "+dir+"/f.txt").Run()
	exec.Command("git", "-C", dir, "add", "-A").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "two").Run()

	err := verifyDeployPin(context.Background(), dir, sha)
	if err == nil {
		t.Fatal("a tree that moved past the gated commit must not deploy")
	}
	if !strings.Contains(err.Error(), "passed the gate") {
		t.Fatalf("the refusal must say WHY it matters, got: %v", err)
	}
}

// Uncommitted edits are, by definition, not in the pinned commit.
func TestDeployPinRefusesADirtyTree(t *testing.T) {
	dir, sha := newPinRepo(t)
	exec.Command("sh", "-c", "echo scratch > "+dir+"/f.txt").Run()

	if err := verifyDeployPin(context.Background(), dir, sha); err == nil {
		t.Fatal("a dirty tree must not deploy under a pin — those edits were never gated")
	}
}

// No pin means no check, so every pre-existing caller keeps working.
func TestDeployPinIsOptional(t *testing.T) {
	dir, _ := newPinRepo(t)
	exec.Command("sh", "-c", "echo dirty > "+dir+"/f.txt").Run()
	if err := verifyDeployPin(context.Background(), dir, ""); err != nil {
		t.Fatalf("an unpinned deploy must not be blocked: %v", err)
	}
}
