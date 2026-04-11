package testkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// Result is the outcome of running a single Spec.
type Result struct {
	Spec       *Spec
	Passed     bool
	// Flaky is true when a previous attempt failed but the final
	// attempt passed (i.e., the spec passed only thanks to FlakeRetries).
	Flaky      bool
	// Attempt is the 1-indexed retry attempt this Result represents.
	// Always 1 when FlakeRetries == 0.
	Attempt    int
	StartedAt  time.Time
	FinishedAt time.Time
	Steps      []StepResult
	// Err is set on a top-level failure (browser launch, setup, validation,
	// teardown). Per-step failures are in Steps[i].Err and also leave Passed
	// false.
	Err error
}

// Duration returns wall-clock duration of the run.
func (r *Result) Duration() time.Duration {
	return r.FinishedAt.Sub(r.StartedAt)
}

// StepResult records what happened to one step.
type StepResult struct {
	Index       int
	Description string // human label of the action ("goto /auth", "click button")
	Phase       string // "setup" | "step" | "teardown"
	StartedAt   time.Time
	Duration    time.Duration
	Err         error
	// ScreenshotPath is set when this step (or its failure) produced a PNG.
	ScreenshotPath string
}

// RunOptions controls how the runner executes one or more specs.
type RunOptions struct {
	// ArtifactsDir is where screenshots / traces land. Defaults to
	// `<spec dir>/.yaver-test-results`. The runner creates per-run
	// subdirectories so reruns don't clobber each other.
	ArtifactsDir string

	// Headful overrides Spec.Headful for ad-hoc debugging from the CLI.
	Headful bool

	// VerboseLog prints every step result to stderr while the run is in
	// progress. Useful for `yaver test run --verbose`.
	VerboseLog bool

	// Snapshot controls snapshot/visual-regression behavior. Zero value
	// means "check mode with default thresholds." Set Mode = SnapshotModeUpdate
	// to refresh baselines (`yaver test run --update-snapshots`).
	Snapshot SnapshotConfig

	// FlakeRetries is the number of times to re-run a failed spec
	// before declaring it a real failure. 0 = no retries (Playwright
	// default). A spec that fails the first attempt and passes a
	// subsequent attempt is tagged "flaky" in the result.
	FlakeRetries int
}

