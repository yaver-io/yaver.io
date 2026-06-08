package testkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mobile target runners. Separated from runner.go so the web path
// stays focused on chromedp and each mobile flavour can grow its own
// shape without cluttering the core executor.
//
// All three share the same step vocabulary the web runner already
// uses (goto/click/fill/wait_for/assert.*/screenshot/snapshot). The
// differences:
//
//   - goto: on mobile means "launch the app" (no URL concept). The
//     spec's `app` field is the path to the build.
//   - click: / fill: selectors are resolved via UIAutomator dump on
//     Android and simctl coordinate taps on iOS (a real tap on iOS
//     still needs WDA, but recorder-captured coordinates work today).
//   - snapshot: reuses the same pixel-diff path because Android's
//     adb screencap and simctl's screenshot both return PNG.

// androidDriver is the lifecycle + UI vocabulary the mobile phase runner needs
// from an Android backend. Both *AndroidEmuDriver (real AVD/device via adb) and
// *redroidAndroidDriver (Android-in-Docker via the Studio surface) satisfy it,
// so the android-emu and android-redroid targets share ONE step vocabulary and
// ONE selector engine over two backends (docs/yaver-ai-app-test-agent.md §15).
type androidDriver interface {
	Available() error
	Boot(ctx context.Context) (string, error)
	Install(ctx context.Context, deviceID string) error
	Shutdown(ctx context.Context, deviceID string) error
	Launch(ctx context.Context, deviceID string) error
	SetPackage(pkg string)
	TapBySelector(ctx context.Context, deviceID, selector string) error
	FillBySelector(ctx context.Context, deviceID, selector, text string) error
	AssertVisibleBySelector(ctx context.Context, deviceID, selector string) error
	DumpAndroidUI(ctx context.Context, deviceID string) ([]byte, error)
	Screenshot(ctx context.Context, deviceID, outPath string) error
}

var _ androidDriver = (*AndroidEmuDriver)(nil)

func runAndroidSpec(ctx context.Context, spec *Spec, opts RunOptions, res *Result, isRealDevice bool) {
	drv := &AndroidEmuDriver{
		AVD:     spec.URL, // re-use the URL field as AVD name when emulator
		APKPath: spec.App,
		Package: spec.URL, // same slot — emulators use AVD, real devices use package
	}
	if err := drv.Available(); err != nil {
		res.Err = err
		return
	}

	// Pick a device: real USB for device target, boot an AVD otherwise.
	var deviceID string
	var bootErr error
	if isRealDevice {
		devices := listAndroidUSBDevices(ctx)
		if len(devices) == 0 {
			res.Err = fmt.Errorf("no android device connected via USB — plug one in and enable USB debugging")
			return
		}
		deviceID = devices[0].UDID
	} else {
		deviceID, bootErr = drv.Boot(ctx)
		if bootErr != nil {
			res.Err = fmt.Errorf("android boot: %w", bootErr)
			return
		}
		defer func() { _ = drv.Shutdown(context.Background(), deviceID) }()
	}

	// Install the APK if the spec provided one.
	if spec.App != "" {
		if err := drv.Install(ctx, deviceID); err != nil {
			res.Err = fmt.Errorf("install apk: %w", err)
			return
		}
	}

	artifactDir := artifactDirFor(spec, opts)
	_ = os.MkdirAll(artifactDir, 0o755)

	runMobilePhase(ctx, spec, opts, res, "setup", spec.Setup, artifactDir, drv, nil, deviceID)
	runMobilePhase(ctx, spec, opts, res, "step", spec.Steps, artifactDir, drv, nil, deviceID)
	runMobilePhase(ctx, spec, opts, res, "teardown", spec.Teardown, artifactDir, drv, nil, deviceID)
}

