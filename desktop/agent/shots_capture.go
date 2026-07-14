package main

// shots_capture.go — Engine 1: capture App Store screenshots natively on a
// simulator. Boot the device class Apple wants screenshots for, install the app,
// drive it, collect the PNGs, and normalize them to the exact store size.
//
// Everything device-specific lives in ONE table — shotsTargets. A target says
// which simulator to boot, how to build for it, how to drive it, the exact pixel
// size Apple demands, and which App Store Connect display type(s) the resulting
// PNGs belong to. Adding a device class = adding a row, not a branch.
//
// Two capture drivers, because the platforms genuinely differ:
//
//	maestro — drive the app through a flow and collect its takeScreenshot PNGs.
//	          iOS simulators only.
//	simctl  — launch the app and shoot the screen with `simctl io … screenshot`.
//	          This is the visionOS path: Maestro has NO visionOS support, and
//	          pretending otherwise would just fail at runtime. A Vision Pro
//	          simulator screenshot is natively 3840x2160 — exactly the size Apple
//	          requires — so launch-and-shoot is a real capture, not a downgrade.
//
// Reuses the simctl patterns from mcp_appdev.go and the Maestro availability
// check from vibe_preview_exercise.go. Does NOT reuse device_install.go's
// installAppOnDevice — that is devicectl (physical device) only.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// App Store 6.7"/6.9" portrait pixel size. simctl shots from a Pro Max are
// already this (or 6.9" 1320x2868); we resample to the universally accepted
// 1290x2796 as a safety stamp. Named constants because the on-device upload path
// (shots_http.go) resizes phone frames to the same size.
const (
	shotsTargetW = 1290
	shotsTargetH = 2796
)

// Apple Vision Pro App Store screenshot size. `xcrun simctl io <udid> screenshot`
// on an Apple Vision Pro simulator produces EXACTLY this — verified against a
// booted visionOS 26 simulator — so no upscaling is involved.
const (
	shotsVisionW = 3840
	shotsVisionH = 2160
)

// capture drivers.
const (
	shotsDriverMaestro = "maestro"
	shotsDriverSimctl  = "simctl"
)

// shotsDeviceTarget is one App Store capture target.
type shotsDeviceTarget struct {
	Key   string // CLI selector: iphone | visionpro
	Label string

	// simulator selection
	RuntimeMatch string // substring of the simctl runtime identifier
	NameMatch    string // substring every candidate device name must carry
	PreferName   string // ranked-first substring ("Pro Max"); "" = no preference

	// xcodebuild
	DestPlatform string   // -destination platform=…
	ProductDirs  []string // Build/Products/<dir>/*.app globs, in priority order

	// capture
	Driver string // shotsDriverMaestro | shotsDriverSimctl
	Width  int    // exact App Store pixel size
	Height int

	// App Store Connect
	Platform     string   // ASC platform enum
	DisplayTypes []string // ASC screenshotDisplayType(s) to populate
	PreviewType  string   // ASC previewType for an app preview video
}

// shotsTargets is the whole device matrix. One row per store device class.
var shotsTargets = []shotsDeviceTarget{
	{
		Key:          "iphone",
		Label:        `iPhone 6.7"/6.9"`,
		RuntimeMatch: "iOS",
		NameMatch:    "iPhone",
		PreferName:   "Pro Max",
		DestPlatform: "iOS Simulator",
		ProductDirs: []string{
			"ios/build/Build/Products/Debug-iphonesimulator/*.app",
			"ios/build/Build/Products/Release-iphonesimulator/*.app",
			"build/Build/Products/Debug-iphonesimulator/*.app",
		},
		Driver:       shotsDriverMaestro,
		Width:        shotsTargetW,
		Height:       shotsTargetH,
		Platform:     "IOS",
		DisplayTypes: []string{"APP_IPHONE_67", "APP_IPHONE_65"},
		PreviewType:  "IPHONE_67",
	},
	{
		Key:   "visionpro",
		Label: "Apple Vision Pro",
		// The visionOS runtime identifier is `…SimRuntime.xrOS-26-2` — it says
		// xrOS, NOT visionOS. Matching on "visionOS" finds nothing. (It also does
		// not contain "iOS", so it never collides with the iPhone target.)
		RuntimeMatch: "xrOS",
		NameMatch:    "Apple Vision Pro",
		DestPlatform: "visionOS Simulator",
		ProductDirs: []string{
			"visionos/build/Build/Products/Debug-xrsimulator/*.app",
			"visionos/build/Build/Products/Release-xrsimulator/*.app",
			"ios/build/Build/Products/Debug-xrsimulator/*.app",
			"ios/build/Build/Products/Release-xrsimulator/*.app",
			"build/Build/Products/Debug-xrsimulator/*.app",
		},
		Driver:       shotsDriverSimctl,
		Width:        shotsVisionW,
		Height:       shotsVisionH,
		Platform:     "VISION_OS",
		DisplayTypes: []string{"APP_APPLE_VISION_PRO"},
		PreviewType:  "APP_APPLE_VISION_PRO",
	},
}

