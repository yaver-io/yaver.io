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

	cdpnetwork "github.com/chromedp/cdproto/network"
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

	// InstrumentationPath is where the console/network/perf dump
	// was written for this run (empty when capture: is all off).
	InstrumentationPath string
	// VideoFramesDir is where the full screencast PNG sequence was
	// flushed for a passing run when RunOptions.ForceVideo is on.
	// Empty for failing runs (where individual step failures already
	// flush their own ring into per-step directories).
	VideoFramesDir string
	// instr is the live state used by step handlers (save_har).
	// Unexported so it doesn't leak into the JSON reporter.
	instr *InstrumentationState
	// frameRing holds the recent screencast frames when
	// Spec.Artifacts.Video is true. runPhase flushes it next to
	// any failing step's screenshot so the mobile
	// FrameSequencePlayer has something to show. Unexported.
	frameRing *FrameRing
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

	// ForceVideo overrides Spec.Artifacts.Video=false so callers
	// (testkit_run --video, the workspace pane's "watch failures"
	// button, CI in deep-debug mode) can flip on screencast capture
	// for every spec in the suite without editing every YAML file.
	// When true the runner starts a FrameRing for each spec; on
	// success the frames are dumped under the artifacts dir alongside
	// the spec's other outputs (so the user can scrub through a
	// passing run too, not just failures).
	ForceVideo bool
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
	case TargetWebPlaywright:
		runPlaywrightSpec(ctx, spec, opts, res)
	case TargetAndroidEmu:
		runAndroidSpec(ctx, spec, opts, res, false)
	case TargetAndroidRedroid:
		runRedroidSpec(ctx, spec, opts, res)
	case TargetIOSSim:
		runIOSSpec(ctx, spec, opts, res, false)
	case TargetDevice:
		// Real USB-attached device; platform disambiguation happens
		// inside the handler based on what's plugged in.
		runDeviceSpec(ctx, spec, opts, res)
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

