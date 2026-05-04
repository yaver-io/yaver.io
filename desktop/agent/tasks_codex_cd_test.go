package main

import (
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

	t.Run("claude includes git trust bypass for non-repo mobile tasks", func(t *testing.T) {
		claude := builtinRunners["claude"]
		args := buildRunnerArgs(claude, "run ls")
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--skip-git-repo-check") {
			t.Fatalf("expected claude args to include --skip-git-repo-check, got: %v", args)
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
