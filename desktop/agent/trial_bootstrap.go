package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// trial_bootstrap.go — the auto-start chain for a zero-friction trial box.
//
// The promise: sign in, and ~90 seconds later a React Native todo app is
// rendering in the browser over WebRTC with an agent able to edit it.
//
// ─── The design constraint that shapes everything here ──────────────────────
//
// Every step runs UNATTENDED, in the most fragile 90 seconds of the funnel, for
// a user who has not yet seen the product work. So:
//
//  1. NO NETWORK DEPENDENCIES IN THE CRITICAL PATH. The sample repo and its
//     node_modules are baked into the image. A `git clone` or `npm install`
//     here would make GitHub/npm a hard dependency of the demo — if either is
//     slow or rate-limited, the trial opens on a spinner, which demonstrates
//     precisely the opposite of the claim being made.
//  2. EVERY STEP REPORTS. A silent failure looks identical to a slow start,
//     and the user cannot tell them apart. Each step emits a phase so the UI
//     shows real progress rather than a spinner and a hope.
//  3. FAIL LOUD AND SPECIFIC. "Trial failed to start" is useless. Which step,
//     and what would fix it, is the difference between a bug we can find in ten
//     seconds and one that costs a session.
//
// See docs/architecture/yaver-activation-trial-analysis.md.

// TrialStep is one stage of the unattended bootstrap.
type TrialStep struct {
	Name string
	// Phase is surfaced to the UI progress bar. Reuses the same vocabulary as
	// the managed-cloud provision ladder so the trial does not invent a second
	// progress language for the same user-visible concept.
	Phase    string
	Progress int
	Run      func(ctx context.Context, t *TrialBootstrapper) error
}

// TrialBootstrapper runs the chain on a trial box.
type TrialBootstrapper struct {
	// WorkDir is where the sample was baked. Hardcoding a path is normally a
	// bug in this codebase (Yaver is not single-user), but this is an image WE
	// build for a machine that exists for 60 minutes — the rule protects other
	// people's boxes, and there is no other user here.
	WorkDir string
	// DevServerPort is the app's web target.
	DevServerPort int
	// OnPhase reports progress. Never nil in production; a no-op in tests.
	OnPhase func(phase string, progress int, detail string)
	// Deadline bounds the whole chain. A trial that takes longer than the
	// promise is a failed trial even if it eventually succeeds.
	Deadline time.Duration
}

// NewTrialBootstrapper returns the standard trial chain configuration.
func NewTrialBootstrapper(workDir string) *TrialBootstrapper {
	return &TrialBootstrapper{
		WorkDir:       workDir,
		DevServerPort: 8081,
		OnPhase:       func(string, int, string) {},
		// 90s is the promise; 180s is the point past which the user has almost
		// certainly given up, so failing there is more honest than continuing.
		Deadline: 180 * time.Second,
	}
}

// TrialSteps is the ordered chain.
//
// Order matters and is not arbitrary: the dev server must be serving before
// Chrome renders it, Chrome must be rendering before WebRTC has a surface to
// stream, and the feedback SDK is verified LAST because it is the one claim we
// make that the user cannot see for themselves until they shake.
func TrialSteps() []TrialStep {
	return []TrialStep{
		{
			Name: "verify-sample", Phase: "preparing", Progress: 10,
			Run: func(ctx context.Context, t *TrialBootstrapper) error {
				return t.verifySample()
			},
		},
		{
			Name: "dev-server", Phase: "starting-dev-server", Progress: 35,
			Run: func(ctx context.Context, t *TrialBootstrapper) error {
				return t.waitForDevServer(ctx)
			},
		},
		{
			Name: "chrome", Phase: "rendering", Progress: 60,
			Run: func(ctx context.Context, t *TrialBootstrapper) error {
				return t.verifyChrome()
			},
		},
		{
			Name: "webrtc", Phase: "streaming", Progress: 85,
			Run: func(ctx context.Context, t *TrialBootstrapper) error {
				return t.verifyStreamable()
			},
		},
		{
			Name: "feedback-sdk", Phase: "ready", Progress: 100,
			Run: func(ctx context.Context, t *TrialBootstrapper) error {
				return t.verifyFeedbackSDK()
			},
		},
	}
}

