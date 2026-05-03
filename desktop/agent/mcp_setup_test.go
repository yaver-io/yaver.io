package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveCodexMCPConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("filesystem stub test")
	}

	home := t.TempDir()
	stubDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatalf("mkdir stub dir: %v", err)
	}
	logPath := filepath.Join(home, "codex-remove-args.txt")
	script := "#!/bin/sh\n" +
		"set -eu\n" +
		"case \"$1 ${2-} ${3-}\" in\n" +
		"  \"mcp get yaver\") exit 0 ;;\n" +
		"  \"mcp remove yaver\") printf '%s' \"$*\" > \"" + logPath + "\" ;;\n" +
		"  *) exit 0 ;;\n" +
		"esac\n"
	codexPath := filepath.Join(stubDir, "codex")
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write codex stub: %v", err)
	}

	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	changed, err := removeCodexMCPConfig()
	if err != nil {
		t.Fatalf("removeCodexMCPConfig: %v", err)
	}
	if !changed {
		t.Fatal("expected codex MCP config to be removed")
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read remove args: %v", err)
	}
	if !strings.Contains(string(args), "mcp remove yaver") {
		t.Fatalf("unexpected codex remove args: %q", string(args))
	}
}

func TestRemoveOpenCodeMCPConfig(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	before := `{
  "theme": "dark",
  "mcp": {
    "yaver": {
      "type": "local",
      "command": ["yaver", "mcp"],
      "enabled": true
    },
    "other": {
      "type": "local",
      "command": ["other"]
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(before), 0o644); err != nil {
		t.Fatalf("write opencode config: %v", err)
	}

	t.Setenv("HOME", home)
	changed, err := removeOpenCodeMCPConfig()
	if err != nil {
		t.Fatalf("removeOpenCodeMCPConfig: %v", err)
	}
	if !changed {
		t.Fatal("expected opencode MCP config to change")
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read opencode config: %v", err)
	}
	text := string(after)
	if strings.Contains(text, `"yaver"`) {
		t.Fatalf("yaver entry still present: %s", text)
	}
	if !strings.Contains(text, `"other"`) {
		t.Fatalf("expected non-yaver MCP entries to remain: %s", text)
	}
}
