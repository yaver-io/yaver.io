package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOpenCodeConfigSummaryParsesJSONCAndModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("OPENCODE_AUTH", "")

	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{
  // top-level defaults
  "$schema": "https://opencode.ai/config.json",
  "default_agent": "plan",
  "model": "ollama/qwen2.5-coder:14b",
  "small_model": "ollama/qwen2.5-coder:1.5b",
  "provider": {
    "ollama": {
      "name": "Remote Ollama",
      "options": {
        "baseURL": "http://127.0.0.1:11434/v1",
      },
      "models": {
        "qwen2.5-coder:14b": { "name": "Qwen 14B" },
        "qwen2.5-coder:1.5b": { "name": "Qwen 1.5B" },
      },
    },
  },
  "agent": {
    "build": { "model": "ollama/qwen2.5-coder:14b" },
    "plan": { "model": "ollama/qwen2.5-coder:1.5b" },
  },
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := loadOpenCodeConfigSummary()
	if err != nil {
		t.Fatalf("loadOpenCodeConfigSummary: %v", err)
	}
	if !summary.Exists {
		t.Fatal("expected config to exist")
	}
	if summary.DefaultAgent != "plan" {
		t.Fatalf("DefaultAgent = %q, want plan", summary.DefaultAgent)
	}
	if summary.BuildModel != "ollama/qwen2.5-coder:14b" {
		t.Fatalf("BuildModel = %q", summary.BuildModel)
	}
	if summary.PlanModel != "ollama/qwen2.5-coder:1.5b" {
		t.Fatalf("PlanModel = %q", summary.PlanModel)
	}
	if len(summary.Models) < 2 {
		t.Fatalf("expected discovered models, got %#v", summary.Models)
	}
	if summary.Models[0].ID != "ollama/qwen2.5-coder:14b" || !summary.Models[0].IsDefault {
		t.Fatalf("unexpected first model %#v", summary.Models[0])
	}
}

// TestProviderSummary_HasAPIKey — the web/mobile composer renders the
// provider chip rail with a "✓ Key configured" badge based on the
// provider summary's HasAPIKey boolean. We never expose the key value
// itself; only the boolean flips. Verifies both the "key set" and "key
// absent" cases so we don't regress the no-key Ollama path that's
// already shipping ("Use Ollama" button + no input).
func TestProviderSummary_HasAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")

	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "zai": {
      "name": "Z.ai Coding Plan",
      "options": {
        "baseURL": "https://api.z.ai/api/coding/paas/v4",
        "apiKey": "secret-not-leaked-to-summary"
      }
    },
    "ollama": {
      "name": "Local Ollama",
      "options": {
        "baseURL": "http://127.0.0.1:11434/v1"
      }
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := loadOpenCodeConfigSummary()
	if err != nil {
		t.Fatalf("loadOpenCodeConfigSummary: %v", err)
	}
	byID := map[string]OpenCodeProviderSummary{}
	for _, p := range summary.Providers {
		byID[p.ID] = p
	}
	if !byID["zai"].HasAPIKey {
		t.Errorf("expected zai.HasAPIKey=true (apiKey is set in opencode.json)")
	}
	if byID["ollama"].HasAPIKey {
		t.Errorf("expected ollama.HasAPIKey=false (no apiKey configured)")
	}
	// Defense-in-depth: HasAPIKey must NEVER carry the key value into
	// any field on the summary — boolean only. Round-trip the full
	// summary through JSON so we catch any new field that might leak it.
	blob, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if strings.Contains(string(blob), "secret-not-leaked-to-summary") {
		t.Fatalf("opencode summary leaked apiKey: %s", string(blob))
	}
}

func TestApplyOpenCodeConfigPatchCreatesAndUpdatesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")

	defaultAgent := "build"
	model := "openai/gpt-5"
	planModel := "openai/gpt-5-mini"
	buildModel := "ollama/qwen2.5-coder:14b"
	smallModel := "ollama/qwen2.5-coder:1.5b"
	summary, err := applyOpenCodeConfigPatch(openCodeConfigPatch{
		DefaultAgent: &defaultAgent,
		Model:        &model,
		SmallModel:   &smallModel,
		BuildModel:   &buildModel,
		PlanModel:    &planModel,
	})
	if err != nil {
		t.Fatalf("applyOpenCodeConfigPatch: %v", err)
	}
	if !summary.Exists {
		t.Fatal("expected created config to exist")
	}
	if summary.DefaultAgent != defaultAgent || summary.Model != model || summary.PlanModel != planModel {
		t.Fatalf("unexpected summary %#v", summary)
	}
	if st, err := os.Stat(summary.Path); err != nil {
		t.Fatalf("stat created config: %v", err)
	} else if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("created config mode = %o, want 600", got)
	}

	cleared := ""
	summary, err = applyOpenCodeConfigPatch(openCodeConfigPatch{
		DefaultAgent: &cleared,
		PlanModel:    &cleared,
	})
	if err != nil {
		t.Fatalf("clear patch: %v", err)
	}
	if summary.DefaultAgent != "" {
		t.Fatalf("expected default agent cleared, got %q", summary.DefaultAgent)
	}
	if summary.PlanModel != "" {
		t.Fatalf("expected plan model cleared, got %q", summary.PlanModel)
	}
}

