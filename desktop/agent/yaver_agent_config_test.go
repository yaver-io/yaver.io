package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestVaultForAgentCfg(t *testing.T) *VaultStore {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	vs, err := NewVaultStore("test-yaver-agent-cfg")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	return vs
}

func TestYaverAgentDefaultModel(t *testing.T) {
	cases := map[string]string{
		"glm":        "glm-4.7",
		"anthropic":  "claude-haiku-4-5",
		"openai":     "gpt-4o-mini",
		"openrouter": "anthropic/claude-haiku-4-5",
		"unknown":    "",
		"  GLM  ":    "glm-4.7", // case + whitespace tolerance
		"OpenRouter": "anthropic/claude-haiku-4-5",
	}
	for in, want := range cases {
		got := yaverAgentDefaultModel(in)
		if got != want {
			t.Errorf("yaverAgentDefaultModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateYaverAgentProvider(t *testing.T) {
	for _, ok := range []string{"glm", "anthropic", "openai", "openrouter", "GLM", " openai "} {
		if err := validateYaverAgentProvider(ok); err != nil {
			t.Errorf("validateYaverAgentProvider(%q) unexpectedly failed: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "claude", "gpt", "groq"} {
		if err := validateYaverAgentProvider(bad); err == nil {
			t.Errorf("validateYaverAgentProvider(%q) should have failed", bad)
		}
	}
}

func TestSaveAndLoadYaverAgentConfig_RoundtripWithDefaultModel(t *testing.T) {
	vs := newTestVaultForAgentCfg(t)

	apiKey := "test-secret-do-not-leak"
	cfg, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{
		Provider: "GLM",
		// Model intentionally empty — should fall back to default.
		BaseURL: "",
		APIKey:  &apiKey,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if cfg.Provider != "glm" {
		t.Errorf("provider not normalized: %q", cfg.Provider)
	}
	if cfg.Model != "glm-4.7" {
		t.Errorf("model default not applied: %q", cfg.Model)
	}
	if !cfg.HasAPIKey {
		t.Errorf("hasApiKey should be true after save")
	}

	loaded, err := loadYaverAgentConfig(vs)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Provider != "glm" || loaded.Model != "glm-4.7" || !loaded.HasAPIKey {
		t.Errorf("roundtrip mismatch: %+v", loaded)
	}

	// The HTTP wire never returns the raw value, but the underlying
	// vault must have stored it.
	stored, err := vs.Get(yaverAgentVaultProject, yaverAgentKeyAPIKey)
	if err != nil {
		t.Fatalf("vault Get API_KEY: %v", err)
	}
	if stored.Value != apiKey {
		t.Errorf("stored api key mismatch: %q", stored.Value)
	}
}

func TestSaveYaverAgentConfig_PreservesAPIKeyOnNilUpdate(t *testing.T) {
	vs := newTestVaultForAgentCfg(t)

	// First save: provide the key.
	first := "first-secret"
	if _, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5",
		APIKey:   &first,
	}); err != nil {
		t.Fatalf("save 1: %v", err)
	}

	// Second save: change the model only, leave APIKey nil.
	if _, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5",
		// APIKey: nil — do not touch.
	}); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	stored, err := vs.Get(yaverAgentVaultProject, yaverAgentKeyAPIKey)
	if err != nil {
		t.Fatalf("vault Get API_KEY after no-op: %v", err)
	}
	if stored.Value != first {
		t.Errorf("expected api key preserved %q, got %q", first, stored.Value)
	}
}

func TestSaveYaverAgentConfig_ClearAPIKeyWithEmptyString(t *testing.T) {
	vs := newTestVaultForAgentCfg(t)

	first := "first-secret"
	if _, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{
		Provider: "openai",
		APIKey:   &first,
	}); err != nil {
		t.Fatalf("save 1: %v", err)
	}

	cleared := ""
	cfg, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{
		Provider: "openai",
		APIKey:   &cleared,
	})
	if err != nil {
		t.Fatalf("save 2: %v", err)
	}
	if cfg.HasAPIKey {
		t.Errorf("hasApiKey should be false after clear, got %+v", cfg)
	}
	if _, err := vs.Get(yaverAgentVaultProject, yaverAgentKeyAPIKey); err == nil {
		t.Errorf("expected vault Get API_KEY to fail after clear, but it succeeded")
	}
}

func TestSaveYaverAgentConfig_RejectsBadInputs(t *testing.T) {
	vs := newTestVaultForAgentCfg(t)

	if _, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{Provider: "bogus"}); err == nil {
		t.Errorf("expected error for unsupported provider")
	}
	if _, err := saveYaverAgentConfig(vs, yaverAgentSetRequest{Provider: "openrouter", BaseURL: "ftp://nope"}); err == nil {
		t.Errorf("expected error for non-http baseUrl")
	}
}

func TestHandleYaverAgentConfig_HTTPRoundtrip(t *testing.T) {
	vs := newTestVaultForAgentCfg(t)
	srv := &HTTPServer{vaultStore: vs}

	// GET on empty store: should return zero-value config + catalog.
	req := httptest.NewRequest(http.MethodGet, "/yaver-agent/config", nil)
	rec := httptest.NewRecorder()
	srv.handleYaverAgentConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status: %d, body=%s", rec.Code, rec.Body.String())
	}
	var getResp struct {
		Config    YaverAgentConfig            `json:"config"`
		Providers []string                    `json:"providers"`
		Defaults  []yaverAgentProviderDefault `json:"defaults"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal GET: %v", err)
	}
	if getResp.Config.HasAPIKey {
		t.Errorf("empty store should report hasApiKey=false")
	}
	if len(getResp.Providers) != 4 || len(getResp.Defaults) != 4 {
		t.Errorf("expected 4 providers / defaults, got %d / %d", len(getResp.Providers), len(getResp.Defaults))
	}

	// POST with a key and let the model default.
	body := map[string]interface{}{
		"provider": "openrouter",
		"baseUrl":  "https://openrouter.ai/api/v1",
		"apiKey":   "sk-or-v1-test",
	}
	raw, _ := json.Marshal(body)
	req2 := httptest.NewRequest(http.MethodPost, "/yaver-agent/config", bytes.NewReader(raw))
	rec2 := httptest.NewRecorder()
	srv.handleYaverAgentConfig(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("POST status: %d, body=%s", rec2.Code, rec2.Body.String())
	}
	var postResp struct {
		Config YaverAgentConfig `json:"config"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &postResp); err != nil {
		t.Fatalf("unmarshal POST: %v", err)
	}
	if postResp.Config.Provider != "openrouter" {
		t.Errorf("provider after POST: %q", postResp.Config.Provider)
	}
	if postResp.Config.Model != "anthropic/claude-haiku-4-5" {
		t.Errorf("model after POST default: %q", postResp.Config.Model)
	}
	if !postResp.Config.HasAPIKey {
		t.Errorf("hasApiKey false after POST with key")
	}

	// The wire response must NEVER include the raw API key.
	if strings.Contains(rec2.Body.String(), "sk-or-v1-test") {
		t.Errorf("API key value leaked in HTTP response: %s", rec2.Body.String())
	}

	// And the catalog must include the GLM Z.ai base URL hint.
	hasGLM := false
	for _, d := range yaverAgentDefaultsCatalog() {
		if d.Provider == "glm" && d.BaseURL == "https://api.z.ai/api/coding/paas/v4" {
			hasGLM = true
		}
	}
	if !hasGLM {
		t.Errorf("expected GLM default to surface Z.ai base URL")
	}
}
