package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeRelay serves /beta/state: GET returns the current phase/idle; POST
// records the controller's update.
type fakeRelay struct {
	mu       sync.Mutex
	phase    string
	idleSec  int64
	lastPOST map[string]any
}

func (f *fakeRelay) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/beta/state", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			var in map[string]any
			_ = json.Unmarshal(body, &in)
			f.lastPOST = in
			if p, ok := in["phase"].(string); ok {
				f.phase = p
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"phase": f.phase, "boxReady": f.phase == "up", "idleSec": f.idleSec,
		})
	})
	return mux
}

func newCtrl(base string) *betaPoolController {
	return &betaPoolController{
		relayBase:  base,
		adminToken: "admintok",
		httpc:      http.DefaultClient,
		maxIdleSec: 1200,
		nowFn:      func() int64 { return 1000 },
	}
}

// waking → controller provisions (dry-run) and flips to up.
func TestBetaPool_WakingProvisions(t *testing.T) {
	f := &fakeRelay{phase: "waking"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := newCtrl(srv.URL)

	action, err := c.tick()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if action != "provisioned" {
		t.Fatalf("action=%q, want provisioned", action)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phase != "up" {
		t.Fatalf("phase=%q after provision, want up", f.phase)
	}
	if f.lastPOST["boxAddr"] != "dry-run-box" {
		t.Fatalf("boxAddr=%v, want dry-run-box (dry-run provision)", f.lastPOST["boxAddr"])
	}
}

// up + idle past threshold → reap and flip to down.
func TestBetaPool_IdleReaps(t *testing.T) {
	f := &fakeRelay{phase: "up", idleSec: 5000}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := newCtrl(srv.URL)

	action, err := c.tick()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if action != "reaped" {
		t.Fatalf("action=%q, want reaped", action)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phase != "down" {
		t.Fatalf("phase=%q after idle reap, want down", f.phase)
	}
}

// up + not idle → stay active (no reap, no spend).
func TestBetaPool_ActiveStaysUp(t *testing.T) {
	f := &fakeRelay{phase: "up", idleSec: 60}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := newCtrl(srv.URL)

	action, err := c.tick()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if action != "active" {
		t.Fatalf("action=%q, want active", action)
	}
	if f.phase != "up" {
		t.Fatalf("phase changed to %q — should stay up", f.phase)
	}
}

// down → controller does nothing (no provision until a beta user wakes it).
func TestBetaPool_DownIsIdle(t *testing.T) {
	f := &fakeRelay{phase: "down"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	c := newCtrl(srv.URL)
	action, err := c.tick()
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if action != "idle" {
		t.Fatalf("action=%q, want idle", action)
	}
}

// The unwire gate: default OFF (owner/Talos box is never a beta host); only an
// explicit YAVER_BETA_HOST=1 pool box may execute beta tenants.
func TestBetaHostEnabledGate(t *testing.T) {
	t.Setenv("YAVER_BETA_HOST", "")
	if betaHostEnabled() {
		t.Fatal("default betaHostEnabled() must be false (owner box not a beta host)")
	}
	for _, v := range []string{"1", "true", "yes", "on"} {
		t.Setenv("YAVER_BETA_HOST", v)
		if !betaHostEnabled() {
			t.Fatalf("YAVER_BETA_HOST=%q should enable", v)
		}
	}
}
