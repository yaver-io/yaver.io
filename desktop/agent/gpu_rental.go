package main

// gpu_rental.go — GPU-rental orchestration: provision burst GPU (Salad) and
// bind serverless inference (DeepInfra) for APPLICATION-runtime use, then mint
// the OpenAI-compatible config the running app reads from its vault project.
//
// This is "Plane B" in docs/gpu-rental-orchestration.md: the deployed product
// (e.g. the e-back call-center: VAD→ASR→LLM→TTS, sub-second budget) calls
// inference on every turn. It is DISTINCT from the coding-runner provider lane
// in provider_keys.go (vault project "runner-provider"), which points coding
// AGENTS at a model. Both can coexist; they store different things.
//
// Lifecycle primitives here are driven by the gpu_* ops verbs (gpu_rental_ops.go)
// and the dispatcher/autoscaler (gpu_autoscaler.go). Provider API keys come from
// the vault-backed accounts store (accountField) — never the request payload,
// never Convex, per the privacy contract.

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	// HostSalad is the GPU container marketplace (hourly community GPU).
	// Its provisioning primitive is a container group, not a VM.
	HostSalad TargetHost = "salad"
	// HostDeepInfra is serverless inference — there is NO machine to create;
	// "provisioning" is a validated binding to an OpenAI-compatible endpoint.
	HostDeepInfra TargetHost = "deepinfra"
)

// inferenceVaultDefaultProject is the vault project an app's companion reads
// its inference config from (CompanionEnvSource{Vault}). Override per-bind via
// opts["bindProject"]; defaults to the call-center driving use case.
const inferenceVaultDefaultProject = "callcenter"

// deepInfraOpenAIBase is DeepInfra's OpenAI-compatible base URL. var (not
// const) so tests can point it at an httptest server.
var deepInfraOpenAIBase = "https://api.deepinfra.com/v1/openai"

// saladPublicAPI is the Salad Container Engine public API root. var (not const)
// so tests can point it at an httptest server.
var saladPublicAPI = "https://api.salad.com/api/public"

// ---------------------------------------------------------------------------
// Catalog (cloud_plans, provider-aware) — with the load-bearing voiceSafe flag
// ---------------------------------------------------------------------------

// GPURentalPlan is one selectable GPU/inference option. `VoiceSafe` encodes the
// realtime-voice model-selection rules from the call-center analysis: reasoning
// models and giant dense models blow the TTFT budget and produce dead air, so
// they're flagged batch-only. For GPU hardware (gpu-group) the flag means the
// box is *capable* of voice-safe serving — the model you put on it still
// decides (see VoiceSafeModel).
type GPURentalPlan struct {
	Provider        string  `json:"provider"`
	ID              string  `json:"id"`   // value passed to cloud_provision opts (gpu class id or model id)
	Kind            string  `json:"kind"` // "gpu-group" | "serverless"
	Workload        string  `json:"workload"` // "llm" | "asr" | "tts" | "any"
	GPU             string  `json:"gpu,omitempty"`
	VRAMGb          int     `json:"vramGb,omitempty"`
	Model           string  `json:"model,omitempty"`
	PricePerHour    float64 `json:"pricePerHour,omitempty"`
	PriceInPerMTok  float64 `json:"priceInPerMTok,omitempty"`
	PriceOutPerMTok float64 `json:"priceOutPerMTok,omitempty"`
	PricePerMinute  float64 `json:"pricePerMinute,omitempty"` // audio (ASR)
	VoiceSafe       bool    `json:"voiceSafe"`
	Notes           string  `json:"notes,omitempty"`
}

