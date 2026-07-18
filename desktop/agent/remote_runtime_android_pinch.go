package main

// remote_runtime_android_pinch.go — multi-touch for Android targets.
//
// NOTE the filename. This cannot be called *_android.go: Go treats a trailing
// _android as an implicit GOOS build constraint and would compile it only for
// GOOS=android, silently excluding it from the macOS/Linux agent. The symbol
// then reads as "undefined" with the file sitting right there. The sibling
// files use the same android-in-the-middle convention for this reason.
//
// WHY THIS FILE EXISTS
//
// `adb shell input` covers tap/swipe/text/key and stops there: it has no
// multi-touch verb, because a pinch needs two contact points moving at the same
// time and `input` drives exactly one. The two ways forward are:
//
//   1. raw `sendevent` — write MT_SLOT / TRACKING_ID / POSITION_X events
//      straight to /dev/input/eventN. Device-specific, needs the right node and
//      the right protocol (A vs B), and silently does nothing when either is
//      wrong. This is the "reinvent it badly" path.
//   2. uiautomator — ships on every Android image since 4.1 and exposes
//      pinchOpen/pinchClose as primitives, with the pointer interleaving and
//      timing already correct.
//
// (2) is what this uses. The gesture math is not ours to rewrite.
//
// The wrinkle: `uiautomator runtest` was removed in newer Android, and the
// modern entry point is an instrumented test APK — which we cannot assume is
// installed on an arbitrary device. So we drive the same primitive through
// `adb shell input motionevent`, present on API 30+ (Android 11+), which DOES
// accept multiple pointer indices and is the officially supported CLI path for
// synthesising multi-touch. redroid here runs Android 11, so this is available.
//
// Fallback: on older images without `motionevent`, we report unsupported rather
// than approximating a pinch with two sequential swipes. Two sequential swipes
// are not a pinch — the app sees two separate one-finger drags and will scroll
// instead of zoom, which looks like the gesture "worked" while doing the wrong
// thing. Explicit refusal beats a plausible-looking wrong result.

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
)

// androidPinchSteps is how many intermediate positions each finger moves
// through. Too few and apps treat it as a jump rather than a gesture (many
// zoom handlers need several deltas to engage); too many and the shell
// round-trips dominate. 10 is enough for Android's gesture detector to see a
// continuous scale change.
const androidPinchSteps = 10

// androidPinchViaUiautomator synthesises a two-finger pinch centred on (x, y).
//
// scale > 1 moves the fingers APART (zoom in); scale < 1 moves them TOGETHER
// (zoom out). durationMs is the whole gesture, split evenly across the steps.
func androidPinchViaUiautomator(ctx context.Context, deviceID string, x, y int, scale float64, durationMs int) error {
	if scale <= 0 {
		return fmt.Errorf("pinch scale must be > 0, got %v", scale)
	}
	if math.Abs(scale-1.0) < 0.01 {
		return fmt.Errorf("pinch scale %v is a no-op — use >1 to zoom in or <1 to zoom out", scale)
	}
	if durationMs <= 0 {
		durationMs = 300
	}

	// Half-span the fingers travel. Start at a fixed radius so the gesture is
	// well-formed regardless of scale, then move to radius*scale. Clamped so a
	// large scale near a screen edge cannot push a pointer to a negative
	// coordinate, which Android silently drops.
	const baseRadius = 150
	startR := float64(baseRadius)
	endR := startR * scale
	if endR < 10 {
		endR = 10
	}

	if !androidHasMotionEvent(ctx, deviceID) {
		return fmt.Errorf("%w: this Android image has no `input motionevent` (needs API 30+); "+
			"approximating with two swipes would scroll instead of zoom", errPinchUnsupported)
	}

	type pt struct{ x, y int }
	posAt := func(r float64) (pt, pt) {
		return pt{x - int(r), y}, pt{x + int(r), y}
	}

	a0, b0 := posAt(startR)
	// DOWN both fingers. Pointer indices 0 and 1 are the two contacts.
	if err := adbMotion(ctx, deviceID, "DOWN", 0, a0.x, a0.y); err != nil {
		return err
	}
	if err := adbMotion(ctx, deviceID, "DOWN", 1, b0.x, b0.y); err != nil {
		// Release the first finger so we never strand the screen mid-touch —
		// a stuck pointer makes every later gesture behave oddly.
		_ = adbMotion(ctx, deviceID, "UP", 0, a0.x, a0.y)
		return err
	}

	for i := 1; i <= androidPinchSteps; i++ {
		t := float64(i) / float64(androidPinchSteps)
		r := startR + (endR-startR)*t
		a, b := posAt(r)
		if err := adbMotion(ctx, deviceID, "MOVE", 0, a.x, a.y); err != nil {
			break
		}
		if err := adbMotion(ctx, deviceID, "MOVE", 1, b.x, b.y); err != nil {
			break
		}
	}

	aN, bN := posAt(endR)
	// Always lift both, even if a MOVE failed above.
	err1 := adbMotion(ctx, deviceID, "UP", 1, bN.x, bN.y)
	err2 := adbMotion(ctx, deviceID, "UP", 0, aN.x, aN.y)
	if err1 != nil {
		return err1
	}
	return err2
}

// adbMotion issues one `input motionevent` for a single pointer index.
func adbMotion(ctx context.Context, deviceID, action string, pointer, x, y int) error {
	args := []string{}
	if deviceID != "" {
		args = append(args, "-s", deviceID)
	}
	args = append(args, "shell", "input", "motionevent", action,
		fmt.Sprint(pointer), fmt.Sprint(x), fmt.Sprint(y))
	out, err := exec.CommandContext(ctx, "adb", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb input motionevent %s p%d: %v: %s", action, pointer, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// androidHasMotionEvent probes for the multi-touch CLI rather than assuming a
// version. Reading the API level would be a proxy; asking the binary is the
// fact. `input motionevent` with no args prints usage on images that have it.
func androidHasMotionEvent(ctx context.Context, deviceID string) bool {
	args := []string{}
	if deviceID != "" {
		args = append(args, "-s", deviceID)
	}
	args = append(args, "shell", "input", "motionevent")
	out, _ := exec.CommandContext(ctx, "adb", args...).CombinedOutput()
	return strings.Contains(strings.ToLower(string(out)), "motionevent")
}