func TestApplyOpenCodeConfigPatchSyncsAuthStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("OPENCODE_AUTH", "")

	defaultAgent := "build"
	model := "zai-coding-plan/glm-4.7"
	secret := "secret-not-leaked-to-summary"
	summary, err := applyOpenCodeConfigPatch(openCodeConfigPatch{
		DefaultAgent: &defaultAgent,
		Model:        &model,
		BuildModel:   &model,
		PlanModel:    &model,
		Providers: []openCodeProviderPatch{{
			ID:      "zai-coding-plan",
			Name:    "Zai Coding Plan",
			BaseURL: "https://api.zai.ai/v1",
			APIKey:  secret,
			Models: map[string]any{
				"glm-4.7": map[string]any{"name": "GLM 4.7 Coding Plan", "tools": true},
			},
		}},
	})
	if err != nil {
		t.Fatalf("applyOpenCodeConfigPatch: %v", err)
	}
	authPath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth store: %v", err)
	}
	if st, err := os.Stat(authPath); err != nil {
		t.Fatalf("stat auth store: %v", err)
	} else if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("auth store mode = %o, want 600", got)
	}
	var auth map[string]map[string]string
	if err := json.Unmarshal(raw, &auth); err != nil {
		t.Fatalf("unmarshal auth store: %v", err)
	}
	if got := auth["zai-coding-plan"]["type"]; got != "api" {
		t.Fatalf("auth type = %q, want api", got)
	}
	if got := auth["zai-coding-plan"]["key"]; got != secret {
		t.Fatalf("auth key was not written to OpenCode auth store")
	}
	blob, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatalf("opencode summary leaked apiKey: %s", string(blob))
	}
	if len(summary.Providers) != 1 || !summary.Providers[0].HasAPIKey {
		t.Fatalf("expected provider hasApiKey summary, got %#v", summary.Providers)
	}
}

func TestLoadOpenCodeConfigSummaryUsesAuthStoreForBuiltinCodingPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_CONFIG", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("OPENCODE_AUTH", "")

	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	rawCfg := `{
  "$schema": "https://opencode.ai/config.json",
  "model": "zai-coding-plan/glm-4.7",
  "enabled_providers": ["zai-coding-plan"],
  "default_agent": "build"
}`
	if err := os.WriteFile(cfgPath, []byte(rawCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(home, ".local", "share", "opencode", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authPath, []byte(`{"zai-coding-plan":{"type":"api","key":"secret-not-leaked-to-summary"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := loadOpenCodeConfigSummary()
	if err != nil {
		t.Fatalf("loadOpenCodeConfigSummary: %v", err)
	}
	var codingPlan OpenCodeProviderSummary
	for _, p := range summary.Providers {
		if p.ID == "zai-coding-plan" {
			codingPlan = p
			break
		}
	}
	if !codingPlan.HasAPIKey {
		t.Fatalf("expected auth-store key to mark zai-coding-plan configured, providers=%#v", summary.Providers)
	}
	if codingPlan.BaseURL != "" {
		t.Fatalf("builtin coding plan provider should not require a custom baseUrl, got %q", codingPlan.BaseURL)
	}
	blob, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if strings.Contains(string(blob), "secret-not-leaked-to-summary") {
		t.Fatalf("opencode summary leaked auth-store key: %s", string(blob))
	}
	if len(summary.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics for builtin coding plan: %#v", summary.Diagnostics)
	}
}

// TestOpenCodeConfiguredModelProblem covers the functional readiness check that
// stops "opencode ready" from lying when the configured model's provider has no
// key — the exact class of failure behind the cryptic "Unexpected server error
// … ref: err_XXXX" a task hits mid-run.
func TestOpenCodeConfiguredModelProblem(t *testing.T) {
	// Isolate auth.json/config lookups to an empty temp home so
	// openCodeAuthProviderKeySet() finds nothing on disk.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_AUTH", filepath.Join(home, "nonexistent-auth.json"))

	model := func(m string) map[string]any { return map[string]any{"model": m} }

	cases := []struct {
		name      string
		cfg       map[string]any
		providers []openCodeRuntimeProvider
		glm       bool
		openai    bool
		anthropic bool
		local     bool
		wantIssue bool
	}{
		{
			name:      "glm model but no glm key -> problem (the reported bug)",
			cfg:       model("zai-coding-plan/glm-4.7"),
			wantIssue: true,
		},
		{
			name: "glm model with glm key present -> ok",
			cfg:  model("zai-coding-plan/glm-4.7"),
			glm:  true,
		},
		{
			name:      "anthropic model with anthropic key -> ok",
			cfg:       model("anthropic/claude-sonnet-4-6"),
			anthropic: true,
		},
		{
			name: "no explicit model -> no verdict",
			cfg:  map[string]any{},
		},
		{
			name: "bare model id without provider -> no verdict",
			cfg:  model("glm-4.7"),
		},
		{
			name:  "local ollama needs no key -> ok",
			cfg:   model("ollama/llama3"),
			local: true,
		},
		{
			name:      "custom provider with baseURL -> ok",
			cfg:       model("mystack/foo"),
			providers: []openCodeRuntimeProvider{{ID: "mystack", BaseURL: "http://127.0.0.1:8080"}},
		},
		{
			name:      "custom provider with key -> ok",
			cfg:       model("mystack/foo"),
			providers: []openCodeRuntimeProvider{{ID: "mystack", HasAPIKey: true}},
		},
		{
			name:      "default_agent build model overrides top-level, no key -> problem",
			cfg:       map[string]any{"default_agent": "build", "agent": map[string]any{"build": map[string]any{"model": "zai-coding-plan/glm-4.7"}}},
			wantIssue: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := openCodeConfiguredModelProblem(tc.cfg, tc.providers, tc.openai, false, tc.glm, tc.anthropic, tc.local)
			if tc.wantIssue && strings.TrimSpace(got) == "" {
				t.Fatalf("expected a configuration problem, got none")
			}
			if !tc.wantIssue && strings.TrimSpace(got) != "" {
				t.Fatalf("expected no problem, got: %s", got)
			}
		})
	}
}