// deepInfraCatalog lists the call-center-relevant serverless models. Prices are
// the published DeepInfra rates captured in e-back/.../deepinfra-model-analysis.md
// (USD per 1M tokens, or per minute of audio for ASR). voiceSafe follows the
// model rules, not a per-request guess.
func deepInfraCatalog() []GPURentalPlan {
	return []GPURentalPlan{
		{Provider: "deepinfra", ID: "nvidia/NVIDIA-Nemotron-3-Super-120B-A12B", Kind: "serverless", Workload: "llm",
			Model: "nvidia/NVIDIA-Nemotron-3-Super-120B-A12B", PriceInPerMTok: 0.10, PriceOutPerMTok: 0.50, VoiceSafe: true,
			Notes: "MoE, 12B active — low TTFT, strong tool-calling. Default voice brain."},
		{Provider: "deepinfra", ID: "nvidia/Nemotron-3-Nano-30B-A3B", Kind: "serverless", Workload: "llm",
			Model: "nvidia/Nemotron-3-Nano-30B-A3B", PriceInPerMTok: 0.04, PriceOutPerMTok: 0.16, VoiceSafe: true,
			Notes: "Cheapest voice-safe brain — high-volume / mostly-scripted calls."},
		{Provider: "deepinfra", ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Kind: "serverless", Workload: "llm",
			Model: "meta-llama/Llama-3.3-70B-Instruct-Turbo", PriceInPerMTok: 0.23, PriceOutPerMTok: 0.40, VoiceSafe: true,
			Notes: "Non-NVIDIA safe default."},
		{Provider: "deepinfra", ID: "Qwen/Qwen3-235B-A22B", Kind: "serverless", Workload: "llm",
			Model: "Qwen/Qwen3-235B-A22B", PriceInPerMTok: 0.13, PriceOutPerMTok: 0.60, VoiceSafe: true,
			Notes: "MoE 22B active — capable, still voice-safe."},
		{Provider: "deepinfra", ID: "deepseek-ai/DeepSeek-R1", Kind: "serverless", Workload: "llm",
			Model: "deepseek-ai/DeepSeek-R1", PriceInPerMTok: 0.55, PriceOutPerMTok: 2.19, VoiceSafe: false,
			Notes: "REASONING model — thinking tokens block TTFT (dead air). Batch-only, never live voice."},
		{Provider: "deepinfra", ID: "mistralai/Voxtral-Mini-3B-2507", Kind: "serverless", Workload: "asr",
			Model: "mistralai/Voxtral-Mini-3B-2507", PricePerMinute: 0.001, VoiceSafe: true,
			Notes: "Default ASR — fast, cheap."},
		{Provider: "deepinfra", ID: "mistralai/Voxtral-Small-24B-2507", Kind: "serverless", Workload: "asr",
			Model: "mistralai/Voxtral-Small-24B-2507", PricePerMinute: 0.003, VoiceSafe: true,
			Notes: "Higher-accuracy ASR fallback."},
	}
}

// saladCatalog lists representative Salad community GPU classes for self-hosting
// vLLM/Voxtral/TTS. Hourly rates are approximate community-tier prices; the
// authoritative list is GET /organizations/{org}/gpu-classes (saladListGPUClasses).
// VoiceSafe = the hardware can serve a voice-safe model (the model still
// decides — see VoiceSafeModel).
func saladCatalog() []GPURentalPlan {
	return []GPURentalPlan{
		{Provider: "salad", ID: "rtx3090", Kind: "gpu-group", Workload: "any", GPU: "RTX 3090", VRAMGb: 24, PricePerHour: 0.10, VoiceSafe: true, Notes: "Cheapest 24GB — fits a 7-14B voice LLM or Voxtral+TTS."},
		{Provider: "salad", ID: "rtx4090", Kind: "gpu-group", Workload: "any", GPU: "RTX 4090", VRAMGb: 24, PricePerHour: 0.20, VoiceSafe: true, Notes: "Best price/throughput for realtime decode."},
		{Provider: "salad", ID: "l40s", Kind: "gpu-group", Workload: "any", GPU: "L40S", VRAMGb: 48, PricePerHour: 0.45, VoiceSafe: true, Notes: "48GB — larger voice model or LLM+ASR+TTS colocated."},
		{Provider: "salad", ID: "a100-80gb", Kind: "gpu-group", Workload: "any", GPU: "A100 80GB", VRAMGb: 80, PricePerHour: 0.90, VoiceSafe: true, Notes: "High concurrency single-box vLLM."},
		{Provider: "salad", ID: "h100", Kind: "gpu-group", Workload: "any", GPU: "H100", VRAMGb: 80, PricePerHour: 1.30, VoiceSafe: true, Notes: "Max concurrency / lowest TTFT."},
	}
}

