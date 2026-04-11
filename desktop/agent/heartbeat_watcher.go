package main

// heartbeat_watcher.go — peer heartbeat monitoring for the
// "my Mac mini upstairs stopped reporting" alert. Solves the
// scary-quiet scenario where a headless box goes offline and
// the dev has no idea until they try to SSH in.
//
// How it works:
//
//   1. Every mobile/desktop/headless yaver agent already pings
//      Convex every 2 min to update `lastHeartbeat` on its
//      device record.
//   2. The local agent remembers the set of devices it's
//      previously connected to / discovered over the relay and
//      fetches their lastHeartbeat from the peer list.
//   3. Every minute, the watcher compares each device's
//      lastHeartbeat against a staleness threshold. When it
//      crosses the threshold (default 5 minutes) we emit a
//      one-shot notification + surface it on /machine/peers.
//   4. When the device starts reporting again, the watcher
//      clears the alert state so a flapping connection doesn't
//      spam the notification channel.
//
// No Convex mutation. We only READ device records (peer
// discovery is the one thing Convex is allowed to do) — the
// alert state itself lives in ~/.yaver/peers/state.json.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PeerState is one tracked peer's most recent observation.
type PeerState struct {
	DeviceID      string `json:"deviceId"`
	Name          string `json:"name,omitempty"`
	LastSeen      string `json:"lastSeen"`       // from Convex device.lastHeartbeat
	ObservedAt    string `json:"observedAt"`     // when we last saw the Convex record
	State         string `json:"state"`          // "online" | "stale" | "offline"
	AlertedAt     string `json:"alertedAt,omitempty"`
	StaleSince    string `json:"staleSince,omitempty"`
}

// peerWatcher is the singleton that runs the loop.
type peerWatcher struct {
	mu     sync.Mutex
	states map[string]*PeerState
	path   string
}

var (
	peerWatcherOnce sync.Once
	peerWatcherInst *peerWatcher
)

// PeerStalenessThreshold is how long without a heartbeat we
// consider a peer "missing." The Convex heartbeat runs every
// 2 minutes, so 5 minutes = two missed intervals.
const PeerStalenessThreshold = 5 * time.Minute

// globalPeerWatcher returns the singleton, lazily constructed.
func globalPeerWatcher() *peerWatcher {
	peerWatcherOnce.Do(func() {
		base, err := ConfigDir()
		if err != nil {
			peerWatcherInst = &peerWatcher{states: map[string]*PeerState{}}
			return
		}
		dir := filepath.Join(base, "peers")
		_ = os.MkdirAll(dir, 0700)
		pw := &peerWatcher{
			states: map[string]*PeerState{},
			path:   filepath.Join(dir, "state.json"),
		}
		_ = pw.loadLocked()
		peerWatcherInst = pw
	})
	return peerWatcherInst
}

func (w *peerWatcher) loadLocked() error {
	data, err := os.ReadFile(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		States map[string]*PeerState `json:"states"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.States != nil {
		w.states = payload.States
	}
	return nil
}

func (w *peerWatcher) saveLocked() error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"states":    w.states,
		"updatedAt": time.Now().UnixMilli(),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// Snapshot returns a copy of the current peer state table for
// the HTTP / CLI surfaces.
func (w *peerWatcher) Snapshot() []*PeerState {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*PeerState, 0, len(w.states))
	for _, s := range w.states {
		cp := *s
		out = append(out, &cp)
	}
	return out
}

// observe ingests a Convex device record snapshot and
// updates the in-memory state. Edge-triggered alert logic
// fires exactly once per online→offline transition.
func (w *peerWatcher) observe(deviceID, name, lastSeen string) (alertMsg string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now().UTC()
	state := w.states[deviceID]
	if state == nil {
		state = &PeerState{DeviceID: deviceID, Name: name}
		w.states[deviceID] = state
	}
	state.Name = name
	state.LastSeen = lastSeen
	state.ObservedAt = now.Format(time.RFC3339)

	// Is the heartbeat stale?
	var hb time.Time
	if lastSeen != "" {
		if t, err := time.Parse(time.RFC3339, lastSeen); err == nil {
			hb = t
		}
	}
	stale := hb.IsZero() || now.Sub(hb) > PeerStalenessThreshold

	prevState := state.State
	if stale {
		if prevState != "offline" {
			state.State = "offline"
			if state.StaleSince == "" {
				state.StaleSince = now.Format(time.RFC3339)
			}
			// Alert once per transition. We re-arm the
			// alert on recovery so a flapping peer still
			// only pings us when it flips.
			if state.AlertedAt == "" || state.AlertedAt != state.StaleSince {
				state.AlertedAt = state.StaleSince
				alertMsg = fmt.Sprintf("peer %s (%s) has stopped responding (last seen %s)",
					nonEmpty(name, deviceID), deviceID, lastSeen)
			}
		}
	} else {
		if prevState == "offline" {
			alertMsg = fmt.Sprintf("peer %s recovered (heartbeat %s)", nonEmpty(name, deviceID), lastSeen)
		}
		state.State = "online"
		state.StaleSince = ""
		state.AlertedAt = ""
	}
	_ = w.saveLocked()
	return alertMsg
}

// nonEmpty picks the first non-empty string. Tiny helper so
// the alert copy reads naturally.
func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- loop ----------------------------------------------------------------

// StartHeartbeatWatcher starts the background loop. Called
// from runServe. Idempotent — duplicate calls are harmless.
func StartHeartbeatWatcher(ctx context.Context) {
	go func() {
		// Small delay so other boot goroutines don't race.
		time.Sleep(5 * time.Second)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pollPeerHeartbeats()
			}
		}
	}()
}

// pollPeerHeartbeats fetches the current peer list from Convex
// (the one thing it's allowed to do) and feeds each record
// through the watcher. Shared HTTP client, tight timeout.
func pollPeerHeartbeats() {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return
	}
	req, err := newBearerRequest("GET", cfg.ConvexSiteURL+"/devices", cfg.AuthToken, nil)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var payload struct {
		Devices []struct {
			DeviceID      string `json:"deviceId"`
			Name          string `json:"name"`
			LastHeartbeat string `json:"lastHeartbeat"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	hostname, _ := os.Hostname()
	watcher := globalPeerWatcher()
	for _, d := range payload.Devices {
		// Skip ourselves — it's trivially "up" and the alert
		// would be meaningless.
		if d.DeviceID == cfg.DeviceID {
			continue
		}
		msg := watcher.observe(d.DeviceID, d.Name, d.LastHeartbeat)
		if msg != "" {
			fmt.Fprintf(os.Stderr, "[heartbeat] %s\n", msg)
			if nm := globalMonitorNotifier; nm != nil {
				nm("peer-heartbeat", hostname, msg, 0)
			}
		}
	}
}

// --- HTTP ----------------------------------------------------------------

// handlePeerHealth serves the peer state table for the mobile
// Monitor > Machines sub-tab (future tab) and for `yaver
// machine peers`.
func (s *HTTPServer) handlePeerHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"peers": globalPeerWatcher().Snapshot(),
	})
}
