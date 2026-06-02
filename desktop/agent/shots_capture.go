package main

// shots_capture.go — Engine 1: capture App Store screenshots natively on
// an iOS simulator. Boot a high-res iPhone, install the app, drive it with
// a Maestro flow (generated or committed), collect the PNGs Maestro writes,
// and normalize them to the App Store 6.7"/6.9" size.
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
// 1290x2796 as a safety stamp.
const (
	shotsTargetW = 1290
	shotsTargetH = 2796
)

// simDevice is one row from `xcrun simctl list devices --json`.
type simDevice struct {
	Name        string `json:"name"`
	UDID        string `json:"udid"`
	State       string `json:"state"`
	IsAvailable bool   `json:"isAvailable"`
	runtime     string // filled in from the map key
}

// pickHighResSim parses the simulator list and returns the best available
// iPhone for App Store shots — preferring a "Pro Max" (6.7"/6.9"), highest
// model number. Returns (name, udid).
func pickHighResSim() (string, string, error) {
	out, err := runCmd("xcrun", "simctl", "list", "devices", "--json")
	if err != nil {
		return "", "", fmt.Errorf("simctl list: %w", err)
	}
	var parsed struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return "", "", fmt.Errorf("parse simctl json: %w", err)
	}
	var candidates []simDevice
	for rt, devs := range parsed.Devices {
		if !strings.Contains(rt, "iOS") {
			continue
		}
		for _, d := range devs {
			if !d.IsAvailable || !strings.Contains(d.Name, "iPhone") {
				continue
			}
			d.runtime = rt
			candidates = append(candidates, d)
		}
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no available iPhone simulators (install one in Xcode)")
	}
	// Rank: Pro Max first, then already-booted, then lexically (so higher
	// model names like "iPhone 17" sort above "iPhone 16").
	sort.SliceStable(candidates, func(i, j int) bool {
		ci, cj := candidates[i], candidates[j]
		pmi, pmj := strings.Contains(ci.Name, "Pro Max"), strings.Contains(cj.Name, "Pro Max")
		if pmi != pmj {
			return pmi
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

// resolveSimUDIDByName finds an available simulator by (case-insensitive)
// exact name and returns (name, udid).
func resolveSimUDIDByName(name string) (string, string, error) {
	out, err := runCmd("xcrun", "simctl", "list", "devices", "--json")
	if err != nil {
		return "", "", fmt.Errorf("simctl list: %w", err)
	}
	var parsed struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return "", "", fmt.Errorf("parse simctl json: %w", err)
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, devs := range parsed.Devices {
		for _, d := range devs {
			if d.IsAvailable && strings.ToLower(d.Name) == want {
				return d.Name, d.UDID, nil
			}
		}
	}
	return "", "", fmt.Errorf("no available simulator named %q (see `xcrun simctl list devices`)", name)
}

// bootSimulator boots the udid (idempotent) and waits for it to be ready,
// then opens the Simulator UI so Maestro can drive it.
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

// resolveSimAppPath finds (or builds) a simulator .app for the project at
// appDir. If a prebuilt sim app is present under ios/build it is reused;
// otherwise xcodebuild produces one. Errors helpfully if there is no ios/
// directory (Expo projects need `expo prebuild -p ios` first).
func resolveSimAppPath(appDir, deviceName string) (string, error) {
	// Already-built sim app?
	if p := findSimAppInDerivedData(appDir); p != "" {
		return p, nil
	}
	iosDir := filepath.Join(appDir, "ios")
	if st, err := os.Stat(iosDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("no ios/ project at %s — run `npx expo prebuild -p ios` first (or pass --ship to build a fresh binary)", appDir)
	}
	return buildSimApp(appDir, deviceName)
}

// findSimAppInDerivedData looks for a simulator .app under the common
// xcodebuild output locations (the -iphonesimulator variants).
func findSimAppInDerivedData(appDir string) string {
	patterns := []string{
		"ios/build/Build/Products/Debug-iphonesimulator/*.app",
		"ios/build/Build/Products/Release-iphonesimulator/*.app",
		"build/Build/Products/Debug-iphonesimulator/*.app",
	}
	return detectArtifact(appDir, patterns)
}

// buildSimApp compiles a simulator .app via xcodebuild and returns its path.
func buildSimApp(appDir, deviceName string) (string, error) {
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
		"-destination", fmt.Sprintf("platform=iOS Simulator,name=%s", deviceName),
		"-derivedDataPath", derived,
		"build",
		"CODE_SIGNING_ALLOWED=NO",
	)
	cmd := exec.Command("xcodebuild", args...)
	cmd.Dir = iosDir
	cmd.Stdout = os.Stderr // build logs to stderr, keep stdout clean
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("xcodebuild (sim): %w", err)
	}
	app := detectArtifact(appDir, []string{"ios/build/Build/Products/Debug-iphonesimulator/*.app"})
	if app == "" {
		return "", fmt.Errorf("build succeeded but no .app found under %s", derived)
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

// normalizeScreenshots copies every PNG from rawDir into outDir (sorted by
// name so 01_/02_ ordering holds) and resamples each to the App Store size
// with sips. Returns the ordered list of normalized file paths.
func normalizeScreenshots(rawDir, outDir string) ([]string, error) {
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
			fmt.Sprint(shotsTargetH), fmt.Sprint(shotsTargetW), dst); err != nil {
			return nil, fmt.Errorf("sips resample %s: %s — %w", name, out, err)
		}
		outPaths = append(outPaths, dst)
	}
	if len(outPaths) == 0 {
		return nil, fmt.Errorf("no screenshots captured in %s — the Maestro flow may not have reached any takeScreenshot step", rawDir)
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