// Run executes a single spec end-to-end and returns the result. The
// runner never panics — even chromedp launch failures are turned into
// Result.Err.
//
// If RunOptions.FlakeRetries > 0, a failing spec is retried up to N
// times. The final Result reports the latest attempt; if any earlier
// attempt failed but the final one passed, Flaky is set so reporters
// can flag it. This matches Playwright's `retries:` behavior.
func Run(ctx context.Context, spec *Spec, opts RunOptions) *Result {
	maxAttempts := 1 + opts.FlakeRetries
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var last *Result
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		r := runOnce(ctx, spec, opts)
		r.Attempt = attempt
		if attempt > 1 && r.Passed {
			r.Flaky = true
		}
		last = r
		if r.Passed {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	return last
}

// runOnce runs the spec exactly once. Run() wraps it for retries.
func runOnce(ctx context.Context, spec *Spec, opts RunOptions) *Result {
	res := &Result{
		Spec:      spec,
		StartedAt: time.Now(),
	}
	defer func() {
		res.FinishedAt = time.Now()
	}()

	if err := spec.Validate(); err != nil {
		res.Err = err
		return res
	}

	switch spec.Target {
	case TargetWeb:
		runWebSpec(ctx, spec, opts, res)
	case TargetIOSSim, TargetAndroidEmu, TargetDevice:
		// Drivers for these land in M5; for now we surface a clear,
		// actionable error rather than silently passing.
		res.Err = fmt.Errorf("target %q is not implemented yet — see docs/roadmap_ci_solo_developer_lower_costs.md (M5)", spec.Target)
	default:
		res.Err = fmt.Errorf("unknown target %q", spec.Target)
	}

	res.Passed = res.Err == nil && allStepsPassed(res.Steps)
	return res
}

func allStepsPassed(steps []StepResult) bool {
	for _, s := range steps {
		if s.Err != nil {
			return false
		}
	}
	return true
}

// runWebSpec drives Chromium via CDP. The browser is launched with
// chromedp's default Chrome locator, which on macOS finds Google Chrome
// in /Applications and on Linux falls back to PATH (`google-chrome`,
// `chromium`, etc.). If no Chrome is found, the error message tells the
// user how to install one — and stays out of the way of the rest of
// Yaver, which is the priority on a solo dev's laptop.
func runWebSpec(ctx context.Context, spec *Spec, opts RunOptions, res *Result) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		res.Err = fmt.Errorf("yaver test run only supports macOS and Linux (current: %s)", runtime.GOOS)
		return
	}

	// chromedp options. We default to headless and let the user override
	// per-spec or per-run.
	headful := spec.Headful || opts.Headful
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !headful),
		chromedp.Flag("disable-gpu", !headful),
		chromedp.Flag("hide-scrollbars", !headful),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-sandbox", true),
	)
	if spec.Viewport != nil {
		allocOpts = append(allocOpts, chromedp.WindowSize(spec.Viewport.Width, spec.Viewport.Height))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// First Run() call boots Chrome. Surface launch failures cleanly.
	if err := chromedp.Run(browserCtx); err != nil {
		res.Err = fmt.Errorf("launch chromium: %w (install Chrome/Chromium and ensure it's on PATH)", err)
		return
	}

	artifactDir := artifactDirFor(spec, opts)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		res.Err = fmt.Errorf("mkdir artifacts: %w", err)
		return
	}

	// Setup phase
	if !runPhase(browserCtx, spec, opts, res, "setup", spec.Setup, artifactDir) {
		// Setup failed — still try teardown but don't run main steps.
		runPhase(browserCtx, spec, opts, res, "teardown", spec.Teardown, artifactDir)
		return
	}

	// Main steps
	mainOK := runPhase(browserCtx, spec, opts, res, "step", spec.Steps, artifactDir)

	// Teardown always runs, regardless of main outcome.
	runPhase(browserCtx, spec, opts, res, "teardown", spec.Teardown, artifactDir)

	_ = mainOK
}

