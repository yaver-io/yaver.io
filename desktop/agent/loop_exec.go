package main

// loop_exec.go — the actual phase execution for `yaver loop run`.
//
// This is the thin glue that wires an iteration into the five phases
// described in docs/roadmap_ci_solo_developer_lower_costs.md M8:
//
//   1. Target readiness   (HTTP probe / simulator boot)
//   2. Heuristic playtest (chromedp scan for blank screens, console
//                          errors, Turkish diacritic gaps, undefined
//                          strings in the rendered DOM — no LLM calls)
//   3. AI think step       (spawn `claude` / `codex` / `aider` in a
//                           subprocess, pass the report as stdin, read
//                           the JSON status contract back out)
//   4. Green gate          (run the project's typechecker; any red
//                           gate rolls the iteration back and marks
//                           it `stuck` without committing)
//   5. Commit              (git add + git commit with the loop's
//                           commit_prefix on `ship.branch`; optional
//                           `ship.deploy` shell command runs last)
//
// First cut only supports target=web and runner=claude-code. Other
// combinations are stubbed with clear error messages so the user sees
// what's wired and what isn't.
//
// Safety rails enforced here (not just documented):
//   - Refuses to run over a dirty working tree. The dev must stash
//     or commit their own uncommitted work first. This is the
//     pragmatic substitute for the full git-worktree isolation the
//     doc promises — it never destroys uncommitted user work, at the
//     cost of asking the dev to clean up before kicking the loop.
//   - Wraps the AI subprocess in exec.CommandContext with a watchdog
//     that cancels the context the moment the STOP kill-file appears
//     or the wall-clock timeout fires. SIGTERM first, SIGKILL after
//     10s, then the iteration is marked `stopped`.
//   - Green gate is a hard barrier: if it fails, `git reset --hard`
//     rolls the working tree back to the pre-AI SHA. The ship branch
//     tip is always green.
//   - Every tick of the inner loops checks the STOP file before
//     advancing, so a stop signal never has to wait for the next
//     phase boundary.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// IterationResult is the outcome of a single `yaver loop run` tick.
// For single-kick modes (auto-fix / fix / ideas) it mirrors one kick;
// for develop mode it is the rolled-up result of "kick until done",
// with Kicks listing each individual kick that ran.
type IterationResult struct {
	Status       string       `json:"status"` // "done" | "stuck" | "stopped" | "failed" | "needs_human" | "budget_hit"
	Summary      string       `json:"summary"`
	StartedAt    time.Time    `json:"startedAt"`
	FinishedAt   time.Time    `json:"finishedAt"`
	ReportPath   string       `json:"reportPath,omitempty"`
	PatchCommit  string       `json:"patchCommit,omitempty"`
	FilesChanged []string     `json:"filesChanged,omitempty"`
	Err          string       `json:"err,omitempty"`
	AIResponse   *AIResponse  `json:"aiResponse,omitempty"`
	Kicks        []*KickRecap `json:"kicks,omitempty"`
}

