//go:build windows

package ghost

// preflight_windows.go — Windows capability truth.
//
// Windows has no TCC layer: GDI BitBlt capture and SendInput injection are
// available to any process in the session. The one real caveat is that the
// agent must be running in an UNLOCKED INTERACTIVE desktop session — a Windows
// service in session 0 can capture a blank desktop and SendInput goes nowhere.
// We cannot read the session id without more syscalls than this is worth, so we
// report the primitives as available and let the first capture/input call name
// the truth (the audit's "probe the operation" rule). The doctor
// (doctor_windows_byo_windows.go) owns the deeper session check.

import "runtime"

func platformPreflight() PermissionStatus {
	return PermissionStatus{Platform: runtime.GOOS, ScreenCapture: true, Input: true}
}
