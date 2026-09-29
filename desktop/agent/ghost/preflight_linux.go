//go:build linux

package ghost

// preflight_linux.go — Linux capability truth.
//
// The Linux ghost is X11-only (xgb/XTest for capture+input, AT-SPI for the
// tree). Under a Wayland session the XWayland root window connects fine but
// contains no native-Wayland client pixels, so capture "succeeds" and returns a
// blank/partial desktop. That is a false green, so a Wayland session is
// reported as not streamable here — matching the desktop-screen target, which
// fails loudly for the same reason (remote_runtime_desktop.go desktopGrabArgs).

import (
	"os"
	"runtime"
)

func platformPreflight() PermissionStatus {
	st := PermissionStatus{Platform: runtime.GOOS}

	if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == "" {
		st.ScreenCapture = false
		st.Input = false
		st.ScreenCaptureReason = "Wayland session detected and no X11 DISPLAY: ghost's Linux backend is X11-only, so screen capture and input cannot work here. Log into an X11 session, or set DISPLAY to a real X server."
		st.InputReason = st.ScreenCaptureReason
		return st
	}
	if os.Getenv("DISPLAY") == "" {
		st.ScreenCapture = false
		st.Input = false
		st.ScreenCaptureReason = "no X11 DISPLAY set: a headless Linux box has no desktop to capture. Run the agent inside a graphical session."
		st.InputReason = st.ScreenCaptureReason
		return st
	}

	// X11 present. Capture/input are available; the AT-SPI tree additionally
	// needs python3-pyatspi + a running bus, which callers discover from the
	// tree call itself (tree_linux.go).
	st.ScreenCapture = true
	st.Input = true
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		// XWayland: X11 clients work, but native-Wayland windows are invisible
		// to X11 capture. Say so instead of pretending the capture is complete.
		st.ScreenCaptureReason = "Wayland session with XWayland: X11 capture works for X11 apps but native-Wayland windows will appear blank. Prefer an X11 session for full-desktop capture."
	}
	return st
}