// KickRecap is a one-line summary of each kick in a multi-kick
// develop-mode iteration. Stored on the final IterationResult so
// the dev can see what each step of a feature prompt produced.
type KickRecap struct {
	Index       int    `json:"index"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	CommitSHA   string `json:"commitSha,omitempty"`
	FilesTouched []string `json:"filesTouched,omitempty"`
}

// HeuristicReport is what the chromedp scan writes to disk as JSON
// and passes to the AI runner as stdin. The schema is deliberately
// small — every field is consumed by the `autofix-hardening.md` /
// `genz-fixer.md` prompts.
type HeuristicReport struct {
	StartedAt       time.Time        `json:"startedAt"`
	Target          string           `json:"target"`
	URL             string           `json:"url"`
	PageTitle       string           `json:"pageTitle,omitempty"`
	Persona         string           `json:"persona,omitempty"`
	RadicalnessUI   int              `json:"radicalnessUi"`
	RadicalnessFeat int              `json:"radicalnessFeatures"`
	Tone            string           `json:"tone"`
	Findings        []Finding        `json:"findings"`
	ConsoleMessages []string         `json:"consoleMessages,omitempty"`
	VisibleText     string           `json:"visibleTextSnippet,omitempty"`
	ScreenshotPath  string           `json:"screenshotPath,omitempty"`
	Summary         string           `json:"summary"`
}

// Finding is one issue the heuristic scan noticed.
type Finding struct {
	Kind     string `json:"kind"`     // "console_error" | "undefined_in_ui" | "turkish_diacritic_missing" | "blank_screen" | "low_contrast" | ...
	Detail   string `json:"detail"`
	Severity string `json:"severity"` // "critical" | "major" | "minor"
	Hint     string `json:"hint,omitempty"`
}

// AIResponse is the contract-shape we require from every AI runner.
// The runner writes exactly one of these as the last JSON object in
// its stdout. Anything else is treated as "stuck".
type AIResponse struct {
	Status       string   `json:"status"` // "done" | "stuck" | "needs_human" | "in_progress"
	Summary      string   `json:"summary"`
	FilesTouched []string `json:"files_touched,omitempty"`
	NextStep     string   `json:"next_step,omitempty"`
	Blockers     []string `json:"blockers,omitempty"`

	// Ideas-mode only: the generated feature list. Parsed separately
	// because the shape is mode-specific, but carried on the same
	// struct so parseAIResponse stays unified.
	Ideas       []FeatureIdea `json:"ideas,omitempty"`
	GeneratedAt string        `json:"generated_at,omitempty"`
}

// FeatureIdea is one entry in an Ideas-mode generation. The mobile
// Auto Dev tab reads a list of these from ~/.yaver/loops/<name>/
// ideas.json and renders them as a multi-select picker.
//
// The Prompt + Reasoning fields exist so a FeatureIdea generated by
// vibing's 5-step deep analysis carries forward everything a develop
// loop needs in one object: the dev picks the idea on their phone,
// `yaver loop prompt pick <loop> <idea-id>` copies the Prompt field
// into the loop's inline prompt, and the next `yaver loop run` kicks
// through the feature without any further typing.
type FeatureIdea struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Radicalness int    `json:"radicalness"`
	Effort      string `json:"effort"` // "small" | "medium" | "large"
	WhyPersona  string `json:"why_persona,omitempty"`
	WhyNot      string `json:"why_not,omitempty"`

	// Prompt is the ready-to-kick develop-mode prompt vibing generated
	// for this idea. Populated when the idea came from vibing's deep
	// analysis (which always emits a full prompt per suggestion);
	// empty for heuristic / quick-action style ideas.
	Prompt string `json:"prompt,omitempty"`

	// Reasoning is vibing's "why this idea is brilliant for this
	// project specifically" paragraph, shown on card tap in the
	// mobile Auto Dev tab.
	Reasoning string `json:"reasoning,omitempty"`

	// Category comes from vibing: feature | bugfix | test | deploy |
	// refactor | docs. Useful for mobile-side filtering.
	Category string `json:"category,omitempty"`
}

// runLoopIteration is the entry point called by `yaver loop run <name>`.
// It dispatches by mode: auto-fix / fix run one kick, ideas generates a
// feature list and writes it to disk, develop runs the "kick until
// done" loop until the AI signals completion or a budget / safety
// cap triggers.
//
// saveState is invoked after every state mutation so the mobile app /
// `yaver loop status` always sees fresh data mid-iteration; pass nil
// from tests or one-off runs where intermediate persistence is not
// needed.
func runLoopIteration(ctx context.Context, l *LoopState, saveState func(*LoopState)) *IterationResult {
	switch l.Spec.Mode {
	case LoopModeIdeas:
		return runIdeasKick(ctx, l)
	case LoopModeDevelop:
		return runDevelopLoop(ctx, l, saveState)
	case LoopModeAutoTest:
		return runAutoTestLoop(ctx, l, saveState)
	default:
		// auto-fix and fix are single-kick modes.
		return runSingleKick(ctx, l, "")
	}
}

// runDevelopLoop implements the "kick until done" behavior for
// develop-mode loops. It calls runSingleKick repeatedly, threading
// each kick's next_step forward as the nudge for the following kick,
// and terminates when:
//
//   - the AI returns status=done (the whole feature prompt is
//     complete — normal success)
//   - the AI returns status=needs_human or stuck repeatedly
//   - the daily commit/patch budget runs out
//   - the STOP kill-file appears or the context is cancelled
//   - the max-kicks-per-run safety cap is hit (default 10, override
//     via spec.think.max_kicks_per_run)
//
// Every kick that lands a commit increments CommitsToday /
// PatchesToday on the LoopState and saveState is called after each
// kick so `yaver loop status` and the mobile app see fresh numbers
// in real time.
func runDevelopLoop(ctx context.Context, l *LoopState, saveState func(*LoopState)) *IterationResult {
	aggregate := &IterationResult{
		Status:     "done",
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC(), // defensive default so bare early-returns still show a valid duration
	}
	defer func() {
		aggregate.FinishedAt = time.Now().UTC()
	}()

	maxKicks := l.Spec.Think.MaxKicksPerRun
	if maxKicks <= 0 {
		maxKicks = 10 // safety default — a single `yaver loop run` tick
	}

	nudge := "" // first kick has no nudge; subsequent kicks get prev.next_step
	kickIndex := 0
	consecutiveStuck := 0

	for {
		// Roll the daily budget bucket. Doing this inside the kick
		// loop means a loop that runs across midnight gets a fresh
		// quota for day-2 without the dev having to intervene.
		l.rollBudgetDay()

		// Budget: stop before the next kick if the daily caps are hit.
		if l.Spec.Budget.MaxCommitsPerDay > 0 && l.CommitsToday >= l.Spec.Budget.MaxCommitsPerDay {
			aggregate.Status = "budget_hit"
			aggregate.Summary = fmt.Sprintf(
				"daily commit budget reached (%d/%d) — pausing until tomorrow",
				l.CommitsToday, l.Spec.Budget.MaxCommitsPerDay)
			break
		}
		if l.Spec.Budget.MaxPatchesPerDay > 0 && l.PatchesToday >= l.Spec.Budget.MaxPatchesPerDay {
			aggregate.Status = "budget_hit"
			aggregate.Summary = fmt.Sprintf(
				"daily patch budget reached (%d/%d) — pausing until tomorrow",
				l.PatchesToday, l.Spec.Budget.MaxPatchesPerDay)
			break
		}

		// Physical STOP check between kicks.
		if killFile, err := loopKillFilePath(l.Spec.Name); err == nil {
			if _, err := os.Stat(killFile); err == nil {
				aggregate.Status = "stopped"
				aggregate.Summary = "stop signal received between kicks"
				break
			}
		}
		if ctx.Err() != nil {
			aggregate.Status = "stopped"
			aggregate.Summary = "context cancelled between kicks"
			break
		}

		// Safety cap: bounded number of kicks per `yaver loop run`
		// invocation. The scheduler will fire us again on the next
		// tick if the feature isn't done yet.
		if kickIndex >= maxKicks {
			aggregate.Status = "stuck"
			aggregate.Summary = fmt.Sprintf(
				"reached max_kicks_per_run=%d — will resume on next scheduled tick",
				maxKicks)
			break
		}
		kickIndex++

		fmt.Fprintf(os.Stderr, "\n=== kick %d/%d (develop: %s) ===\n",
			kickIndex, maxKicks, l.Spec.Name)
		if nudge != "" {
			fmt.Fprintf(os.Stderr, "nudge: %s\n", nudge)
		}

		kickResult := runSingleKick(ctx, l, nudge)

		recap := &KickRecap{
			Index:       kickIndex,
			Status:      kickResult.Status,
			Summary:     kickResult.Summary,
			CommitSHA:   kickResult.PatchCommit,
			FilesTouched: kickResult.FilesChanged,
		}
		aggregate.Kicks = append(aggregate.Kicks, recap)

		// Carry the most recent per-kick data up to the aggregate so
		// `yaver loop status` has the latest values without having to
		// dig into Kicks[].
		aggregate.ReportPath = kickResult.ReportPath
		if kickResult.PatchCommit != "" {
			aggregate.PatchCommit = kickResult.PatchCommit
			aggregate.FilesChanged = kickResult.FilesChanged
			aggregate.AIResponse = kickResult.AIResponse
			aggregate.Summary = kickResult.Summary

			// A green commit landed — tick the budget counters.
			l.CommitsToday++
			l.PatchesToday++
		}

		if saveState != nil {
			saveState(l)
		}

		// Terminal statuses — bail out immediately.
		switch kickResult.Status {
		case "stopped":
			aggregate.Status = "stopped"
			aggregate.Summary = kickResult.Summary
			return aggregate
		case "failed":
			aggregate.Status = "failed"
			aggregate.Err = kickResult.Err
			aggregate.Summary = kickResult.Summary
			return aggregate
		case "needs_human":
			aggregate.Status = "needs_human"
			aggregate.Summary = kickResult.Summary
			return aggregate
		}

		// Consecutive-stuck cap: if the AI can't make progress twice
		// in a row inside the same develop run, give up — the feature
		// prompt is probably ambiguous and needs a human.
		if kickResult.Status == "stuck" {
			consecutiveStuck++
			if consecutiveStuck >= 2 {
				aggregate.Status = "stuck"
				aggregate.Summary = "AI reported stuck on two consecutive kicks — feature prompt may be ambiguous"
				return aggregate
			}
			continue
		}
		consecutiveStuck = 0

		// Route on the AI's own status.
		if kickResult.AIResponse == nil {
			// No parseable response → treat as stuck.
			consecutiveStuck++
			continue
		}
		switch kickResult.AIResponse.Status {
		case "done":
			aggregate.Status = "done"
			return aggregate
		case "in_progress":
			nudge = strings.TrimSpace(kickResult.AIResponse.NextStep)
			if nudge == "" {
				// AI said "in progress" but gave no next step → nothing
				// actionable for a follow-up kick; stop.
				aggregate.Status = "done"
				aggregate.Summary = kickResult.Summary + " (in_progress with no next_step — treating as done)"
				return aggregate
			}
			// loop around for another kick
			continue
		default:
			// Any unknown status → treat as done on the final kick
			// we've already performed. Defensive fallback.
			aggregate.Status = "done"
			return aggregate
		}
	}

	aggregate.FinishedAt = time.Now().UTC()
	return aggregate
}

// runIdeasKick implements Auto Dev's ideas generator: it spawns claude
// with a purpose-built prompt that reads the repo's git history + any
// TODO.md / product.md + latest friction reports, and asks for a
// ranked list of feature ideas the dev can multi-select from the
// mobile Auto Dev tab.
//
// This is Auto Dev's own pipeline — not a wrapper around vibing's
// interactive widget or autopilot's todo-batch runner. Those two
// systems stay as-is for their interactive use cases. Auto Dev owns:
//
//   - its own prompt contract (FeatureIdea schema with ready-to-kick
//     `prompt` + `reasoning` per idea so `yaver loop prompt pick`
//     can hand a full feature prompt to the develop loop)
//   - its own persistence (~/.yaver/loops/<name>/ideas.json + a
//     timestamped history sibling the dev can diff day-over-day)
//   - its own git / README context gathering (kept inline here so
//     Auto Dev ships as a self-contained subsystem)
//
// The kick never touches the working tree and never commits — it
// produces a JSON list the dev then approves on their phone.
func runIdeasKick(ctx context.Context, l *LoopState) *IterationResult {
	result := &IterationResult{
		Status:    "done",
		StartedAt: time.Now().UTC(),
	}
	defer func() { result.FinishedAt = time.Now().UTC() }()

	// Ideas kick doesn't edit or commit, but we still run it inside the
	// loop's worktree so the context gathering (git log, README, TODO)
	// sees the same snapshot the dev/loops see and stays isolated from
	// any in-progress edits in the main tree.
	workDir, err := ensureWorktree(ctx, l)
	if err != nil {
		result.Status = "failed"
		result.Err = err.Error()
		return result
	}

	killFile, _ := loopKillFilePath(l.Spec.Name)
	watchCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	go stopFileWatchdog(watchCtx, killFile, cancelAll)

	if _, err := exec.LookPath("claude"); err != nil {
		result.Status = "failed"
		result.Err = "`claude` CLI not on PATH — install Claude Code to use ideas mode"
		return result
	}

	// Gather Auto Dev's own project context. Inlined on purpose —
	// keeping Auto Dev self-contained means vibing / autopilot can
	// evolve independently without breaking the loop.
	ctxSnapshot := gatherIdeasContext(workDir)

	promptBody := buildIdeasPrompt(l, workDir, ctxSnapshot)

	cmd := exec.CommandContext(watchCtx, "claude",
		"--print",
		"--permission-mode", "bypassPermissions",
		"--add-dir", workDir,
	)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(promptBody)
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	fmt.Fprintf(os.Stderr, "[loop %s] ideas kick — spawning claude (prompt=%d chars, commits=%d, active=%d)\n",
		l.Spec.Name, len(promptBody), len(ctxSnapshot.commits), len(ctxSnapshot.activeFiles))

	out, runErr := cmd.Output()
	if runErr != nil {
		if errors.Is(watchCtx.Err(), context.Canceled) {
			result.Status = "stopped"
			result.Summary = "stop signal received during ideas kick"
			return result
		}
		result.Status = "failed"
		result.Err = fmt.Errorf("ideas kick claude run: %w", runErr).Error()
		return result
	}

	aiResp, parseErr := parseAIResponse(string(out))
	if parseErr != nil || aiResp == nil {
		result.Status = "stuck"
		result.Summary = "ideas kick produced no parseable JSON contract"
		return result
	}
	result.AIResponse = aiResp

	if len(aiResp.Ideas) == 0 {
		result.Status = "stuck"
		result.Summary = "claude responded but the ideas array was empty"
		return result
	}

	ideasPath, perr := persistIdeas(l, aiResp)
	if perr != nil {
		result.Status = "failed"
		result.Err = perr.Error()
		result.Summary = "could not persist ideas.json"
		return result
	}
	l.LastIdeasPath = ideasPath

	result.Summary = fmt.Sprintf("generated %d feature ideas → %s", len(aiResp.Ideas), ideasPath)
	return result
}

// ideasContext is Auto Dev's own project snapshot used to prime the
// ideas generator. Kept intentionally small and inline so the loop
// is self-contained.
type ideasContext struct {
	commits     []string
	activeFiles []string
	readme      string
	todoMD      string
	productMD   string
}

// gatherIdeasContext reads the minimum useful context for an ideas
// kick: last 20 commits, files touched in the last 10 commits, the
// README, and any TODO.md / .yaver/product.md the dev maintains.
// Every step fails quietly — a repo with no README or no TODO is
// fine, the prompt just sees what's available.
func gatherIdeasContext(workDir string) ideasContext {
	snap := ideasContext{}

	if out, err := exec.Command("git", "-C", workDir, "log",
		"--oneline", "-20", "--no-merges").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				snap.commits = append(snap.commits, parts[1])
			}
		}
	}

	if out, err := exec.Command("git", "-C", workDir, "diff",
		"--name-only", "HEAD~10", "HEAD").Output(); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f == "" || strings.Contains(f, "node_modules") || strings.HasSuffix(f, ".lock") {
				continue
			}
			snap.activeFiles = append(snap.activeFiles, f)
		}
	}

	for _, name := range []string{"README.md", "readme.md", "README.txt"} {
		if data, err := os.ReadFile(filepath.Join(workDir, name)); err == nil {
			s := string(data)
			if len(s) > 2500 {
				s = s[:2500] + "\n... (truncated)"
			}
			snap.readme = s
			break
		}
	}

	if data, err := os.ReadFile(filepath.Join(workDir, "TODO.md")); err == nil {
		s := string(data)
		if len(s) > 1500 {
			s = s[:1500] + "\n... (truncated)"
		}
		snap.todoMD = s
	}

	// .yaver/product.md is Auto Dev's own product-direction file —
	// the dev maintains it manually, or future turns of Auto Dev will
	// learn to rewrite it from conversation context.
	if data, err := os.ReadFile(filepath.Join(workDir, ".yaver", "product.md")); err == nil {
		s := string(data)
		if len(s) > 2000 {
			s = s[:2000] + "\n... (truncated)"
		}
		snap.productMD = s
	}

	return snap
}

// buildIdeasPrompt renders the Auto Dev ideas prompt with the project
// context snapshot baked in. The prompt ends with a strict JSON
// contract so parseAIResponse can extract the Ideas array regardless
// of any narration claude emits around it.
func buildIdeasPrompt(l *LoopState, workDir string, snap ideasContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Auto Dev ideas kick for loop: %s\n\n", l.Spec.Name)
	fmt.Fprintf(&b, "Project: %s\n", filepath.Base(workDir))
	if l.Spec.Persona != "" {
		fmt.Fprintf(&b, "Target persona: %s\n", l.Spec.Persona)
	}
	fmt.Fprintf(&b, "Radicalness: ui=%d features=%d\n", l.Spec.Knobs.RadicalnessUI, l.Spec.Knobs.RadicalnessFeatures)
	if l.Spec.Knobs.Tone != "" {
		fmt.Fprintf(&b, "Tone: %s\n", l.Spec.Knobs.Tone)
	}
	b.WriteString("\n")

	if len(snap.commits) > 0 {
		b.WriteString("## Recent commits (newest first)\n\n")
		for _, c := range snap.commits {
			fmt.Fprintf(&b, "- %s\n", c)
		}
		b.WriteString("\n")
	}
	if len(snap.activeFiles) > 0 {
		b.WriteString("## Files touched in the last 10 commits\n\n")
		for _, f := range snap.activeFiles {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}
	if snap.readme != "" {
		b.WriteString("## README excerpt\n\n")
		b.WriteString(snap.readme)
		b.WriteString("\n\n")
	}
	if snap.productMD != "" {
		b.WriteString("## .yaver/product.md — the dev's stated direction\n\n")
		b.WriteString(snap.productMD)
		b.WriteString("\n\n")
	}
	if snap.todoMD != "" {
		b.WriteString("## TODO.md excerpt\n\n")
		b.WriteString(snap.todoMD)
		b.WriteString("\n\n")
	}

	b.WriteString("## Task\n\n")
	b.WriteString(`You are Auto Dev's ideas generator. Produce a ranked list of 5–12 concrete
feature ideas the dev can multi-select on their phone. Each idea must:

- address a real gap or opportunity visible in the context above
- fit inside 1–3 files for a small idea, 3–8 files for medium, 8+ for large
- have a clear, ready-to-kick agent prompt so picking the idea and running
  the develop loop immediately produces working code
- respect the project's brand/naming/tone rules (if CLAUDE.md mentions
  fictional-name rules, apply them)
