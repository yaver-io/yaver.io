package main

// remote_runtime_apple_targets_test.go — P0 fan-out. Guarantees:
//   1. ParseInstalledRuntimeFamilies handles a real simctl fixture (iOS +
//      visionOS installed; watchOS/tvOS absent).
//   2. runtimeTargetFor maps every new id to iosSimulatorTarget with the
//      right pickSimulator substring.
//   3. Capabilities enumeration surfaces all five Apple sim targets, badges
//      them with a Surface, and disables the ones whose runtime is missing.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/yaver-io/agent/testkit"
)

func TestParseInstalledRuntimeFamilies_SimctlFixture(t *testing.T) {
	// Captured from `xcrun simctl list runtimes` on a mac that has iOS 26.4
	// + visionOS 2.1 installed but no watchOS/tvOS runtimes. The visionOS
	// runtime is marked as `xrOS` on older Xcodes and as `visionOS` on
	// newer ones — the parser accepts both.
	fixture := `== Runtimes ==
iOS 26.4 (26.4 - 24E246) - com.apple.CoreSimulator.SimRuntime.iOS-26-4
iOS 17.0 (17.0 - 21A342) - com.apple.CoreSimulator.SimRuntime.iOS-17-0 (unavailable, runtime path is missing)
watchOS 11.0 (11.0 - 22R379) - com.apple.CoreSimulator.SimRuntime.watchOS-11-0 (unavailable, runtime path is missing)
tvOS 18.0 (18.0 - 22J377) - com.apple.CoreSimulator.SimRuntime.tvOS-18-0 (unavailable, runtime path is missing)
visionOS 2.1 (2.1 - 22N320) - com.apple.CoreSimulator.SimRuntime.xrOS-2-1
`
	got := testkit.ParseInstalledRuntimeFamilies(fixture)
	if !got["iOS"] {
		t.Fatalf("iOS should be installed: %v", got)
	}
	if !got["visionOS"] {
		t.Fatalf("visionOS should be installed: %v", got)
	}
	if got["watchOS"] {
		t.Fatalf("watchOS is (unavailable) — should not be reported installed: %v", got)
	}
	if got["tvOS"] {
		t.Fatalf("tvOS is (unavailable) — should not be reported installed: %v", got)
	}
}

func TestParseInstalledRuntimeFamilies_XrOSAliasStillCountsAsVisionOS(t *testing.T) {
	// Older Xcodes labelled the runtime `xrOS` rather than `visionOS`. The
	// parser must map both to the visionOS family.
	fixture := "xrOS 1.0 (1.0 - 21N301) - com.apple.CoreSimulator.SimRuntime.xrOS-1-0\n"
	if !testkit.ParseInstalledRuntimeFamilies(fixture)["visionOS"] {
		t.Fatal("xrOS runtime should register as visionOS family")
	}
}

func TestRuntimeTargetFor_AllAppleSimIDs(t *testing.T) {
	cases := map[string]string{
		"ios-simulator":      "iPhone",
		"ipados-simulator":   "iPad",
		"watchos-simulator":  "Apple Watch",
		"tvos-simulator":     "Apple TV",
		"visionos-simulator": "Apple Vision",
	}
	for id, wantType := range cases {
		tg, err := runtimeTargetFor(id)
		if err != nil {
			t.Fatalf("runtimeTargetFor(%q) errored: %v", id, err)
		}
		iosTg, ok := tg.(iosSimulatorTarget)
		if !ok {
			t.Fatalf("runtimeTargetFor(%q) = %T, want iosSimulatorTarget", id, tg)
		}
		if iosTg.deviceType != wantType {
			t.Fatalf("runtimeTargetFor(%q).deviceType = %q, want %q", id, iosTg.deviceType, wantType)
		}
	}
	if _, err := runtimeTargetFor("watchos-simulator-blah"); err == nil {
		t.Fatal("unknown target id must still error")
	}
}

