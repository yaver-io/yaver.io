package main

import (
	"errors"
	"testing"
	"time"
)

type fakeBurstBackend struct {
	provisionCalls     int
	destroyCalls       int
	rebinds            []string
	endpointReadyAfter int // become ready on the Nth Endpoint() call
	endpointCalls      int
	failProvision      bool
}

func (f *fakeBurstBackend) Provision(class string) (string, error) {
	if f.failProvision {
		return "", errors.New("provision boom")
	}
	f.provisionCalls++
	return "grp_fake", nil
}

func (f *fakeBurstBackend) Endpoint(id string) (string, bool, error) {
	f.endpointCalls++
	if f.endpointCalls >= f.endpointReadyAfter {
		return "https://fake.salad.cloud/v1", true, nil
	}
	return "", false, nil
}

func (f *fakeBurstBackend) Destroy(id string) error { f.destroyCalls++; return nil }

func (f *fakeBurstBackend) Rebind(ep, model string) error {
	f.rebinds = append(f.rebinds, ep)
	return nil
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time            { return c.t }
func (c *fakeClock) advance(d time.Duration)   { c.t = c.t.Add(d) }

func newTestAutoscaler(be GPUBurstBackend, now func() time.Time) *GPUAutoscaler {
	return NewGPUAutoscaler(GPUAutoscalerPolicy{
		BurstAtConcurrency:   10,
		ReapBelowConcurrency: 2,
		SustainTicks:         2,
		ReapAfterIdle:        60 * time.Second,
		BurstGPUClass:        "a100-80gb",
		BaselineEndpoint:     "https://api.deepinfra.com/v1/openai",
		BaselineModel:        "base-model",
		BurstModel:           "burst-model",
	}, be, now)
}

func mustTick(t *testing.T, a *GPUAutoscaler, c int, want GPUAutoAction) {
	t.Helper()
	got, err := a.Tick(LoadSample{Concurrency: c})
	if err != nil {
		t.Fatalf("tick(c=%d): unexpected err %v", c, err)
	}
	if got != want {
		t.Fatalf("tick(c=%d) = %q, want %q (state=%s)", c, got, want, a.Snapshot().State)
	}
}

func TestAutoscalerFullBurstThenReap(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	be := &fakeBurstBackend{endpointReadyAfter: 2}
	a := newTestAutoscaler(be, clk.Now)

	mustTick(t, a, 10, ActNone)          // aboveCount=1
	mustTick(t, a, 12, ActProvision)     // aboveCount=2 ≥ sustain → provision
	if be.provisionCalls != 1 {
		t.Fatalf("provisionCalls=%d", be.provisionCalls)
	}
	mustTick(t, a, 12, ActWaitEndpoint)  // endpoint call #1 not ready
	mustTick(t, a, 12, ActBurst)         // endpoint call #2 ready → rebind to salad
	if len(be.rebinds) != 1 || be.rebinds[0] != "https://fake.salad.cloud/v1" {
		t.Fatalf("expected rebind to salad, got %v", be.rebinds)
	}
	if a.Snapshot().State != "bursted" {
		t.Fatalf("state=%s, want bursted", a.Snapshot().State)
	}

	mustTick(t, a, 0, ActNone)        // belowCount=1
	mustTick(t, a, 0, ActDrainStart) // belowCount=2 → drain
	mustTick(t, a, 0, ActNone)       // drain window not elapsed yet

	clk.advance(61 * time.Second)
	mustTick(t, a, 0, ActReap) // window elapsed → revert to baseline + destroy
	if be.destroyCalls != 1 {
		t.Fatalf("destroyCalls=%d, want 1", be.destroyCalls)
	}
	// Last rebind must be the baseline endpoint (revert before destroy).
	last := be.rebinds[len(be.rebinds)-1]
	if last != "https://api.deepinfra.com/v1/openai" {
		t.Fatalf("last rebind = %q, want baseline", last)
	}
	if a.Snapshot().State != "baseline" {
		t.Fatalf("state=%s, want baseline", a.Snapshot().State)
	}
}

func TestAutoscalerDrainCancelOnLoadReturn(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	be := &fakeBurstBackend{endpointReadyAfter: 1}
	a := newTestAutoscaler(be, clk.Now)

	// Drive to bursted.
	mustTick(t, a, 10, ActNone)
	mustTick(t, a, 10, ActProvision)
	mustTick(t, a, 10, ActBurst)

	// Fall into draining.
	mustTick(t, a, 0, ActNone)
	mustTick(t, a, 0, ActDrainStart)

	// Load returns before the window elapses → cancel, stay bursted, NO destroy.
	mustTick(t, a, 10, ActNone)        // aboveCount=1
	mustTick(t, a, 10, ActDrainCancel) // aboveCount=2 → cancel
	if be.destroyCalls != 0 {
		t.Fatalf("drain-cancel must not destroy; destroyCalls=%d", be.destroyCalls)
	}
	if a.Snapshot().State != "bursted" {
		t.Fatalf("state=%s, want bursted after cancel", a.Snapshot().State)
	}
}

func TestAutoscalerDebounceIgnoresSpike(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	be := &fakeBurstBackend{endpointReadyAfter: 1}
	a := newTestAutoscaler(be, clk.Now)

	// A single high spike between mid-range samples never sustains → no burst.
	mustTick(t, a, 12, ActNone) // above=1
	mustTick(t, a, 5, ActNone)  // mid → above decays to 0
	mustTick(t, a, 12, ActNone) // above=1
	mustTick(t, a, 5, ActNone)  // decays
	if be.provisionCalls != 0 {
		t.Fatalf("a transient spike must not provision; provisionCalls=%d", be.provisionCalls)
	}
}

func TestAutoscalerProvisionErrorStaysBaseline(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	be := &fakeBurstBackend{failProvision: true}
	a := newTestAutoscaler(be, clk.Now)
	mustTick(t, a, 10, ActNone)
	if _, err := a.Tick(LoadSample{Concurrency: 12}); err == nil {
		t.Fatal("expected provision error to surface")
	}
	if a.Snapshot().State != "baseline" {
		t.Fatalf("state=%s, want baseline after provision failure", a.Snapshot().State)
	}
}
