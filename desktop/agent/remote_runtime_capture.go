package main

// remote_runtime_capture.go — speed-first capture-method selection.
//
// The frame-pump historically captured every frame with `simctl io screenshot`.
// Measured on the mini (Xcode 26.4), that call takes ~18 SECONDS per frame — a
// pathological CoreSimulator perf bug — while the guest app's Metro Fast Refresh
// is ~764ms. So the "vibe latency" was never Fast Refresh; it was the capture.
//
// The fix is a real video stream, not per-frame screenshots:
//   - iOS simulator:      `simctl io recordVideo --codec=h264` → H.264 elementary
//                         stream → Pion webrtc-rtp-h264 track (real-time).
//   - Android emu/redroid: scrcpy / emulator H.264 → same RTP track.
//   - fallback:           JPEG-DC screenshot ONLY where neither exists.
//
// This file is the selection logic (pure, testable). The streaming wiring
// (recordVideo → Pion track) builds on top; the point here is that the agent
// must PICK the fast method per target instead of always screenshotting.
// See docs/architecture/WEBRTC_RN_SIMULATOR_STREAMING.md.

// CaptureMethod is how the agent grabs frames for a target.
type CaptureMethod string

const (
	// CaptureH264RecordVideo — iOS sim: continuous hardware-encoded H.264 via
	// `simctl io recordVideo`. Real-time; the ONLY sane iOS path (screenshot is
	// ~18s/frame). This is the premium Relay-Pro streaming seam.
	CaptureH264RecordVideo CaptureMethod = "h264-recordvideo"
	// CaptureH264Scrcpy — Android emulator / redroid: scrcpy H.264. redroid on a
	// Linux Cloud Workspace is a strong fit (cheap, containerized, streams fast).
	CaptureH264Scrcpy CaptureMethod = "h264-scrcpy"
	// CaptureJPEGScreenshot — last resort where no video encoder exists. Slow on
	// iOS (avoid); acceptable only for a target with no better option.
	CaptureJPEGScreenshot CaptureMethod = "jpeg-screenshot"
)

// preferredCaptureMethod picks the fastest capture for a target. Speed-first:
// H.264 video everywhere it exists, screenshot only as a genuine fallback.
func preferredCaptureMethod(targetID string) CaptureMethod {
	switch targetID {
	case "ios-simulator", "ipados-simulator", "watchos-simulator", "tvos-simulator", "visionos-simulator":
		// simctl recordVideo H.264 — NEVER screenshot iOS (18s/frame).
		return CaptureH264RecordVideo
	case "android-emulator", "android-wear", "android-tv", "android-xr", "android-auto", remoteRuntimeRedroidTargetID:
		return CaptureH264Scrcpy
	case desktopScreenTargetID, "browser-window":
		// Screen/browser targets keep the existing JPEG-DC path for now; they
		// don't have the 18s simulator-screenshot pathology.
		return CaptureJPEGScreenshot
	default:
		return CaptureJPEGScreenshot
	}
}

// captureIsRealtime reports whether a method delivers a live video stream (vs.
// per-frame grabs). Callers use it to decide the WebRTC transport
// (webrtc-rtp-h264 vs webrtc-datachannel-jpeg) and to warn when a target is
// stuck on the slow path.
func captureIsRealtime(m CaptureMethod) bool {
	return m == CaptureH264RecordVideo || m == CaptureH264Scrcpy
}
