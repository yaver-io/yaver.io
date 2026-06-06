package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- catalog + voice-safety rules ---

func TestVoiceSafeModel(t *testing.T) {
	cases := map[string]bool{
		"nvidia/NVIDIA-Nemotron-3-Super-120B-A12B": true,
		"meta-llama/Llama-3.3-70B-Instruct-Turbo":  true,
		"mistralai/Voxtral-Mini-3B-2507":           true,
		"Qwen/Qwen3-235B-A22B":                     true,
		"deepseek-ai/DeepSeek-R1":                  false, // reasoning
		"Qwen/QwQ-32B":                             false, // reasoning
		"some-model-thinking-preview":              false,
		"openai/o1":                                false,
		"":                                         true, // unknown → don't block
	}
	for model, want := range cases {
		if got := VoiceSafeModel(model); got != want {
			t.Errorf("VoiceSafeModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestGPURentalCatalog(t *testing.T) {
	di := gpuRentalCatalog("deepinfra")
	if len(di) == 0 {
		t.Fatal("deepinfra catalog empty")
	}
	for _, p := range di {
		if p.Provider != "deepinfra" || p.Kind != "serverless" {
			t.Errorf("deepinfra plan wrong shape: %+v", p)
		}
	}
	sa := gpuRentalCatalog("salad")
	if len(sa) == 0 {
		t.Fatal("salad catalog empty")
	}
	for _, p := range sa {
		if p.Provider != "salad" || p.Kind != "gpu-group" {
			t.Errorf("salad plan wrong shape: %+v", p)
		}
	}
	both := gpuRentalCatalog("")
	if len(both) != len(di)+len(sa) {
		t.Errorf("combined catalog = %d, want %d", len(both), len(di)+len(sa))
	}
	// The reasoning model MUST be flagged batch-only.
	var foundR1 bool
	for _, p := range di {
		if strings.Contains(p.ID, "DeepSeek-R1") {
			foundR1 = true
			if p.VoiceSafe {
				t.Error("DeepSeek-R1 must be voiceSafe=false")
			}
		}
	}
	if !foundR1 {
		t.Error("expected DeepSeek-R1 in deepinfra catalog as the batch-only example")
	}
}

// --- registry + providers wiring ---

func TestGPUProvisionersRegistered(t *testing.T) {
	reg := provisionerRegistry()
	if _, ok := reg[HostSalad]; !ok {
		t.Error("HostSalad must be in provisionerRegistry")
	}
	if _, ok := reg[HostDeepInfra]; !ok {
		t.Error("HostDeepInfra must be in provisionerRegistry")
	}
}

func TestGPUAccountProvidersRegistered(t *testing.T) {
	want := map[AccountProvider]bool{ProviderSalad: false, ProviderDeepInfra: false, ProviderRunPod: false, ProviderVast: false}
	for _, m := range AccountProviders() {
		if _, ok := want[m.ID]; ok {
			want[m.ID] = true
			if len(m.Fields) == 0 || m.Fields[0] != "token" {
				t.Errorf("%s should have a token field", m.ID)
			}
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("provider %s missing from AccountProviders()", id)
		}
	}
}

// --- inference binding into a real vault (redirected HOME, never the real one) ---

func TestWriteInferenceBinding(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate: never touch the user's real vault
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	setRuntimeVaultStore(vs)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	written := writeInferenceBinding("callcenter", inferenceBinding{
		BaseURL: "https://api.deepinfra.com/v1/openai",
		APIKey:  "di_secret_xyz",
		Model:   "nvidia/NVIDIA-Nemotron-3-Super-120B-A12B",
	})
	wantKeys := []string{"DEEPINFRA_API_KEY", "DEEPINFRA_BASE_URL", "LLM_MODEL"}
	sort.Strings(written)
	if strings.Join(written, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("written keys = %v, want %v", written, wantKeys)
	}
	// Values round-trip.
	e, err := vs.Get("callcenter", "DEEPINFRA_API_KEY")
	if err != nil || e == nil {
		t.Fatalf("get key: %v", err)
	}
	if e.Value != "di_secret_xyz" {
		t.Errorf("key value = %q", e.Value)
	}
	if e.Category != "secret" {
		t.Errorf("api key category = %q, want secret", e.Category)
	}
	m, _ := vs.Get("callcenter", "LLM_MODEL")
	if m == nil || m.Value != "nvidia/NVIDIA-Nemotron-3-Super-120B-A12B" {
		t.Errorf("model not bound: %+v", m)
	}
	// Empty fields are skipped (a partial rebind doesn't clobber ASR/TTS).
	if asr, _ := vs.Get("callcenter", "ASR_BASE_URL"); asr != nil {
		t.Error("empty ASR_BASE_URL should not be written")
	}
}

func TestWriteInferenceBindingNoVault(t *testing.T) {
	setRuntimeVaultStore(nil)
	if got := writeInferenceBinding("callcenter", inferenceBinding{BaseURL: "x", Model: "y"}); got != nil {
		t.Errorf("no vault → nil, got %v", got)
	}
}

// --- DeepInfra validation against a fake endpoint ---

func TestDeepInfraValidate(t *testing.T) {
	orig := deepInfraOpenAIBase
	t.Cleanup(func() { deepInfraOpenAIBase = orig })

	// 200 with models → ok
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"id":"nvidia/NVIDIA-Nemotron-3-Super-120B-A12B"}]}`)
	}))
	defer ok.Close()
	deepInfraOpenAIBase = ok.URL + "/v1"
	if err := deepInfraValidate("good"); err != nil {
		t.Errorf("valid key should pass: %v", err)
	}
	if err := deepInfraValidate("bad"); err == nil {
		t.Error("bad key should fail (401)")
	}

	// 500 → hard failure
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer down.Close()
	deepInfraOpenAIBase = down.URL + "/v1"
	if err := deepInfraValidate("good"); err == nil {
		t.Error("500 should be a hard failure")
	}
}

// --- Salad container-group client against a fake API ---

func TestSaladCreateAndEndpoint(t *testing.T) {
	orig := saladPublicAPI
	t.Cleanup(func() { saladPublicAPI = orig })

	var gotMethod, gotPath, gotKey string
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotKey = r.Method, r.URL.Path, r.Header.Get("Salad-Api-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{"id":"grp_123","name":"voice","current_state":{"status":"pending"},"networking":{"dns":"abc.salad.cloud","port":8000}}`)
	}))
	defer srv.Close()
	saladPublicAPI = srv.URL

	g, err := saladCreateContainerGroup("k3y", "my-org", "voice", saladCreateReq{
		Name: "Voice Box!", Image: "vllm/vllm-openai:latest", GPUClass: "a100-80gb", Port: 8000, Replicas: 1, Model: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %s", gotMethod)
	}
	if gotPath != "/organizations/my-org/projects/voice/containers" {
		t.Errorf("path = %s", gotPath)
	}
	if gotKey != "k3y" {
		t.Errorf("api key header = %q", gotKey)
	}
	if g.ID != "grp_123" {
		t.Errorf("id = %q", g.ID)
	}
	if ep := saladEndpoint(g); ep != "https://abc.salad.cloud/v1" {
		t.Errorf("endpoint = %q", ep)
	}
	// Body carried the gpu class + name was sanitized to salad's charset.
	if gotBody["name"] != "voice-box" {
		t.Errorf("name not sanitized: %v", gotBody["name"])
	}
}

