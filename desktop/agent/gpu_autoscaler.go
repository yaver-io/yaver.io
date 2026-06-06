package main

// gpu_autoscaler.go — the dispatcher. A cheap always-on box runs the app and
// keeps DeepInfra serverless as the always-available inference baseline (no
// cold-start risk). When sustained call load crosses a threshold it BURSTS a
// Salad GPU group (cheaper per-call at volume), drains gracefully when load
// falls, then reaps the group to stop the hourly bill. On every transition it
// REBINDS the app's vault inference config (the seam the companion reads), so
// new sessions move to the new backend while in-flight calls finish on the old
// one — no dropped calls.
//
// The state machine (Tick) is pure and clock-injected, so it's exhaustively
// unit-tested with a fake backend (gpu_autoscaler_test.go). The live backend
// (liveGPUBurstBackend) wires it to real Salad provisioning + the vault rebind.
// See docs/gpu-rental-orchestration.md §4 Gap-3.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// GPUBurstBackend is the side-effecting surface the autoscaler drives. Splitting
// it out keeps the policy/state machine testable without touching real cloud.
type GPUBurstBackend interface {
	// Provision requests a GPU group at gpuClass; returns an opaque id. The
	// endpoint is usually not ready yet (Salad assigns DNS as it boots).
	Provision(gpuClass string) (id string, err error)
	// Endpoint reports the group's OpenAI-compatible URL once assigned.
	Endpoint(id string) (endpoint string, ready bool, err error)
	// Destroy deletes the group (stops the hourly bill).
	Destroy(id string) error
	// Rebind points the app's inference config at endpoint (a Salad URL, or
	// the baseline DeepInfra URL when reverting). model may be "".
	Rebind(endpoint, model string) error
}

// GPUAutoscalerPolicy tunes burst/reap behaviour.
type GPUAutoscalerPolicy struct {
	BurstAtConcurrency   int           // sustained concurrency ≥ this → provision
	ReapBelowConcurrency int           // concurrency ≤ this → eligible to drain/reap
	SustainTicks         int           // consecutive ticks before acting (debounce)
	ReapAfterIdle        time.Duration // drain window: wait this long below threshold before reaping
	BurstGPUClass        string        // Salad class id to provision (e.g. "a100-80gb")
	BaselineEndpoint     string        // DeepInfra serverless base URL (always-on)
	BaselineModel        string        // model to bind on the baseline
	BurstModel           string        // model the Salad group serves (for rebind)
}

func (p GPUAutoscalerPolicy) withDefaults() GPUAutoscalerPolicy {
	if p.BurstAtConcurrency <= 0 {
		p.BurstAtConcurrency = 20
	}
	if p.ReapBelowConcurrency < 0 {
		p.ReapBelowConcurrency = 0
	}
	if p.ReapBelowConcurrency >= p.BurstAtConcurrency {
		p.ReapBelowConcurrency = p.BurstAtConcurrency / 2
	}
	if p.SustainTicks <= 0 {
		p.SustainTicks = 3
	}
	if p.ReapAfterIdle <= 0 {
		p.ReapAfterIdle = 5 * time.Minute
	}
	if p.BurstGPUClass == "" {
		p.BurstGPUClass = "a100-80gb"
	}
	return p
}

// LoadSample is one observation of app load (from the VoIP gateway /metrics).
type LoadSample struct {
	Concurrency int
	P95TTFTms   int
}

type gpuAutoState int

const (
	gpuBaseline     gpuAutoState = iota // on DeepInfra serverless
	gpuProvisioning                     // Salad group requested, waiting for endpoint
	gpuBursted                          // on Salad endpoint
	gpuDraining                         // decided to reap; waiting out the drain window
)

func (s gpuAutoState) String() string {
	switch s {
	case gpuProvisioning:
		return "provisioning"
	case gpuBursted:
		return "bursted"
	case gpuDraining:
		return "draining"
	default:
		return "baseline"
	}
}

// GPUAutoAction is the action a Tick took — surfaced for logging + tests.
type GPUAutoAction string

const (
	ActNone         GPUAutoAction = "none"
	ActProvision    GPUAutoAction = "provision"
	ActWaitEndpoint GPUAutoAction = "wait-endpoint"
	ActBurst        GPUAutoAction = "burst"        // rebound to the Salad group
	ActDrainStart   GPUAutoAction = "drain-start"  // load fell; draining before reap
	ActDrainCancel  GPUAutoAction = "drain-cancel" // load returned; stay bursted
	ActReap         GPUAutoAction = "reap"         // rebound to baseline + destroyed group
)

