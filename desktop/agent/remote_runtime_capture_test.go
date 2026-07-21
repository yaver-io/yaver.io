package main

import "testing"

// Speed-first: iOS sims MUST use H.264 recordVideo, never screenshot (18s/frame
// on the mini). Android uses scrcpy H.264; only genuine no-encoder targets fall
// back to JPEG screenshots.
func TestPreferredCaptureMethod(t *testing.T) {
	cases := map[string]CaptureMethod{
		"ios-simulator":      CaptureH264RecordVideo,
		"tvos-simulator":     CaptureH264RecordVideo,
		"visionos-simulator": CaptureH264RecordVideo,
		"android-emulator":   CaptureH264Scrcpy,
		"android-redroid":    CaptureH264Scrcpy,
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
}
