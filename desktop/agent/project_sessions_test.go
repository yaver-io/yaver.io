package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestValidGitRef(t *testing.T) {
	for _, ref := range []string{"HEAD", "main", "feature/cloud-studio", "v1.2.3"} {
		if !validGitRef(ref) {
			t.Errorf("expected valid ref %q", ref)
		}
	}
	for _, ref := range []string{"", "--help", "main..evil", "refs/@{1}", "bad ref", "topic~1"} {
		if validGitRef(ref) {
			t.Errorf("expected invalid ref %q", ref)
		}
	}
}

func TestProjectSessionLifecycleAndIsolation(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("GIT_AUTHOR_NAME", "Yaver Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@yaver.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Yaver Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@yaver.invalid")

	source := filepath.Join(tempHome, "source-project")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	testGit(t, source, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, source, "add", "README.md")
	testGit(t, source, "commit", "-m", "initial")
	// Give the worktree a real but deliberately local origin so PushReview
	// reaches the network-remote policy check instead of stopping earlier on a
	// fixture that has no origin at all.
	testGit(t, source, "remote", "add", "origin", source)

	configDir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	projects := "# Yaver Local Context\n\n## Projects\n\n### " + source + "\n- Branch: main\n"
	if err := os.WriteFile(filepath.Join(configDir, projectsFileName), []byte(projects), 0600); err != nil {
		t.Fatal(err)
	}

	repositories, err := ListGitRepositories(false)
	if err != nil || len(repositories) != 1 {
		t.Fatalf("repositories = %#v, err = %v", repositories, err)
	}
	manager, err := NewProjectSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Create(repositories[0].RepositoryID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if session.WorkDir == source || !strings.HasPrefix(session.ReviewBranch, "yaver/cloud-") {
		t.Fatalf("session is not isolated: %#v", session)
	}
	managedWorktrees, err := DefaultWorkspaceWorktreesDir()
	if err != nil || !isPathWithinRoot(session.WorkDir, managedWorktrees) {
		t.Fatalf("session worktree %q is outside managed worktrees %q: %v", session.WorkDir, managedWorktrees, err)
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), session.WorkDir) || strings.Contains(string(encoded), source) {
		t.Fatalf("session JSON exposed runner paths: %s", encoded)
	}

	if err := os.WriteFile(filepath.Join(session.WorkDir, "README.md"), []byte("hello from Cloud Studio\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := manager.GitDiff(session.ProjectSessionID)
	if err != nil || !strings.Contains(diff, "hello from Cloud Studio") {
		t.Fatalf("diff = %q, err = %v", diff, err)
	}
	sha, err := manager.GitCommit(session.ProjectSessionID, "Update readme")
	if err != nil || len(sha) < 7 {
		t.Fatalf("commit SHA = %q, err = %v", sha, err)
	}
	if _, err := manager.PushReview(session.ProjectSessionID); err == nil || !strings.Contains(err.Error(), "HTTPS or SSH") {
		t.Fatalf("local-origin push should be rejected, got %v", err)
	}
	stopped, err := manager.Stop(session.ProjectSessionID)
	if err != nil || stopped.Status != "stopped" {
		t.Fatalf("stop = %#v, err = %v", stopped, err)
	}

	reloaded, err := NewProjectSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reloaded.Get(session.ProjectSessionID)
	if !ok || loaded.Status != "stopped" {
		t.Fatalf("reloaded session = %#v, ok = %v", loaded, ok)
	}
	if _, err := reloaded.Delete(session.ProjectSessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("deleted checkout still exists: %v", err)
	}
	finalManager, err := NewProjectSessionManager()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := finalManager.Get(session.ProjectSessionID); ok {
		t.Fatal("deleted session was restored from registry")
	}
}

func TestConvergeManagedRepositoryFastForwardsPristineMain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	t.Setenv("YAVER_REPOS_DIR", "")
	t.Setenv("YAVER_WORKTREES_DIR", "")
	t.Setenv("GIT_AUTHOR_NAME", "Yaver Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@yaver.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Yaver Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@yaver.invalid")

	remote := filepath.Join(home, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	seed := filepath.Join(home, "seed")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, seed, "add", "README.md")
	testGit(t, seed, "commit", "-m", "one")
	testGit(t, seed, "push", "-u", "origin", "main")

	repos, err := DefaultWorkspaceReposDir()
	if err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(repos, "app")
	cmd := exec.Command("git", "clone", "-b", "main", remote, managed)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	before := testGit(t, managed, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, seed, "add", "README.md")
	testGit(t, seed, "commit", "-m", "two")
	testGit(t, seed, "push", "origin", "main")
	want := testGit(t, seed, "rev-parse", "HEAD")

	if err := convergeManagedRepository(context.Background(), managed); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, managed, "rev-parse", "HEAD"); got != want || got == before {
		t.Fatalf("managed repo HEAD = %s, want latest %s (before %s)", got, want, before)
	}
	testGit(t, managed, "switch", "-c", "topic")
	if err := convergeManagedRepository(context.Background(), managed); err == nil || !strings.Contains(err.Error(), "Workspace/repos on main") {
		t.Fatalf("managed repository accepted a development branch: %v", err)
	}
}

func TestConvergeManagedRepositoryRefusesDirtyTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	t.Setenv("YAVER_REPOS_DIR", "")
	repos, err := DefaultWorkspaceReposDir()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(repos, "dirty")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "uncommitted.txt"), []byte("preserve me"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = convergeManagedRepository(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "not pristine") {
		t.Fatalf("dirty managed repo was not refused: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "uncommitted.txt")); statErr != nil {
		t.Fatalf("dirty guard lost user work: %v", statErr)
	}
}

func TestConvergeManagedRepositoryNeverMutatesCustomHierarchy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	custom := filepath.Join(home, "src", "custom")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	testGit(t, custom, "init", "-b", "topic")
	if err := os.WriteFile(filepath.Join(custom, "custom.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := convergeManagedRepository(context.Background(), custom); err != nil {
		t.Fatalf("custom hierarchy should bypass managed convergence: %v", err)
	}
	if branch := testGit(t, custom, "branch", "--show-current"); branch != "topic" {
		t.Fatalf("custom branch changed to %q", branch)
	}
}
