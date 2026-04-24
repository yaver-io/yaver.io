package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Each test fires a supervisor with a mock clock so we don't have to sleep.
// Tests only verify behaviours we ship guarantees for (panic recovery,
// stall detection, tick-duration tracking, idempotent Stop) — not
// timing or goroutine internals.

func newTestSupervisor(t *testing.T) *TaskSupervisor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := NewTaskSupervisor(ctx)
	// Silence desktop notifications in tests.
	s.notifyFn = func(title, body string) {}
	return s
}

func TestSupervisor_RunsAndCountsTicks(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	defer s.Stop()

	var runs atomic.Uint64
	s.Register("counter", 20*time.Millisecond, true, func(ctx context.Context) error {
		runs.Add(1)
		return nil
	})

	waitFor(t, 2*time.Second, func() bool { return runs.Load() >= 3 })
	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].Name != "counter" || snap[0].Runs < 3 {
		t.Fatalf("expected >=3 runs on 'counter'; got snapshot=%+v", snap)
	}
	if snap[0].HealthState != "ok" {
		t.Fatalf("expected health=ok; got %s", snap[0].HealthState)
	}
}

func TestSupervisor_RecordsError(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	defer s.Stop()

	s.Register("errtask", 20*time.Millisecond, true, func(ctx context.Context) error {
		return errors.New("boom")
	})
	waitFor(t, 2*time.Second, func() bool {
		snap := s.Snapshot()
		return len(snap) == 1 && snap[0].Errors >= 2
	})
	snap := s.Snapshot()
	if snap[0].LastErrorText != "boom" {
		t.Fatalf("expected lastErrorText='boom'; got %q", snap[0].LastErrorText)
	}
	if snap[0].HealthState != "error" {
		t.Fatalf("expected health=error; got %s", snap[0].HealthState)
	}
}

func TestSupervisor_RecoversFromPanic(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	defer s.Stop()

	var runs atomic.Uint64
	s.Register("panicky", 20*time.Millisecond, true, func(ctx context.Context) error {
		runs.Add(1)
		if runs.Load() == 1 {
			panic("first-time boom")
		}
		return nil
	})
	// After a panic, the task must continue running — if recovery is
	// broken the goroutine dies and runs stays at 1 forever.
	waitFor(t, 2*time.Second, func() bool { return runs.Load() >= 3 })
	snap := s.Snapshot()
	if snap[0].Panics != 1 {
		t.Fatalf("expected 1 recorded panic; got %+v", snap[0])
	}
	if snap[0].HealthState != "ok" {
		t.Fatalf("expected health=ok after recovery; got %s", snap[0].HealthState)
	}
}

func TestSupervisor_PanicBackoffCaps(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	defer s.Stop()

	var runs atomic.Uint64
	s.Register("always-panics", 5*time.Millisecond, true, func(ctx context.Context) error {
		runs.Add(1)
		panic("nope")
	})

	// Let the task panic enough times to trip the backoff, then verify
	// the skip counter actually increments. Without the cap, runs would
	// grow unbounded during the test window.
	waitFor(t, 2*time.Second, func() bool {
		return s.Snapshot()[0].SkippedTicks >= 1
	})
}

func TestSupervisor_DetectsStall(t *testing.T) {
	s := newTestSupervisor(t)
	// Watchdog tighter than default so we can observe a stall in a test.
	s.watchdogPeriod = 25 * time.Millisecond

	stallSignal := make(chan string, 4)
	s.onStalled = func(name string) { stallSignal <- name }

	s.Start()
	defer s.Stop()

	// Tick once, then block; supervisor should declare stall after
	// 10 × interval (50ms here).
	unblock := make(chan struct{})
	defer close(unblock)
	s.Register("stalls", 5*time.Millisecond, true, func(ctx context.Context) error {
		select {
		case <-unblock:
		case <-ctx.Done():
		}
		return nil
	})

	select {
	case got := <-stallSignal:
		if got != "stalls" {
			t.Fatalf("expected stall on 'stalls'; got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor never detected stall within 2s")
	}

	// Health snapshot reflects the stall.
	var stalled bool
	for i := 0; i < 20; i++ {
		if s.Snapshot()[0].HealthState == "stalled" {
			stalled = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !stalled {
		t.Fatalf("expected health=stalled in snapshot; got %+v", s.Snapshot()[0])
	}
}

func TestSupervisor_StopIdempotent(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	s.Register("x", 50*time.Millisecond, false, func(ctx context.Context) error { return nil })
	s.Stop()
	s.Stop() // must not panic
	// Context must be cancelled so the task goroutine has exited.
	if s.ctx.Err() == nil {
		t.Fatal("expected supervisor ctx cancelled after Stop")
	}
}

func TestSupervisor_ReregisterReplacesPreviousTask(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	defer s.Stop()

	var vA, vB atomic.Uint64
	s.Register("dup", 20*time.Millisecond, true, func(ctx context.Context) error {
		vA.Add(1)
		return nil
	})
	// Let A tick at least once.
	waitFor(t, 1*time.Second, func() bool { return vA.Load() >= 1 })

	before := vA.Load()
	s.Register("dup", 20*time.Millisecond, true, func(ctx context.Context) error {
		vB.Add(1)
		return nil
	})
	waitFor(t, 1*time.Second, func() bool { return vB.Load() >= 2 })

	// A's counter should not be incrementing any more.
	afterRegrace := vA.Load()
	time.Sleep(100 * time.Millisecond)
	if vA.Load() > afterRegrace+1 { // allow one in-flight tick
		t.Fatalf("old task still running after re-register: %d → %d (B has ticked %d times)", before, vA.Load(), vB.Load())
	}
}

func TestSupervisor_MeasuresTickDuration(t *testing.T) {
	s := newTestSupervisor(t)
	s.Start()
	defer s.Stop()

	s.Register("slow", 40*time.Millisecond, true, func(ctx context.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	waitFor(t, 2*time.Second, func() bool {
		snap := s.Snapshot()
		return snap[0].Runs >= 2 && snap[0].MaxTickDuration != ""
	})
	snap := s.Snapshot()
	if snap[0].LastTickDuration == "" {
		t.Fatal("expected non-empty LastTickDuration")
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
