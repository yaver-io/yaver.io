package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Pinch is the one gesture that cannot be faked with the existing verbs, and
// the ways it goes wrong are all SILENT:
//
//   - a target with no multi-touch primitive that no-ops instead of refusing
//     looks exactly like a frozen stream;
//   - approximating a pinch with two sequential swipes makes the app SCROLL,
//     which looks like the gesture worked and did something else;
//   - a dropped `scale` argument turns every pinch into scale=0.
//
// Each of those is tested here, because none of them would show up as an error
// in normal use.

// A target that cannot pinch must say so, not quietly succeed.
func TestPinchUnsupportedTargetsRefuseLoudly(t *testing.T) {
	ctx := context.Background()
	for name, err := range map[string]error{
		"iosSimulator": iosSimulatorTarget{}.Pinch(ctx, "dev", 100, 100, 2.0, 300),
		"iosDevice":    iosDeviceTarget{}.Pinch(ctx, "dev", 100, 100, 2.0, 300),
		"streamSource": streamSourceTarget{}.Pinch(ctx, "dev", 100, 100, 2.0, 300),
	} {
		if err == nil {
			t.Errorf("%s: Pinch returned nil — a silent no-op is indistinguishable "+
				"from a dead stream; it must refuse", name)
			continue
		}
		if !errors.Is(err, errPinchUnsupported) {
			t.Errorf("%s: error should wrap errPinchUnsupported so callers can "+
				"detect it programmatically, got: %v", name, err)
		}
		// The message must say WHY, since the user's next question is always
		// "so how do I pinch on this thing?"
		if !strings.Contains(strings.ToLower(err.Error()), "pinch") {
			t.Errorf("%s: message should name the gesture, got: %v", name, err)
		}
	}
}

// Guard the argument validation that protects against a dropped/defaulted
// scale. scale==0 is what arrives when a caller forgets the field.
func TestPinchRejectsMeaninglessScale(t *testing.T) {
	live := &remoteRuntimeLiveState{targetID: "android-emulator", deviceID: "emulator-5554"}
	ctx := context.Background()

	if err := live.pinch(ctx, 100, 100, 0, 300); err == nil {
		t.Error("scale=0 must be rejected — that is exactly what a caller that " +
			"forgot the argument sends, and it would otherwise reach the device")
	}
	if err := live.pinch(ctx, 100, 100, -1, 300); err == nil {
		t.Error("negative scale must be rejected")
	}
	if err := live.pinch(ctx, -5, 100, 2.0, 300); err == nil {
		t.Error("negative coordinates must be rejected")
	}
}

// The Android helper must refuse a scale of ~1: it is a no-op gesture that
// would look like "pinch ran and nothing happened".
func TestAndroidPinchRejectsNoOpScale(t *testing.T) {
	err := androidPinchViaUiautomator(context.Background(), "nonexistent-device", 100, 100, 1.0, 300)
	if err == nil {
		t.Fatal("scale=1.0 is a no-op and must be rejected before touching the device")
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Errorf("the error should explain that scale 1 does nothing, got: %v", err)
	}
}

// Every runtimeTarget must implement Pinch. This is really a compile-time
// assertion — if a new target is added without Pinch, the build breaks — but it
// is written down so the REASON survives: the interface is the only thing
// guaranteeing a new surface cannot silently ship without multi-touch.
func TestAllRuntimeTargetsImplementPinch(t *testing.T) {
	// androidTarget is an embedded BASE (no Attach), not a target in its own
	// right — the concrete ones embed it. Listing the real implementors here is
	// what makes this assertion meaningful.
	targets := map[string]runtimeTarget{
		"iosSimulator":    iosSimulatorTarget{},
		"androidEmulator": androidEmulatorTarget{},
		"redroid":         redroidRuntimeTarget{},
		"browser":         browserWindowTarget{},
		"iosDevice":       iosDeviceTarget{},
		"streamSource":    streamSourceTarget{},
	}
	if len(targets) < 6 {
		t.Fatalf("expected at least 6 targets, got %d", len(targets))
	}
	for name, tgt := range targets {
		if tgt == nil {
			t.Errorf("%s is nil", name)
		}
	}
}
