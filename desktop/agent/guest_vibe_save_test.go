package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitAndPushGuestVibe proves a tester's save lands as a real commit on
// the branch, attributed to the friend, and that a clean tree is a friendly
// no-op (errNothingToCommit) rather than an error. Real local git, no remote.
func TestCommitAndPushGuestVibe(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		if out, err := runGit(dir, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	mustGit("init", "-q")
	mustGit("config", "user.name", "Dev")
	mustGit("config", "user.email", "dev@x.io")
	mustGit("commit", "--allow-empty", "-m", "seed")

	// Clean tree → friendly no-op.
	if _, _, err := commitAndPushGuestVibe(dir, "Pat", "pat@x.io", "msg"); err != errNothingToCommit {
		t.Fatalf("clean tree should be errNothingToCommit, got %v", err)
	}

	// The tester "vibes": a file appears in the working tree.
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("vibes"), 0644); err != nil {
		t.Fatal(err)
	}
	sha, pushed, err := commitAndPushGuestVibe(dir, "Pat Tester", "pat@x.io", "🎸 Pat vibed")
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if strings.TrimSpace(sha) == "" {
		t.Error("expected a commit sha")
	}
	if pushed {
		t.Error("no remote configured → pushed should be false")
	}

	// The commit is attributed to the friend, not the host/agent.
	author, _ := runGit(dir, "log", "-1", "--pretty=%an <%ae>")
	if !strings.Contains(author, "Pat Tester") || !strings.Contains(author, "pat@x.io") {
		t.Errorf("commit should be attributed to the friend, got %q", author)
	}
	// It landed on the current branch (HEAD advanced, file tracked).
	tracked, _ := runGit(dir, "ls-files", "feature.txt")
	if strings.TrimSpace(tracked) == "" {
		t.Error("feature.txt should be committed onto the branch")
	}
}

// TestFunnyVibeCommitMessage checks the default message is deterministic,
// non-empty, and names both the friend and the project.
func TestFunnyVibeCommitMessage(t *testing.T) {
	m := funnyVibeCommitMessage("Sam", "coolapp")
	if !strings.Contains(m, "Sam") || !strings.Contains(m, "coolapp") {
		t.Errorf("message should name friend + project, got %q", m)
	}
	if m != funnyVibeCommitMessage("Sam", "coolapp") {
		t.Error("message must be deterministic (no rand)")
	}
	// Empty inputs fall back gracefully, never empty.
	if strings.TrimSpace(funnyVibeCommitMessage("", "")) == "" {
		t.Error("message must never be empty")
	}
}