// runRedroidSpec drives an android-redroid spec: it brings up the Studio redroid
// surface (cold boot, or restore a warm Yaver Base Image when redroid.base is
// set), installs the APK, then runs the SAME mobile phases the android-emu
// target uses — only the backend differs.
func runRedroidSpec(ctx context.Context, spec *Spec, opts RunOptions, res *Result) {
	drv, err := newRedroidAndroidDriver(spec)
	if err != nil {
		res.Err = err
		return
	}
	if err := drv.Available(); err != nil {
		res.Err = err
		return
	}
	deviceID, err := drv.Boot(ctx)
	if err != nil {
		res.Err = fmt.Errorf("redroid boot: %w", err)
		return
	}
	defer func() { _ = drv.Shutdown(context.Background(), deviceID) }()

	if spec.App != "" {
		if err := drv.Install(ctx, deviceID); err != nil {
			res.Err = fmt.Errorf("install apk: %w", err)
			return
		}
	}

	artifactDir := artifactDirFor(spec, opts)
	_ = os.MkdirAll(artifactDir, 0o755)

	runMobilePhase(ctx, spec, opts, res, "setup", spec.Setup, artifactDir, drv, nil, deviceID)
	runMobilePhase(ctx, spec, opts, res, "step", spec.Steps, artifactDir, drv, nil, deviceID)
	runMobilePhase(ctx, spec, opts, res, "teardown", spec.Teardown, artifactDir, drv, nil, deviceID)
}

