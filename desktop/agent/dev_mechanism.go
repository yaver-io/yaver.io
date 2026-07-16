package main

// dev_mechanism.go — pure resolver for the "which mechanism should we
// use to develop for this surface?" question. P2 of the n2n plan.
//
// The four mechanisms:
//   hermes         — HBC bytecode push into the mobile super-host bridge (phone/tablet only)
//   webrtc-stream  — WebRTC/JPEG stream of a booted simulator/emulator (universal fallback)
//   webview        — /dev/ proxy of a web dev server (web frameworks on WebView-capable clients)
//   native-rebuild — native build + fresh install (RN targets that have no in-place bridge swap)
//
// The resolver is pure: no I/O, no globals. Feed it the framework + the
// surface the user wants + the host capabilities and it returns
// (mechanism, targetId, err). Tests exhaustively cover the table.

import (
	"fmt"
	"strings"
)

// DevMechanism enumerates the four transports/mechanisms we can use to
// preview an app on a remote surface.
type DevMechanism string

const (
	DevMechanismHermes        DevMechanism = "hermes"
	DevMechanismWebRTCStream  DevMechanism = "webrtc-stream"
	DevMechanismWebView       DevMechanism = "webview"
	DevMechanismNativeRebuild DevMechanism = "native-rebuild"
)

// HostCaps is the subset of host capabilities the resolver cares
// about. Populated by the caller from the same probes the picker uses
// (`InstalledRuntimeFamilies`, `findAndroidToolPath`, etc.) so we don't
// re-shell out at resolve time.
type HostCaps struct {
	// AppleRuntimeFamilies is the set returned by
	// testkit.InstalledRuntimeFamilies (`iOS`/`watchOS`/`tvOS`/`visionOS`).
	AppleRuntimeFamilies map[string]bool
	// AndroidEmulatorAvailable is true when adb + emulator binaries are
	// both on PATH — mirrors probeAndroidEmulatorTarget.
	AndroidEmulatorAvailable bool
}

// ResolveMechanism picks the (mechanism, targetId) tuple for a given
// project framework + desired surface + platform. Returns an error
// when no path is possible (e.g. hermes for a watch surface — there
// is no in-place bridge swap on watchOS).
//
// framework is one of expo / react-native / next / vite / flutter /
// swift / kotlin / browser. surface is one of
// phone / tablet / watch / tv / vision / car / web. platform is
// ios / android (empty defaults per-surface).
func ResolveMechanism(framework, surface, platform string, host HostCaps) (DevMechanism, string, error) {
	fw := strings.ToLower(strings.TrimSpace(framework))
	surf := strings.ToLower(strings.TrimSpace(surface))
	plat := strings.ToLower(strings.TrimSpace(platform))

	switch fw {
	case "expo", "react-native", "rn":
		switch surf {
		case "phone", "":
			return DevMechanismHermes, appleOrAndroidPhone(plat), nil
		case "tablet":
			if plat == "android" {
				return DevMechanismHermes, "android-emulator", nil
			}
			return DevMechanismHermes, "ipados-simulator", nil
		case "watch", "tv", "vision", "car":
			// No RN host on these surfaces — native rebuild + stream the
			// booted sim. Android XR / Wear / AndroidTV / Auto land in P6.
			id, err := appleSurfaceTargetID(surf)
			if err != nil {
				return "", "", err
			}
			return DevMechanismNativeRebuild, id, nil
		case "web":
			return DevMechanismWebView, "browser-window", nil
		default:
			return "", "", fmt.Errorf("no mechanism for RN surface %q", surface)
		}
	case "next", "nextjs", "vite", "react":
		return DevMechanismWebView, "browser-window", nil
	case "flutter":
		switch surf {
		case "web":
			return DevMechanismWebView, "browser-window", nil
		case "phone", "":
			if plat == "ios" {
				return DevMechanismNativeRebuild, "ios-simulator", nil
			}
			return DevMechanismNativeRebuild, "android-emulator", nil
		case "tablet":
			if plat == "ios" {
				return DevMechanismNativeRebuild, "ipados-simulator", nil
			}
			return DevMechanismNativeRebuild, "android-emulator", nil
		case "watch", "tv", "vision", "car":
			id, err := appleSurfaceTargetID(surf)
			if err != nil {
				return "", "", err
			}
			return DevMechanismNativeRebuild, id, nil
		}
	case "swift":
		switch surf {
		case "phone", "":
			return DevMechanismNativeRebuild, "ios-simulator", nil
		case "tablet":
			return DevMechanismNativeRebuild, "ipados-simulator", nil
		case "watch", "tv", "vision", "car":
			id, err := appleSurfaceTargetID(surf)
			if err != nil {
				return "", "", err
			}
			return DevMechanismNativeRebuild, id, nil
		}
	case "kotlin":
		if host.AndroidEmulatorAvailable || plat == "android" || plat == "" {
			return DevMechanismNativeRebuild, "android-emulator", nil
		}
		return DevMechanismNativeRebuild, "android-device", nil
	case "browser":
		return DevMechanismWebRTCStream, "browser-window", nil
	}
	return "", "", fmt.Errorf("no mechanism table entry for framework %q surface %q", framework, surface)
}

func appleOrAndroidPhone(platform string) string {
	if strings.ToLower(strings.TrimSpace(platform)) == "android" {
		return "android-emulator"
	}
	return "ios-simulator"
}

func appleSurfaceTargetID(surface string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(surface)) {
	case "watch":
		return "watchos-simulator", nil
	case "tv":
		return "tvos-simulator", nil
	case "vision", "ar-vr", "arvr":
		return "visionos-simulator", nil
	case "car":
		// CarPlay lives inside a running iOS sim's external-display
		// window; no addressable CarPlay-only sim id. We stream the
		// iPhone sim and rely on the user to open the CarPlay window.
		return "ios-simulator", nil
	}
	return "", fmt.Errorf("no Apple sim target for surface %q", surface)
}