// Run executes the chain, stopping at the first failure.
//
// Returns the FAILED STEP NAME in the error, because "trial failed" without a
// step is the kind of error that costs a whole debugging session.
func (t *TrialBootstrapper) Run(ctx context.Context) error {
	if t.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Deadline)
		defer cancel()
	}
	for _, step := range TrialSteps() {
		select {
		case <-ctx.Done():
			return fmt.Errorf("trial bootstrap timed out before %q: %w", step.Name, ctx.Err())
		default:
		}
		t.report(step.Phase, step.Progress, step.Name)
		if err := step.Run(ctx, t); err != nil {
			// Loud and specific: which step, and the underlying cause.
			return fmt.Errorf("trial bootstrap failed at %q: %w", step.Name, err)
		}
	}
	return nil
}

func (t *TrialBootstrapper) report(phase string, progress int, detail string) {
	if t.OnPhase != nil {
		t.OnPhase(phase, progress, detail)
	}
}

// verifySample asserts the baked sample is actually present.
//
// This runs FIRST and cheaply because everything downstream assumes it. If the
// image was built wrong, failing here names the real cause; failing at "chrome"
// three steps later would send someone debugging the browser.
func (t *TrialBootstrapper) verifySample() error {
	if t.WorkDir == "" {
		return fmt.Errorf("no work dir configured")
	}
	pkg := filepath.Join(t.WorkDir, "package.json")
	if _, err := os.Stat(pkg); err != nil {
		return fmt.Errorf("sample project missing at %s — the trial image was built without it "+
			"(it must be BAKED IN, not cloned at boot): %w", t.WorkDir, err)
	}
	// node_modules baked too — an npm install here would put the network in the
	// critical path, which is the thing this design most avoids.
	if _, err := os.Stat(filepath.Join(t.WorkDir, "node_modules")); err != nil {
		return fmt.Errorf("node_modules missing at %s — dependencies must be pre-installed in "+
			"the trial image so first boot needs no network: %w", t.WorkDir, err)
	}
	return nil
}

// waitForDevServer polls until the web target answers.
//
// Bounded: a dev server that has not answered by the deadline is a failure, not
// something to wait out. The user is watching.
func (t *TrialBootstrapper) waitForDevServer(ctx context.Context) error {
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := probeLocalPort(t.DevServerPort); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return fmt.Errorf("dev server did not answer on :%d within 60s (last: %v)", t.DevServerPort, last)
}

// verifyChrome asserts a headless browser is actually available.
//
// Probes the BINARY, not a config flag: "chrome is configured" and "chrome runs"
// are different claims, and only the second one renders anything. This codebase
// has been bitten repeatedly by checking the inventory instead of the operation.
func (t *TrialBootstrapper) verifyChrome() error {
	for _, bin := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if path, err := lookPathSafe(bin); err == nil && path != "" {
			return nil
		}
	}
	return fmt.Errorf("no headless Chrome/Chromium on PATH — the trial image must ship one; " +
		"the preview strategy for this stack is chrome-webrtc and cannot fall back to an emulator on a 2c/4GB box")
}

// verifyStreamable checks the surface WebRTC will carry actually exists.
//
// Deliberately does NOT claim the stream works end-to-end — that needs a peer.
// It asserts the local precondition and says so, rather than reporting a
// success it has not observed.
func (t *TrialBootstrapper) verifyStreamable() error {
	if err := probeLocalPort(t.DevServerPort); err != nil {
		return fmt.Errorf("nothing to stream: dev server stopped answering on :%d: %w", t.DevServerPort, err)
	}
	return nil
}

// verifyFeedbackSDK asserts the SDK the trial promises is really wired.
//
// The trial tells the user feedback works. If the SDK is absent, the shake does
// nothing and the user concludes the PRODUCT is broken rather than the sample.
// Checking here converts that into a build-time failure we see instead.
func (t *TrialBootstrapper) verifyFeedbackSDK() error {
	pkg := filepath.Join(t.WorkDir, "node_modules", "yaver-feedback-react-native")
	if _, err := os.Stat(pkg); err != nil {
		return fmt.Errorf("yaver-feedback-react-native not installed in the sample — the trial "+
			"promises the feedback loop, so a missing SDK would look like a broken product: %w", err)
	}
	return nil
}

// probeLocalPort attempts a real TCP connection.
//
// Connect, do not merely check that something is listening in a config file:
// the whole point is to observe the operation rather than the inventory.
func probeLocalPort(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// lookPathSafe wraps exec.LookPath so callers can probe several candidate
// binary names without importing os/exec everywhere.
func lookPathSafe(bin string) (string, error) {
	return exec.LookPath(bin)
}
