package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	runtimeVaultMu    sync.RWMutex
	runtimeVaultStore *VaultStore
)

func setRuntimeVaultStore(vs *VaultStore) {
	runtimeVaultMu.Lock()
	runtimeVaultStore = vs
	runtimeVaultMu.Unlock()
}

func currentRuntimeVaultStore() *VaultStore {
	runtimeVaultMu.RLock()
	defer runtimeVaultMu.RUnlock()
	return runtimeVaultStore
}

func providerEnvCandidates(name string) []string {
	switch strings.TrimSpace(name) {
	case "GLM_API_KEY":
		return []string{"GLM_API_KEY", "ZAI_API_KEY"}
	case "ZAI_API_KEY":
		return []string{"ZAI_API_KEY", "GLM_API_KEY"}
	default:
		return []string{strings.TrimSpace(name)}
	}
}

func hostSecretValue(name string) (string, string) {
	for _, candidate := range providerEnvCandidates(name) {
		if value := strings.TrimSpace(os.Getenv(candidate)); value != "" {
			return value, candidate
		}
	}
	vs := currentRuntimeVaultStore()
	if vs == nil {
		return "", ""
	}
	for _, candidate := range providerEnvCandidates(name) {
		entry, err := vs.Get("", candidate)
		if err != nil {
			continue
		}
		if value := strings.TrimSpace(entry.Value); value != "" {
			return value, "vault:" + candidate
		}
	}
	return "", ""
}

func collectHostSecretEnv(names []string) map[string]string {
	keys := map[string]string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if value, _ := hostSecretValue(name); value != "" {
			keys[name] = value
		}
	}
	return keys
}

// runnerProviderVaultProject is the vault namespace that points a coding
// runner at a custom model backend (the "no-egress / on-prem / Salad-hosted
// model" lane). It is the CO-EQUAL counterpart to the default OAuth path:
// when this is configured, the runner talks to Ollama / vLLM / an internal
// gateway / a model hosted on a rented box instead of Anthropic/OpenAI via
// the user's subscription. The key (if any) lives only here on the runtime —
// never in Convex, never across machines.
//
// Recognised entries (per-runner suffix `__<runnerId>` wins over the shared
// value, so one runtime can point claude and codex at different endpoints):
//
//	BASE_URL          (required to activate; empty → OAuth/default path)
//	API_KEY           (optional; local Ollama needs none)
//	BASE_URL__claude  / API_KEY__claude   (per-runner override)
const runnerProviderVaultProject = "runner-provider"

type runnerProviderCfg struct {
	baseURL string
	apiKey  string
}

// runnerProviderConfigFor reads the local-model/external-endpoint config for a
// runner from the runtime vault. Returns the zero value (→ OAuth/default path)
// when nothing is configured or no vault is mounted.
func runnerProviderConfigFor(runnerID string) runnerProviderCfg {
	vs := currentRuntimeVaultStore()
	if vs == nil {
		return runnerProviderCfg{}
	}
	runnerID = strings.TrimSpace(runnerID)
	get := func(name string) string {
		if runnerID != "" {
			if e, err := vs.Get(runnerProviderVaultProject, name+"__"+runnerID); err == nil && e != nil {
				if v := strings.TrimSpace(e.Value); v != "" {
					return v
				}
			}
		}
		if e, err := vs.Get(runnerProviderVaultProject, name); err == nil && e != nil {
			return strings.TrimSpace(e.Value)
		}
		return ""
	}
	return runnerProviderCfg{baseURL: get("BASE_URL"), apiKey: get("API_KEY")}
}

// runnerProviderProtocol maps a runner to the wire protocol its endpoint must
// speak. Claude Code only talks Anthropic; everything else (codex, opencode,
// aider, …) is pointed via the OpenAI-compatible env vars.
func runnerProviderProtocol(runnerID string) string {
	switch strings.ToLower(strings.TrimSpace(runnerID)) {
	case "claude", "claude-code", "claudecode":
		return "anthropic"
	default:
		return "openai"
	}
}

