package main

// Real git repos in t.TempDir(), no mocks — the failures this guards against
// are git's actual behaviour (a rebase that strands the tree, a pathspec commit
// that sweeps a sibling's file), and a fake would reproduce neither.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func deployGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newDeployRepo builds an origin + a clone of it, both on main.
func newDeployRepo(t *testing.T) (repo string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	deployGit(t, root, "init", "--bare", "-b", "main", origin)

	repo = filepath.Join(root, "work")
	// Clone without -b: a fresh bare repo has no commits, so main does not
	// exist upstream yet. Create it here and publish it.
	deployGit(t, root, "clone", origin, repo)
	// Name the remote "github" so the code under test picks the same one the
	// real repo uses.
	deployGit(t, repo, "remote", "rename", "origin", "github")
	deployGit(t, repo, "checkout", "-q", "-B", "main")
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployGit(t, repo, "add", "seed.txt")
	deployGit(t, repo, "commit", "-m", "seed")
	deployGit(t, repo, "push", "-u", "github", "main")
	return repo
}

func TestDeploySyncRefusesOffMain(t *testing.T) {
	repo := newDeployRepo(t)
	deployGit(t, repo, "checkout", "-q", "-b", "feature/x")

	_, err := deploySyncGit(context.Background(), repo, nil, "")
	if err == nil {
		t.Fatal("a deploy from a feature branch must not push HEAD:main — that ships unreviewed work under a name nobody expects")
	}
	if !strings.Contains(err.Error(), "feature/x") || !strings.Contains(err.Error(), "always ships main") {
		t.Fatalf("error must name the branch and the rule, got: %v", err)
	}
}

func TestDeploySyncRefusesUnnamedDirtyFiles(t *testing.T) {
	repo := newDeployRepo(t)
	// Mine, and a sibling session's.
	if err := os.WriteFile(filepath.Join(repo, "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "theirs.txt"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployGit(t, repo, "add", "mine.txt", "theirs.txt")

	_, err := deploySyncGit(context.Background(), repo, []string{"mine.txt"}, "just mine")
	if err == nil {
		t.Fatal("committing a file the caller did not name is how a sibling's half-finished work lands in a release")
	}
	if !strings.Contains(err.Error(), "theirs.txt") {
		t.Fatalf("error must name the file it refused to sweep, got: %v", err)
	}
	if strings.Contains(err.Error(), "mine.txt") {
		t.Fatalf("the named path is not the problem and must not be reported as one: %v", err)
	}
}

// The important one: a conflicting rebase must leave the checkout exactly as it
// was found. Left mid-rebase, every later git command in a SHARED tree fails in
// a way nobody connects back to a deploy.
func TestDeploySyncAbortsRebaseOnConflict(t *testing.T) {
	repo := newDeployRepo(t)

	// A second clone publishes a conflicting change to main.
	other := filepath.Join(t.TempDir(), "other")
	originURL := deployGit(t, repo, "remote", "get-url", "github")
	deployGit(t, filepath.Dir(other), "clone", "-b", "main", originURL, other)
	if err := os.WriteFile(filepath.Join(other, "seed.txt"), []byte("remote wins\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployGit(t, other, "commit", "-am", "remote edit")
	deployGit(t, other, "push", "origin", "main")

	// We edit the same line locally, so the rebase must conflict.
	if err := os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("local wins\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployGit(t, repo, "commit", "-am", "local edit")

	_, err := deploySyncGit(context.Background(), repo, nil, "")
	if err == nil {
		t.Fatal("a conflicting rebase must fail the deploy, not invent a resolution")
	}
	if !strings.Contains(err.Error(), "seed.txt") {
		t.Fatalf("error must name the conflicted file, got: %v", err)
	}

	// The checkout must be usable: not mid-rebase, still on main.
	if _, statErr := os.Stat(filepath.Join(repo, ".git", "rebase-merge")); !os.IsNotExist(statErr) {
		t.Fatal("checkout left MID-REBASE — every later git command here fails for reasons nobody traces to a deploy")
	}
	if br := deployGit(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("checkout left on %q, not main", br)
	}
}

func TestDeploySyncFastForwardsWhenOnlyBehind(t *testing.T) {
	repo := newDeployRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	originURL := deployGit(t, repo, "remote", "get-url", "github")
	deployGit(t, filepath.Dir(other), "clone", "-b", "main", originURL, other)
	if err := os.WriteFile(filepath.Join(other, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployGit(t, other, "add", "new.txt")
	deployGit(t, other, "commit", "-m", "remote adds a file")
	deployGit(t, other, "push", "origin", "main")

	log, err := deploySyncGit(context.Background(), repo, nil, "")
	if err != nil {
		t.Fatalf("being merely behind is not a conflict: %v", err)
	}
	if !strings.Contains(strings.Join(log, "; "), "fast-forwarded") {
		t.Fatalf("expected a fast-forward, got steps: %v", log)
	}
	// An unconditional rebase would have rewritten history for no reason.
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatal("fast-forward did not bring the remote commit into the tree")
	}
}
