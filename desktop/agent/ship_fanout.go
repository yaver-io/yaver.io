package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fan-out: the freeze crosses machines, the deploy does not.
//
// These are different machine sets and the code must never conflate them:
//
//   - freezeTargets — every machine running autoruns (the mini, the Hetzner box).
//   - deployHost    — always this Mac, because mobile is in scope: TestFlight can
//     only upload from here (CI runner keychains lack the registered UDIDs).
//
// That asymmetry is the whole reason the gate carries a lease. A coordinator on
// one machine holding a gate on another can die, and the gate has to survive its
// operator's death by thawing rather than by waiting.

// shipCallVerb is the fan-out seam. Tests intercept it so the suite is hermetic
// — the same pattern deployRunCommand uses in deploy_all.go.
var shipCallVerb = shipMachineCall

// shipMachineCall invokes an ops verb on one machine, local or remote.
//
// "local" and "" short-circuit to the in-process gate rather than looping back
// through HTTP: ship runs inside the agent, and a self-proxy would need this
// process's own bearer to talk to itself.
func shipMachineCall(ctx context.Context, s *HTTPServer, machine, verb string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	m := strings.TrimSpace(machine)
	if m == "" || m == "local" {
		return shipLocalVerb(ctx, s, verb, body)
	}
	inner, err := json.Marshal(map[string]interface{}{
		"machine": "local", // forced, to stop hop loops
		"verb":    verb,
		"payload": json.RawMessage(body),
	})
	if err != nil {
		return err
	}
	status, respBody, err := proxyToDevice(ctx, "ops:"+verb, m, "POST", "/ops", inner)
	if err != nil {
		return fmt.Errorf("%s: %w", m, err)
	}
	if status >= 400 {
		return fmt.Errorf("%s: %s returned HTTP %d", m, verb, status)
	}
	var res OpsResult
	if err := json.Unmarshal(respBody, &res); err != nil {
		return fmt.Errorf("%s: malformed response to %s", m, verb)
	}
	if !res.OK {
		return fmt.Errorf("%s: %s failed: %s", m, verb, res.Error)
	}
	return nil
}

// lookupOpsVerb reads the verb registry under its lock.
func lookupOpsVerb(name string) (opsVerbSpec, bool) {
	opsRegistryMu.RLock()
	defer opsRegistryMu.RUnlock()
	spec, ok := opsRegistry[name]
	return spec, ok
}

func shipLocalVerb(ctx context.Context, s *HTTPServer, verb string, payload json.RawMessage) error {
	spec, ok := lookupOpsVerb(verb)
	if !ok {
		return fmt.Errorf("unknown verb %q", verb)
	}
	res := spec.Handler(OpsContext{Ctx: ctx, Server: s, Caller: "owner"}, payload)
	if !res.OK {
		return fmt.Errorf("%s: %s", verb, res.Error)
	}
	return nil
}

// shipFreezeTargets is the machine list, always including this one.
//
// The local machine is always frozen even when the user named only the mini:
// this Mac is where the deploy runs, and an autorun here would be committing
// into the very tree being deployed.
func shipFreezeTargets(opts shipOptions) []string {
	seen := map[string]bool{"local": true}
	out := []string{"local"}
	for _, m := range opts.FreezeMachines {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// shipFreezeAll fans the freeze out. Any unreachable machine aborts the ship.
//
// Freezing three of four machines is not a partial success, it is a false sense
// of one: the fourth machine's loops keep pushing straight into the deploy. The
// caller thaws whatever did freeze.
func shipFreezeAll(ctx context.Context, s *HTTPServer, opts shipOptions) ([]shipMachineState, error) {
	states := []shipMachineState{}
	var failed []string
	for _, m := range shipFreezeTargets(opts) {
		err := shipCallVerb(ctx, s, m, "autorun_pause_all", map[string]interface{}{
			"reason":  "ship: deploying",
			"leaseMs": shipLeaseTTL.Milliseconds(),
		})
		st := shipMachineState{Machine: m, OK: err == nil}
		if err != nil {
			st.Detail = err.Error()
			failed = append(failed, m)
		}
		states = append(states, st)
	}
	if len(failed) > 0 {
		return states, fmt.Errorf("could not freeze %s — their autoruns would push into the middle of the deploy", strings.Join(failed, ", "))
	}
	return states, nil
}

// shipThawAll lifts the freeze everywhere and then sends devam.
//
// Resume first, THEN prompt — the mirror of toparla's prompt-then-freeze. A
// parked loop is not reading its pane, so prompting a frozen fleet would queue a
// message that only gets read once the loop wakes anyway. Wake it, then tell it
// what it missed.
//
// This runs on every exit path including failure, and takes a context detached
// from the caller's: a cancelled ship must still thaw. A fleet frozen forever is
// worse than a failed deploy.
func shipThawAll(ctx context.Context, s *HTTPServer, opts shipOptions, frozen []shipMachineState) []shipMachineState {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	states := []shipMachineState{}
	for _, f := range frozen {
		if !f.OK {
			continue // never froze; nothing to lift
		}
		err := shipCallVerb(ctx, s, f.Machine, "autorun_resume_all", map[string]interface{}{})
		st := shipMachineState{Machine: f.Machine, OK: err == nil}
		if err != nil {
			st.Detail = err.Error()
		}
		states = append(states, st)
	}
	return states
}

// shipRenewLease heartbeats the dead-man lease while ship is alive. The returned
// func stops it.
//
// Without this, a deploy slower than shipLeaseTTL would thaw the fleet
// mid-deploy. With it, the lease only ever fires when this coordinator is
// genuinely gone.
func shipRenewLease(ctx context.Context, s *HTTPServer, opts shipOptions, frozen []shipMachineState) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(shipLeaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, f := range frozen {
					if !f.OK {
						continue
					}
					_ = shipCallVerb(ctx, s, f.Machine, "autorun_pause_all", map[string]interface{}{
						"reason":  "ship: deploying",
						"leaseMs": shipLeaseTTL.Milliseconds(),
						"renew":   true,
					})
				}
			}
		}
	}()
	return cancel
}

// shipRepairTaskPath finds the task file describing how to fix a red main.
//
// Deliberately explicit rather than generated: repair kicks a runner at a broken
// tree with push enabled, and the instruction for that should be a file a human
// wrote and can read, not a string this code improvised. No file, no repair.
func shipRepairTaskPath(workDir string) string {
	for _, rel := range []string{
		filepath.Join("tasks", "ship-repair.md"),
		filepath.Join("docs", "tasks", "ship-repair.md"),
	} {
		p := filepath.Join(workDir, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
