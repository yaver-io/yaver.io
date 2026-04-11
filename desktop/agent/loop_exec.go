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
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
type IterationResult struct {
	Status       string    `json:"status"` // "done" | "stuck" | "stopped" | "failed" | "needs_human"
	Summary      string    `json:"summary"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	ReportPath   string    `json:"reportPath,omitempty"`
	PatchCommit  string    `json:"patchCommit,omitempty"`
	FilesChanged []string  `json:"filesChanged,omitempty"`
	Err          string    `json:"err,omitempty"`
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
	Status       string   `json:"status"`        // "done" | "stuck" | "needs_human" | "in_progress"
	Summary      string   `json:"summary"`
	FilesTouched []string `json:"files_touched,omitempty"`
	NextStep     string   `json:"next_step,omitempty"`
	Blockers     []string `json:"blockers,omitempty"`
}

// runLoopIteration is the entry point called by `yaver loop run <name>`.
// It blocks until the iteration finishes, is cancelled, or fails.
func runLoopIteration(ctx context.Context, l *LoopState) *IterationResult {
	result := &IterationResult{
		Status:    "failed",
		StartedAt: time.Now().UTC(),
	}

	// The dev's own sfmg/yaver.io project directory is the working dir
	// for the loop — this is where the AI runner will see the code, and
	// where git commit will land the patch. We derive it from the spec:
	// the loop.yaml path the dev registered lives in their repo root.
	workDir, err := deriveWorkDir(l)
	if err != nil {
		result.Err = err.Error()
		result.FinishedAt = time.Now().UTC()
		return result
	}

	// Hard bail: refuse to run over uncommitted work in the main tree.
	// This is the pragmatic stand-in for worktree isolation until that
	// lands. Never destroy user work.
	if dirty, files := gitIsDirty(workDir); dirty {
		result.Status = "stuck"
		result.Summary = fmt.Sprintf(
			"refusing to run: working tree has %d uncommitted change(s). Stash or commit first.",
			len(files))
		result.FinishedAt = time.Now().UTC()
		return result
	}

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

	// --- Phase 1: target readiness ---
	if err := phaseReadiness(watchCtx, l); err != nil {
		result.Status = classifyError(watchCtx, err)
		result.Err = err.Error()
		result.Summary = "phase 1 readiness check failed"
		result.FinishedAt = time.Now().UTC()
		return result
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
	aiResp, aiErr := phaseThink(watchCtx, l, workDir, report, reportPath)
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

	if l.Spec.Ship.Deploy != "" && aiResp.Status == "done" {
		if derr := phaseDeploy(watchCtx, l, workDir); derr != nil {
			fmt.Fprintf(os.Stderr, "[loop %s] deploy phase failed (commit still landed): %v\n",
				l.Spec.Name, derr)
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

// deriveWorkDir figures out which project directory a loop belongs to.
// The convention is: the spec file lives in the repo root, so the dir
// the dev ran `yaver loop add <path>` from is the working directory.
// We stash that as an absolute path on the LoopState at add time; for
// now we fall back to the current working directory so the behavior
// is predictable until the persistence lands.
func deriveWorkDir(l *LoopState) (string, error) {
	if l.Spec.Target == "web" && l.Spec.URL == "" {
		return "", fmt.Errorf("spec.url is required for target=web")
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if !looksLikeGitRepo(wd) {
		return "", fmt.Errorf("cwd %s is not a git repo — run `yaver loop run` from the project root", wd)
	}
	return wd, nil
}

func looksLikeGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
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

func phaseThink(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string) (*AIResponse, error) {
	runner := strings.ToLower(l.Spec.Think.Runner)
	switch {
	case runner == "claude-code" || runner == "claude":
		return spawnClaudeCode(ctx, l, workDir, report, reportPath)
	case runner == "codex" || runner == "aider" || strings.HasPrefix(runner, "ollama"):
		return nil, fmt.Errorf("runner %q is not wired yet (this MVP ships claude-code only — PRs welcome)", runner)
	default:
		return nil, fmt.Errorf("unknown runner %q", runner)
	}
}

// spawnClaudeCode invokes the `claude` CLI in print mode with the
// loop's prompt file plus the heuristic report piped via stdin. The
// expected contract: claude reads the prompt, makes at most one edit,
// and emits a JSON AIResponse as the last line of its stdout.
//
// Wire is intentionally thin — no special MCP tricks, no fancy flags.
// If the claude CLI flags change, only this function needs a tweak.
func spawnClaudeCode(ctx context.Context, l *LoopState, workDir string, report *HeuristicReport, reportPath string) (*AIResponse, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("`claude` CLI not on PATH — install Claude Code from https://claude.com/product/claude-code")
	}

	prompt, err := loadPromptFile(workDir, l.Spec.Think.Prompt)
	if err != nil {
		return nil, fmt.Errorf("load prompt file: %w", err)
	}

	// Build the full prompt: system-prompt file + heuristic report + a
	// contract reminder. The prompt file itself already documents the
	// output JSON format; the reminder is belt-and-braces.
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	fullPrompt := fmt.Sprintf(
		"%s\n\n---\n\n# Heuristic report for this iteration\n\n```json\n%s\n```\n\n---\n\n"+
			"Pick exactly one finding to act on. Make the smallest possible patch. "+
			"Emit the JSON status contract as the last line of your response. "+
			"Do not ask clarifying questions — make a judgment call and proceed.",
		prompt, string(reportJSON),
	)

	// Use `claude --print` for non-interactive mode. Feed the prompt
	// via stdin so we don't run into argv length limits.
	cmd := exec.CommandContext(ctx, "claude", "--print")
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(fullPrompt)
	cmd.Stderr = os.Stderr

	// Kill the subprocess via SIGTERM first, SIGKILL after a grace period.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second

	fmt.Fprintf(os.Stderr, "[loop %s] spawning claude CLI (prompt=%d chars, report=%d findings)...\n",
		l.Spec.Name, len(fullPrompt), len(report.Findings))

	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("claude subprocess cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("claude CLI returned error: %w", err)
	}

	return parseAIResponse(string(out))
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
func runTypecheck(ctx context.Context, workDir string) error {
	if _, err := os.Stat(filepath.Join(workDir, "tsconfig.json")); err != nil {
		return nil // no tsconfig → nothing to check
	}
	cmd := exec.CommandContext(ctx, "npx", "--no-install", "tsc", "--noEmit")
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
	branch := l.Spec.Ship.Branch
	if branch != "" && branch != "local" {
		if err := runGitCtx(ctx, workDir,"push", "origin", branch); err != nil {
			// Push failure is not fatal — the commit is still on local,
			// the dev can push manually later. We surface the error in
			// stderr but return success.
			fmt.Fprintf(os.Stderr, "[loop %s] git push origin %s failed: %v\n",
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

// --- git helpers -------------------------------------------------------

func gitIsDirty(workDir string) (bool, []string) {
	out, err := exec.Command("git", "-C", workDir, "status", "--porcelain").Output()
	if err != nil {
		return false, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return false, nil
	}
	return true, lines
}

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
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	files := []string{}
	for _, line := range lines {
		if len(line) > 3 {
			files = append(files, strings.TrimSpace(line[3:]))
		}
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
