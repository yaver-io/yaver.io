//go:build darwin && cgo

package ghost

// preflight_darwin.go — macOS permission truth.
//
// WHY THIS FILE EXISTS (2026-07-29 incident, re-verified 2026-09-29):
// the CoreGraphics injection helpers in input_darwin.go are `void` and
// CGEventPost reports nothing, so every macInput method returned nil. A
// TCC-denied Mac therefore answered POST /rd/input {"ok":true,"applied":N} while
// injecting nothing — a false green that reads to the user as "Yaver is broken
// and lying". AXIsProcessTrusted()/CGPreflightScreenCaptureAccess() are the
// operation-level probes that replace the proxy. This is the same law as the
// rest of the tree: probe the capability, never the inventory.

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CoreGraphics.h>

// ghost_ax_trusted: is THIS process allowed to inject input / read the AX tree?
static int ghost_ax_trusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

// ghost_screen_allowed: is THIS process allowed to capture the screen?
// CGPreflightScreenCaptureAccess is macOS 10.15+; the agent's floor is 11.0.
static int ghost_screen_allowed(void) { return CGPreflightScreenCaptureAccess() ? 1 : 0; }

// ghost_ax_trusted_state: 0 = not trusted, 1 = trusted. Kept separate from the
// prompt-capable variant so callers never trigger a TCC prompt by accident.
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
)

func permissionSubject() string {
	if p, err := os.Executable(); err == nil && p != "" {
		return p
	}
	return "the Yaver agent"
}

func platformPreflight() PermissionStatus {
	st := PermissionStatus{Platform: runtime.GOOS}
	st.ScreenCapture = C.ghost_screen_allowed() == 1
	st.Input = C.ghost_ax_trusted() == 1
	if !st.ScreenCapture {
		st.ScreenCaptureReason = fmt.Sprintf(
			"macOS Screen Recording permission is not granted to %s. Grant it in System Settings → Privacy & Security → Screen Recording, then quit and relaunch the agent (macOS does not apply the grant to an already-running process).",
			permissionSubject())
	}
	if !st.Input {
		st.InputReason = fmt.Sprintf(
			"macOS Accessibility permission is not granted to %s, so clicks and keystrokes are NOT delivered. Grant it in System Settings → Privacy & Security → Accessibility, then quit and relaunch the agent.",
			permissionSubject())
	}
	return st
}

// macInputPreflight is called before every injection so a denied Mac returns a
// real error instead of the historical silent success. It is cheap
// (AXIsProcessTrusted is a cached TCC lookup) and intentionally not memoized:
// granting permission mid-session must start working, and revoking it must
// start failing.
func macInputPreflight() error {
	if C.ghost_ax_trusted() == 1 {
		return nil
	}
	return fmt.Errorf("ghost: %s", platformPreflight().InputReason)
}

// macScreenPreflight is the capture-side twin. Capture already surfaces a raw
// CGDisplayCreateImage failure; this names the cause before we even try.
func macScreenPreflight() error {
	if C.ghost_screen_allowed() == 1 {
		return nil
	}
	return fmt.Errorf("ghost: %s", platformPreflight().ScreenCaptureReason)
}