// runPhase executes a slice of steps. Returns true if every step passed.
// Stops on the first failure within the phase (matches Playwright's
// default behavior).
func runPhase(ctx context.Context, spec *Spec, opts RunOptions, res *Result, phase string, steps []Step, artifactDir string) bool {
	allOK := true
	for i, step := range steps {
		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
		sr := StepResult{
			Index:       i,
			Description: stepDescription(step),
			Phase:       phase,
			StartedAt:   time.Now(),
		}
		err := executeStep(stepCtx, spec, step)
		// On a selector-not-found failure, give the autonomous fix
		// loop one shot at proposing a replacement selector before we
		// declare the step failed. This is the "tests don't rot"
		// payoff: a cosmetic refactor that renames a class doesn't
		// break the suite, the LLM patches the selector and the run
		// continues.
		if err != nil && IsSelectorFailure(err) && step.Click != "" {
			dom := captureDOM(stepCtx)
			fix := SelectorReplaceFromSelfHeal(stepCtx, step.Click, dom, "click target")
			if fix != nil && fix.Strategy == "selector_replace" && fix.SelectorReplace != "" {
				patched := step
				patched.Click = fix.SelectorReplace
				if retryErr := executeStep(stepCtx, spec, patched); retryErr == nil {
					sr.Description += " (auto-healed)"
					err = nil
				}
			}
		}
		if err != nil && IsSelectorFailure(err) && step.Fill != nil {
			dom := captureDOM(stepCtx)
			fix := SelectorReplaceFromSelfHeal(stepCtx, step.Fill.Selector, dom, "input field")
			if fix != nil && fix.Strategy == "selector_replace" && fix.SelectorReplace != "" {
				patched := step
				patched.Fill = &FillStep{Selector: fix.SelectorReplace, Text: step.Fill.Text}
				if retryErr := executeStep(stepCtx, spec, patched); retryErr == nil {
					sr.Description += " (auto-healed)"
					err = nil
				}
			}
		}
		// Snapshot steps need a separate phase: capture + compare against
		// the on-disk baseline. Run after the chromedp action returns
		// successfully (so the page is in its final state).
		if err == nil && step.Snapshot != "" {
			err = runSnapshotStep(stepCtx, spec, step.Snapshot, opts)
		}
		// Inspect step: capture a screenshot, hand it to the user's
		// chosen LLM, fail the step if the verdict is "fail".
		if err == nil && step.Inspect != "" {
			shotPath := filepath.Join(artifactDir, fmt.Sprintf("%s-%02d-inspect.png", phase, i))
			if shotErr := captureScreenshot(stepCtx, shotPath); shotErr == nil {
				cfg := LoadVisionConfig()
				ir := InspectImage(stepCtx, cfg, shotPath, step.Inspect)
				if ir.Verdict == "fail" {
					err = fmt.Errorf("visual inspection failed: %v", ir.Issues)
				}
				sr.ScreenshotPath = shotPath
			}
		}
		cancel()
		sr.Duration = time.Since(sr.StartedAt)

		if err != nil {
			sr.Err = err
			allOK = false
			// Failure screenshot if the spec asks for it.
			if shouldScreenshot(spec, true) {
				p := filepath.Join(artifactDir, fmt.Sprintf("%s-%02d-FAIL.png", phase, i))
				if shotErr := captureScreenshot(ctx, p); shotErr == nil {
					sr.ScreenshotPath = p
				}
			}
		} else if step.Screenshot || shouldScreenshot(spec, false) {
			p := filepath.Join(artifactDir, fmt.Sprintf("%s-%02d.png", phase, i))
			if shotErr := captureScreenshot(ctx, p); shotErr == nil {
				sr.ScreenshotPath = p
			}
		}

		if opts.VerboseLog {
			status := "ok"
			if sr.Err != nil {
				status = "FAIL: " + sr.Err.Error()
			}
			fmt.Fprintf(os.Stderr, "  [%s %d] %s — %s (%s)\n", phase, i, sr.Description, status, sr.Duration.Round(time.Millisecond))
		}

		res.Steps = append(res.Steps, sr)
		if sr.Err != nil {
			break
		}
	}
	return allOK
}

func shouldScreenshot(spec *Spec, isFailure bool) bool {
	if spec.Artifacts.Screenshot != nil && !*spec.Artifacts.Screenshot {
		return false
	}
	switch spec.Artifacts.On {
	case "always":
		return true
	case "never":
		return false
	default: // "failure" (default)
		return isFailure
	}
}

