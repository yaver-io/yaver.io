package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitCommitPushHappyPath drives the full happy path: stage, commit,
// push to a local bare-repo "remote", reach OK with a real commit hash.
// No mocks — same approach the rest of the agent's HTTP tests take.
func TestGitCommitPushHappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in test environment")
	}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	repo := filepath.Join(tmp, "repo")
	mustGit(t, "", "init", "--bare", bare)
	mustGit(t, "", "init", "-b", "main", repo)
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test User")
	mustGit(t, repo, "remote", "add", "origin", bare)
	writeFile(t, filepath.Join(repo, "README.md"), "hello\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-m", "init")
	mustGit(t, repo, "push", "-u", "origin", "main")

	// Add a new untracked file — the deterministic path should auto-stage it.
	writeFile(t, filepath.Join(repo, "feature.txt"), "v1\n")

	body, _ := json.Marshal(map[string]interface{}{
		"workDir": repo,
		"message": "feat: feature",
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/commit-push", bytes.NewReader(body))
	(&HTTPServer{}).handleGitCommitPush(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp commitPushResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK || !resp.Pushed {
		t.Fatalf("expected ok+pushed, got %+v", resp)
	}
	if resp.Hash == "" {
		t.Fatalf("expected hash, got empty")
	}
	if resp.Branch != "main" {
		t.Fatalf("expected branch=main, got %q", resp.Branch)
	}
}

// TestGitCommitPushRebaseOnNonFastForward simulates a divergent remote:
// another commit lands on origin/main while local has its own commit.
// The deterministic flow should fetch + rebase + push.
func TestGitCommitPushRebaseOnNonFastForward(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	mustGit(t, "", "init", "--bare", bare)

	seed := filepath.Join(tmp, "seed")
	mustGit(t, "", "init", "-b", "main", seed)
	mustGit(t, seed, "config", "user.email", "seed@example.com")
	mustGit(t, seed, "config", "user.name", "Seed")
	mustGit(t, seed, "remote", "add", "origin", bare)
	writeFile(t, filepath.Join(seed, "README.md"), "hello\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-m", "init")
	mustGit(t, seed, "push", "-u", "origin", "main")

	// Simulate "someone else pushed first" by adding a commit from a
	// fresh clone and pushing it.
	other := filepath.Join(tmp, "other")
	mustGit(t, "", "clone", bare, other)
	mustGit(t, other, "config", "user.email", "other@example.com")
	mustGit(t, other, "config", "user.name", "Other")
	writeFile(t, filepath.Join(other, "OTHER.md"), "remote-only\n")
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "-m", "remote-only")
	mustGit(t, other, "push")

	// Now make a divergent local commit and try commit-push from a
	// stale checkout (the seed) — push should reject, rebase should
	// succeed (no overlapping files), final push should land.
	writeFile(t, filepath.Join(seed, "LOCAL.md"), "local-only\n")
	body, _ := json.Marshal(map[string]interface{}{"workDir": seed})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/commit-push", bytes.NewReader(body))
	(&HTTPServer{}).handleGitCommitPush(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp commitPushResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.OK || !resp.Pushed || !resp.Rebased {
		t.Fatalf("expected ok+pushed+rebased, got %+v", resp)
	}
	if !containsAction(resp.Actions, "rebase origin/main") || !containsAction(resp.Actions, "fetch") {
		t.Fatalf("expected fetch + rebase actions, got %v", resp.Actions)
	}
}

// TestGitCommitPushConflictRequiresAgent verifies that when rebase
// would conflict, the deterministic path aborts the rebase, leaves the
// tree clean, and signals requiresAgent=true with the conflicted file
// listed — so the caller knows to delegate to a coding agent.
func TestGitCommitPushConflictRequiresAgent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	mustGit(t, "", "init", "--bare", bare)

	seed := filepath.Join(tmp, "seed")
	mustGit(t, "", "init", "-b", "main", seed)
	mustGit(t, seed, "config", "user.email", "seed@example.com")
	mustGit(t, seed, "config", "user.name", "Seed")
	mustGit(t, seed, "remote", "add", "origin", bare)
	writeFile(t, filepath.Join(seed, "shared.txt"), "v0\n")
	mustGit(t, seed, "add", "-A")
	mustGit(t, seed, "commit", "-m", "init")
	mustGit(t, seed, "push", "-u", "origin", "main")

	other := filepath.Join(tmp, "other")
	mustGit(t, "", "clone", bare, other)
	mustGit(t, other, "config", "user.email", "other@example.com")
	mustGit(t, other, "config", "user.name", "Other")
	writeFile(t, filepath.Join(other, "shared.txt"), "remote\n")
	mustGit(t, other, "add", "-A")
	mustGit(t, other, "commit", "-m", "remote")
	mustGit(t, other, "push")

	// Local edit on the same file — rebase will conflict.
	writeFile(t, filepath.Join(seed, "shared.txt"), "local\n")
	body, _ := json.Marshal(map[string]interface{}{"workDir": seed})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/commit-push", bytes.NewReader(body))
	(&HTTPServer{}).handleGitCommitPush(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp commitPushResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.RequiresAgent {
		t.Fatalf("expected requiresAgent=true, got %+v", resp)
	}
	if len(resp.Conflicts) == 0 {
		t.Fatalf("expected at least one conflicted file, got %+v", resp.Conflicts)
	}
	// Tree should be clean after rebase --abort.
	rebaseDir := filepath.Join(seed, ".git", "rebase-merge")
	if _, err := os.Stat(rebaseDir); !os.IsNotExist(err) {
		t.Fatalf("rebase wasn't aborted — .git/rebase-merge still exists")
	}
}

// TestGitCommitPushNothingToCommit confirms the endpoint is a no-op
// (push only) when the working tree is clean — useful for "I forgot to
// push" workflows where commit isn't needed.
func TestGitCommitPushNothingToCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote.git")
	repo := filepath.Join(tmp, "repo")
	mustGit(t, "", "init", "--bare", bare)
	mustGit(t, "", "init", "-b", "main", repo)
	mustGit(t, repo, "config", "user.email", "test@example.com")
	mustGit(t, repo, "config", "user.name", "Test")
	mustGit(t, repo, "remote", "add", "origin", bare)
	writeFile(t, filepath.Join(repo, "README.md"), "x\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-m", "init")
	mustGit(t, repo, "push", "-u", "origin", "main")

	body, _ := json.Marshal(map[string]interface{}{"workDir": repo})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/commit-push", bytes.NewReader(body))
	(&HTTPServer{}).handleGitCommitPush(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp commitPushResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.OK || !resp.NothingToCommit {
		t.Fatalf("expected ok+nothingToCommit, got %+v", resp)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsAction(s []string, sub string) bool {
	for _, v := range s {
		if v == sub {
			return true
		}
	}
	return false
}
