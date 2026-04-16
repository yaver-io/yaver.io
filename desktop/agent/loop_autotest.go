package main

// loop_autotest.go — Auto Test mode for `yaver loop run`.
//
// Mode: `auto-test`. One iteration does:
//
//  1. Ensure worktree is fresh (same as every other loop mode)
//  2. Walk yaver-tests/ for *.test.yaml, run each spec via the
//     embedded testkit runner
//  3. If every spec passes → commit is a no-op; iteration done
//  4. If any spec fails → build a synthetic heuristic report that
//     frames the failure as a finding the AI runner already knows
//     how to fix, spawn phaseThink, re-run the failing spec,
//     loop until green / stuck / max kicks / budget hit
//  5. On final green, phaseCommit writes the fix + Auto Test commit
//     prefix, same as the other modes.
//
// The seam to the AI is the same phaseThink the other modes use,
// which means the fallback chain, green-gate, and worktree isolation
// all apply without special-casing. Auto Test is *just another mode*;
// the heuristic report carries the payload.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// LoopTest is the auto-test-mode config block on LoopSpec.
type LoopTest struct {
	// Root is the directory under workDir where specs live.
	// Default: "yaver-tests".
	Root string `yaml:"root,omitempty" json:"root,omitempty"`
	// RetryFlake is the number of times the runner re-runs a failing
	// spec *before* asking the AI to fix anything. Matches the CLI's
	// --flake-retries flag. Default 0.
	RetryFlake int `yaml:"retry_flake,omitempty" json:"retry_flake,omitempty"`
	// Headful runs browsers visibly — useful for interactive debug
	// runs on the dev's own machine. Defaults to false.
	Headful bool `yaml:"headful,omitempty" json:"headful,omitempty"`
}

