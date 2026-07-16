package main

// remote_runtime_android_surfaces.go — P6 Android surface targets.
//
// Wear OS, Android TV, Android XR, and Android Auto emulators all
// speak adb + expose the same runtimeTarget contract as
// androidEmulatorTarget — the difference is which AVD you boot. We
// wrap `androidTarget` (same tap/screenshot/dims) and only override
// Attach to hint the AVD name to the driver. Callers get four new
// picker entries; each shows as its own Surface (watch/tv/vision/car)
// so the n2n picker can address them independently. The Wear-OS
// crown / TV D-pad remap already lives in `androidKeycodeForName`.

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/yaver-io/agent/testkit"
)

// androidSurfaceTarget is an AVD-hinted variant of androidEmulatorTarget.
// The `avdHint` is a substring the driver's `pickEmulator` step matches
// against `emulator -list-avds`. Empty means "first AVD" (i.e. legacy
// behaviour) — the Enabled flag is what matters at the picker level.
type androidSurfaceTarget struct {
	androidTarget
	avdHint string
}

func (t androidSurfaceTarget) Attach(ctx context.Context) (string, error) {
	// The current AndroidEmuDriver.Boot uses the first online emulator
	// if one exists, otherwise the first AVD. We pass the hint as the
	// AVD name — the driver treats it as an exact match. If no AVD
	// matches, the driver's own error message surfaces
	// (`no AVDs configured …`) which is the right user-facing text.
	return (&testkit.AndroidEmuDriver{AVD: t.avdHint}).Boot(ctx)
}

// probeAndroidSurfaceTarget mirrors probeAndroidEmulatorTarget but
// stamps a Surface badge + friendly label for the specific surface.
// Enablement is identical (adb + emulator on PATH).
func probeAndroidSurfaceTarget(id, surface, label string) RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               id,
		Label:            label,
		Surface:          surface,
		Platform:         "android",
		RuntimeHostClass: runtimeHostClassForAndroid(),
		HostOS:           runtime.GOOS,
		RequiredCLI:      "adb + emulator",
	}
	if findAndroidToolPath("adb") == "" {
		target.Enabled = false
		target.Reason = "adb not found. Install Android platform-tools."
		return target
	}
	if findAndroidToolPath("emulator") == "" {
		target.Enabled = false
		if !androidEmulatorHostSupported() {
			target.Reason = fmt.Sprintf(
				"Google ships no Android emulator binary for %s/%s. Stream from a physical %s device (`yaver wire`) or a macOS / x86-64-Linux host.",
				runtime.GOOS, runtime.GOARCH, strings.ToLower(surface))
		} else {
			target.Reason = "Android emulator binary not found. Run `yaver install remote-runtime`."
		}
		return target
	}
	target.Enabled = true
	return target
}

func probeAndroidWearTarget() RemoteRuntimeTarget {
	return probeAndroidSurfaceTarget("android-wear", "watch", "Wear OS Emulator over WebRTC")
}
func probeAndroidTVTarget() RemoteRuntimeTarget {
	return probeAndroidSurfaceTarget("android-tv", "tv", "Android TV Emulator over WebRTC")
}
func probeAndroidXRTarget() RemoteRuntimeTarget {
	return probeAndroidSurfaceTarget("android-xr", "vision", "Android XR Emulator over WebRTC")
}
func probeAndroidAutoTarget() RemoteRuntimeTarget {
	return probeAndroidSurfaceTarget("android-auto", "car", "Android Auto Emulator over WebRTC")
}
