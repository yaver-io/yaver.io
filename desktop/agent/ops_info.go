package main

// ops_info.go — verb "info": specs + status snapshot of the target
// machine. Cheap, synchronous, no streaming. Used by agents to discover
// what a newly-connected machine can do before they plan further work.
//
// Output shape is a flat map so agents don't need to navigate nested
// structures. Adding a new field is additive — existing callers ignore
// unknown keys.

import (
	"encoding/json"
	"os"
	"runtime"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "info",
		Description: "Return a specs + status snapshot of the target machine: hostname, platform, cpu/ram, local IPs, uptime, agent version. Cheap and synchronous.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsInfoHandler,
		Streaming:  false,
		AllowGuest: true, // guests can read machine specs; they already see device_class in Convex
	})
}

func opsInfoHandler(c OpsContext, _ json.RawMessage) OpsResult {
	hostname, _ := os.Hostname()
	pid := os.Getpid()

	// CPU is sampled via the existing cross-platform helper. Errors
	// are non-fatal — the info verb is best-effort and degrades
	// gracefully when sysctl / /proc reads fail.
	cpuPct, _ := getCPUPercent()

	// Process start time for uptime — use agent-start sentinel if the
	// server exposes one; otherwise fall back to runtime startup time.
	// The startTime package var (set in main.go when the server boots)
	// would be nicer but isn't exported yet; defer to agent version +
	// pid for caller-side correlation.
	out := map[string]interface{}{
		"hostname":    hostname,
		"platform":    runtime.GOOS,
		"arch":        runtime.GOARCH,
		"numCPU":      runtime.NumCPU(),
		"goroutines":  runtime.NumGoroutine(),
		"cpuPercent":  cpuPct,
		"pid":         pid,
		"agentVersion": version, // from main.go's const
		"localIPs":    getLocalIPs(),
		"queriedAt":   time.Now().UTC().Format(time.RFC3339),
		// Echo back the surface the caller declared (tv/watch/car/…), so a
		// client can confirm the agent is surface-aware and adapt. Unknown when
		// the caller didn't send X-Yaver-Surface.
		"surface": string(surfaceFromHeaders(c.RequestHeaders)),
	}
	if c.Server != nil {
		if id := c.Server.deviceID; id != "" {
			out["deviceId"] = id
		}
		if uid := c.Server.ownerUserID; uid != "" {
			// First 8 hex chars only — enough for logs without
			// leaking a full identity across logs.
			if len(uid) > 8 {
				out["userIdPrefix"] = uid[:8]
			} else {
				out["userIdPrefix"] = uid
			}
		}
	}

	return OpsResult{OK: true, Initial: out}
}