// runAutoTestLoop implements mode=auto-test. Signature mirrors
// runDevelopLoop so saveState works the same way.
func runAutoTestLoop(ctx context.Context, l *LoopState, saveState func(*LoopState)) *IterationResult {
	result := &IterationResult{
		Status:    "failed",
		StartedAt: time.Now().UTC(),
	}

	workDir, err := ensureWorktree(ctx, l)
	if err != nil {
		result.Status = "failed"
		result.Err = err.Error()
		result.Summary = "could not prepare loop worktree"
		result.FinishedAt = time.Now().UTC()
		return result
	}

	if l.Spec.Schedule.Timeout != "" {
		if d, perr := time.ParseDuration(l.Spec.Schedule.Timeout); perr == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}
	killFile, _ := loopKillFilePath(l.Spec.Name)
	watchCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	go stopFileWatchdog(watchCtx, killFile, cancelAll)

	preSHA := gitHeadSHA(workDir)

	root := l.Spec.Test.Root
	if root == "" {
		root = "yaver-tests"
	}
	specRoot := filepath.Join(workDir, root)
	if st, serr := os.Stat(specRoot); serr != nil || !st.IsDir() {
		result.Status = "done"
		result.Summary = fmt.Sprintf("auto-test skipped: spec root %s not found", specRoot)
		result.FinishedAt = time.Now().UTC()
		return result
	}

	// Per-kick safety cap: default 5 attempts per iteration so a
	// pathological failure can't drain the whole session window.
	maxKicks := l.Spec.Think.MaxKicksPerRun
	if maxKicks <= 0 {
		maxKicks = 5
	}

	var kicks []*KickRecap
	var lastFailure *autoTestFailure

	for kickIdx := 1; kickIdx <= maxKicks; kickIdx++ {
		if watchCtx.Err() != nil {
			result.Status = "stopped"
			result.Summary = "stop signal received during auto-test loop"
			gitResetHardQuiet(workDir, preSHA)
			result.FinishedAt = time.Now().UTC()
			return result
		}

		fails, runErr := runTestSuite(watchCtx, specRoot, l.Spec.Test)
		if runErr != nil {
			result.Status = "failed"
			result.Err = runErr.Error()
			result.Summary = "test suite discovery / run error"
			result.FinishedAt = time.Now().UTC()
			return result
		}

		if len(fails) == 0 {
			// Suite is green. If the AI touched files in a previous
			// kick, commit them; otherwise this is just a passing
			// sweep and we don't commit anything.
			changed := gitChangedFiles(workDir)
			if len(changed) > 0 {
				aiResp := &AIResponse{
					Status:  "done",
					Summary: fmt.Sprintf("auto-test: fixed %d failing spec(s) in %d kick(s)", len(kicks), kickIdx-1),
				}
				sha, cerr := phaseCommit(watchCtx, l, workDir, aiResp)
				if cerr != nil {
					gitResetHardQuiet(workDir, preSHA)
					result.Status = "failed"
					result.Err = cerr.Error()
					result.Summary = "auto-test commit failed"
					result.FinishedAt = time.Now().UTC()
					return result
				}
				result.PatchCommit = sha
				result.FilesChanged = changed
			}
			result.Status = "done"
			result.Summary = fmt.Sprintf("auto-test green (%d kicks)", kickIdx-1)
			result.Kicks = kicks
			result.AIResponse = &AIResponse{Status: "done", Summary: result.Summary}
			result.FinishedAt = time.Now().UTC()
			return result
		}

		// At least one spec is failing. Package it as a heuristic
		// report with one Finding per failing spec, hand to phaseThink.
		firstFail := fails[0]
		lastFailure = firstFail
		report := buildAutoTestReport(l, firstFail, fails)
		reportPath, _ := persistReport(l, report)
		result.ReportPath = reportPath

		aiResp, aiErr := phaseThink(watchCtx, l, workDir, report, reportPath, "")
		if watchCtx.Err() != nil {
			result.Status = "stopped"
			result.Summary = "stop signal received during AI think phase"
			gitResetHardQuiet(workDir, preSHA)
			result.FinishedAt = time.Now().UTC()
			return result
		}
		if aiErr != nil {
			result.Status = "failed"
			result.Err = aiErr.Error()
			result.Summary = "AI runner failed"
			gitResetHardQuiet(workDir, preSHA)
			result.FinishedAt = time.Now().UTC()
			return result
		}
		if aiResp == nil {
			result.Status = "stuck"
			result.Summary = "AI runner produced no parseable status"
			gitResetHardQuiet(workDir, preSHA)
			result.FinishedAt = time.Now().UTC()
			return result
		}

		kicks = append(kicks, &KickRecap{
			Index:   kickIdx,
			Status:  aiResp.Status,
			Summary: aiResp.Summary,
		})

		if aiResp.Status == "needs_human" || aiResp.Status == "stuck" {
			gitResetHardQuiet(workDir, preSHA)
			result.Status = aiResp.Status
			result.Summary = aiResp.Summary
			result.Kicks = kicks
			result.FinishedAt = time.Now().UTC()
			return result
		}

		// Persist progress so the mobile Auto Dev tab and
		// `yaver loop status` reflect each attempt in real time.
		l.LastSummary = fmt.Sprintf("auto-test kick %d: %s", kickIdx, aiResp.Summary)
		if saveState != nil {
			saveState(l)
		}
	}

	// Fell out of the loop — couldn't green the suite inside the
	// kick budget. Roll back and mark stuck.
	gitResetHardQuiet(workDir, preSHA)
	result.Status = "stuck"
	result.Kicks = kicks
	if lastFailure != nil {
		result.Summary = fmt.Sprintf("auto-test stuck after %d kicks; %s still failing: %s",
			maxKicks, lastFailure.SpecName, lastFailure.FirstError)
	} else {
		result.Summary = fmt.Sprintf("auto-test stuck after %d kicks", maxKicks)
	}
	result.FinishedAt = time.Now().UTC()
	return result
}

// autoTestFailure captures the first failing step of a spec, trimmed
// to what the AI runner actually needs in its prompt.
type autoTestFailure struct {
	SpecName   string
	SpecPath   string
	Phase      string
	StepIndex  int
	StepLabel  string
	FirstError string
	Screenshot string
	DurationMS int64
}

