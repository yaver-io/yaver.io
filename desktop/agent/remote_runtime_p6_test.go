package main

// remote_runtime_p6_test.go — P6 control fidelity + Android surface
// target tests. Pure — no shell-outs, no live simulators.

import (
	"strings"
	"testing"
)

func TestAndroidKeycode_TVDpadResolves(t *testing.T) {
	cases := map[string]int{
		"up": 19, "down": 20, "left": 21, "right": 22, "select": 23,
		"dpad_up": 19, "dpad_left": 21, "ok": 23,
	}
	for name, want := range cases {
		got, ok := androidKeycodeForName(name)
		if !ok {
			t.Fatalf("androidKeycodeForName(%q) = false", name)
		}
		if got != want {
			t.Fatalf("androidKeycodeForName(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestAndroidKeycode_WearCrownResolves(t *testing.T) {
	if code, ok := androidKeycodeForName("crown_up"); !ok || code != 92 {
		t.Fatalf("crown_up = (%d,%v), want (92,true)", code, ok)
	}
	if code, ok := androidKeycodeForName("crown_down"); !ok || code != 93 {
		t.Fatalf("crown_down = (%d,%v), want (93,true)", code, ok)
	}
}

func TestRuntimeTargetFor_AndroidSurfaceIDs(t *testing.T) {
	cases := map[string]string{
		"android-wear": "wear",
		"android-tv":   "tv",
		"android-xr":   "xr",
		"android-auto": "auto",
	}
	for id, hint := range cases {
		tg, err := runtimeTargetFor(id)
		if err != nil {
			t.Fatalf("runtimeTargetFor(%q) errored: %v", id, err)
		}
		s, ok := tg.(androidSurfaceTarget)
		if !ok {
			t.Fatalf("runtimeTargetFor(%q) = %T, want androidSurfaceTarget", id, tg)
		}
		if s.avdHint != hint {
			t.Fatalf("androidSurfaceTarget(%q).avdHint = %q, want %q", id, s.avdHint, hint)
		}
	}
}

func TestProbeAndroidSurfaceTargets_HaveExpectedSurfaceBadge(t *testing.T) {
	for _, tg := range []RemoteRuntimeTarget{
		probeAndroidWearTarget(),
		probeAndroidTVTarget(),
		probeAndroidXRTarget(),
		probeAndroidAutoTarget(),
	} {
		if tg.Platform != "android" {
			t.Errorf("%s Platform = %q, want android", tg.ID, tg.Platform)
		}
		if tg.Surface == "" {
			t.Errorf("%s missing Surface badge", tg.ID)
		}
	}
}

func TestWDAButtonName_TVRemoteReturnsActionableError(t *testing.T) {
	// The WDA client's PressButton returns a friendly error for tvOS
	// remote / watchOS crown / visionOS pinch keys — the wire contract
	// is exercised by the reason table so we don't need a live WDA.
	if _, ok := wdaButtonName("up"); ok {
		t.Fatal("wdaButtonName should not resolve tvOS 'up' to a WDA button")
	}
	reason, surface := unsupportedIOSKeyReason("up")
	if reason == "" || !strings.Contains(surface, "tvOS") {
		t.Fatalf("unsupportedIOSKeyReason(up) = (%q,%q), want tvOS remote guidance", reason, surface)
	}
	if _, s := unsupportedIOSKeyReason("crown_up"); !strings.Contains(s, "watchOS") {
		t.Fatalf("crown_up should be flagged as watchOS surface, got %q", s)
	}
	if _, s := unsupportedIOSKeyReason("pinch"); !strings.Contains(s, "visionOS") {
		t.Fatalf("pinch should be flagged as visionOS surface, got %q", s)
	}
}