// gpuRentalCatalog returns the catalog for a host ("salad"|"deepinfra"|""=both).
func gpuRentalCatalog(host string) []GPURentalPlan {
	switch TargetHost(strings.TrimSpace(host)) {
	case HostSalad:
		return saladCatalog()
	case HostDeepInfra:
		return deepInfraCatalog()
	default:
		out := append([]GPURentalPlan{}, deepInfraCatalog()...)
		return append(out, saladCatalog()...)
	}
}

// VoiceSafeModel applies the realtime-voice rules to a model id. Returns false
// for reasoning models (thinking tokens → TTFT dead air) and obviously giant
// dense models; true otherwise. Conservative: unknown → true (don't block).
func VoiceSafeModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return true
	}
	// Reasoning / "thinking" families — disqualified for live voice.
	for _, bad := range []string{"-r1", "/r1", "deepseek-r1", "reason", "think", "qwq", "o1", "o3"} {
		if strings.Contains(m, bad) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// DeepInfra — serverless binding provisioner (no machine)
// ---------------------------------------------------------------------------

// provisionDeepInfra validates the DeepInfra key and returns an endpoint
// binding. There is no server to create — DeepInfra is serverless — so the
// "resource" is the OpenAI-compatible base URL + the chosen model. If
// opts["bindProject"] is set, the inference config is written into that vault
// project so the app's companion picks it up.
func provisionDeepInfra(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderDeepInfra, "token")
	if token == "" {
		return &ProvisionResult{Provider: "deepinfra", Manual: "Connect DeepInfra first via /accounts/connect (deepinfra.com/dash/api_keys)."}, nil
	}
	model := strings.TrimSpace(opts["model"])
	if model == "" {
		model = "nvidia/NVIDIA-Nemotron-3-Super-120B-A12B"
	}
	if !VoiceSafeModel(model) && opts["allowUnsafe"] != "true" {
		return &ProvisionResult{Provider: "deepinfra", Manual: fmt.Sprintf(
			"model %q is a reasoning/batch model — not voice-safe (TTFT dead air). Pass allowUnsafe=true to bind it anyway, or pick a voiceSafe model from gpu_plans.", model)}, nil
	}
	// Validate reachability + credential (GET /models). Reuses the
	// runner-provider preflight derivation so the probe shape stays in sync.
	if err := deepInfraValidate(token); err != nil {
		return nil, err
	}
	details := map[string]string{"model": model, "baseUrl": deepInfraOpenAIBase, "kind": "serverless"}
	notes := "DeepInfra serverless binding validated. No machine — per-token billing. Use as the always-on inference baseline."
	if proj := strings.TrimSpace(opts["bindProject"]); proj != "" {
		written := writeInferenceBinding(proj, inferenceBinding{
			BaseURL: deepInfraOpenAIBase, APIKey: token, Model: model,
			ASRModel: strings.TrimSpace(opts["asrModel"]),
		})
		details["boundProject"] = proj
		details["boundKeys"] = strings.Join(written, ",")
		notes += fmt.Sprintf(" Bound into vault project %q (%s).", proj, strings.Join(written, ","))
	}
	return &ProvisionResult{
		OK: true, Provider: "deepinfra", Resource: "serverless-binding",
		ID: "deepinfra:" + model, ConnectionString: deepInfraOpenAIBase,
		Details: details, Notes: notes,
	}, nil
}