// runTestSuite walks specRoot, runs every spec, and returns the list
// of failures (one per failing spec, in lexical order). An empty
// slice means the whole suite is green.
func runTestSuite(ctx context.Context, specRoot string, cfg LoopTest) ([]*autoTestFailure, error) {
	specs, err := testkit.DiscoverSpecs(specRoot)
	if err != nil {
		return nil, fmt.Errorf("discover specs in %s: %w", specRoot, err)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no *.test.yaml files found under %s", specRoot)
	}
	opts := testkit.RunOptions{
		FlakeRetries: cfg.RetryFlake,
		Headful:      cfg.Headful,
	}
	var fails []*autoTestFailure
	for _, s := range specs {
		if ctx.Err() != nil {
			return fails, ctx.Err()
		}
		res := testkit.Run(ctx, s, opts)
		if res.Passed {
			continue
		}
		fail := &autoTestFailure{
			SpecName:   s.Name,
			SpecPath:   s.Path,
			DurationMS: res.Duration().Milliseconds(),
		}
		if res.Err != nil {
			fail.Phase = "spec"
			fail.FirstError = res.Err.Error()
		}
		for _, step := range res.Steps {
			if step.Err != nil {
				fail.Phase = step.Phase
				fail.StepIndex = step.Index
				fail.StepLabel = step.Description
				fail.FirstError = step.Err.Error()
				fail.Screenshot = step.ScreenshotPath
				break
			}
		}
		if fail.FirstError == "" {
			fail.FirstError = "spec failed with no captured step error"
		}
		fails = append(fails, fail)
	}
	return fails, nil
}

// buildAutoTestReport wraps a test failure in the same HeuristicReport
// shape other modes use, so phaseThink / buildLoopPrompt can run
// unchanged. The primary finding is the first failing step; every
// additional failing spec is added as a minor finding so the AI has
// full context without re-running discovery.
func buildAutoTestReport(l *LoopState, first *autoTestFailure, all []*autoTestFailure) *HeuristicReport {
	findings := []Finding{
		{
			Kind:     "test_failure",
			Severity: "critical",
			Detail: fmt.Sprintf("spec %q failed at %s step %d (%s): %s",
				first.SpecName, first.Phase, first.StepIndex,
				truncateAutoTest(first.StepLabel, 80),
				truncateAutoTest(first.FirstError, 400)),
			Hint: fmt.Sprintf("edit %s or the code under test so this step passes; "+
				"keep the diff small and do not touch unrelated specs",
				first.SpecPath),
		},
	}
	for i, f := range all {
		if i == 0 {
			continue
		}
		findings = append(findings, Finding{
			Kind:     "test_failure",
			Severity: "major",
			Detail: fmt.Sprintf("spec %q also failing: %s",
				f.SpecName, truncateAutoTest(f.FirstError, 200)),
			Hint: "secondary failure — focus on the primary first",
		})
	}
	report := &HeuristicReport{
		StartedAt:       time.Now().UTC(),
		Target:          l.Spec.Target,
		URL:             l.Spec.URL,
		Persona:         l.Spec.Persona,
		RadicalnessUI:   l.Spec.Knobs.RadicalnessUI,
		RadicalnessFeat: l.Spec.Knobs.RadicalnessFeatures,
		Tone:            l.Spec.Knobs.Tone,
		Findings:        findings,
		Summary: fmt.Sprintf("auto-test: %d failing spec(s), primary %q at step %d",
			len(all), first.SpecName, first.StepIndex),
	}
	if first.Screenshot != "" {
		report.ScreenshotPath = first.Screenshot
	}
	return report
}

// truncateAutoTest limits a string to n runes-ish with an ellipsis.
// Named to avoid colliding with the project-wide truncate helper in
// tasks.go.
func truncateAutoTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

// NOTE: testkit.FixHandler seam is intentionally not installed from
// here yet. Auto Test already covers the "LLM-driven fix" story for
// loop-mode test runs. Wiring the handler for interactive `yaver
// test run` users is a separate commit — it needs a smaller, bounded
// prompt shape (not a full loop kick) and a different response parser.
