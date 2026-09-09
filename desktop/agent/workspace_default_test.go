package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultWorkspaceDir_HomeSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("DefaultWorkspaceDir: %v", err)
	}
	want := filepath.Join(home, "Workspace")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestDefaultWorkspaceDir_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// First call creates
	dir1, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Drop a sentinel file inside
	sentinel := filepath.Join(dir1, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second call must NOT clobber
	dir2, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if dir1 != dir2 {
		t.Errorf("dirs differ: %q vs %q", dir1, dir2)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("idempotent call wiped existing contents: %v", err)
	}
}

func TestResolveWorkspaceParent_Provided(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	custom := filepath.Join(t.TempDir(), "my-custom-path")
	if got := ResolveWorkspaceParent(custom); got != custom {
		t.Errorf("expected provided path verbatim, got %q", got)
	}
	// Whitespace trim
	if got := ResolveWorkspaceParent("  " + custom + "  "); got != custom {
		t.Errorf("whitespace not trimmed: got %q", got)
	}
}

func TestResolveWorkspaceParent_DefaultsToWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := ResolveWorkspaceParent("")
	want := filepath.Join(home, "Workspace")
	if got != want {
		t.Errorf("default mismatch: got %q, want %q", got, want)
	}
}

func TestManagedWorkspaceLayoutSeparatesReposAndWorktrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	t.Setenv("YAVER_REPOS_DIR", "")
	t.Setenv("YAVER_WORKTREES_DIR", "")

	repos, err := DefaultWorkspaceReposDir()
	if err != nil {
		t.Fatal(err)
	}
	worktrees, err := DefaultWorkspaceWorktreesDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Workspace", "repos"); repos != want {
		t.Fatalf("repos = %q, want %q", repos, want)
	}
	if want := filepath.Join(home, "Workspace", "worktrees"); worktrees != want {
		t.Fatalf("worktrees = %q, want %q", worktrees, want)
	}
}

func TestManagedWorkspaceLayoutHonorsExplicitHierarchy(t *testing.T) {
	customRepoParent := filepath.Join(t.TempDir(), "source")
	customWorktreeParent := filepath.Join(t.TempDir(), "development")
	if got := ResolveRepositoryParent(customRepoParent); got != customRepoParent {
		t.Fatalf("repository parent = %q, want explicit %q", got, customRepoParent)
	}
	if got := ResolveWorktreeParent(customWorktreeParent); got != customWorktreeParent {
		t.Fatalf("worktree parent = %q, want explicit %q", got, customWorktreeParent)
	}
}

func TestManagedWorkspaceLayoutHonorsIndependentOverrides(t *testing.T) {
	repos := filepath.Join(t.TempDir(), "repositories")
	worktrees := filepath.Join(t.TempDir(), "trees")
	t.Setenv("YAVER_REPOS_DIR", repos)
	t.Setenv("YAVER_WORKTREES_DIR", worktrees)
	gotRepos, err := DefaultWorkspaceReposDir()
	if err != nil {
		t.Fatal(err)
	}
	gotWorktrees, err := DefaultWorkspaceWorktreesDir()
	if err != nil {
		t.Fatal(err)
	}
	if gotRepos != repos || gotWorktrees != worktrees {
		t.Fatalf("overrides = (%q, %q), want (%q, %q)", gotRepos, gotWorktrees, repos, worktrees)
	}
}

func TestCollectWorkspaceLayoutReportsManagedAndCustomWithoutDeleting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	t.Setenv("YAVER_REPOS_DIR", "")
	t.Setenv("YAVER_WORKTREES_DIR", "")
	repos, _ := DefaultWorkspaceReposDir()
	worktrees, _ := DefaultWorkspaceWorktreesDir()
	for _, dir := range []string{
		filepath.Join(repos, "primary"),
		filepath.Join(worktrees, "project-sessions", "session-a"),
		filepath.Join(home, "Workspace", "custom-layout"),
	} {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	status, err := CollectWorkspaceLayoutStatus()
	if err != nil {
		t.Fatal(err)
	}
	if status.ManagedRepositoryCount != 1 || status.ManagedWorktreeCount != 1 || status.OutsideManagedCount != 1 {
		t.Fatalf("unexpected layout counts: %#v", status)
	}
	if !status.ExplicitPathsSupported || !status.CleanupRequiresExplicit {
		t.Fatalf("custom path or explicit cleanup contract lost: %#v", status)
	}
	if _, err := os.Stat(filepath.Join(home, "Workspace", "custom-layout")); err != nil {
		t.Fatalf("layout inspection mutated a custom checkout: %v", err)
	}
}

func TestResolveWorkspaceParent_NoHomeFallsBackToCwd(t *testing.T) {
	// Force HOME empty and exercise the runtime fallback.
	t.Setenv("HOME", "")
	// Verify the generic fallback is absolute and does not panic.
	got := ResolveWorkspaceParent("")
	if got == "" {
		t.Error("ResolveWorkspaceParent returned empty string")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path fallback, got %q", got)
	}
}

func TestDefaultWorkspaceDirName_IsCapitalW(t *testing.T) {
	// Lock the cross-surface default spelling. Changing it would split existing
	// installs from clone/discovery callers that use the established directory.
	if DefaultWorkspaceDirName != "Workspace" {
		t.Errorf("DefaultWorkspaceDirName changed: %q — verify all callers + docs", DefaultWorkspaceDirName)
	}
}