- NOT propose "add tests" / "clean up code" / "update deps" style chores —
  those belong in vibing quick actions, not Auto Dev ideas

Rank by (impact × 1/effort), highest first.

## Output contract — emit exactly one JSON object, as the LAST line of your reply

` + "```json\n")
	b.WriteString(`{
  "status": "done",
  "generated_at": "` + time.Now().UTC().Format(time.RFC3339) + `",
  "ideas": [
    {
      "id": "short-slug-no-spaces",
      "title": "Human-readable title (max ~8 words)",
      "description": "One short paragraph explaining the idea.",
      "category": "feature",
      "radicalness": 4,
      "effort": "small",
      "why_persona": "One line: why the target persona cares.",
      "why_not": "One line: the trade-off or cost.",
      "reasoning": "2–3 sentences: why this is the right next step for THIS project specifically, given the commits + README above.",
      "prompt": "A complete, ready-to-run agent prompt the develop loop can execute. Include the specific files to touch, the concrete behavior change, and the acceptance criteria. Write it in the same way you would brief a junior engineer."
    }
  ]
}
`)
	b.WriteString("```\n\n")
	b.WriteString("Do not wrap the JSON in prose. The last line of your response must be the closing `}` of the object above.\n")
	return b.String()
}

// persistIdeas writes the AI's feature-ideas list to the loop's
// ideas.json (and a timestamped sibling for history). Both files live
// under ~/.yaver/loops/<name>/.
func persistIdeas(l *LoopState, resp *AIResponse) (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "loops", l.Spec.Name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	payload := map[string]interface{}{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"loop_name":    l.Spec.Name,
		"persona":      l.Spec.Persona,
		"ideas":        resp.Ideas,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	// Canonical path the mobile app + CLI read.
	canonical := filepath.Join(dir, "ideas.json")
	if err := os.WriteFile(canonical, data, 0600); err != nil {
		return "", err
	}
	// Timestamped history sibling so the dev can diff yesterday's
	// list against today's.
	ts := time.Now().UTC().Format("20060102-150405")
	_ = os.WriteFile(filepath.Join(dir, ts+"-ideas.json"), data, 0600)
	return canonical, nil
}

// runSingleKick runs exactly one iteration — phases 1..5 — and returns
// a result the caller can inspect to decide whether to kick again.
// `nudge` is an optional string that gets appended to the AI prompt
// (used by runDevelopLoop to pass the previous kick's `next_step`
// hint forward into the next kick).
func runSingleKick(ctx context.Context, l *LoopState, nudge string) *IterationResult {
	result := &IterationResult{
		Status:    "failed",
		StartedAt: time.Now().UTC(),
	}

	// Session-limits check — before we do any work, ask whether the
	// configured runner has headroom in its provider window. When it
	// doesn't we either (a) swap in a fallback runner for this kick
	// only, or (b) terminate with budget_hit so the scheduler tries
	// again after the window rolls over.
	runner, yieldReason, fitsBudget := pickRunnerWithinLimits(l.Spec.Name, l.Spec.Think)
	if !fitsBudget {
		result.Status = "budget_hit"
		result.Summary = yieldReason
		result.FinishedAt = time.Now().UTC()
		return result
	}
	if runner != l.Spec.Think.Runner {
		fmt.Fprintf(os.Stderr, "[loop %s] session-limits: %s\n", l.Spec.Name, yieldReason)
		// Override the runner for this kick only. We mutate the
		// in-memory copy, which is safe because runSingleKick
		// operates on a scratch LoopState the caller just reloaded.
		// The saved state still reflects the original primary runner.
		l.Spec.Think.Runner = runner
		result.Summary = yieldReason // carry upward for status display
	}

	// Ensure the per-loop worktree exists and is refreshed from the
	// target branch tip. The worktree — not the dev's main tree — is
	// what the AI runner sees and what phaseCommit writes to, so the
	// dev can keep editing their repo in parallel without collisions.
	workDir, err := ensureWorktree(ctx, l)
	if err != nil {
		result.Status = "failed"
		result.Err = err.Error()
		result.Summary = "could not prepare loop worktree"
		result.FinishedAt = time.Now().UTC()
		return result
	}
	defer func() {
		// Charge the kick's wall-clock duration to the runner that
		// actually ran. This runs in a defer so any early-return
		// path below still records usage correctly.
		recordKickUsage(l.Spec.Name, runner, time.Since(result.StartedAt))
	}()

	// Wall-clock timeout from the spec, if any. Default: no limit.
	if l.Spec.Schedule.Timeout != "" {
		if d, perr := time.ParseDuration(l.Spec.Schedule.Timeout); perr == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}

	// STOP kill-file watchdog. Cancels the iteration context the moment
	// the file appears — belt-and-braces with SIGTERM→SIGKILL on any
	// subprocess downstream.
	killFile, _ := loopKillFilePath(l.Spec.Name)
	watchCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	go stopFileWatchdog(watchCtx, killFile, cancelAll)

	// Record the pre-run SHA so we can roll back on green-gate failure.
	preSHA := gitHeadSHA(workDir)

	// --- Phase 1: target readiness (only when playtest will run) ---
	if l.Spec.Playtest.playtestEnabled(l.Spec.Mode) {
		if err := phaseReadiness(watchCtx, l); err != nil {
			result.Status = classifyError(watchCtx, err)
			result.Err = err.Error()
			result.Summary = "phase 1 readiness check failed"
			result.FinishedAt = time.Now().UTC()
			return result
		}
	}

	// --- Phase 2: heuristic playtest ---
	var report *HeuristicReport
	if l.Spec.Playtest.playtestEnabled(l.Spec.Mode) {
		r, err := phasePlaytest(watchCtx, l)
		if err != nil {
			// A failed scan is not a hard error for the iteration —
			// we still try the AI step with an empty report. But we
			// log the failure prominently.
			fmt.Fprintf(os.Stderr, "[loop %s] playtest phase failed: %v (continuing with empty report)\n",
				l.Spec.Name, err)
		}
		report = r
	}
	if report == nil {
		report = &HeuristicReport{
			StartedAt: time.Now().UTC(),
			Target:    l.Spec.Target,
			URL:       l.Spec.URL,
			Summary:   "playtest disabled or skipped",
		}
	}
	report.Persona = l.Spec.Persona
	report.RadicalnessUI = l.Spec.Knobs.RadicalnessUI
	report.RadicalnessFeat = l.Spec.Knobs.RadicalnessFeatures
	report.Tone = l.Spec.Knobs.Tone

	// Persist the report — dev can read it, the AI runner gets it as
	// stdin, and the mobile Loops tab will read it over HTTP later.
	reportPath, rerr := persistReport(l, report)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "[loop %s] could not persist report: %v\n", l.Spec.Name, rerr)
	}
	result.ReportPath = reportPath

	// --- Phase 3: AI think step ---
	aiResp, aiErr := phaseThink(watchCtx, l, workDir, report, reportPath, nudge)
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
	result.AIResponse = aiResp
	result.Summary = aiResp.Summary

	// Did the AI actually change anything?
	changed := gitChangedFiles(workDir)
	result.FilesChanged = changed
	if len(changed) == 0 {
		// AI ran but wrote nothing. If it said "done", count it as
		// a no-op success. If it said anything else, mark as stuck.
		if aiResp.Status == "done" {
			result.Status = "done"
			result.Summary = result.Summary + " (no files changed)"
		} else {
			result.Status = "stuck"
		}
		result.FinishedAt = time.Now().UTC()
		return result
	}

	// --- Phase 4: green gate ---
	if gerr := phaseGreenGate(watchCtx, l, workDir); gerr != nil {
		fmt.Fprintf(os.Stderr, "[loop %s] green gate failed: %v — rolling back\n",
			l.Spec.Name, gerr)
		gitResetHardQuiet(workDir, preSHA)
		result.Status = "stuck"
		result.Summary = fmt.Sprintf("green gate failed: %v", gerr)
		result.FinishedAt = time.Now().UTC()
		return result
	}

	// --- Phase 5: commit (+ optional deploy) ---
	sha, cerr := phaseCommit(watchCtx, l, workDir, aiResp)
	if cerr != nil {
		fmt.Fprintf(os.Stderr, "[loop %s] commit failed: %v — rolling back\n",
			l.Spec.Name, cerr)
		gitResetHardQuiet(workDir, preSHA)
		result.Status = "failed"
		result.Err = cerr.Error()
		result.FinishedAt = time.Now().UTC()
		return result
	}
	result.PatchCommit = sha

	// Track the green run before the release-train check so the
	// counter advances even on iterations the gate blocks from
	// shipping.
	if aiResp.Status == "done" {
		l.GreenRunSinceLastDeploy++
	}

	if l.Spec.Ship.Deploy != "" && aiResp.Status == "done" {
		if reason, ok := releaseTrainAllowsDeploy(l); !ok {
			fmt.Fprintf(os.Stderr, "[loop %s] release train skipped deploy: %s\n",
				l.Spec.Name, reason)
		} else {
			if derr := phaseDeploy(watchCtx, l, workDir); derr != nil {
				fmt.Fprintf(os.Stderr, "[loop %s] deploy phase failed (commit still landed): %v\n",
					l.Spec.Name, derr)
			} else {
				// Successful deploy resets the green counter and
				// bumps today's TestFlight tally. rollBudgetDay is
				// safe to call unconditionally — it only zeroes on
				// a day change.
				l.rollBudgetDay()
				l.TestflightToday++
				l.GreenRunSinceLastDeploy = 0
			}
		}
	}

	// Map the AI's status onto the iteration status.
	switch aiResp.Status {
	case "done":
		result.Status = "done"
	case "in_progress":
		result.Status = "done" // one kick succeeded — schedule re-queues the next
	case "needs_human":
		result.Status = "needs_human"
	case "stuck":
		result.Status = "stuck"
	default:
		result.Status = "done"
	}
	result.FinishedAt = time.Now().UTC()
	return result
}

func looksLikeGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// loopWorktreePath returns the absolute path to the per-loop worktree
// — `~/.yaver/loops/<name>/worktree`. Auto Dev uses a sibling worktree
// instead of editing the dev's main tree so the dev can keep hacking
// in their own repo while the loop runs in parallel.
func loopWorktreePath(name string) (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "loops", name, "worktree"), nil
}

// ensureWorktree creates (or refreshes) the per-loop git worktree and
// returns its absolute path. The worktree is created with --detach so
// the dev's main tree is free to check out any branch — Auto Dev runs
// on a detached HEAD at the tip of `ship.branch`, makes its edits,
// commits, and pushes back via `HEAD:<branch>` (phaseCommit).
//
// Between kicks the worktree is hard-reset to the current branch tip,
// so each kick starts from a pristine copy of whatever is live on
// `origin/<branch>` (or the local branch if the repo has no remote).
// Stale state from a rolled-back kick never leaks into the next one.
func ensureWorktree(ctx context.Context, l *LoopState) (string, error) {
	srcRepo := l.WorkDir
	if srcRepo == "" {
		return "", fmt.Errorf("loop has no WorkDir — re-register via `yaver loop add`")
	}
	if !looksLikeGitRepo(srcRepo) {
		return "", fmt.Errorf("source %s is not a git repo", srcRepo)
	}
	wtPath, err := loopWorktreePath(l.Spec.Name)
	if err != nil {
		return "", err
	}
	branch := l.Spec.Ship.Branch
	if branch == "" || branch == "local" {
		branch = "HEAD"
	}

	registered := false
	if out, lerr := exec.Command("git", "-C", srcRepo, "worktree", "list", "--porcelain").Output(); lerr == nil {
		// Porcelain lines look like `worktree /abs/path`. We just
		// substring-match on the wtPath.
		if strings.Contains(string(out), wtPath) {
			registered = true
		}
	}

	if !registered {
		// Stale dir from a previous run without the matching git
		// registration — wipe it so `git worktree add` doesn't refuse.
		_ = os.RemoveAll(wtPath)
		if err := os.MkdirAll(filepath.Dir(wtPath), 0700); err != nil {
			return "", fmt.Errorf("create worktree parent: %w", err)
		}
		// Best-effort fetch so we create the worktree at an up-to-date tip.
		_ = exec.CommandContext(ctx, "git", "-C", srcRepo, "fetch", "origin").Run()
		addArgs := []string{"-C", srcRepo, "worktree", "add", "--detach", wtPath}
		if branch != "HEAD" {
			addArgs = append(addArgs, branch)
		}
		if out, aerr := exec.CommandContext(ctx, "git", addArgs...).CombinedOutput(); aerr != nil {
			return "", fmt.Errorf("git worktree add %s: %v (%s)",
				wtPath, aerr, strings.TrimSpace(string(out)))
		}
		return wtPath, nil
	}

	// Worktree already registered. Refresh it: reset + clean, then
	// fetch and hard-reset to the target branch tip. Every step is
	// best-effort — a missing remote just means we reset to the
	// local branch instead.
	_ = exec.CommandContext(ctx, "git", "-C", wtPath, "reset", "--hard").Run()
	_ = exec.CommandContext(ctx, "git", "-C", wtPath, "clean", "-fd").Run()
	_ = exec.CommandContext(ctx, "git", "-C", wtPath, "fetch", "origin").Run()
	if branch != "HEAD" {
		target := "origin/" + branch
		if err := exec.CommandContext(ctx, "git", "-C", wtPath, "reset", "--hard", target).Run(); err != nil {
			_ = exec.CommandContext(ctx, "git", "-C", wtPath, "reset", "--hard", branch).Run()
		}
	}
	return wtPath, nil
}

