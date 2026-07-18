package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The MCP command path must survive an agent update.
//
// findYaverBinary used to call filepath.EvalSymlinks unconditionally, which
// turns ~/.yaver/bin/current/<platform>/yaver into ~/.yaver/bin/1.99.311/... —
// pinning a runner's MCP config to whichever version was live at setup time.
// Auto-update then installs 1.99.312, repoints `current`, and the runner says:
//
//	MCP client for `yaver` failed to start: handshaking with MCP server
//	failed: connection closed
//
// Seen on a real box the same day it went 1.99.311 -> 1.99.312. The symlink
// exists so callers need not care about versions; resolving it throws away the
// only property that mattered.

func TestStableCounterpartRewritesVersionedPathOntoCurrent(t *testing.T) {
	home := t.TempDir()
	plat := "darwin-arm64"
	versioned := filepath.Join(home, ".yaver", "bin", "1.99.311", plat, "yaver")
	stable := filepath.Join(home, ".yaver", "bin", "current", plat, "yaver")

	for _, p := range []string{versioned, stable} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := stableCounterpart(versioned)
	if got != stable {
		t.Fatalf("stableCounterpart(%q)\n  = %q\n want %q — a versioned path must be "+
			"rewritten onto `current`, or the MCP entry dies on the next update",
			versioned, got, stable)
	}
}

// If `current` does not exist, the versioned path is all there is. Inventing a
// stable path that isn't there would write a config pointing at nothing —
// strictly worse than a pin that at least works today.
func TestStableCounterpartRefusesWhenCurrentMissing(t *testing.T) {
	home := t.TempDir()
	versioned := filepath.Join(home, ".yaver", "bin", "1.99.311", "darwin-arm64", "yaver")
	if err := os.MkdirAll(filepath.Dir(versioned), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(versioned, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := stableCounterpart(versioned); got != "" {
		t.Errorf("expected \"\" when bin/current is absent, got %q — that path does not exist", got)
	}
}

// A path already on `current` must be returned untouched, not re-mapped.
func TestStableCounterpartLeavesCurrentAlone(t *testing.T) {
	p := "/Users/someone/.yaver/bin/current/darwin-arm64/yaver"
	if got := stableCounterpart(p); got != "" {
		t.Errorf("a `current` path needs no rewrite; got %q", got)
	}
}

// Dev builds, $PATH shims, Homebrew — anything outside ~/.yaver/bin must be
// left to EvalSymlinks, where the resolved location IS the honest answer.
func TestStableCounterpartIgnoresNonYaverInstalls(t *testing.T) {
	for _, p := range []string{
		"/usr/local/bin/yaver",
		"/opt/homebrew/bin/yaver",
		"/Users/dev/Workspace/yaver.io/desktop/agent/agent",
		"",
	} {
		if got := stableCounterpart(p); got != "" {
			t.Errorf("stableCounterpart(%q) = %q, want \"\" — only ~/.yaver/bin installs are remapped", p, got)
		}
	}
}

// The constant must keep the surrounding slashes: a bare ".yaver/bin/current"
// would substring-match paths it has no business claiming.
func TestStableDirConstantIsAnchored(t *testing.T) {
	if !strings.HasPrefix(stableYaverBinaryDir, "/") || !strings.HasSuffix(stableYaverBinaryDir, "/") {
		t.Errorf("stableYaverBinaryDir = %q — must be slash-anchored on both ends to avoid "+
			"matching unrelated paths", stableYaverBinaryDir)
	}
}

// findYaverBinary must never return an empty string: an empty MCP command
// silently produces a config that can never start.
func TestFindYaverBinaryAlwaysReturnsSomething(t *testing.T) {
	if got := findYaverBinary(); strings.TrimSpace(got) == "" {
		t.Error("findYaverBinary returned empty — the MCP entry would be unstartable")
	}
}
