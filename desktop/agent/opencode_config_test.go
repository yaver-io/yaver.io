package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOpenCodeConfigSummaryParsesJSONCAndModels(t *testing.T) {
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