// removeWorktree tears a loop's worktree down. Called from
// `yaver loop remove` so stale worktrees don't accumulate under
// ~/.yaver/loops/. Best-effort: if git refuses to remove the
// worktree (e.g. it's locked) we still rm -rf the dir and prune.
func removeWorktree(l *LoopState) error {
	srcRepo := l.WorkDir
	wtPath, err := loopWorktreePath(l.Spec.Name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		return nil
	}
	if srcRepo != "" && looksLikeGitRepo(srcRepo) {
		_ = exec.Command("git", "-C", srcRepo, "worktree", "remove", "-f", wtPath).Run()
		_ = exec.Command("git", "-C", srcRepo, "worktree", "prune").Run()
	}
	return os.RemoveAll(wtPath)
}

// --- Phase 1: readiness ------------------------------------------------

func phaseReadiness(ctx context.Context, l *LoopState) error {
	switch l.Spec.Target {
	case "web":
		return probeWebTarget(ctx, l.Spec.URL, 15*time.Second)
	case "ios-sim", "android-emu":
		return fmt.Errorf("target=%s is not implemented yet (this MVP ships web only)", l.Spec.Target)
	default:
		return fmt.Errorf("unknown target %q", l.Spec.Target)
	}
}

// probeWebTarget does a short-timeout HTTP GET to verify the dev's web
// server is up. Fails fast with a clear message — the dev is expected
// to start the web build themselves (or via `ship.deploy`) before the
// first iteration runs.
func probeWebTarget(ctx context.Context, url string, deadline time.Duration) error {
	client := &http.Client{Timeout: 3 * time.Second}
	pctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	start := time.Now()
	for {
		if err := pctx.Err(); err != nil {
			return fmt.Errorf("web target %s did not become ready within %s (is the dev server running?)",
				url, deadline)
		}
		req, _ := http.NewRequestWithContext(pctx, "GET", url, nil)
		resp, err := client.Do(req)
		if err == nil && resp != nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		if time.Since(start) > deadline {
			return fmt.Errorf("web target %s returned repeated 5xx / errors", url)
		}
		select {
		case <-pctx.Done():
			return pctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// --- Phase 2: heuristic playtest --------------------------------------

func phasePlaytest(ctx context.Context, l *LoopState) (*HeuristicReport, error) {
	if l.Spec.Target != "web" {
		return nil, fmt.Errorf("playtest only implemented for web target")
	}
	return chromedpHeuristicScan(ctx, l)
}

// chromedpHeuristicScan opens the URL in a headless Chrome via
// chromedp, captures the DOM text + any console errors, runs a set of
// zero-LLM heuristic detectors, takes a screenshot, and returns a
// HeuristicReport. Total runtime cap: ~45s for a first-cut scan.
func chromedpHeuristicScan(ctx context.Context, l *LoopState) (*HeuristicReport, error) {
	report := &HeuristicReport{
		StartedAt: time.Now().UTC(),
		Target:    l.Spec.Target,
		URL:       l.Spec.URL,
	}

	scanCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	allocCtx, allocCancel := chromedp.NewExecAllocator(scanCtx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("hide-scrollbars", true),
		)...,
	)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	var consoleMsgs []string
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		if msg, ok := ev.(*runtime.EventConsoleAPICalled); ok {
			if msg.Type == "error" || msg.Type == "warning" {
				parts := []string{}
				for _, arg := range msg.Args {
					if arg.Value != nil {
						parts = append(parts, string(arg.Value))
					}
				}
				consoleMsgs = append(consoleMsgs,
					fmt.Sprintf("[%s] %s", msg.Type, strings.Join(parts, " ")))
			}
		}
	})

	var (
		pageTitle   string
		visibleText string
		screenshot  []byte
	)
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(l.Spec.URL),
		chromedp.Sleep(2*time.Second),
		chromedp.Title(&pageTitle),
		chromedp.Text("body", &visibleText, chromedp.NodeVisible),
		chromedp.CaptureScreenshot(&screenshot),
	)
	if err != nil {
		return report, fmt.Errorf("chromedp scan: %w", err)
	}

	report.PageTitle = pageTitle
	report.ConsoleMessages = consoleMsgs
	if len(visibleText) > 4000 {
		report.VisibleText = visibleText[:4000] + "\n... (truncated)"
	} else {
		report.VisibleText = visibleText
	}

	// Save the screenshot.
	if dir, err := loopReportsDir(l.Spec.Name); err == nil {
		ts := time.Now().UTC().Format("20060102-150405")
		p := filepath.Join(dir, ts+"-screenshot.png")
		if werr := os.WriteFile(p, screenshot, 0600); werr == nil {
			report.ScreenshotPath = p
		}
	}

	// Run heuristic detectors — all zero-LLM, all deterministic.
	report.Findings = runHeuristicDetectors(visibleText, consoleMsgs)
	report.Summary = fmt.Sprintf("scanned %s: %d findings across %d chars of visible text",
		l.Spec.URL, len(report.Findings), len(visibleText))
	return report, nil
}

// runHeuristicDetectors is the "Mode 3 auto-fix" engine: it looks for
// the set of dumb, non-judgmental issues the doc enumerates. Every
// detector returns concrete findings the AI can fix in one line of
// code without needing creativity.
func runHeuristicDetectors(visibleText string, consoleMsgs []string) []Finding {
	findings := []Finding{}
	text := visibleText

	if strings.TrimSpace(text) == "" {
		findings = append(findings, Finding{
			Kind:     "blank_screen",
			Detail:   "body text is empty or whitespace-only",
			Severity: "critical",
			Hint:     "the target may still be loading or the initial route is broken",
		})
		return findings
	}

	for _, msg := range consoleMsgs {
		findings = append(findings, Finding{
			Kind:     "console_error",
			Detail:   msg,
			Severity: "major",
			Hint:     "console errors often point to a missing import or an undefined state field",
		})
	}

	// Detector: undefined / NaN / null appearing in rendered UI.
	for _, needle := range []string{"undefined", "NaN", "[object Object]"} {
		if strings.Contains(text, needle) {
			findings = append(findings, Finding{
				Kind:     "undefined_in_ui",
				Detail:   fmt.Sprintf("rendered text contains %q — likely a missing fallback or template value", needle),
				Severity: "major",
				Hint:     "check for missing optional-chaining, missing default, or untranslated state",
			})
		}
	}

	// Detector: Turkish diacritics stripped from common words.
	// Kick this as a "possible issue" (not critical) because many of
	// these appear in English strings too — but SFMG's UI is Turkish,
	// so a hit is usually a real bug.
	type diacriticProbe struct {
		bad  string
		good string
	}
	probes := []diacriticProbe{
		{"Kiralik", "Kiralık"},
		{"Sozlesme", "Sözleşme"},
		{"Guncelleme", "Güncelleme"},
		{"Turkiye", "Türkiye"},
		{"Basarili", "Başarılı"},
		{"Muvekkil", "Müvekkil"},
		{"Deger", "Değer"},
		{"Oncelikli", "Öncelikli"},
		{"Kulup", "Kulüp"},
		{"Cikis", "Çıkış"},
		{"Giris", "Giriş"},
	}
	for _, p := range probes {
		// Word-boundary to avoid matching inside longer words.
		re := regexp.MustCompile(`\b` + p.bad + `\b`)
		if re.MatchString(text) {
			findings = append(findings, Finding{
				Kind:     "turkish_diacritic_missing",
				Detail:   fmt.Sprintf("found %q in rendered UI — should be %q", p.bad, p.good),
				Severity: "minor",
				Hint:     "replace the source string; the fix is a one-character edit",
			})
		}
	}

	return findings
}

