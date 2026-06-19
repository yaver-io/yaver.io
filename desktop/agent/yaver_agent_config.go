package main

// yaver_agent_config.go — provider/model/api-key config for the
// mobile-embedded yaver-agent (the small LLM that handles control-plane
// tasks: device auth, primary management, vault setup, status checks).
//
// This is NOT a coding agent. It does not run claude-code/codex/opencode.
// Its only job is to interpret natural-language control-plane requests
// from the phone and dispatch them as direct HTTP calls to the user's
// devices. Tokens like CLAUDE_CODE_OAUTH_TOKEN never enter this LLM —
// they flow through native side-channels (vault P2P sync) instead.
//
// Storage: vault project "yaver-agent" with four entries:
//
//   PROVIDER  → "glm" | "anthropic" | "openai" | "openrouter"
//   MODEL     → provider-specific model id (defaults if empty)
//   BASE_URL  → optional override (only meaningful for openai/openrouter)
//   API_KEY   → the secret. Never returned by GET; only writable.
//
// Wire format (HTTP) hides API_KEY from reads. The mobile/web UI sets
// `apiKey` on POST when the user types a new value; an empty string
// leaves the existing key untouched (so you can edit model/provider
// without re-entering the key).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// yaverAgentVaultProject namespaces the four config entries in vault.
const yaverAgentVaultProject = "yaver-agent"

// Vault entry names under that project.
const (
	yaverAgentKeyProvider = "PROVIDER"
	yaverAgentKeyModel    = "MODEL"
	yaverAgentKeyBaseURL  = "BASE_URL"
	yaverAgentKeyAPIKey   = "API_KEY"
)

// yaverAgentProviders enumerates the providers we support out of the box.
// "openrouter" + "openai" both use OpenAI-compatible chat-completions APIs;
// the difference is the default base URL and model namespace.
var yaverAgentProviders = []string{"glm", "anthropic", "openai", "openrouter"}

// YaverAgentConfig is the wire shape returned by GET /yaver-agent/config.
// Notice: no APIKey field — the value never leaves the host.
type YaverAgentConfig struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"baseUrl,omitempty"`
	HasAPIKey bool   `json:"hasApiKey"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// yaverAgentSetRequest is the wire shape for POST /yaver-agent/config.
// APIKey is write-only; "" means "keep existing".
type yaverAgentSetRequest struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	BaseURL  string  `json:"baseUrl"`
	APIKey   *string `json:"apiKey,omitempty"`
}

// yaverAgentDefaultModel returns the recommended cheap, tool-use-capable
// model for each provider. Users can override via the Model field; this
// only fires when Model is empty on save.
func yaverAgentDefaultModel(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "glm":
		return "glm-4.7"
	case "anthropic":
		return "claude-haiku-4-5"
	case "openai":
		return "gpt-4o-mini"
	case "openrouter":
		return "anthropic/claude-haiku-4-5"
	default:
		return ""
	}
}

// yaverAgentDefaultBaseURL returns the canonical base URL for each
// provider. Empty means "use the SDK's default endpoint."
func yaverAgentDefaultBaseURL(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "glm":
		return "https://api.z.ai/api/coding/paas/v4"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	default:
		return ""
	}
}

// validateYaverAgentProvider returns an error if the provider id is not
// one we support. We're conservative here so the mobile UI doesn't ship
// silent typos to vault.
func validateYaverAgentProvider(provider string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return fmt.Errorf("provider is required")
	}
	for _, p := range yaverAgentProviders {
		if p == provider {
			return nil
		}
	}
	return fmt.Errorf("unsupported provider %q (allowed: %s)", provider, strings.Join(yaverAgentProviders, ", "))
}

// loadYaverAgentConfig reads the four vault entries and returns a
// YaverAgentConfig snapshot. Missing entries are treated as zero values;
// the caller decides whether the config is "configured" (HasAPIKey).
func loadYaverAgentConfig(vs *VaultStore) (YaverAgentConfig, error) {
	cfg := YaverAgentConfig{}
	if vs == nil {
		return cfg, fmt.Errorf("vault not available")
	}
	read := func(name string) (string, int64) {
		entry, err := vs.Get(yaverAgentVaultProject, name)
		if err != nil {
			return "", 0
		}
		return entry.Value, entry.UpdatedAt
	}
	var latest int64
	if v, ts := read(yaverAgentKeyProvider); v != "" {
		cfg.Provider = v
		if ts > latest {
			latest = ts
		}
	}
	if v, ts := read(yaverAgentKeyModel); v != "" {
		cfg.Model = v
		if ts > latest {
			latest = ts
		}
	}
	if v, ts := read(yaverAgentKeyBaseURL); v != "" {
		cfg.BaseURL = v
		if ts > latest {
			latest = ts
		}
	}
	if v, ts := read(yaverAgentKeyAPIKey); v != "" {
		cfg.HasAPIKey = true
		if ts > latest {
			latest = ts
		}
	}
	cfg.UpdatedAt = latest
	return cfg, nil
}

