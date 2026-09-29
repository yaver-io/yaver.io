//go:build !windows && !linux && (!darwin || !cgo)

package ghost

// preflight_other.go — the unsupported-platform stub.
//
// This build tag matches any platform without a ghost backend, INCLUDING
// macOS built with CGO_ENABLED=0. That combination was the production reality
// until 2026-09-29: release-cli.yml and build-cli-native.sh both built darwin
// with CGO_ENABLED=0, so every installed Mac agent had screen capture, input
// injection and the AX tree compiled to ErrUnsupported while the docs, the
// mobile app and the web dashboard all advertised desktop control. The reason
// string below names the fix so a run that lands here says why.

import "runtime"

func platformPreflight() PermissionStatus {
	reason := "ghost has no backend for this build: screen capture and input are unavailable on " + runtime.GOOS + "."
	if runtime.GOOS == "darwin" {
		reason = "this macOS agent was built with CGO_ENABLED=0, so the ghost backend (CoreGraphics capture/input, AX tree) was compiled out. Rebuild/install a release built with CGO_ENABLED=1 — the official darwin binaries are."
	}
	return PermissionStatus{
		Platform:            runtime.GOOS,
		ScreenCapture:       false,
		Input:               false,
		ScreenCaptureReason: reason,
		InputReason:         reason,
	}
}
