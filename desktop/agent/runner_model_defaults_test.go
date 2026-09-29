package main

import "testing"

func TestYaverBuiltinRunnerModelDefaults(t *testing.T) {
	defaults := builtinRunnerModelDefaults()
	if got := defaults["claude"].Model; got != "claude-opus-4-8" {
		t.Fatalf("claude default = %q", got)
	}
	if got := defaults["codex"]; got.Model != "gpt-5.6-sol" || got.ReasoningEffort != "medium" {
		t.Fatalf("codex default = %#v", got)
	}
	if got := defaults["opencode"]; got.Model != "deepseek/deepseek-flash" || got.ReasoningEffort != "" {
		t.Fatalf("opencode default = %#v; reasoning must stay omitted when the CLI exposes none", got)
	}
}

func TestModelFallbackForRefusalIsSingleShot(t *testing.T) {
	LoadYaverModelDefaults(nil)
	fallback, ok := modelFallbackForRefusal("codex", "gpt-5.4", false)
	if !ok || fallback.Model != "gpt-5.6-sol" || fallback.ReasoningEffort != "medium" {
		t.Fatalf("fallback = %#v, %v", fallback, ok)
	}
	if _, ok := modelFallbackForRefusal("codex", "gpt-5.4", true); ok {
		t.Fatal("second fallback attempt must be denied")
	}
	if _, ok := modelFallbackForRefusal("codex", "gpt-5.6-sol", false); ok {
		t.Fatal("a rejected global default must stop, not retry itself")
	}
}

func TestBackendCatalogCannotOverrideYaverDefault(t *testing.T) {
	LoadYaverModelDefaults(nil)
	rows := normalizeBackendModelsWithYaverDefaults([]BackendModel{
		{RunnerID: "codex", ModelID: "gpt-5.4", IsDefault: true},
	})
	var oldDefault, globalDefault bool
	for _, row := range rows {
		if row.ModelID == "gpt-5.4" {
			oldDefault = row.IsDefault
		}
		if row.ModelID == "gpt-5.6-sol" {
			globalDefault = row.IsDefault
		}
	}
	if oldDefault || !globalDefault {
		t.Fatalf("normalized rows = %#v", rows)
	}
}
