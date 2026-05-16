package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Compile-time proof every target satisfies the interface (also the
// first thing that breaks if Phase 3 adds a method without an impl).
var (
	_ runtimeTarget = iosSimulatorTarget{}
	_ runtimeTarget = androidEmulatorTarget{}
	_ runtimeTarget = androidDeviceTarget{}
)

func TestRuntimeTargetFor_KnownAndUnknown(t *testing.T) {
	cases := map[string]string{
		"ios-simulator":    "iosSimulatorTarget",
		"android-emulator": "androidEmulatorTarget",
		"android-device":   "androidDeviceTarget",
	}
	for id, want := range cases {
		tgt, err := runtimeTargetFor(id)
		if err != nil {
			t.Fatalf("runtimeTargetFor(%q) errored: %v", id, err)
		}
		if got := strings.TrimPrefix(fmt.Sprintf("%T", tgt), "main."); got != want {
			t.Fatalf("runtimeTargetFor(%q) = %s, want %s", id, got, want)
		}
	}
	if _, err := runtimeTargetFor("nope"); err == nil ||
		!strings.Contains(err.Error(), "unknown remote runtime target") {
		t.Fatalf("unknown id should error with the legacy message, got %v", err)
	}
}

// The exact error strings the old switch arms returned must survive
// the refactor — viewers/tests key off them.
func TestRuntimeTarget_IOSErrorStringsPreserved(t *testing.T) {
	ios := iosSimulatorTarget{}
	if err := ios.Swipe(context.Background(), "booted", 0, 0, 1, 1, 100); err == nil ||
		!strings.Contains(err.Error(), "swipe is not implemented for ios-simulator yet") {
		t.Fatalf("ios swipe error changed: %v", err)
	}
	if err := ios.Key(context.Background(), "booted", "home"); err == nil ||
		!strings.Contains(err.Error(), "is only supported for Android sessions right now") {
		t.Fatalf("ios key error changed: %v", err)
	}
	if !strings.HasSuffix(fmt.Sprintf("%T", ios), "iosSimulatorTarget") || ios.CanEncodeRTPH264() {
		t.Fatalf("ios must not advertise RTP H.264 (Xcode 26 simctl regression)")
	}
}

func TestRuntimeTarget_AndroidKeyDispatch(t *testing.T) {
	stubAdb(t, usbAndroidLine) // adb present + a device, so KeyEvent succeeds
	a := androidEmulatorTarget{}
	if err := a.Key(context.Background(), "emulator-5554", "82"); err != nil {
		t.Fatalf("numeric keycode should pass through: %v", err)
	}
	if err := a.Key(context.Background(), "emulator-5554", "back"); err != nil {
		t.Fatalf("named keycode should pass through: %v", err)
	}
	if err := a.Key(context.Background(), "emulator-5554", "fhqwhgads"); err == nil ||
		!strings.Contains(err.Error(), "unsupported key") {
		t.Fatalf("bogus key should keep the legacy error: %v", err)
	}
	if !a.CanEncodeRTPH264() {
		t.Fatalf("android must advertise RTP H.264 when adb is on PATH")
	}
}

func TestRuntimeTarget_AndroidDeviceAttachResolvesSerial(t *testing.T) {
	stubAdb(t, usbAndroidLine)
	serial, err := androidDeviceTarget{}.Attach(context.Background())
	if err != nil {
		t.Fatalf("android-device attach: %v", err)
	}
	if serial != "R52W60BEDXD" || strings.HasPrefix(serial, "emulator-") {
		t.Fatalf("attach resolved %q, want the physical USB serial", serial)
	}
}

// agentCanEncodeRTPH264 still routes through the interface and keeps
// its old answers (the var seam tests depend on stays intact).
func TestAgentCanEncodeRTPH264_StillMatchesTargets(t *testing.T) {
	stubAdb(t, usbAndroidLine)
	for _, id := range []string{"android-emulator", "android-device"} {
		if !agentCanEncodeRTPH264(id) {
			t.Fatalf("%s should encode RTP with adb present", id)
		}
	}
	if agentCanEncodeRTPH264("ios-simulator") {
		t.Fatalf("ios-simulator must stay JPEG-only")
	}
	if agentCanEncodeRTPH264("bogus") {
		t.Fatalf("unknown target must be false")
	}
}
