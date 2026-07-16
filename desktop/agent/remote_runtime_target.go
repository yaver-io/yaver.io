package main

// remote_runtime_target.go — Phase 2 of
// docs/physical-device-remote-runtime.md.
//
// Before this, every per-target operation (attach, tap, swipe, text,
// key, screenshot, dims, capture, NAL framing, RTP-encodability) was a
// copy-pasted `switch targetID { case "ios-simulator" / "android-*" }`
// duplicated across remote_runtime_webrtc.go, _video_track.go and
// _dims.go — 9 sites. Adding ios-device (Phase 3) would have meant
// touching all 9 again. `runtimeTarget` collapses the matrix into one
// interface with one impl per target; the call sites become a single
// `runtimeTargetFor(id)` dispatch.
//
// STRICTLY behaviour-preserving: each method body is the *exact* code
// that used to live in the corresponding switch arm (same testkit
// driver construction, same error strings, same fallbacks). This is
// orthogonal to remoteRuntimeStreamer, which owns *transport* (RTP vs
// JPEG); runtimeTarget owns the *device*. The two compose cleanly.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/yaver-io/agent/testkit"
)

// runtimeTarget abstracts one streamable device kind (iOS simulator,
// Android emulator, physical Android …). deviceID is the adb serial /
// simulator UDID resolved by Attach.
type runtimeTarget interface {
	// Attach boots (emulator/sim) or resolves (physical) the device
	// and returns its id.
	Attach(ctx context.Context) (string, error)

	Tap(ctx context.Context, deviceID string, x, y int) error
	Swipe(ctx context.Context, deviceID string, x1, y1, x2, y2, durationMs int) error
	Text(ctx context.Context, deviceID, text string) error
	Key(ctx context.Context, deviceID, key string) error

	// Screenshot writes a PNG (JPEG data-channel path).
	Screenshot(ctx context.Context, deviceID, pngPath string) error
	// Dims is the raw geometry probe; the empty-deviceID/timeout
	// guards stay in ProbeDeviceDims.
	Dims(ctx context.Context, deviceID string) DeviceDims

	// SpawnCapture + NewNALReader feed the RTP H.264 pump.
	SpawnCapture(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error)
	NewNALReader(r io.Reader) (nalSource, error)
	// CanEncodeRTPH264 reports whether this host can produce an RTP
	// H.264 track for the target right now.
	CanEncodeRTPH264() bool
}

// runtimeTargetFor maps a session targetID to its implementation.
// Unknown ids error — matching the old `default:` switch arms.
func runtimeTargetFor(targetID string) (runtimeTarget, error) {
	switch targetID {
	case "ios-simulator":
		return iosSimulatorTarget{deviceType: "iPhone"}, nil
	case "ipados-simulator":
		return iosSimulatorTarget{deviceType: "iPad"}, nil
	case "watchos-simulator":
		return iosSimulatorTarget{deviceType: "Apple Watch"}, nil
	case "tvos-simulator":
		return iosSimulatorTarget{deviceType: "Apple TV"}, nil
	case "visionos-simulator":
		return iosSimulatorTarget{deviceType: "Apple Vision"}, nil
	case "android-emulator":
		return androidEmulatorTarget{}, nil
	case "android-device":
		return androidDeviceTarget{}, nil
	case remoteRuntimeRedroidTargetID:
		return redroidRuntimeTarget{}, nil
	case "ios-device":
		return iosDeviceTarget{}, nil
	case "browser-window":
		return browserWindowTarget{}, nil
	}
	// "stream-<source>" — a Yaver stream source (capture/screen/scene/<pushed>)
	// exposed as a one-way H264 capture for the self-contained WebRTC path
	// (stream_webrtc.go). Decoupled from the interactive session targets above.
	if strings.HasPrefix(targetID, "stream-") {
		return streamSourceTarget{source: strings.TrimPrefix(targetID, "stream-")}, nil
	}
	return nil, fmt.Errorf("unknown remote runtime target %q", targetID)
}

// ---- iOS Simulator -------------------------------------------------

// iosSimulatorTarget dispatches to every Apple simulator family. The
// simctl driver is runtime-agnostic (see testkit/driver_iossim.go) —
// picking a `iPhone` vs `iPad` vs `Apple Watch` UDID is enough to boot
// the right sim; everything downstream (tap/screenshot/dims) takes the
// resolved UDID and is device-type-agnostic. Empty deviceType keeps
// backward compatibility with older ios-simulator dispatch arms.
type iosSimulatorTarget struct{ deviceType string }