// runnerProviderEnv returns the env-var assignments that point the given coding
// runner at the configured local-model/external endpoint. Returns nil when no
// provider is configured (the OAuth-subscription default — the focus path), so
// runners fall back to their own `--claudeai`/ChatGPT credentials untouched.
//
// These values are appended last by taskEnv, so an explicit runtime provider
// config deterministically beats any ambient ANTHROPIC_BASE_URL/OPENAI_BASE_URL
// inherited from the parent environment.
func runnerProviderEnv(runnerID string) []string {
	cfg := runnerProviderConfigFor(runnerID)
	if cfg.baseURL == "" {
		return nil
	}
	var out []string
	switch runnerProviderProtocol(runnerID) {
	case "anthropic":
		out = append(out, "ANTHROPIC_BASE_URL="+cfg.baseURL)
		if cfg.apiKey != "" {
			// ANTHROPIC_AUTH_TOKEN (not ANTHROPIC_API_KEY) so gateways that
			// expect a bearer token work, and so this never looks like the
			// first-party API-billing path.
			out = append(out, "ANTHROPIC_AUTH_TOKEN="+cfg.apiKey)
		}
	default:
		out = append(out, "OPENAI_BASE_URL="+cfg.baseURL, "OPENAI_API_BASE="+cfg.baseURL)
		if cfg.apiKey != "" {
			out = append(out, "OPENAI_API_KEY="+cfg.apiKey)
		}
	}
	return out
}

// RunnerProviderPreflightResult reports whether the configured local-model /
// external endpoint (Ollama / vLLM / a Salad-hosted model / an internal
// gateway) is actually usable from THIS runtime. Convex can't probe an on-prem
// box, so reachability is established on the runtime and surfaced here. Carries
// no secret (the key is never echoed).
type RunnerProviderPreflightResult struct {
	Configured bool     `json:"configured"`        // a BASE_URL is set in the runtime vault
	BaseURL    string   `json:"baseUrl,omitempty"` // the endpoint (not a secret)
	Protocol   string   `json:"protocol"`          // "anthropic" | "openai"
	KeyPresent bool     `json:"keyPresent"`        // whether a key is set (never the key)
	Reachable  bool     `json:"reachable"`         // endpoint answered
	Status     int      `json:"status,omitempty"`  // HTTP status from the probe
	Models     []string `json:"models,omitempty"`  // model ids the endpoint advertises
	Error      string   `json:"error,omitempty"`
}

// providerModelsProbeURL derives the OpenAI-style `/v1/models` (or `/models`)
// listing URL from a provider base URL, tolerating a trailing `/v1` or `/`.
func providerModelsProbeURL(baseURL string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(b, "/v1") {
		return b + "/models"
	}
	return b + "/v1/models"
}

// runnerProviderPreflight probes the configured endpoint for a runner. A 2xx
// lists models; a 401/403 still proves reachability (auth needed). httpGet lets
// tests inject a client; pass nil for the default. Never blocks long — the
// caller is expected to pass a context with a timeout via the http.Client.
func runnerProviderPreflight(runnerID string, client *http.Client) RunnerProviderPreflightResult {
	cfg := runnerProviderConfigFor(runnerID)
	res := RunnerProviderPreflightResult{
		Protocol:   runnerProviderProtocol(runnerID),
		Configured: cfg.baseURL != "",
		BaseURL:    cfg.baseURL,
		KeyPresent: cfg.apiKey != "",
	}
	if cfg.baseURL == "" {
		return res // OAuth/default path — nothing to probe
	}
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, providerModelsProbeURL(cfg.baseURL), nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if cfg.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	// Reachable if the endpoint answered at all with a sane status. 401/403 =
	// reachable but the key is missing/wrong; surface that distinctly.
	res.Reachable = resp.StatusCode > 0 && resp.StatusCode < 500
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		res.Error = "endpoint reachable but rejected the credential (configure API_KEY in the runner-provider vault)"
		return res
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var parsed struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			for _, m := range parsed.Data {
				if m.ID != "" {
					res.Models = append(res.Models, m.ID)
				}
			}
			for _, m := range parsed.Models { // Ollama-style /api/tags shape
				if m.Name != "" {
					res.Models = append(res.Models, m.Name)
				}
			}
		}
	}
	return res
}
