package ghost

import (
	"runtime"
	"testing"
)

// TestPreflightShape asserts the invariant every surface relies on: when the
// ghost reports it cannot do something, it also names WHY. A false capability
// with an empty reason is the "inventory says yes, operation says no" shape
// this whole probe exists to eliminate.
//
// It deliberately does NOT assert the boolean values: on a macOS+cgo build they
// reflect the real TCC state of the test process, which is environment-
// dependent. The shape is what must hold everywhere.
func TestPreflightShape(t *testing.T) {
	st := Preflight()
	if st.Platform != runtime.GOOS {
		t.Fatalf("Platform = %q, want %q", st.Platform, runtime.GOOS)
	}
	if !st.ScreenCapture && st.ScreenCaptureReason == "" {
		t.Error("ScreenCapture=false must carry a reason naming the fix")
	}
	if !st.Input && st.InputReason == "" {
		t.Error("Input=false must carry a reason naming the fix")
	}
	if st.ScreenCapture && st.ScreenCaptureReason != "" {
		// A reason alongside true is allowed only as a caveat (e.g. XWayland
		// partial capture), never as a denial. Keep it non-fatal but visible.
		t.Logf("note: ScreenCapture=true with caveat: %s", st.ScreenCaptureReason)
	}
}