// saveYaverAgentConfig writes the provided config to vault. apiKey is a
// pointer: nil means "leave existing untouched", "" means "clear", and
// any non-empty value replaces the stored key.
func saveYaverAgentConfig(vs *VaultStore, req yaverAgentSetRequest) (YaverAgentConfig, error) {
	if vs == nil {
		return YaverAgentConfig{}, fmt.Errorf("vault not available")
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if err := validateYaverAgentProvider(provider); err != nil {
		return YaverAgentConfig{}, err
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = yaverAgentDefaultModel(provider)
	}
	if model == "" {
		return YaverAgentConfig{}, fmt.Errorf("model is required (no default known for provider %q)", provider)
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL != "" {
		// Cheap sanity check — we don't need full URL parsing, just
		// reject obvious paste mistakes.
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			return YaverAgentConfig{}, fmt.Errorf("baseUrl must start with http:// or https://")
		}
	}

	now := time.Now().Unix()
	write := func(name, value string) error {
		entry := VaultEntry{
			Name:      name,
			Project:   yaverAgentVaultProject,
			Category:  "custom",
			Value:     value,
			UpdatedAt: now,
		}
		// Setting an empty value would store a blank entry. For the
		// "clear" semantic we explicitly delete instead.
		if value == "" {
			if err := vs.Delete(yaverAgentVaultProject, name); err != nil {
				// Treat "not found" as a no-op delete; anything else bubbles up.
				if !strings.Contains(strings.ToLower(err.Error()), "not found") {
					return err
				}
			}
			return nil
		}
		return vs.Set(entry)
	}

	if err := write(yaverAgentKeyProvider, provider); err != nil {
		return YaverAgentConfig{}, err
	}
	if err := write(yaverAgentKeyModel, model); err != nil {
		return YaverAgentConfig{}, err
	}
	if err := write(yaverAgentKeyBaseURL, baseURL); err != nil {
		return YaverAgentConfig{}, err
	}

	if req.APIKey != nil {
		if err := write(yaverAgentKeyAPIKey, strings.TrimSpace(*req.APIKey)); err != nil {
			return YaverAgentConfig{}, err
		}
	}

	return loadYaverAgentConfig(vs)
}

// handleYaverAgentConfig serves both GET and POST on /yaver-agent/config.
// GET returns the current config (without the API-key value); POST
// updates one or more fields. Auth is enforced by the outer route
// registration (see httpserver.go) — same gate as /vault/*.
func (s *HTTPServer) handleYaverAgentConfig(w http.ResponseWriter, r *http.Request) {
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		cfg, err := loadYaverAgentConfig(s.vaultStore)
		if err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"config":    cfg,
			"providers": yaverAgentProviders,
			"defaults":  yaverAgentDefaultsCatalog(),
		})
	case http.MethodPost:
		var body yaverAgentSetRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		cfg, err := saveYaverAgentConfig(s.vaultStore, body)
		if err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"config":    cfg,
			"providers": yaverAgentProviders,
			"defaults":  yaverAgentDefaultsCatalog(),
		})
	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// yaverAgentDefaultsCatalog is what we hand to the UI so it can show a
// "use default" button without hard-coding the same strings in three
// places. Order matches yaverAgentProviders.
type yaverAgentProviderDefault struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"baseUrl,omitempty"`
	Label    string `json:"label"`
	Note     string `json:"note,omitempty"`
}

func yaverAgentDefaultsCatalog() []yaverAgentProviderDefault {
	out := make([]yaverAgentProviderDefault, 0, len(yaverAgentProviders))
	for _, p := range yaverAgentProviders {
		entry := yaverAgentProviderDefault{
			Provider: p,
			Model:    yaverAgentDefaultModel(p),
			BaseURL:  yaverAgentDefaultBaseURL(p),
		}
		switch p {
		case "glm":
			entry.Label = "GLM (Z.ai)"
			entry.Note = "Cheap and fast — recommended default for control-plane tool calls."
		case "anthropic":
			entry.Label = "Anthropic API"
			entry.Note = "Uses your Anthropic API key (NOT your Max subscription). Haiku 4.5 is small and tool-use capable."
		case "openai":
			entry.Label = "OpenAI"
			entry.Note = "Uses your OpenAI API key. gpt-4o-mini handles tool calls reliably."
		case "openrouter":
			entry.Label = "OpenRouter"
			entry.Note = "Pick any model via OpenRouter. Useful for free-tier or experimental models."
		}
		out = append(out, entry)
	}
	return out
}
