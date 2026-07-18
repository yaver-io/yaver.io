package main

// remote_runtime_ios_device.go — Phase 3 of
// docs/physical-device-remote-runtime.md.
//
// `ios-device` streams + controls a PHYSICAL iPhone/iPad. Unlike the
// simulator (`xcrun simctl`, macOS-only, in-process), a real device
// has no simctl: control + capture must go through WebDriverAgent
// (see wda_client.go) reached over the usbmuxd TCP tunnel.
//
// Phase 2's runtimeTarget interface is what makes this a *new file*
// instead of a 10th edit to nine switch statements: iosDeviceTarget
// is one more impl + one more runtimeTargetFor arm.
//
// SCOPE / what is and isn't validated here:
//   - Control/dims/screenshot go through the WDA HTTP client, which
//     is fully tested against an httptest fake WDA (wda_client_test
//     + this file's test) — the request contract is verified.
//   - Capture is ffmpeg transcoding WDA's MJPEG server to Annex-B
//     H.264 (mirrors spawnAdbScreenrecord). The command is built and
//     unit-checked; end-to-end pixels require a signed WDA on a wired
//     iPhone + a Mac, which can't run in CI/this session. That last
//     mile (WDA install/sign + on-device ffmpeg) is the documented
//     out-of-session step in the Phase 3 task.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const remoteRuntimeIOSDeviceTargetID = "ios-device"

// wdaMjpegDefaultURL is WDA's conventional MJPEG server address (a
// separate port from the :8100 control API). YAVER_WDA_MJPEG_URL
// overrides it (tests + non-default usbmuxd forwards).
const wdaMjpegDefaultURL = "http://localhost:9100"

func wdaMjpegURL() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_WDA_MJPEG_URL")); v != "" {
		return v
	}
	return wdaMjpegDefaultURL
}

// Cache one wdaClient per base URL so a session (POST /session) is
// reused across taps instead of re-created every interaction — WDA
// session setup is slow and re-doing it per tap is visibly laggy.
var (
	wdaClientsMu sync.Mutex
	wdaClients   = map[string]*wdaClient{}
)

func wdaClientFor(base string) *wdaClient {
	wdaClientsMu.Lock()
	defer wdaClientsMu.Unlock()
	if c := wdaClients[base]; c != nil {
		return c
	}
	c := newWDAClient(base)
	wdaClients[base] = c
	return c
}

// attachedIOSDevices returns physical iPhones/iPads, USB first then
// wifi (lower latency cabled). Mirrors attachedAndroidDevices.
func attachedIOSDevices(ctx context.Context) []wireDevice {
	devs := append([]wireDevice{}, listIOSWireDevices(ctx)...)
	seen := map[string]bool{}
	for _, d := range devs {
		seen[d.UDID] = true
	}
	for _, d := range listIOSWirelessDevices(ctx) {
		if !seen[d.UDID] {
			devs = append(devs, d)
			seen[d.UDID] = true
		}
	}
	return devs
}

func resolveAttachedIOSDeviceUDID(ctx context.Context) (string, error) {
	for _, d := range attachedIOSDevices(ctx) {
		if d.UDID != "" {
			return d.UDID, nil
		}
	}
	return "", fmt.Errorf("no physical iPhone/iPad attached — connect one over USB (`yaver wire`), trust the Mac, and ensure WebDriverAgent is installed + forwarded")
}

// probeIOSDeviceTarget mirrors probeIOSSimulatorTarget/
// probeAndroidDeviceTarget. macOS-gated (devicectl + WDA build need
// Xcode) + a real device must be attached.
func probeIOSDeviceTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               remoteRuntimeIOSDeviceTargetID,
		Label:            "iPhone/iPad (physical) over WebRTC",
		Platform:         "ios",
		RuntimeHostClass: "macos-ios",
		HostOS:           runtime.GOOS,
		RequiredCLI:      "xcrun devicectl + WebDriverAgent",
	}
	if runtime.GOOS != "darwin" {
		target.Enabled = false
		target.Reason = "Requires a macOS host with Xcode (real-device control needs WebDriverAgent built + signed)."
		return target
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		target.Enabled = false
		target.Reason = "xcrun not found. Install Xcode command line tools or Xcode."
		return target
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := resolveAttachedIOSDeviceUDID(ctx); err != nil {
		target.Enabled = false
		target.Reason = err.Error()
		return target
	}
	target.Enabled = true
	return target
}