// resolveShotsTarget picks a target by key (default: iphone).
func resolveShotsTarget(key string) (shotsDeviceTarget, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		key = "iphone"
	}
	switch key { // friendly aliases for the same row
	case "vision", "visionos", "applevisionpro", "vision-pro", "xros":
		key = "visionpro"
	case "ios", "phone":
		key = "iphone"
	}
	for _, t := range shotsTargets {
		if t.Key == key {
			return t, nil
		}
	}
	keys := make([]string, 0, len(shotsTargets))
	for _, t := range shotsTargets {
		keys = append(keys, t.Key)
	}
	return shotsDeviceTarget{}, fmt.Errorf("unknown shots target %q (have: %s)", key, strings.Join(keys, ", "))
}

// uploadPlan is where this target's screenshots belong in App Store Connect.
func (t shotsDeviceTarget) uploadPlan() ascUploadPlan {
	return ascUploadPlan{Platform: t.Platform, DisplayTypes: t.DisplayTypes}
}

// simDevice is one row from `xcrun simctl list devices --json`.
type simDevice struct {
	Name        string `json:"name"`
	UDID        string `json:"udid"`
	State       string `json:"state"`
	IsAvailable bool   `json:"isAvailable"`
	runtime     string // filled in from the map key
}

// listSimDevices returns every available simulator, tagged with its runtime.
func listSimDevices() ([]simDevice, error) {
	out, err := runCmd("xcrun", "simctl", "list", "devices", "--json")
	if err != nil {
		return nil, fmt.Errorf("simctl list: %w", err)
	}
	var parsed struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("parse simctl json: %w", err)
	}
	var all []simDevice
	for rt, devs := range parsed.Devices {
		for _, d := range devs {
			if !d.IsAvailable {
				continue
			}
			d.runtime = rt
			all = append(all, d)
		}
	}
	return all, nil
}

// pickSimForTarget returns the best available simulator for the target —
// preferring t.PreferName (e.g. "Pro Max"), then an already-booted device, then
// the highest model name. Returns (name, udid).
func pickSimForTarget(t shotsDeviceTarget) (string, string, error) {
	all, err := listSimDevices()
	if err != nil {
		return "", "", err
	}
	var candidates []simDevice
	for _, d := range all {
		if !strings.Contains(d.runtime, t.RuntimeMatch) || !strings.Contains(d.Name, t.NameMatch) {
			continue
		}
		candidates = append(candidates, d)
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no available %s simulator (install a %s runtime in Xcode → Settings → Components)",
			t.Label, t.RuntimeMatch)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ci, cj := candidates[i], candidates[j]
		if t.PreferName != "" {
			pi, pj := strings.Contains(ci.Name, t.PreferName), strings.Contains(cj.Name, t.PreferName)
			if pi != pj {
				return pi
			}
		}
		bi, bj := ci.State == "Booted", cj.State == "Booted"
		if bi != bj {
			return bi
		}
		return ci.Name > cj.Name
	})
	best := candidates[0]
	return best.Name, best.UDID, nil
}

// resolveSimUDIDByName finds an available simulator by (case-insensitive) exact
// name and returns (name, udid).
func resolveSimUDIDByName(name string) (string, string, error) {
	all, err := listSimDevices()
	if err != nil {
		return "", "", err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, d := range all {
		if strings.ToLower(d.Name) == want {
			return d.Name, d.UDID, nil
		}
	}
	return "", "", fmt.Errorf("no available simulator named %q (see `xcrun simctl list devices`)", name)
}

// bootSimulator boots the udid (idempotent) and waits for it to be ready, then
// opens the Simulator UI.
func bootSimulator(udid string) error {
	// `simctl boot` errors if already booted — tolerate that.
	if out, err := runCmd("xcrun", "simctl", "boot", udid); err != nil &&
		!strings.Contains(out, "current state: Booted") &&
		!strings.Contains(strings.ToLower(out+err.Error()), "booted") {
		return fmt.Errorf("simctl boot: %s — %w", out, err)
	}
	_, _ = runCmd("xcrun", "simctl", "bootstatus", udid, "-b")
	_, _ = runCmd("open", "-a", "Simulator")
	return nil
}

// installAppOnSim installs a built .app onto the booted simulator.
func installAppOnSim(udid, appPath string) error {
	if out, err := runCmd("xcrun", "simctl", "install", udid, appPath); err != nil {
		return fmt.Errorf("simctl install: %s — %w", out, err)
	}
	return nil
}

// launchAppOnSim launches an installed app by bundle id on the booted simulator.
func launchAppOnSim(udid, bundleID string) error {
	if out, err := runCmd("xcrun", "simctl", "launch", udid, bundleID); err != nil {
		return fmt.Errorf("simctl launch %s: %s — %w", bundleID, out, err)
	}
	return nil
}

// resolveSimAppPath finds (or builds) a simulator .app for the project at appDir
// for the given target. A prebuilt sim app under the target's product dirs is
// reused; otherwise xcodebuild produces one.
func resolveSimAppPath(appDir, deviceName string, t shotsDeviceTarget) (string, error) {
	if p := detectArtifact(appDir, t.ProductDirs); p != "" {
		return p, nil
	}
	iosDir := filepath.Join(appDir, "ios")
	if st, err := os.Stat(iosDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("no ios/ project at %s — run `npx expo prebuild -p ios` first (or pass --ship to build a fresh binary)", appDir)
	}
	return buildSimApp(appDir, deviceName, t)
}

// buildSimApp compiles a simulator .app via xcodebuild for the target's platform
// and returns its path.
func buildSimApp(appDir, deviceName string, t shotsDeviceTarget) (string, error) {
	iosDir := filepath.Join(appDir, "ios")
	scheme, workspace, project, err := pickXcodeTarget(iosDir)
	if err != nil {
		return "", fmt.Errorf("locate xcode target: %w", err)
	}
	derived := filepath.Join(iosDir, "build")
	args := []string{}
	if workspace != "" {
		args = append(args, "-workspace", workspace)
	} else if project != "" {
		args = append(args, "-project", project)
	}
	args = append(args,
		"-scheme", scheme,
		"-configuration", "Debug",
		"-destination", fmt.Sprintf("platform=%s,name=%s", t.DestPlatform, deviceName),
		"-derivedDataPath", derived,
		"build",
		"CODE_SIGNING_ALLOWED=NO",
	)
	cmd := exec.Command("xcodebuild", args...)
	cmd.Dir = iosDir
	cmd.Stdout = os.Stderr // build logs to stderr, keep stdout clean
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xcodebuild (%s): %w", t.DestPlatform, err)
	}
	app := detectArtifact(appDir, t.ProductDirs)
	if app == "" {
		return "", fmt.Errorf("build succeeded but no %s .app found under %s — the scheme may not support %s",
			t.Label, derived, t.DestPlatform)
	}
	return app, nil
}

