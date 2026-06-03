package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildRunnerArgs_CodexCdInjection — codex's workspace-write
// sandbox treats the cwd's project path as Read-only by default.
// We inject `-C <workDir>` after `exec` so codex adds the project
// to its writable allowlist. Without this, apply_patch + inplace
// shell edits fail with "writing outside of the project / Read-only
// file system" (confirmed in user's e2e screenshot).
func TestBuildRunnerArgs_CodexCdInjection(t *testing.T) {
	codex := builtinRunners["codex"]
	if codex.RunnerID != "codex" {
		t.Fatalf("codex runner config not found")
	}

	t.Run("with workDir injects -C", func(t *testing.T) {
		args := buildRunnerArgsWithWorkDir(codex, "do thing",
			"/root/Workspace/yaver.io/demo/mobile/todo-rn")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-C /root/Workspace/yaver.io/demo/mobile/todo-rn") {
			t.Fatalf("expected -C <workDir> in args, got: %v", args)
		}
		// -C must come AFTER `exec` so codex's subcommand parser sees it.
		var execIdx, cIdx = -1, -1
		for i, a := range args {
			if a == "exec" {
				execIdx = i
			}
			if a == "-C" {
				cIdx = i
			}
		}
		if execIdx == -1 || cIdx == -1 {
			t.Fatalf("missing exec or -C in args: %v", args)
		}
		if cIdx <= execIdx {
			t.Fatalf("-C must come after `exec`; got exec=%d, -C=%d in %v", execIdx, cIdx, args)
		}
	})

	t.Run("empty workDir skips injection (legacy callers)", func(t *testing.T) {
		args := buildRunnerArgs(codex, "do thing")
		for _, a := range args {
			if a == "-C" {
				t.Fatalf("did not expect -C in args when workDir is empty: %v", args)
			}
		}
	})

	t.Run("non-codex runners ignore -C injection", func(t *testing.T) {
		claude := builtinRunners["claude"]
		args := buildRunnerArgsWithWorkDir(claude, "do thing", "/some/dir")
		for _, a := range args {
			if a == "-C" {
				t.Fatalf("claude should not get -C injection: %v", args)
			}
		}
	})

	t.Run("claude omits --skip-git-repo-check (codex-only flag; claude-cli rejects it)", func(t *testing.T) {
		// claude-cli 2.x rejects --skip-git-repo-check ("error: unknown
		// option") and crashes, so builtinRunners["claude"] intentionally
		// drops it — the flag is codex-only. Claude runs fine in non-git
		// dirs without it. (Previously this asserted the opposite, which
		// went stale when the flag was removed from the claude config.)
		claude := builtinRunners["claude"]
		args := buildRunnerArgs(claude, "run ls")
		if containsArg(args, "--skip-git-repo-check") {
			t.Fatalf("claude must NOT receive --skip-git-repo-check (codex-only): %v", args)
		}
	})

	t.Run("workDir trimmed of whitespace", func(t *testing.T) {
		args := buildRunnerArgsWithWorkDir(codex, "do thing", "  /a/b  ")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-C /a/b") {
			t.Fatalf("expected trimmed -C /a/b, got: %v", args)
		}
	})
}

// TestBuildRunnerArgs_CodexSkipGitRepoCheck — codex 0.123.0 aborts
// with "Not inside a trusted directory" when spawned from a non-git
// cwd (e.g. /root on yaver-test-ephemeral). We auto-inject
// --skip-git-repo-check only in that case so real-repo workdirs
// keep their workspace-write detection (which the flag suppresses).
func TestBuildRunnerArgs_CodexSkipGitRepoCheck(t *testing.T) {
	codex := builtinRunners["codex"]

	t.Run("non-git workDir gets --skip-git-repo-check", func(t *testing.T) {
		// macOS resolves /tmp via /private/tmp symlink, but t.TempDir
		// returns the resolved path, so the parent walk in
		// isInsideGitRepo doesn't escape the tmp jail.
		nonRepo := t.TempDir()
		args := buildRunnerArgsWithWorkDir(codex, "Run ls", nonRepo)
		if !containsArg(args, "--skip-git-repo-check") {
			t.Fatalf("expected --skip-git-repo-check for non-git workDir, got: %v", args)
		}
		// Flag must come AFTER `exec` so codex's subcommand parser sees it.
		var execIdx, flagIdx = -1, -1
		for i, a := range args {
			if a == "exec" {
				execIdx = i
			}
			if a == "--skip-git-repo-check" {
				flagIdx = i
			}
		}
		if flagIdx <= execIdx {
			t.Fatalf("--skip-git-repo-check must come after `exec`; got exec=%d, flag=%d in %v",
				execIdx, flagIdx, args)
		}
	})

	t.Run("git workDir omits --skip-git-repo-check", func(t *testing.T) {
		repo := t.TempDir()
		// .git as a directory satisfies isInsideGitRepo's Lstat probe;
		// no need to actually init a git repo for this check.
		if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}
		args := buildRunnerArgsWithWorkDir(codex, "Run ls", repo)
		if containsArg(args, "--skip-git-repo-check") {
			t.Fatalf("did not expect --skip-git-repo-check when inside a git repo: %v", args)
		}
	})

	t.Run("git ancestor still counts as inside repo", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatalf("mkdir .git: %v", err)
		}
		nested := filepath.Join(repo, "src", "components")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("mkdir nested: %v", err)
		}
		args := buildRunnerArgsWithWorkDir(codex, "Run ls", nested)
		if containsArg(args, "--skip-git-repo-check") {
			t.Fatalf("nested dir of git repo should not get --skip-git-repo-check: %v", args)
		}
	})

	t.Run("non-codex runners are not affected", func(t *testing.T) {
		opencode := builtinRunners["opencode"]
		nonRepo := t.TempDir()
		args := buildRunnerArgsWithWorkDir(opencode, "Run ls", nonRepo)
		// opencode never gets --skip-git-repo-check (codex-only flag).
		if containsArg(args, "--skip-git-repo-check") {
			t.Fatalf("opencode should not receive --skip-git-repo-check: %v", args)
		}
	})
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
