package testkit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Android Emulator driver — wraps `emulator` and `adb`. Works on macOS
// and Linux. The dev needs the Android SDK already installed (Yaver
// won't auto-download it; that's not in scope for a CI runner).

// AndroidEmuDriver is the lifecycle wrapper. Mirror of IOSSimDriver.
type AndroidEmuDriver struct {
	AVD      string // emulator AVD name (e.g. "Pixel_7_API_34")
	APKPath  string // path to .apk to install
	Package  string // package name to launch (e.g. "io.yaver.mobile")
	Activity string // optional explicit launcher activity
}

// Available returns nil if both `adb` and `emulator` are on PATH.
func (d *AndroidEmuDriver) Available() error {
	if _, err := os.Stat(resolveTestkitCommandPath("adb")); err != nil {
		return fmt.Errorf("adb not found — install Android SDK platform-tools")
	}
	if _, err := os.Stat(resolveTestkitCommandPath("emulator")); err != nil {
		return fmt.Errorf("emulator not found — install Android SDK emulator package")
	}
	return nil
}

// Boot starts the AVD and waits for it to come online. Returns the
// adb device id once boot is complete.
func (d *AndroidEmuDriver) Boot(ctx context.Context) (string, error) {
	if err := d.Available(); err != nil {
		return "", err
	}
	if deviceID := firstOnlineEmulator(ctx); deviceID != "" {
		if err := waitForBootComplete(ctx, deviceID, 30*time.Second); err != nil {
			return deviceID, err
		}
		return deviceID, nil
	}
	if d.AVD == "" {
		// Auto-pick the first AVD if the user didn't name one.
		out, _ := runCtx(ctx, "emulator", "-list-avds")
		first := strings.SplitN(strings.TrimSpace(out), "\n", 2)
		if first[0] == "" {
			return "", fmt.Errorf("no AVDs configured — run `avdmanager create avd ...`")
		}
		d.AVD = first[0]
	}

	// Spawn the emulator in the background and wait for adb to see it.
	cmd := exec.CommandContext(ctx, resolveTestkitCommandPath("emulator"), "-avd", d.AVD, "-no-snapshot-save", "-no-window")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("emulator start: %w", err)
	}
	// Don't reap — we want the emulator to outlive this Boot() call.
	go func() { _ = cmd.Wait() }()

	deviceID, err := waitForAdbDevice(ctx, 120*time.Second)
	if err != nil {
		return "", err
	}
	if err := waitForBootComplete(ctx, deviceID, 120*time.Second); err != nil {
		return deviceID, err
	}
	return deviceID, nil
}

func firstOnlineEmulator(ctx context.Context) string {
	out, _ := runCtx(ctx, "adb", "devices")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" && strings.HasPrefix(fields[0], "emulator-") {
			return fields[0]
		}
	}
	return ""
}

// Install installs the APK onto the booted device.
func (d *AndroidEmuDriver) Install(ctx context.Context, deviceID string) error {
	if d.APKPath == "" {
		return fmt.Errorf("install: APKPath is empty")
	}
	if _, err := runCtx(ctx, "adb", "-s", deviceID, "install", "-r", d.APKPath); err != nil {
		return fmt.Errorf("adb install: %w", err)
	}
	return nil
}

// Launch starts the app via `monkey` (the simplest way to launch
// without knowing the activity name). Falls back to explicit
// activity if d.Activity is set.
func (d *AndroidEmuDriver) Launch(ctx context.Context, deviceID string) error {
	if d.Package == "" {
		return fmt.Errorf("launch: Package is empty")
	}
	if d.Activity != "" {
		_, err := runCtx(ctx, "adb", "-s", deviceID, "shell", "am", "start", "-n", d.Package+"/"+d.Activity)
		return err
	}
	_, err := runCtx(ctx, "adb", "-s", deviceID, "shell", "monkey", "-p", d.Package, "-c", "android.intent.category.LAUNCHER", "1")
	return err
}

// Screenshot captures a PNG to outPath via `adb exec-out screencap`.
func (d *AndroidEmuDriver) Screenshot(ctx context.Context, deviceID, outPath string) error {
	cmd := exec.CommandContext(ctx, resolveTestkitCommandPath("adb"), "-s", deviceID, "exec-out", "screencap", "-p")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("adb screencap: %w", err)
	}
	return writeFile(outPath, out)
}

// Shutdown stops the emulator. Best-effort.
func (d *AndroidEmuDriver) Shutdown(ctx context.Context, deviceID string) error {
	_, _ = runCtx(ctx, "adb", "-s", deviceID, "emu", "kill")
	return nil
}

// waitForAdbDevice polls `adb devices` until at least one online
// device shows up or timeout. Returns the first online device id.
func waitForAdbDevice(ctx context.Context, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		out, _ := runCtx(ctx, "adb", "devices")
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "List of devices") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == "device" {
				return fields[0], nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return "", fmt.Errorf("no adb device online after %s", timeout)
}

// waitForBootComplete polls the device until `getprop sys.boot_completed`
// returns 1 (Android's "boot animation finished" signal).
func waitForBootComplete(ctx context.Context, deviceID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		out, _ := runCtx(ctx, "adb", "-s", deviceID, "shell", "getprop", "sys.boot_completed")
		if strings.TrimSpace(out) == "1" {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("device %s did not finish booting in %s", deviceID, timeout)
}

func writeFile(path string, data []byte) error {
	return writeFileImpl(path, data)
}

// Tap sends a tap event at (x, y) using `adb shell input tap`. Used
// by the runner for `target: android-emu` specs once we add coordinate
// resolution from selectors. Solo dev typically records taps via
// `yaver test record` against an android-emu target.
func (d *AndroidEmuDriver) Tap(ctx context.Context, deviceID string, x, y int) error {
	_, err := runCtx(ctx, "adb", "-s", deviceID, "shell", "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	return err
}

// Text sends keystrokes via `adb shell input text`. Spaces in text get
// converted to %s per the adb input convention.
func (d *AndroidEmuDriver) Text(ctx context.Context, deviceID, text string) error {
	escaped := ""
	for _, r := range text {
		if r == ' ' {
			escaped += "%s"
		} else {
			escaped += string(r)
		}
	}
	_, err := runCtx(ctx, "adb", "-s", deviceID, "shell", "input", "text", escaped)
	return err
}

// KeyEvent sends a hardware key (e.g. KEYCODE_BACK = 4, KEYCODE_HOME = 3).
func (d *AndroidEmuDriver) KeyEvent(ctx context.Context, deviceID string, keycode int) error {
	_, err := runCtx(ctx, "adb", "-s", deviceID, "shell", "input", "keyevent", fmt.Sprintf("%d", keycode))
	return err
}

// Swipe drags from (x1,y1) to (x2,y2) over durationMs milliseconds.
// Used by the remote-runtime web viewer for pointer drags. adb's
// `input swipe` accepts the duration as a fifth positional arg in
// every supported Android version; <=0 falls back to its default
// (~250 ms).
func (d *AndroidEmuDriver) Swipe(ctx context.Context, deviceID string, x1, y1, x2, y2, durationMs int) error {
	args := []string{"-s", deviceID, "shell", "input", "swipe",
		fmt.Sprintf("%d", x1), fmt.Sprintf("%d", y1),
		fmt.Sprintf("%d", x2), fmt.Sprintf("%d", y2)}
	if durationMs > 0 {
		args = append(args, fmt.Sprintf("%d", durationMs))
	}
	_, err := runCtx(ctx, "adb", args...)
	return err
}
