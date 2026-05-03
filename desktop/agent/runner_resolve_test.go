package main

// Tests resolveRunnerBinary's layered search: PATH first, then well-known
// per-user install dirs, then login-shell `command -v`. The systemd-PATH
// gotcha (claude/codex installed via npm-global but not on the agent's
// PATH) was the actual blocker behind /runner-auth/browser/start failing
// for both mobile and CLI clients.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestResolveRunnerBinary_FallsBackBeyondPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on POSIX permission bits")
	}

	// Spin up a fake "claude" installed only in <home>/.npm-global/bin so
	// PATH lookup will miss it. The resolver should still find it via the
	// well-known-dir fallback.
	tmpHome := t.TempDir()
	binDir := filepath.Join(tmpHome, ".npm-global", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir binDir: %v", err)
	}
	fakeClaude := filepath.Join(binDir, "yaver-test-fake-claude-bin")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	t.Setenv("HOME", tmpHome)
	t.Setenv("PATH", "/this/dir/does/not/exist")
	runnerResolveCache = sync.Map{}

	got := resolveRunnerBinary("yaver-test-fake-claude-bin")
	if got != fakeClaude {
		t.Fatalf("expected resolver to find %s via ~/.npm-global/bin fallback, got %q", fakeClaude, got)
	}
}

func TestResolveRunnerBinary_PreferPathHit(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	pathClaude := filepath.Join(binDir, "yaver-test-fake-codex-bin")
	if err := os.WriteFile(pathClaude, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PATH", binDir)
	runnerResolveCache = sync.Map{}

	got := resolveRunnerBinary("yaver-test-fake-codex-bin")
	if got != pathClaude {
		t.Fatalf("expected PATH hit %s, got %q", pathClaude, got)
	}
}

func TestResolveRunnerBinary_MissingReturnsEmpty(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	runnerResolveCache = sync.Map{}

	got := resolveRunnerBinary("yaver-binary-that-definitely-does-not-exist-anywhere-12345")
	if got != "" {
		t.Fatalf("expected empty for missing binary, got %q", got)
	}
}

func TestRunnerCandidatePaths_IncludesWellKnownDirs(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	got := runnerCandidatePaths("claude")
	wantSubstrings := []string{
		filepath.Join(tmpHome, ".npm-global", "bin", "claude"),
		filepath.Join(tmpHome, ".bun", "bin", "claude"),
		filepath.Join(tmpHome, ".local", "bin", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	}
	joined := strings.Join(got, "\n")
	for _, want := range wantSubstrings {
		if !strings.Contains(joined, want) {
			t.Errorf("candidate paths missing %q\ngot:\n%s", want, joined)
		}
	}
}
