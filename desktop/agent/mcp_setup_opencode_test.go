package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Existing OpenCode users may have paid/provider-specific configuration such
// as DeepSeek. MCP setup owns only mcp.yaver; changing model/provider/default
// agent while pairing Yaver would turn a plug-and-play install into data loss.
func TestEnsureOpenCodeMCPConfigPreservesRunnerSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX executable")
	}
	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))

	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	before := map[string]any{
		"model":        "deepseek/deepseek-flash",
		"defaultAgent": "build",
		"provider": map[string]any{
			"deepseek": map[string]any{"options": map[string]any{"baseURL": "https://example.invalid/v1"}},
		},
	}
	raw, _ := json.Marshal(before)
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := ensureOpenCodeMCPConfig("/stable/yaver")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected Yaver MCP entry to be added")
	}

	var after map[string]any
	raw, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after["model"] != before["model"] || after["defaultAgent"] != before["defaultAgent"] {
		t.Fatalf("OpenCode selection changed: before=%v after=%v", before, after)
	}
	provider, ok := after["provider"].(map[string]any)
	if !ok || provider["deepseek"] == nil {
		t.Fatalf("DeepSeek provider was lost: %v", after)
	}
	mcp, ok := after["mcp"].(map[string]any)
	if !ok || mcp["yaver"] == nil {
		t.Fatalf("Yaver MCP entry missing: %v", after)
	}
}