// seedCookies sets each Spec.Cookie via CDP before the first navigation,
// so authenticated pages can be tested without driving a login UI. httpOnly
// cookies (which a page's JS can't set) work here because CDP sets them at
// the network layer.
func seedCookies(cookies []SpecCookie) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for _, c := range cookies {
			path := c.Path
			if path == "" {
				path = "/"
			}
			if err := cdpnetwork.SetCookie(c.Name, c.Value).
				WithDomain(c.Domain).
				WithPath(path).
				WithSecure(c.Secure).
				WithHTTPOnly(c.HTTPOnly).
				Do(ctx); err != nil {
				return fmt.Errorf("set cookie %q: %w", c.Name, err)
			}
		}
		return nil
	})
}

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

	// Seed pre-auth cookies (Spec.Cookies) before any navigation so the
	// spec can exercise logged-in pages without driving a login form. The
	// values were already ${ENV}-expanded at load time.
	if len(spec.Cookies) > 0 {
		if err := chromedp.Run(browserCtx, seedCookies(spec.Cookies)); err != nil {
			res.Err = fmt.Errorf("seed cookies: %w", err)
			return
		}
	}

	artifactDir := artifactDirFor(spec, opts)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		res.Err = fmt.Errorf("mkdir artifacts: %w", err)
		return
	}

	// Instrumentation — console errors, network capture, perf
	// metrics, etc. Each stream is cheap when off and only adds
	// visible cost when the spec turns it on via `capture:`.
	instr := InstallInstrumentation(browserCtx, spec.Capture)
	res.instr = instr

	// Network emulation — CDP lets us throttle the whole session
	// to mimic slow 3G or go fully offline. The profile applies
	// for the entire spec; individual steps can't opt back in
	// mid-run yet (add a per-step knob later if a solo dev
	// asks for it).
	if err := applyNetworkProfile(browserCtx, spec.NetworkProfile); err != nil {
		fmt.Fprintf(os.Stderr, "[testkit] network profile %q failed: %v — continuing without throttle\n",
			spec.NetworkProfile, err)
	}

	// Screencast capture — opt-in via `artifacts: {video: true}` per
	// spec OR via opts.ForceVideo (testkit_run --video, glasses
	// workspace's "watch failures" button). Holds the last ~120
	// frames in memory; flushed to disk next to a failing step's
	// screenshot so the mobile FrameSequencePlayer can scrub.
	if spec.Artifacts.Video || opts.ForceVideo {
		res.frameRing = NewFrameRing(120)
		if stop, err := StartScreencast(browserCtx, res.frameRing); err == nil {
			defer stop()
		} else {
			fmt.Fprintf(os.Stderr, "[testkit] screencast start failed: %v — continuing without video\n", err)
			res.frameRing = nil
		}
	}

	// Setup phase
	if !runPhase(browserCtx, spec, opts, res, "setup", spec.Setup, artifactDir) {
		// Setup failed — still try teardown but don't run main steps.
		runPhase(browserCtx, spec, opts, res, "teardown", spec.Teardown, artifactDir)
		FinalizeInstrumentation(browserCtx, instr, spec.Capture)
		if p, err := WriteInstrumentation(instr, artifactDir); err == nil {
			res.InstrumentationPath = p
		}
		return
	}

	// Main steps
	mainOK := runPhase(browserCtx, spec, opts, res, "step", spec.Steps, artifactDir)

	// Teardown always runs, regardless of main outcome.
	runPhase(browserCtx, spec, opts, res, "teardown", spec.Teardown, artifactDir)

	// Pull perf metrics + write instrumentation.json after the run.
	FinalizeInstrumentation(browserCtx, instr, spec.Capture)
	if p, err := WriteInstrumentation(instr, artifactDir); err == nil {
		res.InstrumentationPath = p
	}
	// Auto-fail the run if capture.console_errors is on and anything
	// landed in the error bucket — paid SaaS tools bill this as
	// "error tracking integration"; we just treat it as a step
	// failure after the fact.
	if spec.Capture.ConsoleErrors && instr.ConsoleErrorCount() > 0 && res.Err == nil {
		res.Err = fmt.Errorf("captured %d console error(s) during run — see %s",
			instr.ConsoleErrorCount(),
			res.InstrumentationPath)
	}

	// ForceVideo runs flush even on a green path so the user can
	// scrub the full timeline. Per-step flushes on failure already
	// happened inside runPhase, so this is a no-op for failing runs
	// (the ring is empty).
	if opts.ForceVideo && res.frameRing != nil && mainOK {
		if p, ferr := FlushFrames(artifactDir, "video-final", res.frameRing); ferr == nil {
			res.VideoFramesDir = p
		} else if opts.VerboseLog {
			fmt.Fprintf(os.Stderr, "[testkit] flush forced video failed: %v\n", ferr)
		}
	}

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
		// Step-level post-processing. The executor handles the
		// chromedp side; these run here because they need access to
		// the artifact dir + the per-run instrumentation state.
		if err == nil && step.A11y != nil {
			label := fmt.Sprintf("%s-%02d", phase, i)
			if a11yErr := RunA11yAudit(stepCtx, step.A11y, artifactDir, label); a11yErr != nil {
				err = a11yErr
			}
		}
		if err == nil && step.SaveHAR != "" {
			if p, harErr := SaveHAR(res.instr, artifactDir, step.SaveHAR); harErr != nil {
				err = harErr
			} else if opts.VerboseLog {
				fmt.Fprintf(os.Stderr, "  [%s %d] HAR dumped to %s\n", phase, i, p)
			}
		}
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
		// Last-resort general self-heal: the handler-backed path.
		// SelectorReplaceFromSelfHeal above only handles selector
		// failures on Click/Fill steps via the built-in heuristic
		// LLM call. If the registered FixHandler can do better —
		// e.g. an assertion failed because the copy changed and the
		// LLM can propose a new selector targeting the new text —
		// give it one shot. Bounded by the handler's own 60s timeout.
		if err != nil && fixDispatch != nil {
			fixReq := FixRequest{
				Spec:      spec,
				StepIndex: i,
				Phase:     phase,
				Action:    stepDescription(step),
				Error:     err.Error(),
			}
			if fix := AttemptAutonomousFix(stepCtx, fixReq); fix != nil && fix.Strategy == "selector_replace" && fix.SelectorReplace != "" {
				patched := step
				if step.Click != "" {
					patched.Click = fix.SelectorReplace
				} else if step.Fill != nil {
					patched.Fill = &FillStep{Selector: fix.SelectorReplace, Text: step.Fill.Text}
				}
				if retryErr := executeStep(stepCtx, spec, patched); retryErr == nil {
					sr.Description += " (auto-healed via handler)"
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
			// Flush screencast frames next to the failure so the
			// mobile FrameSequencePlayer can scrub through them.
			// Writes to `<phase>-<idx>-frames/` siblingto the screenshot.
			if res.frameRing != nil {
				label := fmt.Sprintf("%s-%02d", phase, i)
				if _, ferr := FlushFrames(artifactDir, label, res.frameRing); ferr != nil && opts.VerboseLog {
					fmt.Fprintf(os.Stderr, "[testkit] flush frames failed: %v\n", ferr)
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

	case step.A11y != nil:
		// Accessibility audit via axe-core. Injects the bundle,
		// runs axe.run, writes the full violation list to the
		// spec's artifacts dir, fails the step if any violation
		// crosses min_impact (default "serious").
		return nil

	case step.SaveHAR != "":
		// HAR dump is handled by the runner (needs access to the
		// per-spec instrumentation state).
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
	case step.A11y != nil:
		return "a11y audit"
	case step.SaveHAR != "":
		return "save_har " + step.SaveHAR
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

// applyNetworkProfile issues a CDP
// Network.emulateNetworkConditions command for the profile name,
// using Chrome DevTools' documented preset values. Empty /
// "online" is a no-op.
func applyNetworkProfile(ctx context.Context, profile string) error {
	p := strings.ToLower(strings.TrimSpace(profile))
	if p == "" || p == "online" || p == "wifi" {
		return nil
	}
	// Latency (ms), download (bytes/s), upload (bytes/s).
	var (
		offline  bool
		latency  float64
		download float64
		upload   float64
	)
	switch p {
	case "offline":
		offline = true
	case "fast-3g":
		latency = 150
		download = 1.6 * 1024 * 1024 / 8
		upload = 768 * 1024 / 8
	case "slow-3g", "3g":
		latency = 400
		download = 500 * 1024 / 8
		upload = 500 * 1024 / 8
	case "2g":
		latency = 800
		download = 250 * 1024 / 8
		upload = 250 * 1024 / 8
	default:
		return fmt.Errorf("unknown network_profile %q (expected online|fast-3g|slow-3g|2g|offline)", profile)
	}
	// Newer cdproto/network dropped the simple
	// EmulateNetworkConditions wrapper in favor of
	// EmulateNetworkConditionsByRule, whose Do() returns
	// ([]string, error) so it isn't a chromedp.Action. We wrap
	// it in an ActionFunc so chromedp.Run can still sequence it.
	conds := []*cdpnetwork.Conditions{{
		URLPattern:         "",
		Latency:            latency,
		DownloadThroughput: download,
		UploadThroughput:   upload,
	}}
	emulate := chromedp.ActionFunc(func(c context.Context) error {
		_, err := cdpnetwork.EmulateNetworkConditionsByRule(offline, conds).Do(c)
		return err
	})
	return chromedp.Run(ctx,
		cdpnetwork.Enable(),
		emulate,
	)
}