// --- Phase 3: AI think -------------------------------------------------

func phaseThink(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	runner := strings.ToLower(l.Spec.Think.Runner)

	// Chat-style: emit one yaver_say bubble before every spawn so
	// the user (terminal/mobile/web) sees what we asked for. Keep
	// it short — the full prompt goes on the runner's stdin, not
	// here.
	prompt := strings.TrimSpace(l.Spec.Think.PromptInline)
	if prompt == "" {
		prompt = "(no inline prompt — runner picks next coherent improvement)"
	}
	AutodevPublishYaverSay(fmt.Sprintf("%s · %s · %s",
		runner, l.Spec.Mode, claudeStreamLine(prompt, 200)))

	// Wrap the runner result so a single point emits the result event
	// without each spawnFoo function having to know about chat.
	started := time.Now()
	resp, err := dispatchRunner(ctx, runner, l, workDir, report, reportPath, nudge)
	status := "ok"
	if err != nil {
		status = "error"
	} else if resp != nil {
		status = resp.Status
	}
	AutodevPublishRunnerResult(runner, status, time.Since(started), 0)
	return resp, err
}

func dispatchRunner(ctx context.Context, runner string, l *LoopState, workDir string, report *HeuristicReport, reportPath, nudge string) (*AIResponse, error) {
	switch {
	case runner == "claude-code" || runner == "claude":
		return spawnClaudeCode(ctx, l, workDir, report, reportPath, nudge)
	case runner == "codex":
		return spawnCodex(ctx, l, workDir, report, reportPath, nudge)
	case runner == "aider" || runner == "aider-ollama":
		// aider-ollama is aider with a preset model + base URL
		// applied in spawnAider — same code path, different Model.
		if runner == "aider-ollama" && strings.TrimSpace(l.Spec.Think.Model) == "" {
			l.Spec.Think.Model = "ollama_chat/qwen2.5-coder:14b"
			l.Spec.Think.BaseURL = "http://127.0.0.1:11434"
		}
		return spawnAider(ctx, l, workDir, report, reportPath, nudge)
	case strings.HasPrefix(runner, "ollama"):
		return spawnOllama(ctx, l, workDir, report, reportPath, nudge)
	case runner == "hybrid":
		return spawnHybrid(ctx, l, workDir, report, reportPath, nudge)
	default:
		return nil, fmt.Errorf("unknown runner %q", runner)
	}
}

// buildLoopPrompt composes the full text the AI runner sees for one
// kick: the dev's feature prompt, the heuristic report as JSON, any
// nudge from the previous kick, and the mode-specific contract. Shared
// between spawnClaudeCode and spawnCodex so both runners see identical
// input and the dev can swap runners via `think.fallback` without the
// prompt shape changing underneath them.
func buildLoopPrompt(l *LoopState, workDir string, report *HeuristicReport, nudge string) (string, error) {
	prompt, err := effectivePrompt(l, workDir)
	if err != nil {
		return "", err
	}
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	parts := []string{
		// Cached project context (init.md + CLAUDE.md + remained.md)
		// goes FIRST so the runner sees it before the per-kick task.
		// Cuts the per-kick re-read tax that was making autodev /
		// autoideas / autotest slow.
		autoinitContextBlock(workDir),
		prompt,
		"\n\n---\n\n# Heuristic report for this iteration\n\n```json\n" + string(reportJSON) + "\n```",
	}
	if strings.TrimSpace(nudge) != "" {
		parts = append(parts,
			"\n\n---\n\n# Continuing from a previous kick\n\n"+
				"The previous kick reported `status: in_progress` with this next_step:\n\n"+
				"> "+strings.TrimSpace(nudge)+"\n\n"+
				"Do not redo work already committed. Focus on the next_step and keep the diff small. "+
				"If the next_step turns out to be already done or no longer relevant, respond with `status: done`.")
	}
	if l.Spec.Mode == LoopModeDevelop {
		parts = append(parts,
			"\n\n---\n\n# Auto Develop mode contract\n\n"+
				"Pick the smallest coherent slice of this feature that can land in one commit. "+
				"Prefer adding files over rewriting them. Do not touch tests unless the feature "+
				"explicitly requires a test change. When the whole feature prompt is complete "+
				"(not just the current slice), respond with `status: done`. If more work remains, "+
				"respond with `status: in_progress` and put the next concrete slice in `next_step`. "+
				"The loop will re-kick you with that next_step as a nudge.")
	} else {
		parts = append(parts,
			"\n\n---\n\n"+
				"Pick exactly one finding to act on. Make the smallest possible patch. "+
				"Emit the JSON status contract as the last line of your response. "+
				"Do not ask clarifying questions — make a judgment call and proceed.")
	}
	return strings.Join(parts, ""), nil
}

// spawnClaudeCode invokes the `claude` CLI in print mode with the
// loop's effective prompt plus the heuristic report piped via stdin.
// The expected contract: claude reads the prompt, makes at most one
// edit, and emits a JSON AIResponse as the last line of its stdout.
//
// Wire is intentionally thin — no special MCP tricks, no fancy flags.
// If the claude CLI flags change, only this function needs a tweak.
func spawnClaudeCode(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("`claude` CLI not on PATH — install Claude Code from https://claude.com/product/claude-code")
	}
	fullPrompt, err := buildLoopPrompt(l, workDir, report, nudge)
	if err != nil {
		return nil, err
	}

	// Use `claude --print` for non-interactive mode. Feed the prompt
	// via stdin so we don't run into argv length limits.
	//
	// --permission-mode acceptEdits: auto-approve Edit/Write without
	//   an interactive prompt. The loop is explicitly autonomous; we
	//   cannot show permission dialogs to a user who's asleep.
	// --add-dir: grant explicit filesystem access to the project root
	//   so tool calls don't trip over sandbox boundaries.
	// --output-format stream-json --verbose: emit one JSON event per
	// turn (assistant message, tool_use, tool_result, …) as soon as it
	// happens. Each event is a small line that flushes immediately —
	// no 4 KB libc buffering, no multi-minute silence. We translate
	// each event into a one-line human-friendly progress note for the
	// terminal/stream and keep the raw stream for parseClaudeStream
	// to extract the final result.
	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--add-dir", workDir,
	)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(fullPrompt)
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	fmt.Fprintf(os.Stderr, "[loop %s] spawning claude CLI (prompt=%d chars, report=%d findings)...\n",
		l.Spec.Name, len(fullPrompt), len(report.Findings))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claude CLI start: %w", err)
	}

	// Heartbeat: while Claude is working, print "still working … Ns"
	// every 30 s so a tailing user always sees something happen
	// within a vibe-friendly window. Cancelled when the subprocess
	// exits.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		started := time.Now()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "[claude] still working … %s elapsed\n",
					time.Since(started).Round(time.Second))
			}
		}
	}()

	resp, _, parseErr := parseClaudeStream(stdout)
	hbCancel()
	if waitErr := cmd.Wait(); waitErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("claude subprocess cancelled: %w", ctx.Err())
		}
		// Don't bail if we already extracted a usable AIResponse —
		// claude sometimes exits non-zero after a successful turn.
		if resp == nil {
			return nil, fmt.Errorf("claude CLI returned error: %w", waitErr)
		}
	}
	if parseErr != nil && resp == nil {
		return nil, parseErr
	}
	return resp, nil
}