// runShotsFlow runs the Maestro flow against the udid, writing takeScreenshot
// PNGs into rawDir (Maestro writes them relative to the process CWD).
func runShotsFlow(udid, flowPath, rawDir string) error {
	if !MaestroAvailable() {
		return fmt.Errorf("maestro not on PATH — install it (https://maestro.mobile.dev) to capture screenshots")
	}
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}
	cmd := exec.Command(maestroPath, "test", "--udid", udid, flowPath)
	cmd.Dir = rawDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("maestro test: %w", err)
	}
	return nil
}

// captureViaSimctl launches the app and shoots the screen `count` times, waiting
// `settle` between shots so an animated or async-loading first frame is not what
// lands in the store. This is the visionOS driver: `simctl io … screenshot` on an
// Apple Vision Pro simulator yields a native 3840x2160 PNG — the required size.
func captureViaSimctl(udid, bundleID, rawDir string, count int, settle time.Duration) error {
	if strings.TrimSpace(bundleID) == "" {
		return fmt.Errorf("bundle id required to launch the app for a simctl capture")
	}
	if count < 1 {
		count = 1
	}
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}
	if err := launchAppOnSim(udid, bundleID); err != nil {
		return err
	}
	for i := 1; i <= count; i++ {
		time.Sleep(settle)
		out := filepath.Join(rawDir, fmt.Sprintf("%02d_launch.png", i))
		if o, err := runCmd("xcrun", "simctl", "io", udid, "screenshot", out); err != nil {
			return fmt.Errorf("simctl screenshot %d: %s — %w", i, o, err)
		}
	}
	return nil
}

// normalizeScreenshots copies every PNG from rawDir into outDir (sorted by name
// so 01_/02_ ordering holds) and resamples each to the target's exact App Store
// size with sips. Returns the ordered list of normalized file paths.
//
// Resampling is a no-op when the capture is already the right size (a Vision Pro
// simulator shot is natively 3840x2160), but it is kept for every target as the
// safety stamp: the store rejects off-by-one dimensions outright.
func normalizeScreenshots(rawDir, outDir string, t shotsDeviceTarget) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var outPaths []string
	for _, name := range names {
		src := filepath.Join(rawDir, name)
		dst := filepath.Join(outDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, err
		}
		// sips arg order is height then width.
		if out, err := runCmd("sips", "--resampleHeightWidth",
			fmt.Sprint(t.Height), fmt.Sprint(t.Width), dst); err != nil {
			return nil, fmt.Errorf("sips resample %s: %s — %w", name, out, err)
		}
		outPaths = append(outPaths, dst)
	}
	if len(outPaths) == 0 {
		return nil, fmt.Errorf("no screenshots captured in %s — the capture step reached no screenshot", rawDir)
	}
	return outPaths, nil
}

// shotsRunDir returns ~/.yaver/shots/<app>/<runID> with raw/ and upload/
// subdirs. runID is timestamp-based.
func shotsRunDir(app string) (raw, upload, root string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}
	runID := time.Now().Format("20060102-150405")
	root = filepath.Join(home, ".yaver", "shots", sanitizeBranchName(app), runID)
	raw = filepath.Join(root, "raw")
	upload = filepath.Join(root, "upload")
	return raw, upload, root, nil
}
