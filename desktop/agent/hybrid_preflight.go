package main

// hybrid_preflight.go — dependency check for `yaver hybrid`.
//
// Surfaces the two failure modes users hit first:
//   1. planner runner not on PATH
//   2. implementer runner not on PATH
//
// Called from the CLI (`yaver hybrid --check`) and from the HTTP
// handler before a run starts, so the caller gets a fast, actionable
// error instead of a cryptic stack trace three minutes in.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// HybridPreflight reports which dependencies are ready for a hybrid
// run using (planner, implementer).
type HybridPreflight struct {
	PlannerID         string `json:"plannerId"`
	PlannerOK         bool   `json:"plannerOk"`
	PlannerVersion    string `json:"plannerVersion,omitempty"`
	ImplementerID     string `json:"implementerId"`
	ImplementerOK     bool   `json:"implementerOk"`
	ImplementerVersion string `json:"implementerVersion,omitempty"`
	Hint              string `json:"hint,omitempty"`
}

// checkHybrid probes the planner and implementer binaries. Any failing
// probe sets a single "next command to run" hint so the user can
// copy/paste a fix rather than decode separate error strings.
func checkHybrid(planner, implementer string) HybridPreflight {
	planner = normalizeRunnerID(strings.TrimSpace(planner))
	implementer = normalizeRunnerID(strings.TrimSpace(implementer))
	if planner == "" {
		planner = "claude"
	}
	if implementer == "" {
		implementer = "opencode"
	}

	pf := HybridPreflight{PlannerID: planner, ImplementerID: implementer}

	probe := func(runnerID string) (ok bool, version string) {
		cfg, exists := builtinRunners[runnerID]
		if !exists {
			return false, ""
		}
		if _, err := exec.LookPath(cfg.Command); err != nil {
			return false, ""
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, cfg.Command, "--version").Output()
		v := strings.TrimSpace(string(out))
		if nl := strings.IndexByte(v, '\n'); nl > 0 {
			v = v[:nl]
		}
		return true, v
	}

	pf.PlannerOK, pf.PlannerVersion = probe(planner)
	pf.ImplementerOK, pf.ImplementerVersion = probe(implementer)

	switch {
	case !IsSupportedRunner(planner):
		pf.Hint = fmt.Sprintf("Planner %q is not supported. Use claude, codex, or opencode.", planner)
	case !IsSupportedRunner(implementer):
		pf.Hint = fmt.Sprintf("Implementer %q is not supported. Use claude, codex, or opencode.", implementer)
	case !pf.PlannerOK:
		pf.Hint = fmt.Sprintf("Install the planner CLI: yaver install %s", planner)
	case !pf.ImplementerOK:
		pf.Hint = fmt.Sprintf("Install the implementer CLI: yaver install %s", implementer)
	}
	return pf
}

func (pf HybridPreflight) AllOK() bool {
	return pf.PlannerOK && pf.ImplementerOK
}
