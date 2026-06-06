package main

// gpu_autoscaler_loop.go — the live run-loop that drives a GPUAutoscaler on a
// dispatcher box: poll the app's metrics endpoint for current call load, Tick
// the state machine, let it burst/drain/reap Salad GPU and rebind the app's
// vault inference config. Controlled by the gpu_autoscale_{start,status,stop}
// ops verbs. Best-effort Convex bookkeeping via globalConvexSync.
//
// The metrics contract is generic (any app can satisfy it): the metricsUrl
// returns JSON {"concurrency": <int>, "p95TtftMs": <int>}. The call-center's
// VoIP gateway exposes calls-in-flight + rolling TTFT; aliases activeCalls /
// active_calls and ttftP95 / p95_ttft_ms are accepted. See
// docs/gpu-rental-orchestration.md §4 Gap-3.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type managedAutoscaler struct {
	key        string
	a          *GPUAutoscaler
	cancel     context.CancelFunc
	metricsURL string
	intervalS  int
	startedAt  time.Time
	lastErr    string
	mu         sync.Mutex
}

func (m *managedAutoscaler) setErr(e string) {
	m.mu.Lock()
	m.lastErr = e
	m.mu.Unlock()
}

func (m *managedAutoscaler) getErr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

var (
	gpuAutoscalersMu sync.Mutex
	gpuAutoscalers   = map[string]*managedAutoscaler{}
)

// gpuMetricsClient is overridable in tests.
var gpuMetricsClient = &http.Client{Timeout: 8 * time.Second}

// fetchLoadSample GETs the metrics endpoint and parses a LoadSample, tolerating
// a few field-name aliases so different apps can satisfy the contract.
func fetchLoadSample(ctx context.Context, url string) (LoadSample, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return LoadSample{}, err
	}
	resp, err := gpuMetricsClient.Do(req)
	if err != nil {
		return LoadSample{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LoadSample{}, fmt.Errorf("metrics HTTP %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return LoadSample{}, fmt.Errorf("metrics not JSON: %w", err)
	}
	num := func(keys ...string) int {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				if f, ok := v.(float64); ok {
					return int(f)
				}
			}
		}
		return 0
	}
	return LoadSample{
		Concurrency: num("concurrency", "activeCalls", "active_calls", "inFlight"),
		P95TTFTms:   num("p95TtftMs", "ttftP95", "p95_ttft_ms"),
	}, nil
}