// deepInfraValidate probes DeepInfra's /models with the key. 2xx or an explicit
// auth rejection both prove the endpoint is reachable; only a transport error
// or a 5xx is treated as a hard failure.
func deepInfraValidate(token string) error {
	req, err := http.NewRequest(http.MethodGet, providerModelsProbeURL(deepInfraOpenAIBase), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := provisionHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("deepinfra unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("deepinfra rejected the API key (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("deepinfra error HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Salad — GPU container-group provisioner (hourly burst)
// ---------------------------------------------------------------------------

// saladContainerGroup is the slice of Salad's container-group object we use.
type saladContainerGroup struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CurrentState struct {
		Status string `json:"status"`
	} `json:"current_state"`
	Networking struct {
		DNS  string `json:"dns"`
		Port int    `json:"port"`
	} `json:"networking"`
}

// saladEndpoint builds the OpenAI-compatible base URL Salad exposes for a group
// (https://<dns>/v1). Empty until the group's networking DNS is assigned.
func saladEndpoint(g saladContainerGroup) string {
	if strings.TrimSpace(g.Networking.DNS) == "" {
		return ""
	}
	return "https://" + strings.TrimRight(g.Networking.DNS, "/") + "/v1"
}

// provisionSalad creates a GPU container group running an OpenAI-compatible
// inference server (vLLM by default). org + project are per-provision opts (not
// stored with the account). The numeric/string group id is returned so the
// group can be destroyed (saladDeleteContainerGroup). The access endpoint may
// be empty at create time — Salad assigns DNS as the group boots; poll
// saladGetContainerGroup until Networking.DNS is set, then bind.
func provisionSalad(name string, opts map[string]string) (*ProvisionResult, error) {
	token := accountField(ProviderSalad, "token")
	if token == "" {
		return &ProvisionResult{Provider: "salad", Manual: "Connect Salad first via /accounts/connect (portal.salad.com → API Access)."}, nil
	}
	org := strings.TrimSpace(opts["organization"])
	project := strings.TrimSpace(opts["project"])
	if org == "" || project == "" {
		return &ProvisionResult{Provider: "salad", Manual: "Salad needs organization + project in opts (e.g. {\"organization\":\"my-org\",\"project\":\"voice\",\"gpu\":\"rtx4090\"})."}, nil
	}
	gpu := strings.TrimSpace(opts["gpu"])
	if gpu == "" {
		gpu = "rtx4090"
	}
	image := strings.TrimSpace(opts["image"])
	if image == "" {
		image = "vllm/vllm-openai:latest"
	}
	port := 8000
	replicas := 1
	g, err := saladCreateContainerGroup(token, org, project, saladCreateReq{
		Name: saladSafeName(name), Image: image, GPUClass: gpu, Port: port, Replicas: replicas,
		Model: strings.TrimSpace(opts["model"]),
	})
	if err != nil {
		return nil, err
	}
	endpoint := saladEndpoint(g)
	details := map[string]string{
		"organization": org, "project": project, "gpu": gpu, "image": image,
		"status": g.CurrentState.Status, "kind": "gpu-group",
	}
	if endpoint != "" {
		details["endpoint"] = endpoint
	}
	notes := "Salad GPU container group creating. Poll gpu_status until endpoint (Networking.DNS) is assigned, then gpu_bind it into your app's vault project. Hourly billing — reap when idle (the autoscaler does this automatically)."
	return &ProvisionResult{
		OK: true, Provider: "salad", Resource: "container-group", ID: g.ID,
		ConnectionString: endpoint, Details: details, Notes: notes,
	}, nil
}

type saladCreateReq struct {
	Name     string
	Image    string
	GPUClass string
	Port     int
	Replicas int
	Model    string
}

// saladCreateContainerGroup POSTs a container-group create. The body follows
// the Salad Container Engine public API shape; gpu class ids are resolved from
// the friendly catalog id when possible, else passed through verbatim.
func saladCreateContainerGroup(token, org, project string, r saladCreateReq) (saladContainerGroup, error) {
	url := fmt.Sprintf("%s/organizations/%s/projects/%s/containers", saladPublicAPI, org, project)
	cmd := []string{}
	if r.Model != "" {
		cmd = []string{"--model", r.Model}
	}
	body := map[string]interface{}{
		"name": saladSafeName(r.Name),
		"container": map[string]interface{}{
			"image": r.Image,
			"resources": map[string]interface{}{
				"cpu":          4,
				"memory":       16384,
				"gpu_classes":  []string{r.GPUClass},
			},
			"command": cmd,
		},
		"autostart_policy": true,
		"restart_policy":   "always",
		"replicas":         r.Replicas,
		"networking": map[string]interface{}{
			"protocol": "http", "port": r.Port, "auth": false,
		},
	}
	var out saladContainerGroup
	if err := doJSON("POST", url, saladHeaders(token), body, &out); err != nil {
		return saladContainerGroup{}, err
	}
	return out, nil
}

// saladGetContainerGroup fetches a group (used to poll for the assigned DNS).
func saladGetContainerGroup(token, org, project, id string) (saladContainerGroup, error) {
	url := fmt.Sprintf("%s/organizations/%s/projects/%s/containers/%s", saladPublicAPI, org, project, id)
	var out saladContainerGroup
	if err := doJSON("GET", url, saladHeaders(token), nil, &out); err != nil {
		return saladContainerGroup{}, err
	}
	return out, nil
}

// saladDeleteContainerGroup stops the hourly bill by deleting the group.
func saladDeleteContainerGroup(token, org, project, id string) error {
	url := fmt.Sprintf("%s/organizations/%s/projects/%s/containers/%s", saladPublicAPI, org, project, id)
	return doJSON("DELETE", url, saladHeaders(token), nil, nil)
}

func saladHeaders(token string) map[string]string {
	return map[string]string{"Salad-Api-Key": token}
}

// saladSafeName lowercases + restricts to the [a-z0-9-] charset Salad accepts.
func saladSafeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "yaver-gpu"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == ' ' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "yaver-gpu"
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}

// ---------------------------------------------------------------------------
// Inference binding — the seam that ties provisioning to the running app
// ---------------------------------------------------------------------------

// inferenceBinding is the app-runtime inference config written into a vault
// project the app's companion reads (CompanionEnvSource{Vault}). The env-var
// names match what the call-center app reads (e-back/.../src/config.ts):
// DEEPINFRA_BASE_URL / DEEPINFRA_API_KEY / LLM_MODEL / ASR_BASE_URL / ASR_MODEL
// / TTS_URL. These names are the de-facto OpenAI-compatible contract; a
// different app maps them in its own config.
type inferenceBinding struct {
	BaseURL  string
	APIKey   string
	Model    string
	ASRBase  string
	ASRModel string
	TTSURL   string
}

// writeInferenceBinding writes the binding into the given vault project and
// returns the list of KEY NAMES written (never the values). No-op (returns nil)
// when no runtime vault is mounted. Empty fields are skipped so a rebind that
// only changes the LLM endpoint doesn't clobber an unrelated ASR/TTS setting.
func writeInferenceBinding(project string, b inferenceBinding) []string {
	vs := currentRuntimeVaultStore()
	if vs == nil {
		return nil
	}
	if strings.TrimSpace(project) == "" {
		project = inferenceVaultDefaultProject
	}
	pairs := []struct{ k, v string }{
		{"DEEPINFRA_BASE_URL", b.BaseURL},
		{"DEEPINFRA_API_KEY", b.APIKey},
		{"LLM_MODEL", b.Model},
		{"ASR_BASE_URL", b.ASRBase},
		{"ASR_MODEL", b.ASRModel},
		{"TTS_URL", b.TTSURL},
	}
	var written []string
	for _, p := range pairs {
		if strings.TrimSpace(p.v) == "" {
			continue
		}
		cat := "config"
		if strings.HasSuffix(p.k, "_API_KEY") {
			cat = "secret"
		}
		if err := vs.Set(VaultEntry{Project: project, Name: p.k, Value: p.v, Category: cat}); err == nil {
			written = append(written, p.k)
		}
	}
	sort.Strings(written)
	return written
}

// rebindInference is the single mutation the scale verb + autoscaler use to
// point the app at a new inference backend (a DeepInfra model swap, or a freshly
// provisioned Salad endpoint). It writes the binding and returns the keys
// written. apiKey is resolved from the relevant account when empty.
func rebindInference(project, baseURL, apiKey, model string) []string {
	if apiKey == "" && strings.Contains(baseURL, "deepinfra.com") {
		apiKey = accountField(ProviderDeepInfra, "token")
	}
	return writeInferenceBinding(project, inferenceBinding{BaseURL: baseURL, APIKey: apiKey, Model: model})
}
