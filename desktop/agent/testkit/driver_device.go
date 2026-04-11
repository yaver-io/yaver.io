package testkit

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Real device drivers — USB-attached iOS or Android phones the dev
// owns. The point is that a solo developer who already plugs their
// own iPhone into their MacBook every day shouldn't have to rent a
// device cloud just to run automated tests. The simulator/emulator
// drivers in driver_iossim.go and driver_androidemu.go cover the
// "fast feedback during development" loop; this file covers "before I
// hit ship, run the same spec on the actual hardware I'm shipping
// to."
//
// Both flavors deliberately reuse the existing simctl/adb wrappers
// so we don't duplicate the lifecycle code. The only differences:
//
//   - Real devices are not booted or shut down by the runner. The
//     dev plugged it in; the runner respects that.
//   - Real devices require provisioning (iOS) or USB debugging
//     enabled (Android). We surface a clear error if those are
//     missing instead of getting stuck in a half-installed state.
//   - We use the device's UDID/serial as the simctl/adb target
//     instead of looking it up by AVD or simulator name.
//
// Lifecycle for `target: device` with `platform: ios|android` is:
//
//   1. List() → returns currently connected matching devices.
//   2. Verify() → check the device is paired/authorised and the
//      app is installable.
//   3. Install() → install the .app/.apk from disk.
//   4. Launch() → start the app.
//   5. Run steps via the same Tap/Text/KeyEvent helpers as the
//      simulator drivers.
//   6. Screenshot() on failure.
//
// Step 6 also writes to the spec's normal artifacts dir, so failures
// from a real device show up in the mobile "Runs" tab next to web
// failures with no special handling.

// DevicePlatform is which OS family a real device runs.
type DevicePlatform string

const (
	DevicePlatformIOS     DevicePlatform = "ios"
	DevicePlatformAndroid DevicePlatform = "android"
)

// USBDevice describes one connected device returned by ListUSBDevices.
type USBDevice struct {
	Platform DevicePlatform
	UDID     string // simctl udid (iOS) or adb serial (Android)
	Name     string // human-friendly name (model + iOS version, AVD name, etc)
	OS       string // iOS or Android version when known
}

// ListUSBDevices returns every connected device the host can see.
// Combines `idevice_id -l` (iOS) and `adb devices -l` (Android).
// Either probe failing is non-fatal — the dev may only have one
// platform installed.
func ListUSBDevices(ctx context.Context) ([]USBDevice, error) {
	out := []USBDevice{}
	if runtime.GOOS == "darwin" {
		if devs := listIOSUSBDevices(ctx); len(devs) > 0 {
			out = append(out, devs...)
		}
	}
	if devs := listAndroidUSBDevices(ctx); len(devs) > 0 {
		out = append(out, devs...)
	}
	return out, nil
}

func listIOSUSBDevices(ctx context.Context) []USBDevice {
	// `idevice_id -l` from libimobiledevice prints one UDID per line.
	// macOS dev's already have it via `brew install libimobiledevice`.
	if _, err := exec.LookPath("idevice_id"); err != nil {
		return nil
	}
	out, err := runCtx(ctx, "idevice_id", "-l")
	if err != nil {
		return nil
	}
	devs := []USBDevice{}
	for _, line := range strings.Split(out, "\n") {
		udid := strings.TrimSpace(line)
		if udid == "" {
			continue
		}
		name := udid
		if info, err := runCtx(ctx, "ideviceinfo", "-u", udid, "-k", "DeviceName"); err == nil {
			n := strings.TrimSpace(info)
			if n != "" {
				name = n
			}
		}
		osVer := ""
		if info, err := runCtx(ctx, "ideviceinfo", "-u", udid, "-k", "ProductVersion"); err == nil {
			osVer = "iOS " + strings.TrimSpace(info)
		}
		devs = append(devs, USBDevice{
			Platform: DevicePlatformIOS,
			UDID:     udid,
			Name:     name,
			OS:       osVer,
		})
	}
	return devs
}

func listAndroidUSBDevices(ctx context.Context) []USBDevice {
	if _, err := exec.LookPath("adb"); err != nil {
		return nil
	}
	out, err := runCtx(ctx, "adb", "devices", "-l")
	if err != nil {
		return nil
	}
	devs := []USBDevice{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}
		serial := fields[0]
		// Skip emulators — they're handled by AndroidEmuDriver.
		if strings.HasPrefix(serial, "emulator-") {
			continue
		}
		name := serial
		osVer := ""
		for _, kv := range fields[2:] {
			if strings.HasPrefix(kv, "model:") {
				name = strings.TrimPrefix(kv, "model:")
			}
			if strings.HasPrefix(kv, "device:") && name == serial {
				name = strings.TrimPrefix(kv, "device:")
			}
		}
		if v, err := runCtx(ctx, "adb", "-s", serial, "shell", "getprop", "ro.build.version.release"); err == nil {
			v = strings.TrimSpace(v)
			if v != "" {
				osVer = "Android " + v
			}
		}
		devs = append(devs, USBDevice{
			Platform: DevicePlatformAndroid,
			UDID:     serial,
			Name:     name,
			OS:       osVer,
		})
	}
	return devs
}