type gpuAutoscaleStartReq struct {
	Key          string `json:"key"`
	Organization string `json:"organization"`
	Project      string `json:"project"`
	BindProject  string `json:"bindProject"`
	MetricsURL   string `json:"metricsUrl"`
	GPUClass     string `json:"gpuClass"`
	Image        string `json:"image"`
	BurstAt      int    `json:"burstAt"`
	ReapBelow    int    `json:"reapBelow"`
	SustainTicks int    `json:"sustainTicks"`
	ReapAfterSec int    `json:"reapAfterSec"`
	IntervalSec  int    `json:"intervalSec"`
	BaselineURL  string `json:"baselineUrl"`
	BaselineMdl  string `json:"baselineModel"`
	BurstModel   string `json:"burstModel"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_autoscale_start",
		Description: "Start the GPU dispatcher on this box: poll metricsUrl for call load and auto-burst a Salad GPU group when sustained load crosses burstAt, draining + reaping when it falls (DeepInfra serverless stays the always-on baseline). Rebinds the app's vault inference config on each transition. metricsUrl must return JSON {concurrency, p95TtftMs}. Requires a connected Salad account + organization/project.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"organization", "project", "metricsUrl"},
			"properties": map[string]interface{}{
				"key":           map[string]interface{}{"type": "string", "description": "Unique id for this dispatcher (default: bindProject). Used by status/stop."},
				"organization":  map[string]interface{}{"type": "string"},
				"project":       map[string]interface{}{"type": "string"},
				"bindProject":   map[string]interface{}{"type": "string", "description": "Vault project the app companion reads (default: callcenter)"},
				"metricsUrl":    map[string]interface{}{"type": "string"},
				"gpuClass":      map[string]interface{}{"type": "string", "description": "Salad GPU class to burst (default a100-80gb)"},
				"image":         map[string]interface{}{"type": "string"},
				"burstAt":       map[string]interface{}{"type": "integer", "description": "Concurrency to burst at (default 20)"},
				"reapBelow":     map[string]interface{}{"type": "integer"},
				"sustainTicks":  map[string]interface{}{"type": "integer"},
				"reapAfterSec":  map[string]interface{}{"type": "integer", "description": "Drain window seconds before reap (default 300)"},
				"intervalSec":   map[string]interface{}{"type": "integer", "description": "Metrics poll interval (default 15)"},
				"baselineUrl":   map[string]interface{}{"type": "string", "description": "Baseline inference URL (default DeepInfra)"},
				"baselineModel": map[string]interface{}{"type": "string"},
				"burstModel":    map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGPUAutoscaleStartHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_autoscale_status",
		Description: "Show running GPU dispatchers on this box (state, burst group id/endpoint, last load sample). Pass key to filter.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{"key": map[string]interface{}{"type": "string"}},
			"additionalProperties": false,
		},
		Handler:    opsGPUAutoscaleStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gpu_autoscale_stop",
		Description: "Stop a GPU dispatcher loop (does NOT reap a running burst group — use gpu_destroy for that, or let it drain first). Requires key.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"required":             []string{"key"},
			"properties":           map[string]interface{}{"key": map[string]interface{}{"type": "string"}},
			"additionalProperties": false,
		},
		Handler:    opsGPUAutoscaleStopHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsGPUAutoscaleStartHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gpuAutoscaleStartReq
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Organization) == "" || strings.TrimSpace(p.Project) == "" || strings.TrimSpace(p.MetricsURL) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "organization, project, metricsUrl required"}
	}
	bindProject := strings.TrimSpace(p.BindProject)
	if bindProject == "" {
		bindProject = inferenceVaultDefaultProject
	}
	key := strings.TrimSpace(p.Key)
	if key == "" {
		key = bindProject
	}
	if currentRuntimeVaultStore() == nil {
		return OpsResult{OK: false, Code: "no_vault", Error: "no runtime vault mounted — cannot rebind inference"}
	}
	be, err := newLiveGPUBurstBackend(p.Organization, p.Project, bindProject, p.Image)
	if err != nil {
		return OpsResult{OK: false, Code: "no_account", Error: err.Error()}
	}
	baselineURL := strings.TrimSpace(p.BaselineURL)
	if baselineURL == "" {
		baselineURL = deepInfraOpenAIBase
	}
	policy := GPUAutoscalerPolicy{
		BurstAtConcurrency:   p.BurstAt,
		ReapBelowConcurrency: p.ReapBelow,
		SustainTicks:         p.SustainTicks,
		ReapAfterIdle:        time.Duration(p.ReapAfterSec) * time.Second,
		BurstGPUClass:        strings.TrimSpace(p.GPUClass),
		BaselineEndpoint:     baselineURL,
		BaselineModel:        strings.TrimSpace(p.BaselineMdl),
		BurstModel:           strings.TrimSpace(p.BurstModel),
	}
	a := NewGPUAutoscaler(policy, be, nil)
	// Best-effort bookkeeping: emit privacy-safe Convex rows on transitions.
	if globalConvexSync != nil {
		attachGpuRentalBookkeeping(a, globalConvexSync, globalConvexSync.deviceID, bindProject)
	}

	interval := p.IntervalSec
	if interval <= 0 {
		interval = 15
	}

	gpuAutoscalersMu.Lock()
	if _, exists := gpuAutoscalers[key]; exists {
		gpuAutoscalersMu.Unlock()
		return OpsResult{OK: false, Code: "already_running", Error: "a dispatcher with key " + key + " is already running (gpu_autoscale_stop first)"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &managedAutoscaler{key: key, a: a, cancel: cancel, metricsURL: strings.TrimSpace(p.MetricsURL), intervalS: interval, startedAt: time.Now()}
	gpuAutoscalers[key] = m
	gpuAutoscalersMu.Unlock()

	go runAutoscaleLoop(ctx, m)

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"key":         key,
		"bindProject": bindProject,
		"intervalSec": interval,
		"baseline":    sanitizeInferenceEndpoint(baselineURL),
		"hint":        "DeepInfra serverless is the always-on baseline; Salad bursts in on sustained load. gpu_autoscale_status to watch; gpu_autoscale_stop to halt.",
	}}
}

func runAutoscaleLoop(ctx context.Context, m *managedAutoscaler) {
	defer func() {
		gpuAutoscalersMu.Lock()
		delete(gpuAutoscalers, m.key)
		gpuAutoscalersMu.Unlock()
	}()
	ticker := time.NewTicker(time.Duration(m.intervalS) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample, err := fetchLoadSample(ctx, m.metricsURL)
			if err != nil {
				m.setErr(err.Error())
				continue // transient metrics blip — don't act on missing data
			}
			m.setErr("")
			if _, terr := m.a.Tick(sample); terr != nil {
				m.setErr(terr.Error())
			}
		}
	}
}

func opsGPUAutoscaleStatusHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Key string `json:"key"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	gpuAutoscalersMu.Lock()
	defer gpuAutoscalersMu.Unlock()
	out := []map[string]interface{}{}
	for key, m := range gpuAutoscalers {
		if p.Key != "" && p.Key != key {
			continue
		}
		snap := m.a.Snapshot()
		out = append(out, map[string]interface{}{
			"key":         key,
			"state":       snap.State,
			"burstId":     snap.BurstID,
			"endpoint":    sanitizeInferenceEndpoint(snap.BurstEndpoint),
			"gpuClass":    snap.BurstGPUClass,
			"lastSample":  snap.LastSample,
			"metricsUrl":  sanitizeInferenceEndpoint(m.metricsURL),
			"intervalSec": m.intervalS,
			"startedAt":   m.startedAt.Format(time.RFC3339),
			"lastError":   m.getErr(),
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"dispatchers": out}}
}

func opsGPUAutoscaleStopHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Key) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "key required"}
	}
	gpuAutoscalersMu.Lock()
	m, ok := gpuAutoscalers[p.Key]
	gpuAutoscalersMu.Unlock()
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "no dispatcher with key " + p.Key}
	}
	m.cancel()
	return OpsResult{OK: true, Initial: map[string]interface{}{"stopped": p.Key, "notes": "loop halted; any running burst group left intact — reap via gpu_destroy if desired"}}
}