func (t iosSimulatorTarget) Attach(ctx context.Context) (string, error) {
	return (&testkit.IOSSimDriver{DeviceType: t.deviceType}).Boot(ctx)
}
func (iosSimulatorTarget) Tap(ctx context.Context, deviceID string, x, y int) error {
	if err := (&testkit.IOSSimDriver{}).Tap(ctx, deviceID, x, y); err == nil {
		return nil
	}
	if err := wdaClientFor(wdaBaseURL()).Tap(ctx, x, y); err != nil {
		return fmt.Errorf("ios-simulator tap requires simctl tap or WebDriverAgent (`yaver install wda`): %w", err)
	}
	return nil
}
func (iosSimulatorTarget) Swipe(ctx context.Context, _ string, x1, y1, x2, y2, durationMs int) error {
	if err := wdaClientFor(wdaBaseURL()).Swipe(ctx, x1, y1, x2, y2, durationMs); err != nil {
		return fmt.Errorf("ios-simulator swipe requires WebDriverAgent (`yaver install wda`): %w", err)
	}
	return nil
}
func (iosSimulatorTarget) Text(ctx context.Context, deviceID, text string) error {
	if err := (&testkit.IOSSimDriver{}).SendText(ctx, deviceID, text); err == nil {
		return nil
	}
	return wdaClientFor(wdaBaseURL()).Text(ctx, text)
}
func (iosSimulatorTarget) Key(ctx context.Context, _ string, key string) error {
	if err := wdaClientFor(wdaBaseURL()).PressButton(ctx, key); err != nil {
		return fmt.Errorf("ios-simulator key %q requires WebDriverAgent (`yaver install wda`): %w", key, err)
	}
	return nil
}
func (iosSimulatorTarget) Screenshot(ctx context.Context, deviceID, pngPath string) error {
	if png, err := wdaClientFor(wdaBaseURL()).Screenshot(ctx); err == nil {
		return os.WriteFile(pngPath, png, 0o644)
	}
	return (&testkit.IOSSimDriver{}).Screenshot(ctx, deviceID, pngPath)
}
func (iosSimulatorTarget) Dims(ctx context.Context, deviceID string) DeviceDims {
	if w, h, err := wdaClientFor(wdaBaseURL()).WindowSize(ctx); err == nil && w > 0 && h > 0 {
		rot := "portrait"
		if w > h {
			rot = "landscape"
		}
		return DeviceDims{Width: w, Height: h, Scale: 3, Rotation: rot}
	}
	return probeIOSDims(ctx, deviceID)
}
func (iosSimulatorTarget) SpawnCapture(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	return spawnXcrunRecordVideo(ctx, deviceID)
}
func (iosSimulatorTarget) NewNALReader(r io.Reader) (nalSource, error) { return MP4ToAnnexB(r) }
func (iosSimulatorTarget) CanEncodeRTPH264() bool {
	// Xcode 26's simctl no longer supports recordVideo to stdout
	// ("rendering to standard out is no longer supported"). The RTP
	// pump needs a streaming pipe, so keep iOS on WebRTC JPEG
	// data-channel frames until we replace this with a file-backed
	// fragment tailer or another live capture source.
	return false
}

// ---- Android (shared emulator + physical device) -------------------

// androidTarget holds every operation that is identical between an
// emulator serial and a physical-device serial — which is all of them
// except Attach. `adb -s <serial> …` does not care whether the serial
// is `emulator-5554` or `R52W60BEDXD`/`192.168.1.7:5555`.
type androidTarget struct{}

func (androidTarget) Tap(ctx context.Context, deviceID string, x, y int) error {
	return (&testkit.AndroidEmuDriver{}).Tap(ctx, deviceID, x, y)
}
func (androidTarget) Swipe(ctx context.Context, deviceID string, x1, y1, x2, y2, durationMs int) error {
	return (&testkit.AndroidEmuDriver{}).Swipe(ctx, deviceID, x1, y1, x2, y2, durationMs)
}
func (androidTarget) Text(ctx context.Context, deviceID, text string) error {
	return (&testkit.AndroidEmuDriver{}).Text(ctx, deviceID, text)
}
func (androidTarget) Key(ctx context.Context, deviceID, key string) error {
	driver := &testkit.AndroidEmuDriver{}
	if keycode, ok := androidKeycodeForName(key); ok {
		return driver.KeyEvent(ctx, deviceID, keycode)
	}
	// Numeric escape hatch — `{"action":"key","key":"82"}` still
	// works for any KEYCODE_* the user wants, even ones we don't
	// have a friendly name for.
	if code, err := strconv.Atoi(strings.TrimSpace(key)); err == nil {
		return driver.KeyEvent(ctx, deviceID, code)
	}
	return fmt.Errorf("unsupported key %q", key)
}
func (androidTarget) Screenshot(ctx context.Context, deviceID, pngPath string) error {
	return (&testkit.AndroidEmuDriver{}).Screenshot(ctx, deviceID, pngPath)
}
func (androidTarget) Dims(ctx context.Context, deviceID string) DeviceDims {
	return probeAndroidDims(ctx, deviceID)
}
func (androidTarget) SpawnCapture(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	return spawnAdbScreenrecord(ctx, deviceID)
}
func (androidTarget) NewNALReader(r io.Reader) (nalSource, error) { return NewAnnexBReader(r), nil }
func (androidTarget) CanEncodeRTPH264() bool {
	_, err := exec.LookPath("adb")
	return err == nil
}

// androidEmulatorTarget boots an AVD.
type androidEmulatorTarget struct{ androidTarget }

func (androidEmulatorTarget) Attach(ctx context.Context) (string, error) {
	return (&testkit.AndroidEmuDriver{}).Boot(ctx)
}

// androidDeviceTarget resolves an already-attached physical serial —
// there is no AVD to boot.
type androidDeviceTarget struct{ androidTarget }

func (androidDeviceTarget) Attach(ctx context.Context) (string, error) {
	return resolveAttachedAndroidDeviceSerial(ctx)
}