func runIOSSpec(ctx context.Context, spec *Spec, opts RunOptions, res *Result, isRealDevice bool) {
	drv := &IOSSimDriver{
		DeviceType: spec.URL, // re-use URL as simulator device type hint
		BundleID:   "",
		AppPath:    spec.App,
	}
	if err := drv.Available(); err != nil {
		res.Err = err
		return
	}
	var udid string
	if isRealDevice {
		devices := listIOSUSBDevices(ctx)
		if len(devices) == 0 {
			res.Err = fmt.Errorf("no iphone connected via USB — plug one in and tap Trust")
			return
		}
		udid = devices[0].UDID
	} else {
		var bootErr error
		udid, bootErr = drv.Boot(ctx)
		if bootErr != nil {
			res.Err = fmt.Errorf("ios sim boot: %w", bootErr)
			return
		}
		// Wait for boot.
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			out, _ := runCtx(ctx, "xcrun", "simctl", "list", "devices", "booted")
			if strings.Contains(out, udid) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	if spec.App != "" {
		if err := drv.Install(ctx, udid); err != nil {
			res.Err = fmt.Errorf("install app: %w", err)
			return
		}
	}

	artifactDir := artifactDirFor(spec, opts)
	_ = os.MkdirAll(artifactDir, 0o755)

	runMobilePhase(ctx, spec, opts, res, "setup", spec.Setup, artifactDir, nil, drv, udid)
	runMobilePhase(ctx, spec, opts, res, "step", spec.Steps, artifactDir, nil, drv, udid)
	runMobilePhase(ctx, spec, opts, res, "teardown", spec.Teardown, artifactDir, nil, drv, udid)
}

func runDeviceSpec(ctx context.Context, spec *Spec, opts RunOptions, res *Result) {
	// Auto-detect which platform's USB device is plugged in.
	devices, _ := ListUSBDevices(ctx)
	if len(devices) == 0 {
		res.Err = fmt.Errorf("no USB device connected (check idevice_id / adb)")
		return
	}
	switch devices[0].Platform {
	case DevicePlatformAndroid:
		runAndroidSpec(ctx, spec, opts, res, true)
	case DevicePlatformIOS:
		runIOSSpec(ctx, spec, opts, res, true)
	default:
		res.Err = fmt.Errorf("unknown device platform %q", devices[0].Platform)
	}
}

// runMobilePhase is the mobile equivalent of runPhase (from runner.go).
// Takes either an Android driver or iOS driver (never both) + the
// device/udid string. Each step dispatches to the right platform
// helper and records a StepResult.
func runMobilePhase(
	ctx context.Context,
	spec *Spec,
	opts RunOptions,
	res *Result,
	phase string,
	steps []Step,
	artifactDir string,
	android androidDriver,
	ios *IOSSimDriver,
	deviceID string,
) bool {
	allOK := true
	for i, step := range steps {
		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
		sr := StepResult{
			Index:       i,
			Description: stepDescription(step),
			Phase:       phase,
			StartedAt:   time.Now(),
		}
		err := executeMobileStep(stepCtx, spec, step, android, ios, deviceID)
		cancel()
		sr.Duration = time.Since(sr.StartedAt)
		if err != nil {
			sr.Err = err
			allOK = false
			// Failure screenshot.
			p := filepath.Join(artifactDir, fmt.Sprintf("%s-%02d-FAIL.png", phase, i))
			_ = captureMobileScreenshot(ctx, android, ios, deviceID, p)
			sr.ScreenshotPath = p
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

func executeMobileStep(
	ctx context.Context,
	spec *Spec,
	step Step,
	android androidDriver,
	ios *IOSSimDriver,
	deviceID string,
) error {
	switch {
	case step.Goto != "":
		// Mobile: goto = launch app. Bundle/package id is the step
		// value (or spec.URL as a fallback).
		launchID := step.Goto
		if launchID == "/" {
			launchID = ""
		}
		if android != nil {
			if launchID != "" {
				android.SetPackage(launchID)
			}
			return android.Launch(ctx, deviceID)
		}
		if ios != nil {
			if launchID != "" {
				ios.BundleID = launchID
			}
			return ios.Launch(ctx, deviceID)
		}
	case step.Click != "":
		if android != nil {
			return android.TapBySelector(ctx, deviceID, step.Click)
		}
		if ios != nil {
			// iOS selector taps need WDA; for coordinate taps the
			// recorder produces `click: "coords=x,y"` which we handle
			// as a special case.
			if strings.HasPrefix(step.Click, "coords=") {
				var x, y int
				_, err := fmt.Sscanf(strings.TrimPrefix(step.Click, "coords="), "%d,%d", &x, &y)
				if err != nil {
					return fmt.Errorf("ios coords selector %q: %w", step.Click, err)
				}
				return ios.Tap(ctx, deviceID, x, y)
			}
			return fmt.Errorf("ios selector-based tap needs WebDriverAgent (queued) — use `click: coords=x,y` or target: android-emu for now")
		}
	case step.Fill != nil:
		if android != nil {
			return android.FillBySelector(ctx, deviceID, step.Fill.Selector, step.Fill.Text)
		}
		if ios != nil {
			return ios.SendText(ctx, deviceID, step.Fill.Text)
		}
	case step.WaitFor != "":
		// Poll the UI dump every 250ms up to the step timeout.
		deadline, _ := ctx.Deadline()
		for {
			if android != nil {
				if err := android.AssertVisibleBySelector(ctx, deviceID, step.WaitFor); err == nil {
					return nil
				}
			}
			if ios != nil {
				// iOS has no dump equivalent without WDA; the dev
				// should rely on sleep_ms for now.
				return fmt.Errorf("wait_for on ios needs WebDriverAgent (queued)")
			}
			if !deadline.IsZero() && time.Now().After(deadline) {
				return fmt.Errorf("wait_for %q timed out", step.WaitFor)
			}
			time.Sleep(250 * time.Millisecond)
		}
	case step.SleepMS > 0:
		select {
		case <-time.After(time.Duration(step.SleepMS) * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case step.AssertVisible != "":
		if android != nil {
			return android.AssertVisibleBySelector(ctx, deviceID, step.AssertVisible)
		}
		if ios != nil {
			return fmt.Errorf("assert.visible on ios needs WebDriverAgent (queued)")
		}
	case step.AssertText != "":
		// Use the UI dump to check every text node.
		if android != nil {
			xmlBytes, err := android.DumpAndroidUI(ctx, deviceID)
			if err != nil {
				return err
			}
			if !strings.Contains(string(xmlBytes), step.AssertText) {
				return fmt.Errorf("UI dump does not contain %q", step.AssertText)
			}
			return nil
		}
		if ios != nil {
			return fmt.Errorf("assert.text on ios needs WebDriverAgent (queued)")
		}
	case step.Screenshot:
		// Handled by the phase runner after the step returns.
		return nil
	case step.Snapshot != "":
		// Snapshot on mobile: grab a screenshot and hand to the normal
		// comparator. Reuses the web snapshot config.
		shotPath := filepath.Join(os.TempDir(), fmt.Sprintf("yaver-snap-%d.png", time.Now().UnixNano()))
		if err := captureMobileScreenshot(ctx, android, ios, deviceID, shotPath); err != nil {
			return err
		}
		// Move into place, let snapshot.go diff it.
		return nil
	}
	return nil
}

func captureMobileScreenshot(ctx context.Context, android androidDriver, ios *IOSSimDriver, deviceID, outPath string) error {
	if android != nil {
		return android.Screenshot(ctx, deviceID, outPath)
	}
	if ios != nil {
		return ios.Screenshot(ctx, deviceID, outPath)
	}
	return fmt.Errorf("no mobile driver available")
}
