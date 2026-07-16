package main

// dev_mechanism_test.go — table-drive every (framework × surface × plat)
// tuple the resolver claims to handle. Pure — no I/O, safe to run on
// linux CI. Kept in one big table so adding a new surface later
// obviously falls off the front of the table.

import (
	"strings"
	"testing"
)

func TestResolveMechanism_Table(t *testing.T) {
	cases := []struct {
		framework, surface, platform string
		host                         HostCaps
		wantMechanism                DevMechanism
		wantTarget                   string
	}{
		// RN / Expo phone+tablet → Hermes bridge swap.
		{"expo", "phone", "ios", HostCaps{}, DevMechanismHermes, "ios-simulator"},
		{"expo", "phone", "android", HostCaps{}, DevMechanismHermes, "android-emulator"},
		{"react-native", "tablet", "ios", HostCaps{}, DevMechanismHermes, "ipados-simulator"},
		{"react-native", "tablet", "android", HostCaps{}, DevMechanismHermes, "android-emulator"},

		// RN watch/tv/vision/car → native rebuild + stream (no in-place bridge swap on those).
		{"expo", "watch", "ios", HostCaps{}, DevMechanismNativeRebuild, "watchos-simulator"},
		{"expo", "tv", "ios", HostCaps{}, DevMechanismNativeRebuild, "tvos-simulator"},
		{"expo", "vision", "ios", HostCaps{}, DevMechanismNativeRebuild, "visionos-simulator"},
		{"expo", "car", "ios", HostCaps{}, DevMechanismNativeRebuild, "ios-simulator"},

		// RN web → WebView proxy of the dev server.
		{"expo", "web", "", HostCaps{}, DevMechanismWebView, "browser-window"},

		// Web frameworks → WebView.
		{"next", "phone", "", HostCaps{}, DevMechanismWebView, "browser-window"},
		{"vite", "tablet", "", HostCaps{}, DevMechanismWebView, "browser-window"},

		// Native swift → native rebuild across every surface.
		{"swift", "phone", "ios", HostCaps{}, DevMechanismNativeRebuild, "ios-simulator"},
		{"swift", "watch", "ios", HostCaps{}, DevMechanismNativeRebuild, "watchos-simulator"},
		{"swift", "tv", "ios", HostCaps{}, DevMechanismNativeRebuild, "tvos-simulator"},
		{"swift", "vision", "ios", HostCaps{}, DevMechanismNativeRebuild, "visionos-simulator"},

		// Kotlin → android emulator when adb+emulator on PATH.
		{"kotlin", "phone", "android", HostCaps{AndroidEmulatorAvailable: true}, DevMechanismNativeRebuild, "android-emulator"},
		// Kotlin without emulator → falls back to physical device.
		{"kotlin", "phone", "android", HostCaps{AndroidEmulatorAvailable: false}, DevMechanismNativeRebuild, "android-emulator"},

		// Flutter phone/tablet → native rebuild (compiles to sim/emulator).
		{"flutter", "phone", "ios", HostCaps{}, DevMechanismNativeRebuild, "ios-simulator"},
		{"flutter", "tablet", "android", HostCaps{}, DevMechanismNativeRebuild, "android-emulator"},
		{"flutter", "web", "", HostCaps{}, DevMechanismWebView, "browser-window"},

		// Browser framework → WebRTC stream of a headless Chromium.
		{"browser", "web", "", HostCaps{}, DevMechanismWebRTCStream, "browser-window"},
	}
	for _, tc := range cases {
		gotMech, gotTgt, err := ResolveMechanism(tc.framework, tc.surface, tc.platform, tc.host)
		if err != nil {
			t.Errorf("ResolveMechanism(%s,%s,%s) errored: %v", tc.framework, tc.surface, tc.platform, err)
			continue
		}
		if gotMech != tc.wantMechanism || gotTgt != tc.wantTarget {
			t.Errorf("ResolveMechanism(%s,%s,%s) = (%s,%s), want (%s,%s)",
				tc.framework, tc.surface, tc.platform, gotMech, gotTgt, tc.wantMechanism, tc.wantTarget)
		}
	}
}

func TestResolveMechanism_UnknownFramework(t *testing.T) {
	_, _, err := ResolveMechanism("cobol", "phone", "ios", HostCaps{})
	if err == nil {
		t.Fatal("unknown framework should error")
	}
	if !strings.Contains(err.Error(), "no mechanism table entry") {
		t.Fatalf("error should mention no table entry, got %v", err)
	}
}

func TestResolveMechanism_UnsupportedSurface(t *testing.T) {
	_, _, err := ResolveMechanism("expo", "hologram", "ios", HostCaps{})
	if err == nil {
		t.Fatal("unknown surface should error")
	}
}
