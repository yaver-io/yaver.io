package main

import (
	"testing"
	"time"
)

// Speed-first where the encoder is proven: iOS sims MUST use H.264 recordVideo,
// never screenshot (18s/frame on the mini). Android stays on JPEG-DC until the
// product probes screenrecord bytes, because ATD can exit 0 with an empty H.264
// stream and leave the viewer connected to black video.
func TestPreferredCaptureMethod(t *testing.T) {
	cases := map[string]CaptureMethod{
		"ios-simulator":      CaptureH264RecordVideo,
		"tvos-simulator":     CaptureH264RecordVideo,
		"visionos-simulator": CaptureH264RecordVideo,
		"android-emulator":   CaptureJPEGScreenshot,
		"android-device":     CaptureJPEGScreenshot,
		"android-redroid":    CaptureJPEGScreenshot,
		"desktop-screen":     CaptureH264Desktop,
		"browser-window":     CaptureJPEGScreenshot,
	}
	for target, want := range cases {
		if got := preferredCaptureMethod(target); got != want {
			t.Errorf("preferredCaptureMethod(%q) = %q, want %q", target, got, want)
		}
	}
	// The whole point: no iOS sim target may resolve to the slow screenshot path.
	for _, ios := range []string{"ios-simulator", "ipados-simulator", "watchos-simulator"} {
		if preferredCaptureMethod(ios) == CaptureJPEGScreenshot {
			t.Errorf("%s must NOT use screenshot (18s/frame) — it must stream H.264", ios)
		}
		if !captureIsRealtime(preferredCaptureMethod(ios)) {
			t.Errorf("%s capture must be realtime video", ios)
		}
	}
	if !captureIsRealtime(preferredCaptureMethod("desktop-screen")) {
		t.Fatal("desktop-screen must select its native H.264 RTP capture path")
	}
}

func TestRemoteRuntimeNeedsAttachFrameProbe(t *testing.T) {
	if !remoteRuntimeNeedsAttachFrameProbe("android-wear") {
		t.Fatal("android-wear must prove JPEG capture before reporting WebRTC ready")
	}
	if !remoteRuntimeNeedsAttachFrameProbe("android-emulator") {
		t.Fatal("android-emulator must prove JPEG capture before reporting WebRTC ready")
	}
	if remoteRuntimeNeedsAttachFrameProbe("ios-simulator") {
		t.Fatal("iOS RTP targets should not run JPEG attach frame probes")
	}
}

func TestBrowserJPEGFramesUseInteractiveCadenceWithoutChangingDeviceFallbacks(t *testing.T) {
	if got := remoteRuntimeJPEGFrameInterval("browser-window"); got > 150*time.Millisecond {
		t.Fatalf("browser frame interval = %s, want an interactive browser-first lane", got)
	}
	if got := remoteRuntimeJPEGFrameInterval(physicalKVMTargetID); got > 150*time.Millisecond {
		t.Fatalf("physical KVM frame interval = %s, want interactive capture-card cadence", got)
	}
	if got := remoteRuntimeJPEGFrameInterval("android-emulator"); got != 700*time.Millisecond {
		t.Fatalf("Android screenshot interval = %s, want conservative device cadence", got)
	}
}
