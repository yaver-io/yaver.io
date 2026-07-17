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

// The trust dialog is a SECOND blocking first-run screen, independent of
// onboarding. A config that has completed onboarding still stops dead in a
// directory Claude has not seen.

func TestEnsureClaudeFolderTrustedMarksTheWorkDir(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	if err := ensureClaudeFolderTrusted(home, work); err != nil {
		t.Fatal(err)
	}
	cfg := readClaudeConfig(t, filepath.Join(home, ".claude.json"))
	projects, _ := cfg["projects"].(map[string]any)
	entry, _ := projects[work].(map[string]any)
	if entry == nil {
		t.Fatalf("no projects entry for %s; got %v", work, projects)
	}
	if trusted, _ := entry["hasTrustDialogAccepted"].(bool); !trusted {
		t.Fatalf("hasTrustDialogAccepted not set for %s: %v", work, entry)
	}
}

// Trusting one worktree must not forget the user's real projects, their MCP
// servers, or their onboarding state.
func TestEnsureClaudeFolderTrustedPreservesEverythingElse(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	seed := map[string]any{
		"hasCompletedOnboarding": true,
		"oauthAccount":           map[string]any{"emailAddress": "someone@example.com"},
		"projects": map[string]any{
			"/Users/x/Workspace/real": map[string]any{
				"hasTrustDialogAccepted": true,
				"mcpServers":             map[string]any{"yaver": map[string]any{}},
			},
		},
	}
	data, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeFolderTrusted(home, work); err != nil {
		t.Fatal(err)
	}
	cfg := readClaudeConfig(t, path)
	if done, _ := cfg["hasCompletedOnboarding"].(bool); !done {
		t.Fatal("dropped hasCompletedOnboarding")
	}
	if cfg["oauthAccount"] == nil {
		t.Fatal("dropped oauthAccount — that is the user's credential binding")
	}
	projects, _ := cfg["projects"].(map[string]any)
	real, _ := projects["/Users/x/Workspace/real"].(map[string]any)
	if real == nil || real["mcpServers"] == nil {
		t.Fatalf("clobbered an existing project entry: %v", projects)
	}
	if _, ok := projects[work]; !ok {
		t.Fatalf("did not add the new workDir: %v", projects)
	}
}

func TestEnsureClaudeFolderTrustedIsIdempotent(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	if err := ensureClaudeFolderTrusted(home, work); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeFolderTrusted(home, work); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("second call rewrote an already-trusted config")
	}
}

// Same reasoning as onboarding: a config we cannot parse is not ours to
// rewrite. A blocked TUI is recoverable; a destroyed ~/.claude.json is not.
func TestEnsureClaudeFolderTrustedRefusesToClobberUnparseableConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureClaudeFolderTrusted(home, t.TempDir()); err == nil {
		t.Fatal("expected a parse error rather than a clobbered config")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{not json" {
		t.Fatalf("config was modified despite the parse failure: %q", data)
	}
}
