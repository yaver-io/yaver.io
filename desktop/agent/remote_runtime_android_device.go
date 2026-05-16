package main

// remote_runtime_android_device.go — Phase 1 of
// docs/physical-device-remote-runtime.md.
//
// `android-device` is the remote-runtime target for a PHYSICAL Android
// phone/tablet attached to this agent host over USB or wifi (`yaver
// wire` / `yaver wireless`). It exists because an ARM Linux cloud box
// (linux/aarch64) can never run the Google emulator — see memory
// `project_no_linux_arm64_android_emulator` and
// `emulatorHostSupported`.
//
// It is deliberately a thin sibling of `android-emulator`: every
// capture / dims / control path is `adb -s <serial> …`, which is
// serial-generic — a real device serial (USB serial or `ip:port`)
// behaves identically to an `emulator-5554` serial. The ONLY thing
// that differs is attach: there is no AVD to boot, so we resolve the
// serial of an already-attached physical device instead. All the
// `case "android-emulator"` switches in remote_runtime_video_track.go
// / _webrtc.go / _dims.go gain `"android-device"` as a grouped case;
// this file owns only the probe + serial resolution.

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

const remoteRuntimeAndroidDeviceTargetID = "android-device"

// attachedAndroidDevices returns physical Android devices reachable
// from this host, USB first then wifi. Emulators are already excluded
// by androidDevicesFromAdb (it drops `emulator-*`). The slice order is
// the resolution preference: a cabled device beats a wireless one
// (lower latency for screenrecord + input).
func attachedAndroidDevices(ctx context.Context) []wireDevice {
	devs := append([]wireDevice{}, listAndroidWireDevices(ctx)...)
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.UDID] = true
	}
	for _, d := range listAndroidWirelessDevices(ctx) {
		if !seen[d.UDID] {
			devs = append(devs, d)
			seen[d.UDID] = true
		}
	}
	return devs
}

// resolveAttachedAndroidDeviceSerial picks the serial the session will
// drive. Phase 1 takes the first attached device (USB-preferred);
// multi-device disambiguation is a later concern — solo devs almost
// always have exactly one phone plugged in.
func resolveAttachedAndroidDeviceSerial(ctx context.Context) (string, error) {
	for _, d := range attachedAndroidDevices(ctx) {
		if d.UDID != "" {
			return d.UDID, nil
		}
	}
	return "", fmt.Errorf("no physical Android device attached — connect one over USB (`yaver wire`) or wifi (`yaver wireless`), enable USB debugging, and accept the RSA prompt")
}

// probeAndroidDeviceTarget mirrors probeAndroidEmulatorTarget but for
// a real device. Enabled iff adb is present AND at least one physical
// Android device is currently attached — unlike the emulator target,
// "device not connected yet" is a normal, recoverable state, so the
// reason text tells the user exactly what to do.
func probeAndroidDeviceTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               remoteRuntimeAndroidDeviceTargetID,
		Label:            "Android device (physical) over WebRTC",
		Platform:         "android",
		RuntimeHostClass: runtimeHostClassForAndroid(),
		HostOS:           runtime.GOOS,
		RequiredCLI:      "adb",
	}
	if findAndroidToolPath("adb") == "" {
		target.Enabled = false
		target.Reason = "adb not found. Run `yaver install remote-runtime` to provision platform-tools."
		return target
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := resolveAttachedAndroidDeviceSerial(ctx); err != nil {
		target.Enabled = false
		target.Reason = err.Error()
		return target
	}
	target.Enabled = true
	return target
}
