package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRunnerRuntimeStatusCodexEnvKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CODEX_HOME", t.TempDir())

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("codex"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected codex to be ready, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected codex auth to be detected")
	}
	if status.AuthSource != "OPENAI_API_KEY" {
		t.Fatalf("expected OPENAI_API_KEY auth source, got %q", status.AuthSource)
	}
}

func TestDetectRunnerRuntimeStatusCodexAuthFile(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"access_token":"test"}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("codex"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected codex auth file to make runner ready, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected codex auth file to be detected")
	}
	if !strings.HasSuffix(status.AuthSource, "auth.json") {
		t.Fatalf("expected auth.json source, got %q", status.AuthSource)
	}
}

func TestDetectRunnerRuntimeStatusOpenCodeAllowsOpenAIOAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	xdgData := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", xdgData)
	authDir := filepath.Join(xdgData, "opencode")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"openai":{"type":"oauth","token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("opencode"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected opencode openai oauth to be allowed, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected opencode auth to be detected")
	}
}

func TestDetectRunnerRuntimeStatusOpenCodeBlocksAnthropicOAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	xdgData := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", xdgData)
	authDir := filepath.Join(xdgData, "opencode")
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(`{"anthropic":{"type":"oauth","token":"x"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("opencode"), t.TempDir())
	if status.Ready {
		t.Fatalf("expected opencode anthropic oauth to be blocked")
	}
	if !strings.Contains(strings.ToLower(status.Error), "direct `claude` runner") {
		t.Fatalf("expected direct-claude guidance, got %q", status.Error)
	}
}