// iosDeviceTarget implements runtimeTarget over WebDriverAgent.
type iosDeviceTarget struct{}

func (iosDeviceTarget) wda() *wdaClient { return wdaClientFor(wdaBaseURL()) }

func (t iosDeviceTarget) Attach(ctx context.Context) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("ios-device requires a macOS host with Xcode + WebDriverAgent")
	}
	udid, err := resolveAttachedIOSDeviceUDID(ctx)
	if err != nil {
		return "", err
	}
	// WDA must be up + forwarded before any control works; fail
	// fast with an actionable message instead of at first tap.
	if err := t.wda().Status(ctx); err != nil {
		return "", fmt.Errorf("WebDriverAgent unreachable at %s: %w — build/run WDA on the device and forward its port (usbmuxd)", wdaBaseURL(), err)
	}
	return udid, nil
}

func (t iosDeviceTarget) Tap(ctx context.Context, _ string, x, y int) error {
	return t.wda().Tap(ctx, x, y)
}
func (t iosDeviceTarget) Swipe(ctx context.Context, _ string, x1, y1, x2, y2, durationMs int) error {
	// Real iOS *does* support drag via WDA — unlike the simulator,
	// which still returns "not implemented".
	return t.wda().Swipe(ctx, x1, y1, x2, y2, durationMs)
}
func (t iosDeviceTarget) Text(ctx context.Context, _, text string) error {
	return t.wda().Text(ctx, text)
}
func (t iosDeviceTarget) Key(ctx context.Context, _, key string) error {
	return t.wda().PressButton(ctx, key)
}
func (t iosDeviceTarget) Screenshot(ctx context.Context, _, pngPath string) error {
	png, err := t.wda().Screenshot(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(pngPath, png, 0o644)
}
func (t iosDeviceTarget) Dims(ctx context.Context, _ string) DeviceDims {
	w, h, err := t.wda().WindowSize(ctx)
	if err != nil || w == 0 || h == 0 {
		return fallbackDims
	}
	rot := "portrait"
	if w > h {
		rot = "landscape"
	}
	// WDA reports points; @3x is the right informational default for
	// modern iPhones (Scale is non-load-bearing, see DeviceDims).
	return DeviceDims{Width: w, Height: h, Scale: 3, Rotation: rot}
}

func (iosDeviceTarget) SpawnCapture(ctx context.Context, _ string) (*exec.Cmd, io.ReadCloser, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, nil, fmt.Errorf("ffmpeg not on PATH — run `yaver install remote-runtime` (ffmpeg transcodes WDA's MJPEG stream to H.264)")
	}
	// Read WDA's MJPEG server, emit raw Annex-B H.264 to stdout —
	// same contract spawnAdbScreenrecord gives the pump, so the
	// NAL reader + RTP path are unchanged. zerolatency/ultrafast
	// because this is a live interactive stream, not an archive.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-loglevel", "error",
		"-f", "mjpeg", "-i", wdaMjpegURL(),
		"-an",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p",
		"-f", "h264", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, stdout, nil
}

func (iosDeviceTarget) NewNALReader(r io.Reader) (nalSource, error) {
	return NewAnnexBReader(r), nil
}

func (iosDeviceTarget) CanEncodeRTPH264() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// Pinch on a PHYSICAL iOS device.
//
// Same wall as the simulator, for the same reason: Apple exposes no public
// gesture-injection API outside XCUITest, and XCUITest requires a test bundle
// signed and installed alongside the app. WebDriverAgent (what Appium drives)
// is exactly that bundle — so supporting pinch here means adopting WDA, which
// is a real dependency decision rather than a few lines.
//
// Until that decision is made, refuse honestly. Silently doing nothing would
// present as a frozen stream.
// Navigate is refused on a physical iOS device: simctl openurl is
// simulator-only, and the WDA bridge here exposes input, not URL entry.
// Refusing beats a silent no-op, which would look like a frozen stream.
func (t iosDeviceTarget) Navigate(_ context.Context, _, _ string) error {
	return fmt.Errorf("%w: physical iOS devices have no simctl openurl equivalent here (simctl is simulator-only)", errNavigateUnsupported)
}

func (t iosDeviceTarget) Pinch(_ context.Context, _ string, _, _ int, _ float64, _ int) error {
	return fmt.Errorf("%w: physical iOS needs WebDriverAgent/XCUITest for pinch", errPinchUnsupported)
}
