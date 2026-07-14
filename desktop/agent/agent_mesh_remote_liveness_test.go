package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The Mac mini advertised a LAN IP on an unreachable subnet AND a Tailscale
// address with Tailscale stopped, with the RELAY last. The sequential walk burned
// its per-leg floor on each dead leg and the relay — the only one that worked —
// was cut off by the deadline. `yaver devices` printed "unreachable" and
// `yaver primary status` said "every transport candidate failed", for a box that
// answered /info over that same relay in 0.4s.

// blackhole is a listener that accepts and never answers — a dead LAN leg, which
// is the case that actually hurts (it burns the budget instead of failing fast).
func blackholeURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestLivenessProbePutsTheWorkingLegFirst(t *testing.T) {
	var liveHits int32
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt32(&liveHits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer live.Close()

	// Dead legs FIRST, live leg LAST — exactly the mini's candidate order.
	candidates := []RemoteAgentCandidate{
		{BaseURL: blackholeURL(t), DeviceID: "d"},
		{BaseURL: blackholeURL(t), DeviceID: "d"},
		{BaseURL: live.URL, DeviceID: "d"},
	}

	start := time.Now()
	_, status, _, err := doRemoteAgentRequest(context.Background(), candidates, "tok",
		http.MethodGet, "/agent/runners", nil, 7*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("request failed with a working relay leg present: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	// Serially this took ~2s/dead leg and ran out of budget. With the probe the
	// live leg is tried first, so it must land fast.
	if elapsed > 4*time.Second {
		t.Errorf("took %v — the dead legs are still starving the live one", elapsed)
	}
}

// A raced REAL request would reach the same agent twice and create two tasks.
// The probe must ensure the actual request is delivered EXACTLY once.
func TestRealRequestIsSentExactlyOnce(t *testing.T) {
	var realHits int32
	mk := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			atomic.AddInt32(&realHits, 1)
			w.Write([]byte(`{"ok":true}`))
		}))
	}
	a, b := mk(), mk()
	defer a.Close()
	defer b.Close()

	// BOTH legs are healthy — the dangerous case for a naive race.
	candidates := []RemoteAgentCandidate{
		{BaseURL: a.URL, DeviceID: "d"},
		{BaseURL: b.URL, DeviceID: "d"},
	}
	_, _, _, err := doRemoteAgentRequest(context.Background(), candidates, "tok",
		http.MethodPost, "/tasks", []byte(`{"prompt":"hi"}`), 7*time.Second)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := atomic.LoadInt32(&realHits); got != 1 {
		t.Fatalf("real request delivered %d times, want exactly 1 — racing the POST would double-create the task", got)
	}
}

// If nothing answers the probe, we must not silently succeed or hang; the walk
// still runs and reports honest per-leg errors.
func TestProbeFindingNothingStillReportsRealErrors(t *testing.T) {
	candidates := []RemoteAgentCandidate{
		{BaseURL: "http://127.0.0.1:1", DeviceID: "d"},
		{BaseURL: "http://127.0.0.1:2", DeviceID: "d"},
	}
	_, _, _, err := doRemoteAgentRequest(context.Background(), candidates, "tok",
		http.MethodGet, "/agent/runners", nil, 3*time.Second)
	if err == nil {
		t.Fatal("expected an error when every leg is dead")
	}
}