func TestCapabilitiesEnumeratesAllAppleSurfacesAndBadgesSurface(t *testing.T) {
	// Force the runtime-families probe to a known set so this test works
	// on any host (linux CI or a Mac missing some runtimes). Only iOS +
	// visionOS installed here; watchOS + tvOS absent.
	cleanup := setAppleRuntimeFamiliesForTest(map[string]bool{
		"iOS": true, "visionOS": true,
	})
	defer cleanup()

	caps := remoteRuntimeCapabilitiesForProject("/tmp/swift-app", "swift")
	if !caps.RemoteRuntimeEligible {
		t.Fatal("swift caps should be remote-runtime eligible")
	}
	wantSurface := map[string]string{
		"ios-simulator":      "phone",
		"ipados-simulator":   "tablet",
		"watchos-simulator":  "watch",
		"tvos-simulator":     "tv",
		"visionos-simulator": "vision",
	}
	seen := map[string]RemoteRuntimeTarget{}
	for _, tg := range caps.Targets {
		if _, want := wantSurface[tg.ID]; want {
			seen[tg.ID] = tg
			if tg.Surface != wantSurface[tg.ID] {
				t.Fatalf("target %q surface = %q, want %q", tg.ID, tg.Surface, wantSurface[tg.ID])
			}
		}
	}
	if len(seen) != len(wantSurface) {
		t.Fatalf("expected all five Apple sim targets, got %d (%v)", len(seen), seen)
	}
	// The runtime-gate assertions only mean anything on a Mac — on Linux
	// every Apple target is disabled by the host gate first, so the
	// runtime message never gets a chance to fire.
	if runtime.GOOS == "darwin" {
		if !seen["ios-simulator"].Enabled {
			t.Fatalf("iOS runtime installed but target disabled: %+v", seen["ios-simulator"])
		}
		if !seen["visionos-simulator"].Enabled {
			t.Fatalf("visionOS runtime installed but target disabled: %+v", seen["visionos-simulator"])
		}
		if seen["watchos-simulator"].Enabled {
			t.Fatalf("watchOS runtime missing but target enabled: %+v", seen["watchos-simulator"])
		}
		if !strings.Contains(seen["watchos-simulator"].Reason, "watchOS runtime not installed") {
			t.Fatalf("watchos-simulator reason should point at the missing runtime, got %q", seen["watchos-simulator"].Reason)
		}
		if !strings.Contains(seen["tvos-simulator"].Reason, "tvOS runtime not installed") {
			t.Fatalf("tvos-simulator reason should point at the missing runtime, got %q", seen["tvos-simulator"].Reason)
		}
	}
}

// TestHandleRemoteRuntimeCapabilitiesReturnsAppleFanOut is the P0
// closed-loop check: fire the real HTTP handler with a stubbed
// families map and assert the JSON body carries every Apple sim id
// with the right Surface badge. Mirrors the audit's "GET
// /remote-runtime/capabilities?framework=swift lists ios/ipados/
// watchos/tvos/visionos targets" acceptance criterion.
func TestHandleRemoteRuntimeCapabilitiesReturnsAppleFanOut(t *testing.T) {
	cleanup := setAppleRuntimeFamiliesForTest(map[string]bool{
		"iOS": true, "watchOS": true, "tvOS": true, "visionOS": true,
	})
	defer cleanup()

	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet,
		"/remote-runtime/capabilities?workDir=/tmp/swift-app&framework=swift", nil)
	rec := httptest.NewRecorder()
	srv.handleRemoteRuntimeCapabilities(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body RemoteRuntimeCapabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, rec.Body.String())
	}
	wantSurface := map[string]string{
		"ios-simulator":      "phone",
		"ipados-simulator":   "tablet",
		"watchos-simulator":  "watch",
		"tvos-simulator":     "tv",
		"visionos-simulator": "vision",
	}
	got := map[string]RemoteRuntimeTarget{}
	for _, tg := range body.Targets {
		got[tg.ID] = tg
	}
	for id, wantSurf := range wantSurface {
		tg, ok := got[id]
		if !ok {
			t.Fatalf("capabilities missing target %q", id)
		}
		if tg.Surface != wantSurf {
			t.Fatalf("target %q Surface=%q, want %q", id, tg.Surface, wantSurf)
		}
	}
}

func TestInstalledRuntimeFamilies_NonDarwinReturnsEmpty(t *testing.T) {
	// Guard rail: InstalledRuntimeFamilies must not shell out on Linux
	// (there is no xcrun to shell to). It returns an empty map + nil
	// error so callers treat every Apple target as disabled-by-host.
	if runtime.GOOS == "darwin" {
		t.Skip("darwin has xcrun — the empty-map path is a linux invariant")
	}
	fams, err := testkit.InstalledRuntimeFamilies(context.Background())
	if err != nil {
		t.Fatalf("InstalledRuntimeFamilies on non-darwin errored: %v", err)
	}
	if len(fams) != 0 {
		t.Fatalf("InstalledRuntimeFamilies on non-darwin returned %v, want empty", fams)
	}
}
