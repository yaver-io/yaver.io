package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Idle auto-shutdown signal (server side: cloudLifecycle.idleSweep).
//
// On a Yaver-MANAGED cloud box, cloud-init writes /etc/yaver/machine.json
// with this box's machine token + Convex site URL (cloudMachines.ts). When
// the agent does real work (starts a task), it POSTs /machine/activity so
// the server's idle sweep keeps the box alive instead of pausing it. On a
// non-managed box (a dev laptop, a BYO box) the file is absent and every
// call is a cheap no-op.
//
// Throttled client-side so a busy box pings at most once per interval; the
// server also throttles the write. Privacy-safe: no payload, just a ping.

type machineIdentity struct {
	MachineID    string `json:"machineId"`
	MachineToken string `json:"machineToken"`
	ConvexSite   string `json:"convexSite"`
	Hostname     string `json:"hostname"`
}

const (
	machineIdentityPath        = "/etc/yaver/machine.json"
	machineActivityMinInterval = 2 * time.Minute
)

var (
	machineIDOnce   sync.Once
	machineIDCached *machineIdentity

	machineActMu   sync.Mutex
	machineActLast time.Time
)

// loadMachineIdentity reads /etc/yaver/machine.json once and caches it.
// Returns nil off a managed box (file absent / incomplete) — callers no-op.
func loadMachineIdentity() *machineIdentity {
	machineIDOnce.Do(func() {
		b, err := os.ReadFile(machineIdentityPath)
		if err != nil {
			return
		}
		var m machineIdentity
		if err := json.Unmarshal(b, &m); err != nil {
			return
		}
		if m.MachineID == "" || m.MachineToken == "" || m.ConvexSite == "" {
			return
		}
		machineIDCached = &m
	})
	return machineIDCached
}

