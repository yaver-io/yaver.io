package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
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