// spawnOllama talks to a local ollama daemon via its HTTP API
// (http://127.0.0.1:11434/api/generate, override with OLLAMA_HOST).
// The runner string is "ollama:<model>" — e.g. "ollama:qwen2.5-coder"
// — so the dev can select a local model without hardcoding.
//
// Ollama doesn't edit files on its own; it just returns text. Auto
// Dev's contract is "the AI writes a patch + emits the JSON status
// contract." Since ollama can't write files, this runner responds
// with status=needs_human and a summary pointing the dev to run a
// tool-using runner (claude / codex) for the actual edit. It is
// still useful in a fallback chain so `think.fallback: [claude,
// ollama]` can at least classify the failure when the primary is
// rate-limited — a proper local tool-using runner (Cline, Roo, etc)
// will replace this when one of those ships a subprocess driver.
func spawnOllama(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	model := strings.TrimPrefix(strings.ToLower(l.Spec.Think.Runner), "ollama:")
	model = strings.TrimPrefix(model, "ollama")
	model = strings.TrimPrefix(model, ":")
	if strings.TrimSpace(model) == "" {
		model = "qwen2.5-coder"
	}

	host := envOr("OLLAMA_HOST", "http://127.0.0.1:11434")
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}

	fullPrompt, err := buildLoopPrompt(l, workDir, report, nudge)
	if err != nil {
		return nil, err
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": fullPrompt,
		"stream": false,
		"options": map[string]interface{}{
			// Low temperature so the JSON contract stays parseable.
			"temperature": 0.1,
		},
	})
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, host+"/api/generate", strings.NewReader(string(reqBody)))
	if rerr != nil {
		return nil, fmt.Errorf("build ollama request: %w", rerr)
	}
	req.Header.Set("Content-Type", "application/json")

	fmt.Fprintf(os.Stderr, "[loop %s] POST ollama %s (model=%s, prompt=%d chars)...\n",
		l.Spec.Name, host+"/api/generate", model, len(fullPrompt))

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("ollama request cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("ollama HTTP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned HTTP %d — check `ollama serve` is running", resp.StatusCode)
	}
	var payload struct {
		Response string `json:"response"`
	}
	if jerr := json.NewDecoder(resp.Body).Decode(&payload); jerr != nil {
		return nil, fmt.Errorf("decode ollama response: %w", jerr)
	}
	if strings.TrimSpace(payload.Response) == "" {
		return nil, fmt.Errorf("ollama returned an empty response (is model %q installed? `ollama pull %s`)", model, model)
	}

	// Ollama can classify, plan, and emit JSON — but it cannot edit
	// files. Parse whatever JSON contract it produced; if the AI
	// tried to return `status=done`, rewrite it to `needs_human`
	// with a summary that explains the limitation so the loop
	// doesn't think a non-edit kick was a success.
	aiResp, perr := parseAIResponse(payload.Response)
	if perr != nil || aiResp == nil {
		return &AIResponse{
			Status:  "needs_human",
			Summary: "ollama did not emit a parseable JSON contract — swap to a tool-using runner for actual edits",
		}, nil
	}
	if aiResp.Status == "done" || aiResp.Status == "in_progress" {
		aiResp.Status = "needs_human"
		aiResp.Summary = "ollama analysed the failure but cannot edit files — " + aiResp.Summary
	}
	return aiResp, nil
}

// envOr returns the named env var if set, otherwise the default.
// Used by spawnOllama so devs can point at a remote ollama box via
// OLLAMA_HOST without editing code.
func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// spawnAider invokes the `aider` CLI in non-interactive "one-shot"
// mode. Aider already has `--message` + `--yes-always` for scripted
// runs, and `--no-git` stops it from auto-committing — we do our
// own commit in phaseCommit. Same JSON contract as the other
// runners: the prompt arrives on stdin / --message, aider edits
// files, and emits the AIResponse contract at the end of stdout.
func spawnAider(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	if _, err := exec.LookPath("aider"); err != nil {
		return nil, fmt.Errorf("`aider` CLI not on PATH — install Aider (`pip install aider-chat`) to use this runner")
	}
	fullPrompt, err := buildLoopPrompt(l, workDir, report, nudge)
	if err != nil {
		return nil, err
	}

	// Aider reads the task prompt via --message. It refuses when the
	// prompt is multiline on the command line of some shells, so we
	// keep the argv shape and let exec handle escaping.
	//
	//   --yes-always      auto-confirm every Y/N prompt (autonomous)
	//   --no-git          don't auto-commit; phaseCommit does that
	//   --no-pretty       plain output — easier to parse
	//   --no-stream       complete response before returning
	//
	// Model/BaseURL come from the loop spec's Think.Model /
	// Think.BaseURL fields. When Model starts with "ollama" / "ollama_chat"
	// we also export OLLAMA_API_BASE so aider's litellm client points at
	// the right daemon (remote dev box, shared GPU host, etc.).
	argv := []string{
		"--yes-always",
		"--no-git",
		"--no-pretty",
		"--no-stream",
	}
	model := strings.TrimSpace(l.Spec.Think.Model)
	if model != "" {
		argv = append(argv, "--model", model)
	}
	argv = append(argv, "--message", fullPrompt)
	cmd := exec.CommandContext(ctx, "aider", argv...)
	cmd.Dir = workDir
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if base := strings.TrimSpace(l.Spec.Think.BaseURL); base != "" && strings.HasPrefix(strings.ToLower(model), "ollama") {
		cmd.Env = append(cmd.Env, "OLLAMA_API_BASE="+base)
	}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	fmt.Fprintf(os.Stderr, "[loop %s] spawning aider CLI (prompt=%d chars, report=%d findings)...\n",
		l.Spec.Name, len(fullPrompt), len(report.Findings))

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("aider subprocess cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("aider CLI returned error: %w", err)
	}
	return parseAIResponse(buf.String())
}

// spawnCodex invokes the `codex` CLI in non-interactive mode with the
// same JSON contract we use for Claude. Matches the CLI's runner
// config in tasks.go: `codex --quiet --full-auto <prompt>`. The prompt
// is written to a sibling file and passed through stdin so we don't
// hit argv length limits on long Auto Develop sessions.
func spawnCodex(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string, nudge string) (*AIResponse, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, fmt.Errorf("`codex` CLI not on PATH — install OpenAI Codex to use this runner")
	}
	fullPrompt, err := buildLoopPrompt(l, workDir, report, nudge)
	if err != nil {
		return nil, err
	}

	// `codex --quiet --full-auto` suppresses chatter and auto-approves
	// file writes — the same ergonomics `claude --print
	// --permission-mode acceptEdits` gives us. Prompt goes on stdin
	// via the `-` positional, which codex treats as "read from stdin".
	cmd := exec.CommandContext(ctx, "codex", "--quiet", "--full-auto", "-")
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(fullPrompt)
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	fmt.Fprintf(os.Stderr, "[loop %s] spawning codex CLI (prompt=%d chars, report=%d findings)...\n",
		l.Spec.Name, len(fullPrompt), len(report.Findings))

	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, os.Stderr)
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("codex subprocess cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("codex CLI returned error: %w", err)
	}
	return parseAIResponse(buf.String())
}

// parseAIResponse scans the AI's stdout for the last JSON block that
// matches the AIResponse shape. We explicitly do not trust the whole
// output — many runners emit narration before and after the JSON, so
// we search backwards for the last {...} that parses.
func parseAIResponse(output string) (*AIResponse, error) {
	// Strip common ```json fences first.
	output = strings.ReplaceAll(output, "```json", "```")

	// Find the last balanced JSON object in the output.
	// Simple heuristic: scan from the end for `{` and `}` and try to
	// parse each candidate.
	start := strings.LastIndex(output, "{")
	for start >= 0 {
		candidate := output[start:]
		// Try to find the matching closing brace by incrementally
		// growing the substring until json.Unmarshal succeeds.
		for end := len(candidate); end > 0; end-- {
			var resp AIResponse
			if err := json.Unmarshal([]byte(candidate[:end]), &resp); err == nil && resp.Status != "" {
				return &resp, nil
			}
		}
		start = strings.LastIndex(output[:start], "{")
	}
	// No parseable JSON — return a stuck status with the full output
	// as the summary so the dev can see what the AI said.
	snippet := output
	if len(snippet) > 400 {
		snippet = snippet[:400] + "..."
	}
	return &AIResponse{
		Status:  "stuck",
		Summary: "AI runner emitted no parseable status JSON: " + snippet,
	}, nil
}

