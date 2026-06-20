package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// beta_signal.go — the relay-side SIGNALLING for the scale-to-zero shared beta
// runtime. The free relay is the always-on, public entry; the pool CONTROLLER
// (owner-side, separate process) polls /beta/state and provisions/reaps the
// ephemeral Hetzner box. The relay holds only control signalling (phase +
// timestamps) — never task data — same class as presence/bandwidth.
//
// COST SAFETY (the whole point): a wake can ONLY be triggered by a verified
// beta user. The relay does not trust the caller; it forwards the bearer token
// to Convex /gateway/authorize (the authority), which resolves a scoped beta
// token to a userId. No valid beta token → no wake → no provision → no spend.
// An attacker hitting /beta/wake with no/garbage token is rejected before any
// pool state changes. Per-user cooldown + a single shared phase debounce a
// burst (even from a real beta user) down to ONE box.

type betaPoolState struct {
	mu             sync.Mutex
	phase          string           // "down" | "waking" | "up"
	lastWakeAt     int64            // unix sec — last authorized wake
	lastActivityAt int64            // unix sec — last beta traffic / controller heartbeat
	boxAddr        string           // set by the controller when up (opaque to clients)
	wakeCount      int64            // total authorized wakes (observability)
	perUser        map[string]int64 // userId -> last wake unix sec (cooldown)
}

func newBetaPoolState() *betaPoolState {
	return &betaPoolState{phase: "down", perUser: map[string]int64{}}
}

// betaWakeCooldownSec bounds how often a single authorized beta user can
// re-trigger a wake — defence-in-depth against a compromised beta token being
// used to thrash provisioning.
const betaWakeCooldownSec = 8

// betaAuthorize asks Convex /gateway/authorize to resolve a bearer token to a
// userId. Returns (userId, true) ONLY for an authorized token (scoped beta or
// cloud). This is the cost gate: the relay never decides who's a beta user.
func (s *RelayServer) betaAuthorize(bearer string) (string, bool) {
	convexURL := strings.TrimRight(s.convexURL, "/")
	if convexURL == "" || bearer == "" {
		return "", false
	}
	req, err := http.NewRequest(http.MethodPost, convexURL+"/gateway/authorize", bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var out struct {
		UserID string `json:"userId"`
		Allow  bool   `json:"allow"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", false
	}
	if !out.Allow || strings.TrimSpace(out.UserID) == "" {
		return "", false
	}
	return out.UserID, true
}

// handleBetaWake — POST /beta/wake. A beta client signals "I want to code".
// Authenticated as a beta user via Convex; only then does the shared pool flip
// to "waking" so the controller provisions a box. Async by design: the client
// polls /beta/state until phase=="up" (the box cold-start is ~30-60s, hidden
// behind the "Setting up your project" UX).
func (s *RelayServer) handleBetaWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	userID, ok := s.betaAuthorize(bearer)
	if !ok {
		// Cost gate: unauthenticated callers can NEVER move pool state.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not an authorized beta user"})
		return
	}
	now := time.Now().Unix()
	p := s.betaPool
	p.mu.Lock()
	defer p.mu.Unlock()
	if last, seen := p.perUser[userID]; seen && now-last < betaWakeCooldownSec {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"phase":         p.phase,
			"retryAfterSec": betaWakeCooldownSec - (now - last),
		})
		return
	}
	p.perUser[userID] = now
	p.lastWakeAt = now
	p.lastActivityAt = now
	p.wakeCount++
	if p.phase == "down" {
		p.phase = "waking" // controller (polling /beta/state) provisions exactly ONE box
	}
	boxAddr := ""
	if p.phase == "up" {
		boxAddr = p.boxAddr
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"phase":        p.phase,
		"boxReady":     p.phase == "up",
		"boxAddr":      boxAddr,
		"pollAfterSec": 5,
	})
}

// handleBetaState — GET returns the shared pool phase (clients poll this until
// up; the controller polls it to decide provision/reap). POST is controller-
// only (admin-token gated) and sets phase/boxAddr/activity.
func (s *RelayServer) handleBetaState(w http.ResponseWriter, r *http.Request) {
	p := s.betaPool
	now := time.Now().Unix()

	if r.Method == http.MethodPost {
		if !s.authorizeAdmin(w, r) {
			return // authorizeAdmin already wrote the 401
		}
		var in struct {
			Phase    string `json:"phase"`
			BoxAddr  string `json:"boxAddr"`
			Activity bool   `json:"activity"`
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		_ = json.Unmarshal(body, &in)
		p.mu.Lock()
		switch in.Phase {
		case "down":
			p.phase = "down"
			p.boxAddr = ""
		case "waking", "up":
			p.phase = in.Phase
		}
		if in.BoxAddr != "" {
			p.boxAddr = in.BoxAddr
		}
		if in.Activity {
			p.lastActivityAt = now
		}
		p.mu.Unlock()
	}

	p.mu.Lock()
	phase, lastWake, lastAct, wakes := p.phase, p.lastWakeAt, p.lastActivityAt, p.wakeCount
	p.mu.Unlock()
	idle := int64(0)
	if lastAct > 0 {
		idle = now - lastAct
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"phase":          phase,
		"boxReady":       phase == "up",
		"lastWakeAt":     lastWake,
		"lastActivityAt": lastAct,
		"idleSec":        idle,
		"wakeCount":      wakes,
	})
}
