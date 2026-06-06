package main

// gpu_rental_ops.go — ops verbs for the GPU-rental orchestration layer.
//
//	gpu_plans    list the GPU/inference catalog (with voiceSafe), per host
//	gpu_bind     point an app's vault project at an inference backend (the
//	             rebind seam; a DeepInfra model swap or a Salad endpoint)
//	gpu_status   poll a Salad container group for its assigned endpoint
//	gpu_destroy  delete a Salad container group (stop the hourly bill)
//
// Provisioning a Salad group / DeepInfra binding goes through the existing
// cloud_provision verb+tool (host=salad|deepinfra) since the provisioners are
// registered in provisionerRegistry. These verbs cover the GPU-specific
// lifecycle the generic cloud verbs don't. See docs/gpu-rental-orchestration.md.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_plans",
		Description: "List the GPU/inference catalog: DeepInfra serverless models (per-token, voiceSafe flag) and Salad GPU classes (hourly). Pass host=salad|deepinfra to filter, or omit for both. voiceSafe=false means the model is a reasoning/batch model that blows the realtime-voice TTFT budget — never use it for live calls.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"host": map[string]interface{}{"type": "string", "description": "salad | deepinfra | (omit for both)"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGPUPlansHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_bind",
		Description: "Bind an inference backend into an app's vault project so its companion service reads it at (re)start. This is the rebind seam: a DeepInfra model swap (provider=deepinfra + model) or pointing at a freshly-provisioned Salad endpoint (baseUrl). Writes DEEPINFRA_BASE_URL/DEEPINFRA_API_KEY/LLM_MODEL (+ optional ASR/TTS). Refuses non-voiceSafe models unless allowUnsafe=true. Returns the key NAMES written, never values.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"project":     map[string]interface{}{"type": "string", "description": "Vault project the app companion reads (default: callcenter)"},
				"provider":    map[string]interface{}{"type": "string", "description": "deepinfra (shorthand: fills baseUrl + apiKey from account)"},
				"model":       map[string]interface{}{"type": "string"},
				"baseUrl":     map[string]interface{}{"type": "string", "description": "Explicit OpenAI-compatible base URL (e.g. a Salad group https://<dns>/v1)"},
				"apiKey":      map[string]interface{}{"type": "string", "description": "Optional; resolved from the DeepInfra account when omitted"},
				"asrBaseUrl":  map[string]interface{}{"type": "string"},
				"asrModel":    map[string]interface{}{"type": "string"},
				"ttsUrl":      map[string]interface{}{"type": "string"},
				"allowUnsafe": map[string]interface{}{"type": "boolean", "description": "Bind a reasoning/batch model even though it's not voice-safe"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGPUBindHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_status",
		Description: "Poll a Salad GPU container group for its current state + assigned endpoint (Salad assigns DNS as the group boots). Returns {status, endpoint, ready}. Use after cloud_provision host=salad, then gpu_bind the endpoint once ready.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"organization", "project", "id"},
			"properties": map[string]interface{}{
				"organization": map[string]interface{}{"type": "string"},
				"project":      map[string]interface{}{"type": "string"},
				"id":           map[string]interface{}{"type": "string", "description": "Salad container-group id"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGPUStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_destroy",
		Description: "Delete a Salad GPU container group to stop its hourly bill. Requires confirm=true. BYO-token-only (the caller's own Salad account) — cannot touch another user's resources. Stateless inference, so no snapshot.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"organization", "project", "id", "confirm"},
			"properties": map[string]interface{}{
				"organization": map[string]interface{}{"type": "string"},
				"project":      map[string]interface{}{"type": "string"},
				"id":           map[string]interface{}{"type": "string"},
				"confirm":      map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGPUDestroyHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsGPUPlansHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Host string `json:"host"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	plans := gpuRentalCatalog(p.Host)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"plans": plans,
		"hint":  "provision: cloud_provision host=deepinfra|salad. bind: gpu_bind. voiceSafe=false → batch-only, never live voice.",
	}}
}

func opsGPUBindHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Project     string `json:"project"`
		Provider    string `json:"provider"`
		Model       string `json:"model"`
		BaseURL     string `json:"baseUrl"`
		APIKey      string `json:"apiKey"`
		ASRBaseURL  string `json:"asrBaseUrl"`
		ASRModel    string `json:"asrModel"`
		TTSURL      string `json:"ttsUrl"`
		AllowUnsafe bool   `json:"allowUnsafe"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	baseURL := strings.TrimSpace(p.BaseURL)
	apiKey := strings.TrimSpace(p.APIKey)
	// DeepInfra shorthand: fill baseURL + key from the connected account.
	if strings.EqualFold(strings.TrimSpace(p.Provider), "deepinfra") {
		if baseURL == "" {
			baseURL = deepInfraOpenAIBase
		}
		if apiKey == "" {
			apiKey = accountField(ProviderDeepInfra, "token")
		}
		if apiKey == "" {
			return OpsResult{OK: false, Code: "no_account", Error: "DeepInfra not connected — /accounts/connect first (BYO key)"}
		}
	}
	if baseURL == "" && strings.TrimSpace(p.ASRBaseURL) == "" && strings.TrimSpace(p.TTSURL) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "need a baseUrl (or provider=deepinfra), or an asrBaseUrl/ttsUrl to bind"}
	}
	if m := strings.TrimSpace(p.Model); m != "" && !VoiceSafeModel(m) && !p.AllowUnsafe {
		return OpsResult{OK: false, Code: "not_voice_safe", Error: "model " + m + " is a reasoning/batch model (TTFT dead air) — pass allowUnsafe=true to bind anyway"}
	}
	vs := currentRuntimeVaultStore()
	if vs == nil {
		return OpsResult{OK: false, Code: "no_vault", Error: "no runtime vault mounted — cannot persist inference binding"}
	}
	written := writeInferenceBinding(p.Project, inferenceBinding{
		BaseURL: baseURL, APIKey: apiKey, Model: strings.TrimSpace(p.Model),
		ASRBase: strings.TrimSpace(p.ASRBaseURL), ASRModel: strings.TrimSpace(p.ASRModel),
		TTSURL: strings.TrimSpace(p.TTSURL),
	})
	proj := strings.TrimSpace(p.Project)
	if proj == "" {
		proj = inferenceVaultDefaultProject
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"project":     proj,
		"keysWritten": written, // names only, never values
		"hint":        "restart the app's companion service (companion_up) to pick up the new binding; in-flight calls finish on the old endpoint",
	}}
}

func opsGPUStatusHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Organization string `json:"organization"`
		Project      string `json:"project"`
		ID           string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Organization == "" || p.Project == "" || strings.TrimSpace(p.ID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "organization, project, id required"}
	}
	token := accountField(ProviderSalad, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Salad not connected — /accounts/connect first (BYO token)"}
	}
	g, err := saladGetContainerGroup(token, p.Organization, p.Project, strings.TrimSpace(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "status_failed", Error: err.Error()}
	}
	endpoint := saladEndpoint(g)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"status":   g.CurrentState.Status,
		"endpoint": endpoint,
		"ready":    endpoint != "" && strings.EqualFold(g.CurrentState.Status, "running"),
	}}
}

func opsGPUDestroyHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Organization string `json:"organization"`
		Project      string `json:"project"`
		ID           string `json:"id"`
		Confirm      bool   `json:"confirm"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Organization == "" || p.Project == "" || strings.TrimSpace(p.ID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "organization, project, id required"}
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "unauthorized", Error: "gpu_destroy requires confirm=true"}
	}
	token := accountField(ProviderSalad, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Salad not connected — /accounts/connect first (BYO token)"}
	}
	if err := saladDeleteContainerGroup(token, p.Organization, p.Project, strings.TrimSpace(p.ID)); err != nil {
		return OpsResult{OK: false, Code: "destroy_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"deleted": p.ID, "notes": "Salad group deleted; hourly billing stopped"}}
}