// reportMachineActivity tells the server this managed box is in use so idle
// auto-shutdown doesn't pause it. Fire-and-forget + throttled; no-op off a
// managed box. Safe to call on every task start.
func reportMachineActivity() {
	id := loadMachineIdentity()
	if id == nil {
		return
	}

	machineActMu.Lock()
	if !machineActLast.IsZero() && time.Since(machineActLast) < machineActivityMinInterval {
		machineActMu.Unlock()
		return
	}
	machineActLast = time.Now()
	machineActMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		url := id.ConvexSite + "/machine/activity?machineId=" + id.MachineID
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
		if err != nil {
			return
		}
		req.Header.Set("X-Machine-Token", id.MachineToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()
}

// HasActiveTasks reports whether any task is running or queued — one "box is in
// use" signal for the managed-box idle monitor.
func (tm *TaskManager) HasActiveTasks() bool {
	if tm == nil {
		return false
	}
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	for _, t := range tm.tasks {
		if t.Status == TaskStatusRunning || t.Status == TaskStatusQueued {
			return true
		}
	}
	return false
}

// machineInUse reports whether ANY "the box is being used" signal is live, so
// the managed-box idle sweep never snapshot+deletes a box mid-work. Mirrors the
// operator's idle definition (2026-07-11): a box is IDLE only when there is
//   - no wrapped runner attached (codex / claude-code / opencode via /ws/runner,
//     which also covers a phone / web UI / CLI connected to one),
//   - no dev server serving (Metro / Vite / Hermes reload / Next), and
//   - no agent task running or queued.
func (s *HTTPServer) machineInUse() bool {
	if len(listRunnerPTYSessions()) > 0 {
		return true
	}
	if s.devServerMgr != nil && s.devServerMgr.Status() != nil {
		return true
	}
	if s.taskMgr != nil && s.taskMgr.HasActiveTasks() {
		return true
	}
	return false
}

// startMachineActivityMonitor keeps a MANAGED box's idle clock fresh while it is
// genuinely in use, so cloudLifecycle.idleSweep never reaps it out from under an
// active session. reportMachineActivity is throttled + a no-op off a managed box
// (identity file absent), so the loop is cheap everywhere.
func (s *HTTPServer) startMachineActivityMonitor() {
	if loadMachineIdentity() == nil {
		return // not a managed box — nothing to keep alive or park
	}
	SupervisedGo("machine-activity", 90*time.Second, false, func(ctx context.Context) error {
		if s.machineInUse() {
			reportMachineActivity()
			touchParkActivity() // pin the idle clock + cancel any armed park
			return nil
		}
		// Idle. Self-park is the COST-FREE replacement for the removed Convex
		// idle-sweep cron (crons.ts): the box decides locally via the same
		// idle+grace-confirm policy the watch/car use (scaleToZeroDecision), and
		// only calls the server at the instant it actually parks. A box that
		// isn't running pays nothing to decide it should stop.
		s.maybeSelfPark(ctx)
		return nil
	})
}

// maybeSelfPark decides, locally and for free, whether this managed box should
// scale itself to zero, and if so asks the server to snapshot+delete it. Opt-in
// (YAVER_CLOUD_IDLE_ENABLE) and grace-confirmed — a notify arms a grace window;
// any activity or a machine_keepalive during it cancels the park.
func (s *HTTPServer) maybeSelfPark(ctx context.Context) {
	if strings.TrimSpace(os.Getenv("YAVER_CLOUD_IDLE_ENABLE")) == "" {
		return // auto-off is opt-in — mirrors the server-side gate
	}
	id := loadMachineIdentity()
	if id == nil {
		return // managed boxes only
	}
	now := time.Now()
	parkMu.Lock()
	lastActive := parkLastActiveAt
	notifiedAt := parkNotifiedAt
	keepUntil := parkKeepAliveUntil
	parkMu.Unlock()

	in := ScaleToZeroInput{
		Tier:           HostingManaged, // identity file present ⇒ managed
		ActiveSessions: 0,              // machineInUse() already returned false
		IdleFor:        durSince(now, lastActive),
		IdleTimeout:    resolveIdleParkMinutes(),
		GraceNotified:  !notifiedAt.IsZero(),
		GraceFor:       durSince(now, notifiedAt),
		GraceWindow:    resolveParkGraceMinutes(),
		KeepAlive:      now.Before(keepUntil),
	}
	switch scaleToZeroDecision(in) {
	case ParkNotify:
		parkMu.Lock()
		if parkNotifiedAt.IsZero() {
			parkNotifiedAt = now
		}
		parkMu.Unlock()
		// The grace WINDOW is the real safety (activity cancels the park); a
		// delivered push is a refinement. Say `yaver machine keepalive` or use
		// the box to cancel.
		log.Printf("[machine-park] idle past threshold — parking in ~%s unless kept alive",
			resolveParkGraceMinutes())
	case ParkExecute:
		s.parkSelf(ctx, id)
		// Reset the idle clock so a delayed teardown doesn't re-request every
		// cycle; if the box survives, it re-evaluates after another idle window.
		touchParkActivity()
	}
}

// parkSelf asks the server to scale THIS managed box to zero (snapshot+delete).
// Machine-token authed — the box parking itself. The server gates on
// YAVER_CLOUD_IDLE_ENABLE + HCLOUD_TOKEN and snapshots before deleting.
func (s *HTTPServer) parkSelf(ctx context.Context, id *machineIdentity) {
	url := id.ConvexSite + "/machine/park-self?machineId=" + id.MachineID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return
	}
	req.Header.Set("X-Machine-Token", id.MachineToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[machine-park] park-self request failed: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[machine-park] requested self-park (idle past threshold); server status %d", resp.StatusCode)
}

// managedDeviceIDFromMachineIdentity returns the deterministic device id a
// MANAGED cloud box registers under — `cloud-<shortId>`, minted by the control
// plane (backend/convex/cloudMachines.ts) and mirrored into
// /etc/yaver/machine.json as `<shortId>.cloud.yaver.io`.
//
// This exists so a box that loses its config can recover its OWN identity
// instead of minting a random UUID. An orphaned box re-registers as a new
// device: the owner's primary pointer, aliases and ACLs still name the old id,
// so the machine is running and healthy yet reports "no device responded".
//
// Returns "" on a non-managed box (no machine.json), where a random UUID is
// the correct behavior.
func managedDeviceIDFromMachineIdentity() string {
	id := loadMachineIdentity()
	if id == nil {
		return ""
	}
	// Prefer the hostname (`<shortId>.cloud.yaver.io`) — that IS the shortId the
	// control plane derived the device id from.
	if host := strings.TrimSpace(id.Hostname); host != "" {
		if short := strings.SplitN(host, ".", 2)[0]; short != "" {
			return "cloud-" + short
		}
	}
	return ""
}