// USBDeviceDriver is the runner-facing wrapper for one connected
// device. It delegates to IOSSimDriver / AndroidEmuDriver for the
// shared step actions but skips boot/shutdown.
type USBDeviceDriver struct {
	Device   USBDevice
	IOSSim   *IOSSimDriver
	Android  *AndroidEmuDriver
}

// NewUSBDeviceDriver builds a driver pointed at one device. The
// caller hands it the .app or .apk path via the underlying
// simulator/emulator driver.
func NewUSBDeviceDriver(d USBDevice) *USBDeviceDriver {
	w := &USBDeviceDriver{Device: d}
	switch d.Platform {
	case DevicePlatformIOS:
		w.IOSSim = &IOSSimDriver{UDID: d.UDID}
	case DevicePlatformAndroid:
		w.Android = &AndroidEmuDriver{}
	}
	return w
}

// Verify checks that the device is reachable and trusted. Returns a
// human-readable error so the mobile "Runs" tab can show "iPhone is
// locked, unlock it" or "Android USB debugging needs authorisation."
func (d *USBDeviceDriver) Verify(ctx context.Context) error {
	switch d.Device.Platform {
	case DevicePlatformIOS:
		if _, err := exec.LookPath("idevice_id"); err != nil {
			return fmt.Errorf("install libimobiledevice: brew install libimobiledevice")
		}
		// Probe with `idevicesyslog -e` would block; use a quick
		// status command instead.
		if _, err := runCtx(ctx, "ideviceinfo", "-u", d.Device.UDID, "-k", "ProductVersion"); err != nil {
			return fmt.Errorf("ios device %s not reachable: %w (unlock the phone and tap Trust)", d.Device.UDID, err)
		}
		return nil
	case DevicePlatformAndroid:
		out, err := runCtx(ctx, "adb", "-s", d.Device.UDID, "get-state")
		if err != nil || strings.TrimSpace(out) != "device" {
			return fmt.Errorf("android device %s not in 'device' state — enable USB debugging and tap Allow on the phone", d.Device.UDID)
		}
		return nil
	}
	return fmt.Errorf("unknown platform %q", d.Device.Platform)
}

// Install installs the given app onto the device. Path must be a
// .app for iOS or a .apk for Android.
func (d *USBDeviceDriver) Install(ctx context.Context, appPath string) error {
	switch d.Device.Platform {
	case DevicePlatformIOS:
		if _, err := exec.LookPath("ideviceinstaller"); err != nil {
			return fmt.Errorf("install ideviceinstaller: brew install ideviceinstaller")
		}
		_, err := runCtx(ctx, "ideviceinstaller", "-u", d.Device.UDID, "-i", appPath)
		return err
	case DevicePlatformAndroid:
		_, err := runCtx(ctx, "adb", "-s", d.Device.UDID, "install", "-r", appPath)
		return err
	}
	return fmt.Errorf("unknown platform")
}

// Launch starts the app by bundle id (iOS) or package + activity
// (Android). Reuses the simctl / monkey paths from the sim drivers.
func (d *USBDeviceDriver) Launch(ctx context.Context, bundleOrPackage, activity string) error {
	switch d.Device.Platform {
	case DevicePlatformIOS:
		if _, err := exec.LookPath("idevicedebug"); err != nil {
			return fmt.Errorf("install libimobiledevice: brew install libimobiledevice")
		}
		_, err := runCtx(ctx, "idevicedebug", "-u", d.Device.UDID, "run", bundleOrPackage)
		return err
	case DevicePlatformAndroid:
		d.Android.Package = bundleOrPackage
		d.Android.Activity = activity
		return d.Android.Launch(ctx, d.Device.UDID)
	}
	return fmt.Errorf("unknown platform")
}

// Screenshot saves a PNG to outPath.
func (d *USBDeviceDriver) Screenshot(ctx context.Context, outPath string) error {
	switch d.Device.Platform {
	case DevicePlatformIOS:
		if _, err := exec.LookPath("idevicescreenshot"); err != nil {
			return fmt.Errorf("install libimobiledevice: brew install libimobiledevice")
		}
		_, err := runCtx(ctx, "idevicescreenshot", "-u", d.Device.UDID, outPath)
		return err
	case DevicePlatformAndroid:
		return d.Android.Screenshot(ctx, d.Device.UDID, outPath)
	}
	return fmt.Errorf("unknown platform")
}

// Tap dispatches a tap on the device.
func (d *USBDeviceDriver) Tap(ctx context.Context, x, y int) error {
	switch d.Device.Platform {
	case DevicePlatformAndroid:
		return d.Android.Tap(ctx, d.Device.UDID, x, y)
	case DevicePlatformIOS:
		// iOS real-device taps require WebDriverAgent; that's a M5+
		// scope. For now, surface a clear error so the runner doesn't
		// silently no-op.
		return fmt.Errorf("ios real-device taps need WebDriverAgent (planned)")
	}
	return fmt.Errorf("unknown platform")
}

// Text sends keystrokes.
func (d *USBDeviceDriver) Text(ctx context.Context, text string) error {
	switch d.Device.Platform {
	case DevicePlatformAndroid:
		return d.Android.Text(ctx, d.Device.UDID, text)
	case DevicePlatformIOS:
		return fmt.Errorf("ios real-device text input needs WebDriverAgent (planned)")
	}
	return fmt.Errorf("unknown platform")
}
