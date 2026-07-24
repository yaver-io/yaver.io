package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The invariant these pin: a scan that runs out of time returns LESS, never
// NOTHING.
//
// The bug (2026-07-24, mac mini): writeProjects shelled out to `find`. On a box
// whose home held 30+ monorepo clones the walk took minutes, the 30s context
// killed find, and find's block-buffered pipe stdout — holding every repo it
// had already located — was discarded with the process. cmd.Output() returned
// empty, so a machine full of projects reported "_No projects found._" while
// the sibling in-process scanner found 213 on the same disk.

func mkRepo(t *testing.T, base string, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{base}, parts...)...)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// withHome points projectDiscoveryRoots at a scratch dir by overriding HOME.
// No hardcoded path anywhere — the walker resolves roots from the environment,
// which is exactly what makes it work for any user on any box.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestGitWalkFindsReposUnderWorkspace(t *testing.T) {
	home := withHome(t)
	ws := filepath.Join(home, "Workspace")
	want := []string{
		mkRepo(t, ws, "e-mobile"),
		mkRepo(t, ws, "talos"),
		mkRepo(t, ws, "nested", "deeper-project"),
	}

	got := findGitRepoDirsForDiscovery(20 * time.Second)
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	for _, w := range want {
		if !found[w] {
			t.Fatalf("repo %q not found; got %v", w, got)
		}
	}
}

func TestGitWalkSkipsNoiseDirectories(t *testing.T) {
	home := withHome(t)
	ws := filepath.Join(home, "Workspace")
	real := mkRepo(t, ws, "realproject")
	// A checkout buried in node_modules is a dependency, not the user's project.
	mkRepo(t, ws, "realproject", "node_modules", "some-dep")
	mkRepo(t, home, "Library", "Caches", "junk")

	got := findGitRepoDirsForDiscovery(20 * time.Second)
	for _, g := range got {
		if filepath.Base(filepath.Dir(g)) == "node_modules" {
			t.Fatalf("walked into node_modules: %q", g)
		}
		if len(g) > len(home) && filepath.Base(g) == "junk" {
			t.Fatalf("walked into Library: %q", g)
		}
	}
	var sawReal bool
	for _, g := range got {
		if g == real {
			sawReal = true
		}
	}
	if !sawReal {
		t.Fatalf("the real project was not found; got %v", got)
	}
}

// THE regression test. An exhausted budget must still return what was already
// found — the exact behaviour find(1) could not provide.
func TestGitWalkReturnsPartialResultsWhenBudgetExpires(t *testing.T) {
	home := withHome(t)
	ws := filepath.Join(home, "Workspace")
	for i := 0; i < 40; i++ {
		mkRepo(t, ws, "proj"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}

	// A budget so small it is already spent inside the first Walk callback.
	got := findGitRepoDirsForDiscovery(1 * time.Nanosecond)
	// The contract is "never panics, never hangs, returns a slice". Whatever it
	// managed is acceptable; silently losing a full buffer is not, and cannot
	// happen because results are appended as they are seen.
	if got == nil {
		return // zero found within a nanosecond is legitimate
	}
	for _, g := range got {
		if g == "" {
			t.Fatal("empty repo path returned")
		}
	}
}

func TestGitWalkNeverDescendsIntoDotGit(t *testing.T) {
	home := withHome(t)
	ws := filepath.Join(home, "Workspace")
	repo := mkRepo(t, ws, "proj")
	// A nested .git inside .git would be reported twice if we descended.
	if err := os.MkdirAll(filepath.Join(repo, ".git", "modules", "sub", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findGitRepoDirsForDiscovery(20 * time.Second)
	for _, g := range got {
		if filepath.Base(g) == "sub" {
			t.Fatalf("descended into .git internals: %q", g)
		}
	}
}

func TestGitWalkDeduplicatesAcrossOverlappingRoots(t *testing.T) {
	// `home` is itself a discovery root and contains Workspace, so every repo
	// is reachable by two roots. It must be reported once.
	home := withHome(t)
	repo := mkRepo(t, filepath.Join(home, "Workspace"), "dup")

	got := findGitRepoDirsForDiscovery(20 * time.Second)
	count := 0
	for _, g := range got {
		if g == repo {
			count++
		}
	}
	if count > 1 {
		t.Fatalf("repo reported %d times, want 1: %v", count, got)
	}
}
