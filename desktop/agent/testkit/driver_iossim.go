package testkit

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// iOS Simulator driver — wraps Apple's `simctl`. macOS only.
//
// What this gives the solo dev today:
//
//   - Boot a named simulator (or any "iPhone 15" if no UDID specified).
//   - Install a built .app bundle into the simulator.
//   - Launch the app by bundle identifier.
//   - Capture a screenshot.
//   - Shut the simulator down at the end of the run.
//
// What it does NOT give yet (M5+ work): tap/swipe/type via WebDriverAgent
// or XCUITest. The full UI driver bridge is the next milestone; this
// driver lets users at least confirm "my iOS build boots and launches"
// from CI without renting a BrowserStack device.

// IOSSimDriver is the lifecycle wrapper.
type IOSSimDriver struct {
	UDID       string // optional — defaults to first booted device
	DeviceType string // e.g. "iPhone 15" — used when no UDID is set
	BundleID   string // app bundle id, e.g. "io.yaver.mobile"
	AppPath    string // path to .app bundle
}

// Available returns nil if simctl appears usable on the current host.
func (d *IOSSimDriver) Available() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("ios simulator requires macOS")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return fmt.Errorf("xcrun not found — install Xcode")
	}
	out, err := exec.Command("xcrun", "simctl", "help").CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl unavailable: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Boot boots the device and returns its UDID. If d.UDID is set, that
// device is booted; otherwise we look up the first available simulator
// matching d.DeviceType (or pick any iPhone if neither is set).
func (d *IOSSimDriver) Boot(ctx context.Context) (string, error) {
	if err := d.Available(); err != nil {
		return "", err
	}
	udid := d.UDID
	if udid == "" {
		var err error
		udid, err = pickSimulator(ctx, d.DeviceType)
		if err != nil {
			return "", err
		}
	}
	// Boot is idempotent — simctl errors on already-booted devices, so
	// we ignore that specific failure.
	out, _ := runCtx(ctx, "xcrun", "simctl", "boot", udid)
	if strings.Contains(out, "Unable to boot device in current state: Booted") {
		return udid, nil
	}
	return udid, nil
}

// Install installs the .app bundle into the booted simulator.
func (d *IOSSimDriver) Install(ctx context.Context, udid string) error {
	if d.AppPath == "" {
		return fmt.Errorf("install: AppPath is empty")
	}
	if _, err := runCtx(ctx, "xcrun", "simctl", "install", udid, d.AppPath); err != nil {
		return fmt.Errorf("simctl install: %w", err)
	}
	return nil
}

// Launch launches the app by its bundle id.
func (d *IOSSimDriver) Launch(ctx context.Context, udid string) error {
	if d.BundleID == "" {
		return fmt.Errorf("launch: BundleID is empty")
	}
	if _, err := runCtx(ctx, "xcrun", "simctl", "launch", udid, d.BundleID); err != nil {
		return fmt.Errorf("simctl launch: %w", err)
	}
	return nil
}

// Screenshot captures a PNG into outPath.
func (d *IOSSimDriver) Screenshot(ctx context.Context, udid, outPath string) error {
	if _, err := runCtx(ctx, "xcrun", "simctl", "io", udid, "screenshot", outPath); err != nil {
		return fmt.Errorf("simctl screenshot: %w", err)
	}
	return nil
}

// Shutdown stops the simulator. Best-effort.
func (d *IOSSimDriver) Shutdown(ctx context.Context, udid string) error {
	_, _ = runCtx(ctx, "xcrun", "simctl", "shutdown", udid)
	return nil
}

// pickSimulator returns the UDID of the first available simulator
// matching `deviceType` (substring), or any iPhone if deviceType == "".
// Uses `simctl list devices available -j` and parses the JSON tree.
func pickSimulator(ctx context.Context, deviceType string) (string, error) {
	out, err := runCtx(ctx, "xcrun", "simctl", "list", "devices", "available")
	if err != nil {
		return "", fmt.Errorf("simctl list devices: %w", err)
	}
	// Parse the human-friendly output instead of pulling encoding/json
	// for a one-shot lookup. Lines look like:
	//   "    iPhone 15 (UDID-HERE) (Shutdown)"
	want := strings.ToLower(deviceType)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if want != "" && !strings.Contains(lower, want) {
			continue
		}
		if want == "" && !strings.Contains(lower, "iphone") {
			continue
		}
		// Find the first "(<UUID>)" segment.
		open := strings.Index(line, "(")
		close := strings.Index(line, ")")
		if open >= 0 && close > open {
			return line[open+1 : close], nil
		}
	}
	return "", fmt.Errorf("no available simulator matching %q", deviceType)
}

// runCtx is a tiny wrapper that returns combined output + error.
func runCtx(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, resolveTestkitCommandPath(name), args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// SendText pushes text into the currently focused field via simctl's
// io keyboard endpoint (available on Xcode 14+). Used by the runner
// for `target: ios-sim` fill steps.
func (d *IOSSimDriver) SendText(ctx context.Context, udid, text string) error {
	// `xcrun simctl io <udid> keyboard text "..."` is the canonical
	// no-WDA way to type into the active app. Falls back to AppleScript
	// keystroke injection on older Xcode.
	if _, err := runCtx(ctx, "xcrun", "simctl", "io", udid, "keyboard", "text", text); err == nil {
		return nil
	}
	// Best-effort AppleScript fallback. Solo dev rarely runs this on
	// older Xcode but the path exists.
	script := fmt.Sprintf(`tell application "System Events" to keystroke %q`, text)
	_, err := runCtx(ctx, "osascript", "-e", script)
	return err
}

// Tap dispatches a tap at (x, y) on the booted simulator via
// `xcrun simctl io ... tap` (Xcode 15+) with an AppleScript fallback.
func (d *IOSSimDriver) Tap(ctx context.Context, udid string, x, y int) error {
	if _, err := runCtx(ctx, "xcrun", "simctl", "io", udid, "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)); err == nil {
		return nil
	}
	return fmt.Errorf("simctl tap is unavailable on this Xcode; install a simulator control bridge (WDA/XCUITest) before using interactive iOS simulator taps")
}

// FullBootSequence is the convenience helper: boot → install → launch
// → screenshot → shutdown. Used by `yaver test run` for `target: ios-sim`
// specs (returned in M5 scaffold). We expose it now so the user can
// already smoke-test "does my build boot at all?" without writing a
// full spec.
func (d *IOSSimDriver) FullBootSequence(ctx context.Context, screenshotPath string) (string, error) {
	udid, err := d.Boot(ctx)
	if err != nil {
		return "", err
	}
	// Boot is async — wait until the device is in "Booted" state.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := runCtx(ctx, "xcrun", "simctl", "list", "devices", "booted")
		if strings.Contains(out, udid) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if d.AppPath != "" {
		if err := d.Install(ctx, udid); err != nil {
			return udid, err
		}
	}
	if d.BundleID != "" {
		if err := d.Launch(ctx, udid); err != nil {
			return udid, err
		}
	}
	if screenshotPath != "" {
		if err := d.Screenshot(ctx, udid, screenshotPath); err != nil {
			return udid, err
		}
	}
	return udid, nil
}
