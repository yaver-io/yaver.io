package main

import (
	"strings"
	"testing"
)

// envMap collapses a KEY=VALUE slice to a map (last value wins, matching how
// the OS resolves a duplicated env var in a child process).
func envMap(kvs []string) map[string]string {
	m := map[string]string{}
	for _, kv := range kvs {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			m[kv[:eq]] = kv[eq+1:]
		}
	}
	return m
}

func TestRunnerProviderEnv_DefaultOAuthPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	// No runner-provider config → nil (runner uses its own OAuth creds).
	for _, runner := range []string{"claude", "codex", "opencode"} {
		if got := runnerProviderEnv(runner); got != nil {
			t.Fatalf("runner %q: expected nil on default OAuth path, got %v", runner, got)
		}
	}
}

func TestRunnerProviderEnv_AnthropicAndOpenAI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "BASE_URL", Value: "http://10.0.0.5:8000/v1"}); err != nil {
		t.Fatalf("set BASE_URL: %v", err)
	}
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "API_KEY", Value: "sk-local-xyz"}); err != nil {
		t.Fatalf("set API_KEY: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	// Claude → Anthropic protocol env.
	cl := envMap(runnerProviderEnv("claude"))
	if cl["ANTHROPIC_BASE_URL"] != "http://10.0.0.5:8000/v1" {
		t.Fatalf("claude ANTHROPIC_BASE_URL = %q", cl["ANTHROPIC_BASE_URL"])
	}
	if cl["ANTHROPIC_AUTH_TOKEN"] != "sk-local-xyz" {
		t.Fatalf("claude ANTHROPIC_AUTH_TOKEN = %q", cl["ANTHROPIC_AUTH_TOKEN"])
	}
	if _, ok := cl["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("claude must use AUTH_TOKEN, not the API-billing ANTHROPIC_API_KEY")
	}

	// Codex → OpenAI-compatible protocol env.
	cx := envMap(runnerProviderEnv("codex"))
	if cx["OPENAI_BASE_URL"] != "http://10.0.0.5:8000/v1" || cx["OPENAI_API_BASE"] != "http://10.0.0.5:8000/v1" {
		t.Fatalf("codex openai base urls = %q / %q", cx["OPENAI_BASE_URL"], cx["OPENAI_API_BASE"])
	}
	if cx["OPENAI_API_KEY"] != "sk-local-xyz" {
		t.Fatalf("codex OPENAI_API_KEY = %q", cx["OPENAI_API_KEY"])
	}
}

func TestRunnerProviderEnv_GLMBareKeyDefaultsToZai(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// A bare z.ai key (env) with NO runner-provider config is enough to
	// activate the glm runner against z.ai's default Anthropic endpoint.
	t.Setenv("ZAI_API_KEY", "zai-test-key")
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	glm := envMap(runnerProviderEnv("glm"))
	if glm["ANTHROPIC_BASE_URL"] != zaiDefaultAnthropicBaseURL {
		t.Fatalf("glm ANTHROPIC_BASE_URL = %q, want z.ai default", glm["ANTHROPIC_BASE_URL"])
	}
	if glm["ANTHROPIC_AUTH_TOKEN"] != "zai-test-key" {
		t.Fatalf("glm ANTHROPIC_AUTH_TOKEN = %q", glm["ANTHROPIC_AUTH_TOKEN"])
	}
	if _, ok := glm["OPENAI_BASE_URL"]; ok {
		t.Fatalf("glm must speak the Anthropic protocol, not OpenAI")
	}
	// The z.ai key must NOT leak into the real-Anthropic claude runner.
	if got := runnerProviderEnv("claude"); got != nil {
		t.Fatalf("claude must stay on its own OAuth path, got %v", got)
	}
	// Status detection sees the key.
	if st := detectGLMStatus(); !st.AuthConfigured {
		t.Fatalf("detectGLMStatus AuthConfigured = false, want true")
	}
}

func TestRunnerProviderEnv_GLMExplicitBaseURLOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("ZAI_API_KEY", "zai-test-key")
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	// An explicit per-runner base URL wins over the z.ai default.
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "BASE_URL__glm", Value: "http://localhost:8080/anthropic"}); err != nil {
		t.Fatalf("set BASE_URL__glm: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	glm := envMap(runnerProviderEnv("glm"))
	if glm["ANTHROPIC_BASE_URL"] != "http://localhost:8080/anthropic" {
		t.Fatalf("glm BASE_URL override not applied: %q", glm["ANTHROPIC_BASE_URL"])
	}
}

func TestRunnerProviderEnv_PerRunnerOverrideAndKeylessOllama(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	// Shared default points everything at a gateway with a key...
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "BASE_URL", Value: "http://gateway:8080/v1"}); err != nil {
		t.Fatalf("set BASE_URL: %v", err)
	}
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "API_KEY", Value: "gw-key"}); err != nil {
		t.Fatalf("set API_KEY: %v", err)
	}
	// ...but codex is pinned to a local keyless Ollama via per-runner override.
	if err := vs.Set(VaultEntry{Project: runnerProviderVaultProject, Name: "BASE_URL__codex", Value: "http://localhost:11434/v1"}); err != nil {
		t.Fatalf("set BASE_URL__codex: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	cx := envMap(runnerProviderEnv("codex"))
	if cx["OPENAI_BASE_URL"] != "http://localhost:11434/v1" {
		t.Fatalf("codex per-runner override not applied: %q", cx["OPENAI_BASE_URL"])
	}
	// API_KEY has no per-runner override, so the shared key still flows. A truly
	// keyless Ollama is configured by simply not setting any API_KEY.
	if cx["OPENAI_API_KEY"] != "gw-key" {
		t.Fatalf("codex inherited shared key = %q", cx["OPENAI_API_KEY"])
	}

	// Claude still uses the shared gateway.
	cl := envMap(runnerProviderEnv("claude"))
	if cl["ANTHROPIC_BASE_URL"] != "http://gateway:8080/v1" {
		t.Fatalf("claude shared base = %q", cl["ANTHROPIC_BASE_URL"])
	}
}
