package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectRunnerRuntimeStatusCodexEnvKey(t *testing.T) {
	stubCodexLinuxSandboxPrereq(t, "")
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
	stubCodexLinuxSandboxPrereq(t, "")
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

func TestDetectRunnerRuntimeStatusCodexVaultKey(t *testing.T) {
	stubCodexLinuxSandboxPrereq(t, "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_HOME", t.TempDir())
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "OPENAI_API_KEY", Category: "api-key", Value: "vault-openai-key"}); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	setRuntimeVaultStore(vs)
	defer setRuntimeVaultStore(nil)

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("codex"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected codex vault auth to make runner ready, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected codex vault auth to be detected")
	}
	if status.AuthSource != "vault:OPENAI_API_KEY" {
		t.Fatalf("expected vault OPENAI_API_KEY auth source, got %q", status.AuthSource)
	}
}

func stubCodexLinuxSandboxPrereq(t *testing.T, err string) {
	t.Helper()
	prev := codexLinuxSandboxPrereqErrorFunc
	codexLinuxSandboxPrereqErrorFunc = func() string { return err }
	t.Cleanup(func() {
		codexLinuxSandboxPrereqErrorFunc = prev
	})
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

func TestDetectRunnerRuntimeStatusOpenCodeGLMEnvKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLM_API_KEY", "glm-test")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("opencode"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected opencode GLM env key to be allowed, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected opencode GLM auth to be detected")
	}
	if status.AuthSource != "GLM_API_KEY" {
		t.Fatalf("expected GLM_API_KEY auth source, got %q", status.AuthSource)
	}
}

func TestDetectRunnerRuntimeStatusOpenCodeZAIVaultKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GLM_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "ZAI_API_KEY", Category: "api-key", Value: "vault-zai-key"}); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	setRuntimeVaultStore(vs)
	defer setRuntimeVaultStore(nil)

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("opencode"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected opencode ZAI vault auth to be allowed, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected opencode ZAI vault auth to be detected")
	}
	if status.AuthSource != "vault:ZAI_API_KEY" {
		t.Fatalf("expected vault:ZAI_API_KEY auth source, got %q", status.AuthSource)
	}
}

func TestDetectRunnerRuntimeStatusOpenCodeCustomProviderConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GLM_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	xdgConfig := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	cfgPath := filepath.Join(xdgConfig, "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	raw := `{
  "provider": {
    "my-gateway": {
      "options": {
        "baseURL": "https://llm.example.com/v1",
        "apiKey": "sk-test"
      }
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write opencode config: %v", err)
	}

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("opencode"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected custom provider config to be allowed, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected custom provider config to be detected")
	}
	if status.AuthSource != cfgPath {
		t.Fatalf("expected auth source %q, got %q", cfgPath, status.AuthSource)
	}
}

func TestDetectRunnerRuntimeStatusOpenCodeRemoteOllamaConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GLM_API_KEY", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	xdgConfig := filepath.Join(t.TempDir(), "config")
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	cfgPath := filepath.Join(xdgConfig, "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	raw := `{
  "provider": {
    "ollama": {
      "options": {
        "baseURL": "http://100.64.0.12:11434/v1"
      }
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write opencode config: %v", err)
	}

	status := DetectRunnerRuntimeStatus(GetRunnerConfig("opencode"), t.TempDir())
	if !status.Ready {
		t.Fatalf("expected remote ollama config to be allowed, got error: %s", status.Error)
	}
	if !status.AuthConfigured {
		t.Fatalf("expected remote ollama config to be detected")
	}
	if status.AuthSource != "local provider config" {
		t.Fatalf("expected local provider config source, got %q", status.AuthSource)
	}
}