// GPUAutoscaler holds the live state. Tick is the only mutator; it's guarded by
// a mutex so a metrics goroutine and a control call can't race.
type GPUAutoscaler struct {
	mu     sync.Mutex
	policy GPUAutoscalerPolicy
	be     GPUBurstBackend
	now    func() time.Time

	state         gpuAutoState
	burstID       string
	burstEndpoint string
	aboveCount    int
	belowCount    int
	drainSince    time.Time
	lastSample    LoadSample

	// OnTransition, if set, is called after every state change (action !=
	// ActNone) with a secret-free snapshot. The live wiring uses it to emit
	// privacy-safe Convex bookkeeping (buildGpuRentalUpsertPayload); tests
	// use it to assert transitions. Decoupled so the state machine never
	// imports the syncer. Invoked outside the lock.
	OnTransition func(GPUAutoAction, GPUAutoscalerSnapshot)
}

// NewGPUAutoscaler builds an autoscaler. now may be nil (defaults to time.Now).
func NewGPUAutoscaler(policy GPUAutoscalerPolicy, be GPUBurstBackend, now func() time.Time) *GPUAutoscaler {
	if now == nil {
		now = time.Now
	}
	return &GPUAutoscaler{policy: policy.withDefaults(), be: be, now: now, state: gpuBaseline}
}

// Tick advances the state machine by one observation and performs at most one
// backend side effect. Returns the action taken (or ActNone). The OnTransition
// hook (if set) fires outside the lock for any non-None action.
func (a *GPUAutoscaler) Tick(s LoadSample) (GPUAutoAction, error) {
	a.mu.Lock()
	action, err := a.tickLocked(s)
	snap := a.snapshotLocked()
	a.mu.Unlock()
	if action != ActNone && a.OnTransition != nil {
		a.OnTransition(action, snap)
	}
	return action, err
}

func (a *GPUAutoscaler) tickLocked(s LoadSample) (GPUAutoAction, error) {
	a.lastSample = s

	// Debounce counters: a sample is "high" (≥ burst), "low" (≤ reap), or
	// "mid" (stable — decays both so we don't act on a single spike).
	switch {
	case s.Concurrency >= a.policy.BurstAtConcurrency:
		a.aboveCount++
		a.belowCount = 0
	case s.Concurrency <= a.policy.ReapBelowConcurrency:
		a.belowCount++
		a.aboveCount = 0
	default:
		if a.aboveCount > 0 {
			a.aboveCount--
		}
		if a.belowCount > 0 {
			a.belowCount--
		}
	}

	switch a.state {
	case gpuBaseline:
		if a.aboveCount >= a.policy.SustainTicks {
			id, err := a.be.Provision(a.policy.BurstGPUClass)
			if err != nil {
				return ActNone, fmt.Errorf("provision: %w", err)
			}
			a.burstID = id
			a.state = gpuProvisioning
			return ActProvision, nil
		}
		return ActNone, nil

	case gpuProvisioning:
		endpoint, ready, err := a.be.Endpoint(a.burstID)
		if err != nil {
			return ActWaitEndpoint, fmt.Errorf("endpoint poll: %w", err)
		}
		if !ready || endpoint == "" {
			return ActWaitEndpoint, nil
		}
		if err := a.be.Rebind(endpoint, a.policy.BurstModel); err != nil {
			return ActNone, fmt.Errorf("rebind to burst: %w", err)
		}
		a.burstEndpoint = endpoint
		a.state = gpuBursted
		return ActBurst, nil

	case gpuBursted:
		if a.belowCount >= a.policy.SustainTicks {
			a.drainSince = a.now()
			a.state = gpuDraining
			return ActDrainStart, nil
		}
		return ActNone, nil

	case gpuDraining:
		// Load came back: cancel the drain, stay on the Salad group.
		if a.aboveCount >= a.policy.SustainTicks {
			a.state = gpuBursted
			a.drainSince = time.Time{}
			return ActDrainCancel, nil
		}
		// Drain window elapsed: revert to baseline FIRST (new sessions move
		// off the group), then destroy it. In-flight calls already finished
		// during the window.
		if a.now().Sub(a.drainSince) >= a.policy.ReapAfterIdle {
			if err := a.be.Rebind(a.policy.BaselineEndpoint, a.policy.BaselineModel); err != nil {
				return ActNone, fmt.Errorf("rebind to baseline: %w", err)
			}
			if err := a.be.Destroy(a.burstID); err != nil {
				// Rebind already happened; surface destroy error but don't
				// leave the machine stuck in draining — the group is no
				// longer serving traffic. Reset state; the orphaned group is
				// reported so it can be reaped by gpu_destroy.
				a.resetToBaseline()
				return ActReap, fmt.Errorf("group %s reverted but destroy failed (orphan — reap via gpu_destroy): %w", a.burstID, err)
			}
			a.resetToBaseline()
			return ActReap, nil
		}
		return ActNone, nil
	}
	return ActNone, nil
}