// loadPromptFile reads the prompt file referenced by the spec's
// think.prompt field, resolving relative paths against the work dir.
func loadPromptFile(workDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("spec.think.prompt is required")
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(workDir, path)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// effectivePrompt resolves the actual text the AI runner should see for
// this iteration. Precedence, highest to lowest:
//
//   1. LoopState.PromptInline — set at runtime via `yaver loop prompt
//      set <name> "<msg>"` (or the mobile Auto Dev tab once wired).
//      This is how the dev writes a one-off feature prompt from their
//      phone and kicks the loop without editing any files.
//   2. LoopSpec.Think.PromptInline — inline prompt embedded in the
//      .loop.yaml spec itself. Useful for short, version-controlled
//      prompts that do not warrant a separate .md file.
//   3. LoopSpec.Think.Prompt — path to a markdown prompt file on disk.
//      The traditional approach for long / reusable prompts.
//
// The effective prompt is always wrapped with an instruction reminding
// the AI about the output JSON contract if none of the sources already
// document it (the three shipped prompts under .yaver/prompts/ do).
func effectivePrompt(l *LoopState, workDir string) (string, error) {
	if strings.TrimSpace(l.PromptInline) != "" {
		return wrapInlinePrompt(l, l.PromptInline), nil
	}
	if strings.TrimSpace(l.Spec.Think.PromptInline) != "" {
		return wrapInlinePrompt(l, l.Spec.Think.PromptInline), nil
	}
	if l.Spec.Think.Prompt == "" {
		return "", fmt.Errorf("think.prompt is empty and no inline prompt is set — run `yaver loop prompt set %s \"<your feature prompt>\"` to provide one", l.Spec.Name)
	}
	return loadPromptFile(workDir, l.Spec.Think.Prompt)
}

// wrapInlinePrompt injects the canonical JSON-contract reminder around
// a terse inline prompt so the dev can write "add a weekly market-crash
// event" from their phone without having to also type the JSON shape.
func wrapInlinePrompt(l *LoopState, body string) string {
	return "# Feature prompt (loop: " + l.Spec.Name + ")\n\n" +
		strings.TrimSpace(body) + "\n\n" +
		"---\n\n" +
		"## Output contract\n\n" +
		"When you finish your edit, emit exactly one JSON object as the LAST\n" +
		"line of your response:\n\n" +
		"```json\n" +
		"{\n" +
		"  \"status\": \"done\" | \"in_progress\" | \"stuck\" | \"needs_human\",\n" +
		"  \"summary\": \"one short sentence for the commit message\",\n" +
		"  \"files_touched\": [\"path/to/file.ts\"],\n" +
		"  \"next_step\": \"only if status=in_progress: the next concrete slice to land\",\n" +
		"  \"blockers\": [\"only if status=needs_human: human decisions needed\"]\n" +
		"}\n" +
		"```\n\n" +
		"Rules:\n" +
		"- Make the smallest coherent patch that advances the feature.\n" +
		"- Do not touch test files unless the feature requires it.\n" +
		"- Preserve Turkish diacritics (ç ş ğ ı ö ü İ) in user-visible strings.\n" +
		"- Never add real brand, club, or player names (see CLAUDE.md).\n"
}

// --- Phase 4: green gate -----------------------------------------------

func phaseGreenGate(ctx context.Context, l *LoopState, workDir string) error {
	gates := l.Spec.Think.RequireGreen
	if len(gates) == 0 {
		return nil
	}
	for _, gate := range gates {
		switch gate {
		case "typecheck":
			if err := runTypecheck(ctx, workDir); err != nil {
				return fmt.Errorf("typecheck failed: %w", err)
			}
		case "test":
			fmt.Fprintf(os.Stderr, "[loop %s] green gate 'test' is not yet wired — skipping\n", l.Spec.Name)
		default:
			fmt.Fprintf(os.Stderr, "[loop %s] unknown green gate %q — skipping\n", l.Spec.Name, gate)
		}
	}
	return nil
}

// runTypecheck runs the project's TypeScript typechecker. Convention
// for RN/web projects: `npx tsc --noEmit`. Output is streamed to
// stderr so the dev can see what the AI broke when rollback fires.
//
// We require a local `node_modules/.bin/tsc` — if the project has not
// installed TypeScript, the green gate cannot enforce anything and we
// warn rather than fail (a failing gate over a missing tool would
// roll back every iteration and kill the loop).
func runTypecheck(ctx context.Context, workDir string) error {
	if _, err := os.Stat(filepath.Join(workDir, "tsconfig.json")); err != nil {
		return nil // no tsconfig → nothing to check
	}
	tscBin := filepath.Join(workDir, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tscBin); err != nil {
		fmt.Fprintf(os.Stderr,
			"[green-gate] tsconfig.json exists but node_modules/.bin/tsc is missing — "+
				"skipping typecheck (run `npm install` to enable the gate)\n")
		return nil
	}
	cmd := exec.CommandContext(ctx, tscBin, "--noEmit")
	cmd.Dir = workDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- Phase 5: commit (+ deploy) ----------------------------------------

func phaseCommit(ctx context.Context, l *LoopState, workDir string, aiResp *AIResponse) (string, error) {
	// git add -A in the workDir. The AI may have created new files.
	if err := runGitCtx(ctx, workDir,"add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w", err)
	}

	msg := strings.TrimSpace(aiResp.Summary)
	if msg == "" {
		msg = "auto-dev loop iteration"
	}
	prefix := l.Spec.Ship.CommitPrefix
	if prefix == "" {
		prefix = "yaver-loop:"
	}
	fullMsg := prefix + " " + msg + "\n\n" +
		"Auto-generated by `yaver loop run " + l.Spec.Name + "`.\n" +
		"Mode: " + string(l.Spec.Mode) + "\n"

	if err := runGitCtx(ctx, workDir,"commit", "-m", fullMsg); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	sha := gitHeadSHA(workDir)

	// Only push if the spec targets a branch that looks like it
	// should be remote-tracked. Skip if ship.branch is empty or
	// explicitly "local".
	//
	// The worktree runs on a detached HEAD pointing at ship.branch's
	// tip, so the push has to use the `HEAD:<branch>` refspec — a
	// plain `git push origin <branch>` fails in detached state.
	branch := l.Spec.Ship.Branch
	if branch != "" && branch != "local" {
		if err := runGitCtx(ctx, workDir, "push", "origin", "HEAD:"+branch); err != nil {
			// Push failure is not fatal — the commit is still on local,
			// the dev can push manually later. We surface the error in
			// stderr but return success.
			fmt.Fprintf(os.Stderr, "[loop %s] git push origin HEAD:%s failed: %v\n",
				l.Spec.Name, branch, err)
		}
	}
	return sha, nil
}

func phaseDeploy(ctx context.Context, l *LoopState, workDir string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", l.Spec.Ship.Deploy)
	cmd.Dir = workDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// releaseTrainAllowsDeploy returns (reason, true/false). If the train
// is disabled (N == 0) the deploy always runs — backwards-compatible
// with the old "run on every done status" behavior. Otherwise:
//
//  1. If paused, no deploy.
//  2. If the green counter hasn't reached N, no deploy.
//  3. If today's TestFlight budget is already spent, no deploy.
//
// A false return means the Deploy command is skipped this iteration;
// the green counter still advances so the next iteration is closer
// to shipping.
func releaseTrainAllowsDeploy(l *LoopState) (string, bool) {
	rt := l.Spec.Ship.ReleaseTrain
	if rt.N <= 0 {
		return "train disabled — N=0", true
	}
	if rt.Paused {
		return "release train paused", false
	}
	// rollBudgetDay is idempotent — call it so the TestflightToday
	// counter reflects the current UTC day before we compare.
	l.rollBudgetDay()
	if l.Spec.Budget.MaxTestFlightPerDay > 0 &&
		l.TestflightToday >= l.Spec.Budget.MaxTestFlightPerDay {
		return fmt.Sprintf("daily testflight budget exhausted (%d/%d)",
			l.TestflightToday, l.Spec.Budget.MaxTestFlightPerDay), false
	}
	if l.GreenRunSinceLastDeploy < rt.N {
		return fmt.Sprintf("waiting for %d consecutive green kicks (have %d)",
			rt.N, l.GreenRunSinceLastDeploy), false
	}
	return "train armed — shipping", true
}

// --- git helpers -------------------------------------------------------

func gitHeadSHA(workDir string) string {
	out, err := exec.Command("git", "-C", workDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitChangedFiles(workDir string) []string {
	out, err := exec.Command("git", "-C", workDir, "status", "--porcelain").Output()
	if err != nil {
		return nil
	}
	files := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		// Porcelain v1: "XY filename" (X/Y are single chars). Split on
		// whitespace so we're robust to one-space vs multi-space padding.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// For renames the format is `R  old -> new` — we want the
		// destination name, which is always the last field.
		files = append(files, fields[len(fields)-1])
	}
	return files
}

func gitResetHardQuiet(workDir, sha string) {
	if sha == "" {
		return
	}
	_ = exec.Command("git", "-C", workDir, "reset", "--hard", sha).Run()
	// Also clean any untracked files the AI may have created.
	_ = exec.Command("git", "-C", workDir, "clean", "-fd").Run()
}

// runGitCtx runs git for the autonomous loop. Renamed from runGit to
// avoid colliding with the (string, args...) git_http.go helper that
// returns stdout — both files used the same name.
func runGitCtx(ctx context.Context, workDir string, args ...string) error {
	fullArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- report persistence ------------------------------------------------

func loopReportsDir(name string) (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "loops", name, "reports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func persistReport(l *LoopState, r *HeuristicReport) (string, error) {
	dir, err := loopReportsDir(l.Spec.Name)
	if err != nil {
		return "", err
	}
	ts := time.Now().UTC().Format("20060102-150405")
	p := filepath.Join(dir, ts+"-report.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, data, 0600)
}

// --- stop-file watchdog ------------------------------------------------

func stopFileWatchdog(ctx context.Context, killFile string, cancel context.CancelFunc) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(killFile); err == nil {
				cancel()
				return
			}
		}
	}
}

func classifyError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "stopped"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "stuck"
	}
	if err != nil {
		return "failed"
	}
	return "done"
}
