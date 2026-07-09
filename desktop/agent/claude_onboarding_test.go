package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readClaudeConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

// A machine that has never run `claude` has no config at all. That is exactly
// the box Yaver provisions, and the one whose TUI opened on a browser login.
func TestEnsureClaudeOnboardingCreatesMarker(t *testing.T) {
	home := t.TempDir()
	if err := ensureClaudeOnboardingComplete(home); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cfg := readClaudeConfig(t, filepath.Join(home, ".claude.json"))
	if cfg["hasCompletedOnboarding"] != true {
		t.Fatalf("hasCompletedOnboarding = %v, want true", cfg["hasCompletedOnboarding"])
	}
}

// The file also holds project history and MCP config. Setting the flag must
// never cost the user those.
func TestEnsureClaudeOnboardingPreservesExistingKeys(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	original := `{"userID":"abc","mcpServers":{"vercel":{"url":"https://x"}},"numStartups":7}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeOnboardingComplete(home); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cfg := readClaudeConfig(t, path)
	if cfg["hasCompletedOnboarding"] != true {
		t.Error("flag not set")
	}
	if cfg["userID"] != "abc" {
		t.Errorf("userID lost: %v", cfg["userID"])
	}
	if cfg["numStartups"] != float64(7) {
		t.Errorf("numStartups lost: %v", cfg["numStartups"])
	}
	mcp, ok := cfg["mcpServers"].(map[string]any)
	if !ok || mcp["vercel"] == nil {
		t.Errorf("mcpServers lost: %v", cfg["mcpServers"])
	}
}

func TestEnsureClaudeOnboardingIsIdempotent(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := ensureClaudeOnboardingComplete(home); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A second call must short-circuit rather than rewrite: `claude` may be
	// reading the file, and a needless write is a needless race.
	if err := ensureClaudeOnboardingComplete(home); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(info.ModTime()) {
		t.Error("already-onboarded config was rewritten; the call should be a no-op")
	}
	if perm := after.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// A config we cannot parse might use a shape a newer Claude understands.
// Losing it is far worse than showing the wizard once.
func TestEnsureClaudeOnboardingRefusesToClobberUnparseableConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeOnboardingComplete(home); err == nil {
		t.Fatal("expected an error rather than a clobbered config")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{not json" {
		t.Fatalf("config was modified: %q (%v)", string(data), err)
	}
}

// With CLAUDE_CONFIG_DIR set, Claude reads <dir>/.claude.json and ignores
// $HOME/.claude.json entirely. Writing the marker to the wrong file would look
// like a fix and change nothing.
func TestClaudeConfigJSONPathHonorsConfigDir(t *testing.T) {
	home := t.TempDir()
	if got, want := claudeConfigJSONPath(home), filepath.Join(home, ".claude.json"); got != want {
		t.Errorf("default path = %q, want %q", got, want)
	}
	cfgDir := filepath.Join(home, "custom")
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	if got, want := claudeConfigJSONPath(home), filepath.Join(cfgDir, ".claude.json"); got != want {
		t.Errorf("CLAUDE_CONFIG_DIR path = %q, want %q", got, want)
	}
	if err := ensureClaudeOnboardingComplete(home); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, ".claude.json")); err != nil {
		t.Errorf("marker not written into CLAUDE_CONFIG_DIR: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Error("marker must not be written to $HOME when CLAUDE_CONFIG_DIR is set")
	}
}