func (a *GPUAutoscaler) resetToBaseline() {
	a.state = gpuBaseline
	a.burstID = ""
	a.burstEndpoint = ""
	a.drainSince = time.Time{}
	a.aboveCount = 0
	a.belowCount = 0
}

// Snapshot is the public, secret-free view of the autoscaler (for status UIs).
type GPUAutoscalerSnapshot struct {
	State         string     `json:"state"`
	BurstID       string     `json:"burstId,omitempty"`
	BurstEndpoint string     `json:"burstEndpoint,omitempty"`
	LastSample    LoadSample `json:"lastSample"`
	BurstGPUClass string     `json:"burstGpuClass"`
}

func (a *GPUAutoscaler) Snapshot() GPUAutoscalerSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked()
}

func (a *GPUAutoscaler) snapshotLocked() GPUAutoscalerSnapshot {
	return GPUAutoscalerSnapshot{
		State:         a.state.String(),
		BurstID:       a.burstID,
		BurstEndpoint: a.burstEndpoint,
		LastSample:    a.lastSample,
		BurstGPUClass: a.policy.BurstGPUClass,
	}
}

// ---------------------------------------------------------------------------
// Live backend — wires the autoscaler to real Salad + the vault rebind
// ---------------------------------------------------------------------------

// liveGPUBurstBackend implements GPUBurstBackend against a real Salad account.
// Token is the caller's BYO Salad key (vault accounts store), never a field.
type liveGPUBurstBackend struct {
	token       string
	org         string
	project     string
	bindProject string // vault project the app companion reads
	image       string
}

func newLiveGPUBurstBackend(org, project, bindProject, image string) (*liveGPUBurstBackend, error) {
	token := accountField(ProviderSalad, "token")
	if token == "" {
		return nil, fmt.Errorf("Salad not connected — /accounts/connect first (BYO token)")
	}
	if strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("salad organization + project required")
	}
	if strings.TrimSpace(bindProject) == "" {
		bindProject = inferenceVaultDefaultProject
	}
	if strings.TrimSpace(image) == "" {
		image = "vllm/vllm-openai:latest"
	}
	return &liveGPUBurstBackend{token: token, org: org, project: project, bindProject: bindProject, image: image}, nil
}

func (b *liveGPUBurstBackend) Provision(gpuClass string) (string, error) {
	g, err := saladCreateContainerGroup(b.token, b.org, b.project, saladCreateReq{
		Name: saladSafeName("yaver-burst"), Image: b.image, GPUClass: gpuClass, Port: 8000, Replicas: 1,
	})
	if err != nil {
		return "", err
	}
	return g.ID, nil
}

func (b *liveGPUBurstBackend) Endpoint(id string) (string, bool, error) {
	g, err := saladGetContainerGroup(b.token, b.org, b.project, id)
	if err != nil {
		return "", false, err
	}
	ep := saladEndpoint(g)
	return ep, ep != "" && strings.EqualFold(g.CurrentState.Status, "running"), nil
}

func (b *liveGPUBurstBackend) Destroy(id string) error {
	return saladDeleteContainerGroup(b.token, b.org, b.project, id)
}

func (b *liveGPUBurstBackend) Rebind(endpoint, model string) error {
	// Salad groups created with auth:false need no key; rebindInference fills
	// the DeepInfra key automatically when reverting to the baseline URL.
	if currentRuntimeVaultStore() == nil {
		return fmt.Errorf("no runtime vault mounted — cannot rebind inference")
	}
	rebindInference(b.bindProject, endpoint, "", model)
	return nil
}
