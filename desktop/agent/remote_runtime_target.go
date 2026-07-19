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
	"errors"
	"fmt"
	"io"
	neturl "net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/yaver-io/agent/testkit"
)

// runtimeTarget abstracts one streamable device kind (iOS simulator,
// Android emulator, physical Android …). deviceID is the adb serial /
// simulator UDID resolved by Attach.
// errPinchUnsupported is returned by targets with no multi-touch primitive.
// Explicit refusal beats a silent no-op: a gesture that quietly does nothing is
// indistinguishable from a frozen stream, which is exactly the class of "the UI
// lied to me" bug this codebase keeps paying for.
var errPinchUnsupported = errors.New("this target cannot pinch: no multi-touch primitive available")

// errNavigateUnsupported is returned by targets that cannot be pointed at a URL.
var errNavigateUnsupported = errors.New("this target cannot navigate: no URL entry point available")

type runtimeTarget interface {
	// Attach boots (emulator/sim) or resolves (physical) the device
	// and returns its id.
	Attach(ctx context.Context) (string, error)

	Tap(ctx context.Context, deviceID string, x, y int) error
	Swipe(ctx context.Context, deviceID string, x1, y1, x2, y2, durationMs int) error
	Text(ctx context.Context, deviceID, text string) error
	Key(ctx context.Context, deviceID, key string) error

	// Pinch is the one gesture that cannot be expressed as a Swipe: it needs
	// two contact points moving simultaneously. scale > 1 zooms in (fingers
	// apart), scale < 1 zooms out (fingers together), centred on x,y.
	//
	// Kept as its own method rather than a generic multi-pointer event because
	// every platform below already exposes pinch as a primitive — CDP
	// Input.dispatchTouchEvent, uiautomator's pinchIn/pinchOut, XCUITest's
	// pinch(withScale:velocity:). Modelling raw pointer streams would mean
	// re-deriving each of those from scratch, which is the one thing worth
	// avoiding here.
	//
	// A target that genuinely cannot pinch returns errPinchUnsupported so the
	// caller can say so plainly instead of silently doing nothing — a gesture
	// that no-ops looks identical to a broken stream.
	Pinch(ctx context.Context, deviceID string, x, y int, scale float64, durationMs int) error

	// Navigate points the target at a URL. For browser-window this is the ONLY
	// way to show anything: chromedp opens about:blank and there was previously
	// no route to change that, so a browser session could be streamed and
	// clicked but never given content — every frame was a blank page, and input
	// that "succeeded" provably changed nothing.
	//
	// On the device targets this is deep-link entry (simctl openurl / am start
	// -a VIEW), which is the same primitive users already reach for when
	// testing a link into an app.
	//
	// Targets with no URL entry point return errNavigateUnsupported.
	Navigate(ctx context.Context, deviceID, url string) error

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
	case "android-wear":
		return androidSurfaceTarget{avdHint: "wear"}, nil
	case "android-tv":
		return androidSurfaceTarget{avdHint: "tv"}, nil
	case "android-xr":
		return androidSurfaceTarget{avdHint: "xr"}, nil
	case "android-auto":
		return androidSurfaceTarget{avdHint: "auto"}, nil
	case "android-device":
		return androidDeviceTarget{}, nil
	case remoteRuntimeRedroidTargetID:
		return redroidRuntimeTarget{}, nil
	case "ios-device":
		return iosDeviceTarget{}, nil
	case "browser-window":
		return browserWindowTarget{}, nil
	case desktopScreenTargetID:
		// The host's own desktop (remote_runtime_desktop.go). Gated by the
		// Remote Desktop consent policy, not `--ghost`.
		return desktopScreenTarget{}, nil
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

// Pinch on an iOS SIMULATOR.
//
// simctl has no gesture injection at all — it can boot, install and screenshot,
// but not touch. Real multi-touch on a simulator needs XCUITest
// (XCUIElement.pinch(withScale:velocity:)) driven from a test bundle attached
// to the app under test, which is a build-time relationship this streaming path
// does not have.
//
// So this refuses rather than pretending. A wrong-but-silent gesture here would
// be worse than none: the viewer would see a still frame and conclude the
// stream was dead.
func (iosSimulatorTarget) Pinch(_ context.Context, _ string, _, _ int, _ float64, _ int) error {
	return fmt.Errorf("%w: iOS Simulator needs an XCUITest bundle for pinch (simctl has no gesture API)", errPinchUnsupported)
}

// validateNavigateURL constrains navigation to http/https.
//
// This is a security boundary, not tidiness. The URL arrives over MCP/HTTP and
// is handed to a browser the operator is watching but not driving: `javascript:`
// would execute attacker-chosen script in that page's origin, and `file://`
// would turn a "show me a page" verb into an arbitrary local-file reader whose
// contents are then streamed back as video frames. Both are exfiltration
// primitives, so the scheme is allow-listed rather than deny-listed.
func validateNavigateURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := neturl.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("url is not parseable: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("url scheme %q is not allowed: only http and https can be navigated to "+
			"(javascript: and file: would run script or read local files in the streamed page)", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("url has no host: %q", trimmed)
	}
	return u.String(), nil
}

// Navigate on the iOS Simulator is `simctl openurl` — the same deep-link entry
// point Xcode uses. Unlike Pinch this genuinely exists in simctl, so it is
// implemented rather than refused.
func (iosSimulatorTarget) Navigate(ctx context.Context, deviceID, rawURL string) error {
	target, err := validateNavigateURL(rawURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(deviceID) == "" {
		return fmt.Errorf("navigate needs a booted simulator udid")
	}
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "openurl", deviceID, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl openurl failed: %v — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Navigate on Android is an ACTION_VIEW intent, which is how a link into an app
// is delivered on-device.
func (androidTarget) Navigate(ctx context.Context, deviceID, rawURL string) error {
	return androidNavigateViaIntent(ctx, deviceID, rawURL)
}

// Pinch on Android via uiautomator's own gesture engine.
//
// `adb shell input` deliberately has no multi-touch verb — two simultaneous
// contacts need either raw `sendevent` (device-specific, fragile, needs exact
// input-device nodes) or uiautomator, which ships on every Android image and
// exposes pinchOpen/pinchClose as primitives. Using uiautomator is the whole
// point of not reinventing this: the gesture math, timing and pointer
// interleaving are already correct there.
func (androidTarget) Pinch(ctx context.Context, deviceID string, x, y int, scale float64, durationMs int) error {
	return androidPinchViaUiautomator(ctx, deviceID, x, y, scale, durationMs)
}