func TestSaladDelete(t *testing.T) {
	orig := saladPublicAPI
	t.Cleanup(func() { saladPublicAPI = orig })
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(202)
	}))
	defer srv.Close()
	saladPublicAPI = srv.URL
	if err := saladDeleteContainerGroup("k", "o", "p", "grp_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/organizations/o/projects/p/containers/grp_1" {
		t.Errorf("delete sent %s %s", gotMethod, gotPath)
	}
}

// --- privacy: bookkeeping payload carries no secrets / paths ---

func TestSanitizeInferenceEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://abc.salad.cloud/v1":                     "https://abc.salad.cloud/v1",
		"https://user:secret@abc.salad.cloud/v1?token=x": "https://abc.salad.cloud/v1", // strip userinfo+query
		"https://api.deepinfra.com/v1/openai/":           "https://api.deepinfra.com/v1/openai",
		"ftp://nope":                                     "",
		"not a url":                                      "",
		"":                                               "",
	}
	for in, want := range cases {
		if got := sanitizeInferenceEndpoint(in); got != want {
			t.Errorf("sanitizeInferenceEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGpuRentalUpsertPayloadHasNoConfidentialFields(t *testing.T) {
	buf, teardown := installConvexRecorder(t)
	defer teardown()
	s := &convexSyncer{deviceID: "test-device"}
	// Deliberately hostile: an endpoint that smuggles a credential must be
	// scrubbed before it reaches the payload.
	syncGpuRental(s, GPURentalSummary{
		DeviceID:    "test-device",
		Provider:    "salad",
		ResourceID:  "grp_123",
		Kind:        "gpu-group",
		GPUClass:    "a100-80gb",
		Endpoint:    "https://user:supersecret@abc.salad.cloud/v1?token=leak",
		Model:       "meta-llama/Llama-3.3-70B-Instruct-Turbo",
		BindProject: "callcenter",
		VoiceSafe:   true,
		Status:      "running",
		HoursUsed:   1.5,
		CostCents:   135,
	})
	if len(*buf) != 1 {
		t.Fatalf("expected 1 recorded mutation, got %d", len(*buf))
	}
	for _, rec := range *buf {
		assertNoForbiddenFields(t, rec)
		assertNoAbsolutePaths(t, rec)
	}
	// The endpoint must be the sanitized public host (no userinfo/query).
	ep, _ := (*buf)[0].Args["endpoint"].(string)
	if ep != "https://abc.salad.cloud/v1" {
		t.Errorf("endpoint not sanitized in payload: %q", ep)
	}
	if strings.Contains(ep, "secret") || strings.Contains(ep, "leak") {
		t.Errorf("credential leaked into endpoint: %q", ep)
	}
}

func TestAutoscalerTransitionHookFires(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	be := &fakeBurstBackend{endpointReadyAfter: 1}
	a := newTestAutoscaler(be, clk.Now)
	var actions []GPUAutoAction
	a.OnTransition = func(act GPUAutoAction, snap GPUAutoscalerSnapshot) {
		actions = append(actions, act)
	}
	mustTick(t, a, 10, ActNone) // no transition → hook not called
	mustTick(t, a, 10, ActProvision)
	mustTick(t, a, 10, ActBurst)
	if len(actions) != 2 || actions[0] != ActProvision || actions[1] != ActBurst {
		t.Fatalf("hook actions = %v, want [provision burst]", actions)
	}
}

func TestSaladSafeName(t *testing.T) {
	cases := map[string]string{
		"Voice Box!":   "voice-box",
		"my_app":       "my-app",
		"  --x--  ":    "x",
		"":             "yaver-gpu",
		"!!!":          "yaver-gpu",
		"UPPER":        "upper",
	}
	for in, want := range cases {
		if got := saladSafeName(in); got != want {
			t.Errorf("saladSafeName(%q) = %q, want %q", in, got, want)
		}
	}
}
