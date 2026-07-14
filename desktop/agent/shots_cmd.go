package main

// shots_cmd.go — `yaver shots` : auto-generate App Store screenshots on a
// simulator and (optionally) stage + submit the app for review. The lazy
// one-command path: capture → normalize → upload → metadata → submit.
//
// Façade style mirrors publish_ship.go. The binary itself still flows
// through /deploy/ship (via --ship); shots only adds the screenshot +
// metadata + submit phases that ship deliberately omits — no third engine.
//
//	yaver shots                       # iPhone screenshots for the cwd's app
//	yaver shots --target visionpro    # Apple Vision Pro (3840x2160)
//	yaver shots --submit              # + set metadata + attempt submit-for-review
//	yaver shots --ship --submit       # also build+upload a fresh binary first
//
// Which simulator to boot, how to build for it, how to drive it, the exact
// pixel size and the App Store Connect display type all come from ONE row in
// shotsTargets (shots_capture.go) — the target is the only device knob.
//
// App Store Connect auth: the vault first, then the APP_STORE_KEY_ID / _ISSUER /
// _PATH env triple that `yaver vault env --project mobile` exports.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ShotsPlan is a resolved shots run. Reused by the CLI and the publishJobs
// worker (Part C) so mobile-triggered runs share one code path.
type ShotsPlan struct {
	App      string
	Path     string
	Stack    string
	BundleID string
	Locale   string
	Target   string // shotsTargets key: iphone (default) | visionpro
	Device   string // sim name; "" = auto-pick for the target
	Shots    int    // simctl driver: how many frames to capture (default 1)
	Submit   bool
	Ship     bool
	Version  string // version string for submit when none is editable yet
	Machine  string // for --ship remote targeting
	Timeout  int
}

func runShots(args []string) {
	fs := flag.NewFlagSet("shots", flag.ExitOnError)
	app := fs.String("app", "", "App/project name (default: project directory name)")
	path := fs.String("path", "", "Project path (default: current directory)")
	stack := fs.String("stack", "react-native-expo", "Project stack")
	bundleID := fs.String("bundle-id", "", "iOS bundle identifier (default: from app.json expo.ios.bundleIdentifier)")
	locale := fs.String("locale", "en-US", "App Store localization locale")
	target := fs.String("target", "iphone", "Capture target: iphone | visionpro")
	device := fs.String("device", "", "Simulator name (default: auto-pick for the target)")
	shots := fs.Int("shots", 1, "simctl-driven targets (visionpro): how many frames to capture")
	submit := fs.Bool("submit", false, "Set metadata + attempt submit-for-review after upload")
	ship := fs.Bool("ship", false, "Also build + upload a fresh binary via /deploy/ship first")
	version := fs.String("version", "", "Version string to create if no editable App Store version exists")
	machine := fs.String("machine", "", "With --ship: build on a remote Mac you own (deviceId)")
	timeout := fs.Int("timeout", 0, "With --ship: timeout seconds (0 = server default)")
	fs.Parse(args)

	resolvedPath := strings.TrimSpace(*path)
	if resolvedPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			resolvedPath = cwd
		}
	}
	if abs, err := filepath.Abs(resolvedPath); err == nil {
		resolvedPath = abs
	}

	resolvedApp := strings.TrimSpace(*app)
	if resolvedApp == "" {
		resolvedApp = filepath.Base(resolvedPath)
	}

	resolvedBundle := strings.TrimSpace(*bundleID)
	if resolvedBundle == "" {
		resolvedBundle = readBundleIDFromAppJSON(resolvedPath)
	}
	if resolvedBundle == "" {
		fmt.Fprintln(os.Stderr, "Could not determine the iOS bundle id. Pass --bundle-id, or run from a project with app.json (expo.ios.bundleIdentifier).")
		os.Exit(1)
	}

	plan := ShotsPlan{
		App:      resolvedApp,
		Path:     resolvedPath,
		Stack:    *stack,
		BundleID: resolvedBundle,
		Locale:   *locale,
		Target:   *target,
		Device:   *device,
		Shots:    *shots,
		Submit:   *submit,
		Ship:     *ship,
		Version:  *version,
		Machine:  *machine,
		Timeout:  *timeout,
	}
	os.Exit(plan.Run())
}

// step prints a phase header.
func shotsStep(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "→ "+format+"\n", a...)
}
func shotsOK(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "  ✓ "+format+"\n", a...)
}
func shotsFail(format string, a ...interface{}) int {
	fmt.Fprintf(os.Stderr, "  ✗ "+format+"\n", a...)
	return 1
}

