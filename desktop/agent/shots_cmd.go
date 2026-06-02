package main

// shots_cmd.go — `yaver shots` : auto-generate App Store screenshots on a
// simulator and (optionally) stage + submit the app for review. The lazy
// one-command path: capture → normalize → upload → metadata → submit.
//
// Façade style mirrors publish_ship.go. The binary itself still flows
// through /deploy/ship (via --ship); shots only adds the screenshot +
// metadata + submit phases that ship deliberately omits — no third engine.
//
//	yaver shots                 # capture + upload screenshots for cwd's app
//	yaver shots --submit        # + set metadata + attempt submit-for-review
//	yaver shots --ship --submit # also build+upload a fresh binary first
//
// App Store Connect auth comes from the env (APP_STORE_KEY_ID / _ISSUER /
// _PATH) — the same triple `yaver vault env --project mobile` exports.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ShotsPlan is a resolved shots run. Reused by the CLI and the publishJobs
// worker (Part C) so mobile-triggered runs share one code path.
type ShotsPlan struct {
	App      string
	Path     string
	Stack    string
	BundleID string
	Locale   string
	Device   string // sim name; "" = auto-pick a Pro Max
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
	device := fs.String("device", "", "Simulator name (default: auto-pick a Pro Max)")
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
		Device:   *device,
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
	fmt.Fprintf(os.Stderr, "yaver shots — %s (%s)\n", p.App, p.BundleID)

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

	// 1. Pick + boot a simulator.
	device := p.Device
	if device == "" {
		name, udid, err := pickHighResSim()
		if err != nil {
			return shotsFail("%v", err)
		}
		device = name
		shotsStep("Booting simulator: %s", device)
		if err := bootSimulator(udid); err != nil {
			return shotsFail("%v", err)
		}
		return p.captureAndPublish(udid, device)
	}
	// Explicit device name → resolve its udid via the list.
	_, udid, err := resolveSimUDIDByName(device)
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsStep("Booting simulator: %s", device)
	if err := bootSimulator(udid); err != nil {
		return shotsFail("%v", err)
	}
	return p.captureAndPublish(udid, device)
}

// captureAndPublish runs the install→drive→capture→ASC phases on a booted udid.
func (p ShotsPlan) captureAndPublish(udid, device string) int {
	// 2. Resolve / build the simulator .app and install it.
	shotsStep("Resolving simulator build…")
	appPath, err := resolveSimAppPath(p.Path, device)
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsOK("app: %s", appPath)
	shotsStep("Installing on simulator…")
	if err := installAppOnSim(udid, appPath); err != nil {
		return shotsFail("%v", err)
	}

	// 3. Resolve the Maestro flow — committed override wins, else generate.
	flow := findShotsFlow(p.Path)
	if flow == "" {
		analysis, err := AnalyzeExpoRouter(p.Path)
		if err != nil {
			return shotsFail("analyze routes: %v", err)
		}
		flow, err = generateShotsFlow(p.Path, p.BundleID, analysis)
		if err != nil {
			return shotsFail("generate flow: %v", err)
		}
		shotsOK("generated draft flow (%d visible tabs): %s", analysis.VisibleTabs, flow)
		fmt.Fprintf(os.Stderr, "    (edit + copy to .yaver/shots.flow.yaml for reliable captures)\n")
	} else {
		shotsOK("using committed flow: %s", flow)
	}

	// 4. Drive the app + capture.
	raw, upload, root, err := shotsRunDir(p.App)
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsStep("Driving app with Maestro + capturing…")
	if err := runShotsFlow(udid, flow, raw); err != nil {
		return shotsFail("%v", err)
	}
	files, err := normalizeScreenshots(raw, upload)
	if err != nil {
		return shotsFail("%v", err)
	}
	shotsOK("%d screenshots → %s", len(files), upload)

	// 5. Upload to App Store Connect.
	shotsStep("Uploading screenshots to App Store Connect…")
	if err := ascUploadScreenshots(p.BundleID, upload, p.Locale); err != nil {
		return shotsFail("%v", err)
	}
	shotsOK("screenshots uploaded")

	if !p.Submit {
		fmt.Fprintf(os.Stderr, "\nDone. Screenshots are in App Store Connect (run dir: %s).\n", root)
		fmt.Fprintf(os.Stderr, "Re-run with --submit to set metadata + submit for review.\n")
		return 0
	}

	// 6. Metadata + age rating + submit.
	shotsStep("Setting App Store metadata…")
	if err := ascSetMetadata(p.BundleID, p.Path); err != nil {
		// Metadata is best-effort — keep going to the submit attempt.
		shotsFail("metadata: %v (continuing)", err)
	} else {
		shotsOK("metadata set")
	}

	shotsStep("Submitting for review…")
	submitted, err := ascSubmitForReview(p.BundleID, p.Version)
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
