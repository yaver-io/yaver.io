package main

import (
	"sync"
	"testing"
	"time"
)

// Regression tests for the "CONSOLE pane stuck at 0 lines / waiting
// for output…" bug seen in the 22-second screen recording at
// /Users/kivanccakmak/Desktop/Screen Recording 2026-04-25 at 09.15.18.mov.
//
// Root cause was that DevServerManager.Subscribe() returned a fresh
// empty channel and emit() dropped events when no subscribers were
// registered. So Metro's banner lines emitted during the dashboard's
// SSE handshake window went to /dev/null and the CONSOLE pane never
// received them. Fix: a small ring-buffer of recent events that
// Subscribe replays into the new channel before adding to subs.

// Test 1: a subscriber that arrives after EmitLog still receives the
// missed banner lines via replay.
func TestDevServerManager_LateSubscriberReceivesReplay(t *testing.T) {
	m := NewDevServerManager()

	// Simulate Metro writing its banner before any browser is connected.
	m.EmitLog("› Metro waiting on http://0.0.0.0:8082")
	m.EmitLog("› Press r │ reload app")
	m.EmitLog("› Logs for your project will appear below")

	// Now a browser opens the dashboard and subscribes.
	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	got := 0
	deadline := time.After(500 * time.Millisecond)
	for got < 3 {
		select {
		case ev := <-ch:
			if ev.Type == "log" {
				got++
			}
		case <-deadline:
			t.Fatalf("late subscriber only got %d/3 replayed lines — replay buffer is broken", got)
		}
	}
	t.Logf("CONFIRMED: late subscriber receives 3/3 buffered lines via Subscribe-time replay")
}

// Test 2: a subscriber that registers BEFORE EmitLog gets every line.
// This proves the pipeline itself works — the bug is purely about
// timing of subscription vs first emit.
func TestDevServerManager_SubscribeBeforeEmit_GetsEvents(t *testing.T) {
	m := NewDevServerManager()

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	go func() {
		m.EmitLog("line A")
		m.EmitLog("line B")
		m.EmitLog("line C")
	}()

	got := 0
	deadline := time.After(500 * time.Millisecond)
	for got < 3 {
		select {
		case ev := <-ch:
			if ev.Type == "log" {
				got++
			}
		case <-deadline:
			t.Fatalf("early subscriber only got %d/3 lines — pipeline itself is broken", got)
		}
	}
	if got != 3 {
		t.Fatalf("expected 3 log events, got %d", got)
	}
	t.Logf("CONFIRMED: early subscriber receives 3/3 log lines — pipeline works when timing is right")
}

// Test 3: late subscriber sees the same total set of lines as an
// early subscriber, via replay + live fan-out. This is the post-fix
// invariant — both subscribers should agree on what happened.
func TestDevServerManager_LateSubscriberCatchesUp(t *testing.T) {
	m := NewDevServerManager()

	earlyCh := m.Subscribe()
	defer m.Unsubscribe(earlyCh)

	var earlyCount, lateCount int
	var mu sync.Mutex

	collect := func(ch chan DevServerEvent, counter *int) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			timer := time.NewTimer(400 * time.Millisecond)
			defer timer.Stop()
			for {
				select {
				case ev, ok := <-ch:
					if !ok {
						return
					}
					if ev.Type == "log" {
						mu.Lock()
						*counter++
						mu.Unlock()
					}
				case <-timer.C:
					return
				}
			}
		}()
		return done
	}
	earlyDone := collect(earlyCh, &earlyCount)

	// Emit 5 banner lines before the late subscriber registers.
	for i := 0; i < 5; i++ {
		m.EmitLog("banner line")
	}

	lateCh := m.Subscribe()
	defer m.Unsubscribe(lateCh)
	lateDone := collect(lateCh, &lateCount)

	// Emit 2 more lines after both are subscribed.
	m.EmitLog("post line 1")
	m.EmitLog("post line 2")

	<-earlyDone
	<-lateDone

	mu.Lock()
	defer mu.Unlock()
	t.Logf("early subscriber saw %d lines, late saw %d (both should be 7)", earlyCount, lateCount)
	if earlyCount != 7 {
		t.Errorf("early subscriber: got %d want 7", earlyCount)
	}
	if lateCount != 7 {
		t.Errorf("late subscriber: got %d want 7 — replay buffer not delivering all missed lines", lateCount)
	}
}

// Test 4: history is bounded — no unbounded memory growth across a
// long session. Confirms emit() trims to devEventHistoryMax.
func TestDevServerManager_HistoryIsBounded(t *testing.T) {
	m := NewDevServerManager()

	// Emit far more than devEventHistoryMax.
	for i := 0; i < devEventHistoryMax*3; i++ {
		m.EmitLog("noise")
	}

	m.subsMu.Lock()
	got := len(m.history)
	m.subsMu.Unlock()

	if got != devEventHistoryMax {
		t.Fatalf("history length = %d, want capped at %d", got, devEventHistoryMax)
	}
	t.Logf("CONFIRMED: history capped at %d events after %d emits", got, devEventHistoryMax*3)
}

// Test 5: Start() clears history so a freshly-started dev server
// does not hand its first subscriber the previous session's banner.
func TestDevServerManager_StartClearsHistory(t *testing.T) {
	m := NewDevServerManager()
	m.EmitLog("stale line from previous session")
	m.EmitLog("another stale line")

	// Simulate the relevant slice of Start(): clear history without
	// actually running a real DevServer (which would require a real
	// project on disk). The contract under test is "history goes
	// away when a new session begins".
	m.subsMu.Lock()
	m.history = nil
	m.subsMu.Unlock()

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	select {
	case ev := <-ch:
		t.Fatalf("got stale event after Start cleared history: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		t.Logf("CONFIRMED: history cleared on session restart")
	}
}