// Run executes the full shots pipeline and returns a process exit code.
func (p ShotsPlan) Run() int {
	target, err := resolveShotsTarget(p.Target)
	if err != nil {
		return shotsFail("%v", err)
	}
	fmt.Fprintf(os.Stderr, "yaver shots — %s (%s) → %s %dx%d\n",
		p.App, p.BundleID, target.Label, target.Width, target.Height)

	// 0. Optional: build + upload a fresh binary via the existing ship spine.
	if p.Ship {
		shotsStep("Building + uploading binary via /deploy/ship (testflight)…")
		cfg, err := LoadConfig()
		if err != nil || cfg.AuthToken == "" {
			return shotsFail("not authenticated — run 'yaver auth' first (needed for --ship)")
		}
		if code := shipToAgent(cfg, p.App, []string{"testflight"}, p.Stack, p.Path, p.Timeout, p.Machine); code != 0 {
			return shotsFail("binary ship failed (exit %d)", code)
		}
		shotsOK("binary uploaded")
	}

	// 1. Resolve + boot the simulator for this target.
	device := p.Device
	var udid string
	if device == "" {
		device, udid, err = pickSimForTarget(target)
	} else {
		_, udid, err = resolveSimUDIDByName(device)
	}
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsStep("Booting simulator: %s", device)
	if err := bootSimulator(udid); err != nil {
		return shotsFail("%v", err)
	}
	return p.captureAndPublish(udid, device, target)
}

// captureAndPublish runs the install→drive→capture→ASC phases on a booted udid.
func (p ShotsPlan) captureAndPublish(udid, device string, target shotsDeviceTarget) int {
	// 2. Resolve / build the simulator .app and install it.
	shotsStep("Resolving %s simulator build…", target.Label)
	appPath, err := resolveSimAppPath(p.Path, device, target)
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsOK("app: %s", appPath)
	shotsStep("Installing on simulator…")
	if err := installAppOnSim(udid, appPath); err != nil {
		return shotsFail("%v", err)
	}

	raw, upload, root, err := shotsRunDir(p.App)
	if err != nil {
		return shotsFail("%v", err)
	}

	// 3. Drive the app + capture, with the target's driver.
	if err := p.capture(udid, raw, target); err != nil {
		return shotsFail("%v", err)
	}
	files, err := normalizeScreenshots(raw, upload, target)
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsOK("%d screenshots (%dx%d) → %s", len(files), target.Width, target.Height, upload)

	// 4. Upload to App Store Connect, into this target's display type(s).
	shotsStep("Uploading screenshots to App Store Connect (%s)…", strings.Join(target.DisplayTypes, ", "))
	if err := ascUploadScreenshots(p.BundleID, upload, p.Locale, target.uploadPlan()); err != nil {
		return shotsFail("%v", err)
	}
	shotsOK("screenshots uploaded")

	if !p.Submit {
		fmt.Fprintf(os.Stderr, "\nDone. Screenshots are in App Store Connect (run dir: %s).\n", root)
		fmt.Fprintf(os.Stderr, "Re-run with --submit to set metadata + submit for review.\n")
		return 0
	}

	// 5. Metadata + age rating + submit.
	shotsStep("Setting App Store metadata…")
	if err := ascSetMetadata(p.BundleID, p.Path, target.Platform); err != nil {
		// Metadata is best-effort — keep going to the submit attempt.
		shotsFail("metadata: %v (continuing)", err)
	} else {
		shotsOK("metadata set")
	}

	shotsStep("Submitting for review…")
	submitted, err := ascSubmitForReview(p.BundleID, p.Version, target.Platform)
	if err != nil {
		return shotsFail("%v", err)
	}
	if submitted {
		shotsOK("submitted for App Store review 🎉")
	} else {
		shotsOK("staged — finish the one gated item printed above and tap Submit")
	}
	return 0
}

// capture drives the app and collects raw PNGs, using the target's driver.
func (p ShotsPlan) capture(udid, raw string, target shotsDeviceTarget) error {
	switch target.Driver {
	case shotsDriverSimctl:
		// visionOS: Maestro cannot drive it, so launch the app and shoot the
		// screen. The frames are natively the exact store size.
		n := p.Shots
		if n < 1 {
			n = 1
		}
		shotsStep("Launching app + capturing %d frame(s) via simctl…", n)
		return captureViaSimctl(udid, p.BundleID, raw, n, 3*time.Second)

	case shotsDriverMaestro:
		// Committed flow wins, else generate a draft from the app's routes.
		flow := findShotsFlow(p.Path)
		if flow == "" {
			analysis, err := AnalyzeExpoRouter(p.Path)
			if err != nil {
				return fmt.Errorf("analyze routes: %w", err)
			}
			flow, err = generateShotsFlow(p.Path, p.BundleID, analysis)
			if err != nil {
				return fmt.Errorf("generate flow: %w", err)
			}
			shotsOK("generated draft flow (%d visible tabs): %s", analysis.VisibleTabs, flow)
			fmt.Fprintf(os.Stderr, "    (edit + copy to .yaver/shots.flow.yaml for reliable captures)\n")
		} else {
			shotsOK("using committed flow: %s", flow)
		}
		shotsStep("Driving app with Maestro + capturing…")
		return runShotsFlow(udid, flow, raw)

	default:
		return fmt.Errorf("target %q has no capture driver", target.Key)
	}
}
