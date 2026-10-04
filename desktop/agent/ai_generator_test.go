package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeGeneratorStub(t *testing.T, dir, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPickAIGeneratorCLIDefaultsToOpenCode(t *testing.T) {
	bin := t.TempDir()
	writeGeneratorStub(t, bin, "claude")
	writeGeneratorStub(t, bin, "codex")
	writeGeneratorStub(t, bin, "opencode")
	t.Setenv("PATH", bin)

	if got := pickAIGeneratorCLI(AIGeneratorSpec{}); got != "opencode" {
		t.Fatalf("default generator = %q, want opencode", got)
	}
}

func TestPickAIGeneratorCLIHonorsExplicitRunner(t *testing.T) {
	bin := t.TempDir()
	writeGeneratorStub(t, bin, "claude")
	writeGeneratorStub(t, bin, "opencode")
	t.Setenv("PATH", bin)

	if got := pickAIGeneratorCLI(AIGeneratorSpec{Runner: "claude-code"}); got != "claude" {
		t.Fatalf("explicit generator = %q, want claude", got)
	}
}
