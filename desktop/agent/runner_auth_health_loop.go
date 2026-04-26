package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"
)

// runnerAuthHealthLoop polls each installed runner CLI's "do you
// still have a valid token?" probe at a low cadence so the
// dashboard's [SIGN IN] / [auth ✓] badge reflects the reality on the
// box without the user kicking off a task first. The probe set is
// the same one collectRunnerAuthStatusRows() runs — a lightweight
// CLI check (no API call, no token spend), so it's safe to run
// every 6h indefinitely. Detected expirations propagate via
// syncRunnerAuthIncidents() into the existing dev-incident Convex
// stream that mobile + web both consume.
//
// Cadence: 6h. Refresh tokens for Claude Code / Codex usually live
// for months; 6h is plenty of slack to surface a rotation on the
// next dashboard load instead of mid-run.
//
// First tick fires after a short warmup delay (90s) so the agent
// boot sequence — Convex pairing, primary device handshake, runner
// install probes, etc. — completes before we add another CLI
// invocation to the queue.
func (s *HTTPServer) runnerAuthHealthLoop(ctx context.Context) {
	const (
		warmup    = 90 * time.Second
		interval  = 6 * time.Hour
		jitterCap = 30 * time.Minute
	)

	// Stagger the first tick so a fleet of agents that all rebooted
	// from the same release update don't hit Convex with synchronized
	// /agent-incident POSTs.
	time.Sleep(warmup + time.Duration(randInt63n(int64(jitterCap))))

	tick := func() {
		rows, err := collectRunnerAuthStatusRows()
		if err != nil {
			log.Printf("[runner-auth-health] probe failed: %v", err)
			return
		}
		workDir, _ := os.Getwd()
		deviceID := ""
		if s.taskMgr != nil {
			deviceID = strings.TrimSpace(s.taskMgr.DeviceID)
		}
		s.syncRunnerAuthIncidents(rows, workDir, deviceID)
	}

	tick()

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