func captureScreenshot(ctx context.Context, path string) error {
	var buf []byte
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// executeStep dispatches a single step to chromedp. Each action returns
// an error or nil; the runner attaches the step's index/phase outside.
func executeStep(ctx context.Context, spec *Spec, step Step) error {
	switch {
	case step.Goto != "":
		url := step.Goto
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			if spec.URL == "" {
				return fmt.Errorf("relative goto %q but spec has no top-level url", url)
			}
			url = strings.TrimRight(spec.URL, "/") + "/" + strings.TrimLeft(url, "/")
		}
		return chromedp.Run(ctx, chromedp.Navigate(url))

	case step.Click != "":
		return chromedp.Run(ctx,
			chromedp.WaitVisible(step.Click, chromedp.ByQuery),
			chromedp.Click(step.Click, chromedp.ByQuery),
		)

	case step.Fill != nil:
		return chromedp.Run(ctx,
			chromedp.WaitVisible(step.Fill.Selector, chromedp.ByQuery),
			chromedp.Clear(step.Fill.Selector, chromedp.ByQuery),
			chromedp.SendKeys(step.Fill.Selector, step.Fill.Text, chromedp.ByQuery),
		)

	case step.WaitFor != "":
		return chromedp.Run(ctx, chromedp.WaitVisible(step.WaitFor, chromedp.ByQuery))

	case step.WaitForURL != "":
		return waitForURL(ctx, step.WaitForURL)

	case step.SleepMS > 0:
		select {
		case <-time.After(time.Duration(step.SleepMS) * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}

	case step.AssertVisible != "":
		return chromedp.Run(ctx, chromedp.WaitVisible(step.AssertVisible, chromedp.ByQuery))

	case step.AssertText != "":
		var body string
		if err := chromedp.Run(ctx, chromedp.Text("body", &body, chromedp.ByQuery)); err != nil {
			return err
		}
		if !strings.Contains(body, step.AssertText) {
			return fmt.Errorf("body does not contain %q", step.AssertText)
		}
		return nil

	case step.AssertTitle != "":
		var title string
		if err := chromedp.Run(ctx, chromedp.Title(&title)); err != nil {
			return err
		}
		if !strings.Contains(title, step.AssertTitle) {
			return fmt.Errorf("title %q does not contain %q", title, step.AssertTitle)
		}
		return nil

	case step.AssertURL != "":
		var loc string
		if err := chromedp.Run(ctx, chromedp.Location(&loc)); err != nil {
			return err
		}
		if !strings.Contains(loc, step.AssertURL) {
			return fmt.Errorf("url %q does not contain %q", loc, step.AssertURL)
		}
		return nil

	case step.Eval != "":
		var ignored interface{}
		return chromedp.Run(ctx, chromedp.Evaluate(step.Eval, &ignored))

	case step.Snapshot != "":
		// Snapshot is handled by the runner via runSnapshotStep — but
		// the executor must still match this case so the step doesn't
		// fall through to the "no action" error.
		return nil

	case step.Inspect != "":
		// Visual LLM inspection is handled by the runner after the
		// step's chromedp action returns.
		return nil

	case step.Screenshot:
		// Handled by the runner (it knows the artifact dir + index).
		return nil
	}
	return errors.New("step has no action set")
}

// waitForURL polls the current location until it contains `substr` or
// the context times out. chromedp has no built-in URL matcher.
func waitForURL(ctx context.Context, substr string) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait_for_url %q: %w", substr, ctx.Err())
		default:
		}
		var loc string
		if err := chromedp.Run(ctx, chromedp.Location(&loc)); err == nil && strings.Contains(loc, substr) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// captureDOM grabs the current page's outerHTML via chromedp.
// Best-effort — returns "" on any error so the autonomous fix path
// can fall back to a code edit instead.
func captureDOM(ctx context.Context) string {
	var html string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery)); err != nil {
		return ""
	}
	return html
}

// stepDescription returns a human label for a step, used in reports.
func stepDescription(step Step) string {
	switch {
	case step.Goto != "":
		return "goto " + step.Goto
	case step.Click != "":
		return "click " + step.Click
	case step.Fill != nil:
		return "fill " + step.Fill.Selector
	case step.WaitFor != "":
		return "wait_for " + step.WaitFor
	case step.WaitForURL != "":
		return "wait_for_url " + step.WaitForURL
	case step.SleepMS > 0:
		return fmt.Sprintf("sleep %dms", step.SleepMS)
	case step.AssertVisible != "":
		return "assert.visible " + step.AssertVisible
	case step.AssertText != "":
		return "assert.text " + step.AssertText
	case step.AssertTitle != "":
		return "assert.title " + step.AssertTitle
	case step.AssertURL != "":
		return "assert.url " + step.AssertURL
	case step.Eval != "":
		return "eval"
	case step.Snapshot != "":
		return "snapshot " + step.Snapshot
	case step.Screenshot:
		return "screenshot"
	}
	return "(noop)"
}

func artifactDirFor(spec *Spec, opts RunOptions) string {
	if opts.ArtifactsDir != "" {
		return filepath.Join(opts.ArtifactsDir, sanitizeName(spec.Name))
	}
	base := filepath.Dir(spec.Path)
	return filepath.Join(base, ".yaver-test-results", sanitizeName(spec.Name))
}

func sanitizeName(name string) string {
	r := strings.NewReplacer(" ", "-", "/", "-", string(filepath.Separator), "-")
	return r.Replace(name)
}
