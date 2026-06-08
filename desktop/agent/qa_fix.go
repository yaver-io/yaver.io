package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/yaver-io/agent/studio"
)

// qa_fix.go — fix mode (P4, docs/yaver-ai-app-test-agent.md §16). The closed
// loop: catch → dispatch a coding-agent DRAFT (never commit/push) → reload the
// app so the patched code runs → re-drive the flow → confirm the bug is gone.
// The two external actions are seams (bugFixer, appReloader) so the loop is
// fully unit-tested with fakes; the production wiring dispatches the existing
// coding-agent lane and rebuilds via /dev/build-native.

// bugFixer dispatches a code change for one caught bug. It MUST be draft-only —
// leave a working-tree diff / draft PR, never commit or push (CLAUDE.md rule).
type bugFixer interface {
	Fix(ctx context.Context, bug studio.Bug, flow studio.Scenario) (fixAttempt, error)
}

// appReloader rebuilds + reloads the app-under-test so the verify pass exercises
// the patched code (production: POST /dev/build-native then reload the bundle).
type appReloader interface {
	Reload(ctx context.Context) error
}

type fixAttempt struct {
	Patched bool   // a draft change was produced
	Summary string // human summary / diff stat (shown in the report; never auto-applied to git)
}

// fixFlow attempts to fix the bugs caught in a flow, then re-drives the flow once
// to verify. Bugs whose (oracle,title) no longer appears after the patched
// reload are marked "fixed"; the rest "attempted-unresolved". New regressions
// the fix introduced are appended (marked "caught"). Returns the reconciled bug
// list + the verify pass's verdicts.
func (rc *runContext) fixFlow(ctx context.Context, flow studio.Scenario, caught []studio.Bug) ([]studio.Bug, []studio.AssertVerdict) {
	cfg := rc.cfg
	patchedAny := false
	limit := cfg.MaxFixes
	for i := range caught {
		if caught[i].Outcome == "" {
			caught[i].Outcome = "caught"
		}
		if i >= limit {
			continue
		}
		cfg.logf("  🔧 dispatching fix: %s", caught[i].Title)
		att, err := cfg.Fixer.Fix(ctx, caught[i], flow)
		if err != nil {
			cfg.logf("    fix dispatch failed: %v", err)
			continue
		}
		if att.Patched {
			patchedAny = true
			caught[i].FixSummary = att.Summary
		}
	}

	if !patchedAny {
		return caught, nil // nothing changed; leave everything "caught"
	}

	if cfg.Reloader != nil {
		cfg.logf("  ↻ rebuilding + reloading patched app")
		if err := cfg.Reloader.Reload(ctx); err != nil {
			cfg.logf("    reload failed (cannot verify fixes): %v", err)
			return caught, nil
		}
	}

	cfg.logf("  ✓ verify pass for %s", flow.Name)
	after, verdicts, _ := rc.driveFlowOnce(ctx, flow)
	stillPresent := map[string]bool{}
	for _, b := range after {
		stillPresent[b.Key()] = true
	}

	caughtKeys := map[string]bool{}
	for i := range caught {
		caughtKeys[caught[i].Key()] = true
		if caught[i].FixSummary == "" {
			continue // wasn't attempted (beyond MaxFixes) — stays "caught"
		}
		if stillPresent[caught[i].Key()] {
			caught[i].Outcome = "attempted-unresolved"
		} else {
			caught[i].Outcome = "fixed"
			rc.report.Fixed++
			cfg.logf("    ✓ fixed: %s", caught[i].Title)
		}
	}

	// Surface regressions the fix introduced (new bugs not in the original set).
	for _, b := range after {
		if !caughtKeys[b.Key()] {
			b.Outcome = "caught"
			b.Detail = "[regression after fix] " + b.Detail
			caught = append(caught, b)
		}
	}
	return caught, verdicts
}

// --- production wiring (used by qa_jobs.go; the loop above is what's unit-tested) ---

// mobileReloader rebuilds the Hermes bundle and reloads it into the app via the
// local agent's dev server — the same /dev/build-native path the mobile app uses.
type mobileReloader struct {
	workDir  string
	platform string
	reload   func(ctx context.Context, workDir, platform string) error
}

func (m *mobileReloader) Reload(ctx context.Context) error {
	if m.reload == nil {
		return fmt.Errorf("no reload wiring")
	}
	return m.reload(ctx, m.workDir, m.platform)
}

// codingAgentFixer dispatches a repair to the coding-agent lane on the app repo.
// dispatch is injected (the agent-task entry); it returns a short change summary.
// It is draft-only by contract — the prompt forbids commit/push.
type codingAgentFixer struct {
	workDir  string
	dispatch func(ctx context.Context, workDir, prompt string) (summary string, changed bool, err error)
}

func (f *codingAgentFixer) Fix(ctx context.Context, bug studio.Bug, flow studio.Scenario) (fixAttempt, error) {
	if f.dispatch == nil {
		return fixAttempt{}, fmt.Errorf("no coding-agent wiring")
	}
	summary, changed, err := f.dispatch(ctx, f.workDir, buildFixPrompt(bug, flow))
	if err != nil {
		return fixAttempt{}, err
	}
	return fixAttempt{Patched: changed, Summary: summary}, nil
}

func buildFixPrompt(bug studio.Bug, flow studio.Scenario) string {
	var sb strings.Builder
	sb.WriteString("An automated QA run of this app caught a bug. Fix the ROOT CAUSE in the source.\n\n")
	fmt.Fprintf(&sb, "Flow goal: %s\n", flow.Goal)
	fmt.Fprintf(&sb, "Bug: [%s] %s (severity %s)\n", bug.Oracle, bug.Title, bug.Severity)
	fmt.Fprintf(&sb, "Detail: %s\n\n", bug.Detail)
	sb.WriteString("Make the minimal change that fixes it. Do NOT commit or push — leave the change in the working tree for review. Reply with a one-line summary of what you changed.")
	return sb.String()
}
