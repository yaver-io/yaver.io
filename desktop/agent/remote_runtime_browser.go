package main

// remote_runtime_browser.go — "browser-window" runtimeTarget.
//
// Each session = one headless Chromium tab driven via CDP (chromedp).
// Screen frames flow through the existing jpegDataChannelStreamer
// pump: the runtime framework calls Screenshot(deviceID, pngPath)
// at ~1.4 fps and ships the PNG → JPEG bytes over the frames DC.
// Pointer + keyboard events arrive on the events DC from
// remote_runtime_dispatch.go and are translated into CDP
// Input.dispatchMouseEvent / dispatchKeyEvent calls here.
//
// CanEncodeRTPH264 is false on purpose: shipping an x264 encoder
// in-process is a separate Phase. JPEG-DC at 30-60KB/frame is
// already enough for a usable browser quad in a Quest headset
// over LAN, and it reuses the exact same plumbing that already
// ships for the iOS-simulator viewer.
//
// URL is NOT taken via Attach — chromedp opens about:blank and
// navigation is a separate step. This matches how a real browser
// window works (open → then go) and avoids passing extra args
// through the runtimeTarget.Attach(ctx) signature, which is shared
// with every other target.
//
// That second step used to exist ONLY as ops "glass_pc_navigate",
// so a runtime session streaming this browser had no way to show
// anything: it rendered about:blank forever while tap/pinch cheerfully
// returned 200. runtimeTarget.Navigate now exposes the same
// browserPool.navigate primitive to the session itself.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

type browserWindowEntry struct {
	id            string
	allocCtx      context.Context
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc
	width         int
	height        int
	createdAt     time.Time
	lastUsedAt    time.Time
	url           string
}

type browserWindowPool struct {
	mu      sync.Mutex
	entries map[string]*browserWindowEntry
}

var browserPool = &browserWindowPool{entries: map[string]*browserWindowEntry{}}

func (p *browserWindowPool) open(ctx context.Context, width, height int) (*browserWindowEntry, error) {
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 800
	}
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("hide-scrollbars", false),
		chromedp.WindowSize(width, height),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)

	// Boot off browserCtx, NOT the request ctx.
	//
	// chromedp.Run only accepts a context created by chromedp.NewContext; give
	// it anything else and it returns ErrInvalidContext without ever launching
	// a browser. This previously derived from `ctx` (the inbound request), so
	// every browser-window session failed with:
	//
	//   launch headless chromium: invalid context (install Chrome or Chromium)
	//
	// — on a machine with Chrome installed at the standard macOS path. The
	// "install Chrome" hint sent you looking for a missing dependency that was
	// never missing.
	//
	// The request deadline still applies: browserCtx descends from allocCtx,
	// and the caller's cancellation is honoured via the timeout below plus the
	// pool's own lifecycle.
	// Boot on browserCtx ITSELF — not a timeout child.
	//
	// chromedp allocates the browser against whichever context you first Run
	// with. Passing a `context.WithTimeout(browserCtx, …)` child therefore ties
	// the browser process to that child, and the `defer cancel()` kills it the
	// moment this function returns: the session is created, then every frame
	// and control call fails with "context canceled".
	//
	// That is the second half of this bug. The first version passed the inbound
	// request ctx (never launched, ErrInvalidContext); the naive fix passed a
	// timeout child (launched, died immediately). Only the parent works, and
	// its lifetime is owned by the pool via browserCancel.
	//
	// The boot deadline is enforced with a watchdog instead of a context, so
	// nothing the browser is bound to ever gets cancelled.
	bootErr := make(chan error, 1)
	go func() { bootErr <- chromedp.Run(browserCtx) }()
	var bootFailure error
	select {
	case bootFailure = <-bootErr:
	case <-time.After(25 * time.Second):
		bootFailure = fmt.Errorf("timed out after 25s waiting for the browser to start")
	case <-ctx.Done():
		bootFailure = ctx.Err()
	}
	if err := bootFailure; err != nil {
		browserCancel()
		allocCancel()
		// Only claim the browser is missing when it actually is — otherwise
		// report what failed. Misattributing this cost real debugging time.
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), "executable file not found") {
			return nil, fmt.Errorf("launch headless chromium: %w (install Chrome or Chromium)", err)
		}
		return nil, fmt.Errorf("launch headless chromium: %w", err)
	}

	// Turn on touch emulation, or Pinch dispatches events that never become a
	// gesture.
	//
	// Input.dispatchTouchEvent DELIVERS touches to the page regardless, which is
	// why pinch appeared to work: the call returned 200 and the frame changed.
	// But Chrome only synthesises pinch-ZOOM from those touches when the target
	// is emulating a touch device — otherwise the page just handles them as
	// stray pointer input and the content shifts instead of scaling. Verified by
	// pinching a loaded page and watching the text disappear rather than grow.
	//
	// mobile:true is what enables viewport pinch-zoom semantics; touch points
	// must be >0 for the page to report touch support at all.
	if err := chromedp.Run(browserCtx,
		emulation.SetTouchEmulationEnabled(true).WithMaxTouchPoints(5),
		emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1, true),
	); err != nil {
		// Non-fatal: a browser that streams and taps is still useful, and
		// failing the whole session over a gesture nicety would be worse. But
		// say so, because the symptom (pinch silently not zooming) is otherwise
		// indistinguishable from the bug this replaced.
		log.Printf("[browser-window] touch emulation unavailable — pinch will not zoom: %v", err)
	}

	now := time.Now()
	entry := &browserWindowEntry{
		id:            "bw_" + uuid.NewString(),
		allocCtx:      allocCtx,
		allocCancel:   allocCancel,
		browserCtx:    browserCtx,
		browserCancel: browserCancel,
		width:         width,
		height:        height,
		createdAt:     now,
		lastUsedAt:    now,
	}
	p.mu.Lock()
	p.entries[entry.id] = entry
	p.mu.Unlock()
	return entry, nil
}

func (p *browserWindowPool) get(deviceID string) (*browserWindowEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[deviceID]
	if ok {
		e.lastUsedAt = time.Now()
	}
	return e, ok
}

func (p *browserWindowPool) close(deviceID string) bool {
	p.mu.Lock()
	e, ok := p.entries[deviceID]
	if ok {
		delete(p.entries, deviceID)
	}
	p.mu.Unlock()
	if !ok {
		return false
	}
	e.browserCancel()
	e.allocCancel()
	return true
}

func (p *browserWindowPool) list() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]any, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, map[string]any{
			"id":        e.id,
			"url":       e.url,
			"width":     e.width,
			"height":    e.height,
			"createdAt": e.createdAt.UTC().Format(time.RFC3339),
			"lastUsed":  e.lastUsedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (p *browserWindowPool) navigate(deviceID, url string) error {
	e, ok := p.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	if url == "" {
		return errors.New("url is required")
	}
	if err := chromedp.Run(e.browserCtx, chromedp.Navigate(url)); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}
	p.mu.Lock()
	e.url = url
	e.lastUsedAt = time.Now()
	p.mu.Unlock()
	return nil
}

// browserWindowTarget plugs into the runtimeTarget interface so the
// session pump in remote_runtime_webrtc.go works unchanged.
type browserWindowTarget struct{}

func (browserWindowTarget) Attach(ctx context.Context) (string, error) {
	entry, err := browserPool.open(ctx, 0, 0)
	if err != nil {
		return "", err
	}
	return entry.id, nil
}

func (browserWindowTarget) Tap(ctx context.Context, deviceID string, x, y int) error {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	return chromedp.Run(e.browserCtx, chromedp.MouseClickXY(float64(x), float64(y)))
}

func (browserWindowTarget) Swipe(ctx context.Context, deviceID string, x1, y1, x2, y2, durationMs int) error {
	// Browsers translate "swipe" to a scroll. dispatchMouseEvent with
	// type "mouseWheel" + a deltaY proportional to (y2-y1) does the
	// right thing for the common case of "drag finger up to scroll".
	e, ok := browserPool.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	dy := float64(y1 - y2)
	dx := float64(x1 - x2)
	return chromedp.Run(e.browserCtx, chromedp.ActionFunc(func(c context.Context) error {
		return input.DispatchMouseEvent(input.MouseWheel, float64(x1), float64(y1)).
			WithDeltaX(dx).
			WithDeltaY(dy).
			Do(c)
	}))
}

func (browserWindowTarget) Text(ctx context.Context, deviceID, text string) error {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	return chromedp.Run(e.browserCtx, chromedp.ActionFunc(func(c context.Context) error {
		return input.InsertText(text).Do(c)
	}))
}

func (browserWindowTarget) Key(ctx context.Context, deviceID, key string) error {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	// chromedp.KeyEvent understands Windows-style names ("Enter",
	// "Tab", "ArrowLeft" …) plus single characters. The HUD
	// KeyboardRouter ships symbols that match this set, so we pass
	// through without translation.
	return chromedp.Run(e.browserCtx, chromedp.KeyEvent(key))
}

func (browserWindowTarget) Screenshot(ctx context.Context, deviceID, pngPath string) error {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	var buf []byte
	if err := chromedp.Run(e.browserCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return fmt.Errorf("capture screenshot: %w", err)
	}
	if err := os.WriteFile(pngPath, buf, 0o600); err != nil {
		return fmt.Errorf("write png: %w", err)
	}
	return nil
}

func (browserWindowTarget) Dims(ctx context.Context, deviceID string) DeviceDims {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return DeviceDims{Width: 1280, Height: 800, Scale: 1.0, Rotation: "portrait"}
	}
	var width, height int64
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = chromedp.Run(probeCtx, chromedp.ActionFunc(func(c context.Context) error {
		_, _, _, _, _, contentSize, err := page.GetLayoutMetrics().Do(c)
		if err == nil && contentSize != nil {
			width = int64(contentSize.Width)
			height = int64(contentSize.Height)
		}
		return nil
	}))
	if width == 0 || height == 0 {
		width = int64(e.width)
		height = int64(e.height)
	}
	return DeviceDims{
		Width:    int(width),
		Height:   int(height),
		Scale:    1.0,
		Rotation: "landscape",
	}
}

func (browserWindowTarget) SpawnCapture(ctx context.Context, deviceID string) (*exec.Cmd, io.ReadCloser, error) {
	return nil, nil, errors.New("browser-window target does not support RTP H264 capture yet")
}

func (browserWindowTarget) NewNALReader(r io.Reader) (nalSource, error) {
	return nil, errors.New("browser-window target does not support RTP H264 capture yet")
}

func (browserWindowTarget) CanEncodeRTPH264() bool { return false }

// probeBrowserWindowTarget reports whether the host can launch
// chromedp's headless browser. We don't run a real browser to test
// — we just check that the binary chromedp would invoke exists.
// The browser-window family is enabled by default on every OS, and
// degrades to a clear error if no Chrome/Chromium is installed.
func probeBrowserWindowTarget() RemoteRuntimeTarget {
	target := RemoteRuntimeTarget{
		ID:               "browser-window",
		Label:            "Remote browser window",
		Platform:         "browser",
		RuntimeHostClass: "any",
		HostOS:           "any",
		RequiredCLI:      "google-chrome / chromium / msedge",
	}
	if !browserBinaryAvailable() {
		target.Enabled = false
		target.Reason = "No usable browser on this host. " + ChromeInstallHint()
		return target
	}
	target.Enabled = true
	return target
}

func browserBinaryAvailable() bool {
	// Prefer the VERIFIED probe: DiscoverChromeBinary runs `--version` and
	// requires the output to name chrome/chromium. A LookPath hit proves
	// nothing — Ubuntu's `chromium-browser` is a snap stub that resolves on
	// PATH and then refuses to launch, so a name-only check reports a usable
	// browser and the stream fails later with no explanation. See
	// chrome_install.go.
	if DiscoverChromeBinary() != "" {
		return true
	}
	// Edge and bare `chrome` are not covered by DiscoverChromeBinary, so they
	// still get a PATH lookup — but VERIFIED, never name-only. A LookPath hit
	// that cannot run is precisely what made this report a usable browser on a
	// box that had none.
	for _, bin := range []string{"microsoft-edge", "edge", "chrome"} {
		if p, err := exec.LookPath(bin); err == nil && chromeBinaryUsable(p) {
			return true
		}
	}
	// macOS app-bundle locations chromedp probes by default.
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	} {
		// Stat proves a file exists, not that it launches. Verify.
		if _, err := os.Stat(p); err == nil && chromeBinaryUsable(p) {
			return true
		}
	}
	return false
}

// captureScreenshotBase64 is a convenience used by the HUD push
// path to render a static thumbnail of the remote page (e.g. for
// the spatial view's "you are here" sidebar). Not on the hot path
// — the JPEG pump still owns frame delivery.
func captureBrowserScreenshotBase64(deviceID string) (string, error) {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return "", fmt.Errorf("browser-window %q not found", deviceID)
	}
	var buf []byte
	if err := chromedp.Run(e.browserCtx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// browserGetTitle is exposed to ops so the HUD view can display
// the page title alongside the URL.
func browserGetTitle(deviceID string) (string, error) {
	e, ok := browserPool.get(deviceID)
	if !ok {
		return "", fmt.Errorf("browser-window %q not found", deviceID)
	}
	var title string
	if err := chromedp.Run(e.browserCtx, chromedp.Title(&title)); err != nil {
		return "", err
	}
	return title, nil
}

// Pinch in a browser window via CDP's real touch API.
//
// This is the one target where multi-touch is genuinely first-class:
// Input.dispatchTouchEvent takes an ARRAY of touch points, so a pinch is just
// two points moving in opposite directions — no synthesis, no per-device
// quirks. chromedp is already a dependency and already drives Tap/Swipe here,
// so nothing new is introduced.
//
// Note this dispatches TOUCH, not mouse: Swipe above uses mouseWheel because
// scrolling is what a swipe means on a desktop page, but a pinch has no mouse
// analogue and pages listen for touchstart/touchmove to zoom.
// Navigate is what makes a browser-window session useful at all.
//
// chromedp opens about:blank and, until this existed, nothing in the
// remote-runtime API could change that: runtime_create took no url and the
// control verbs were tap/swipe/text/key. A session could therefore be created,
// streamed and clicked while every frame stayed blank — and because input
// returned 200, the lane looked healthy. The gap was found by pinching a
// session and getting a byte-identical frame back.
//
// browserPool.navigate already existed for the glass/AR-VR surface
// (ops_glass_pc.go); this exposes the same primitive to the runtime session
// that is being streamed, rather than adding a second mechanism.
func (browserWindowTarget) Navigate(_ context.Context, deviceID, rawURL string) error {
	target, err := validateNavigateURL(rawURL)
	if err != nil {
		return err
	}
	if _, ok := browserPool.get(deviceID); !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}
	return browserPool.navigate(deviceID, target)
}

func (browserWindowTarget) Pinch(ctx context.Context, deviceID string, x, y int, scale float64, durationMs int) error {
	if scale <= 0 {
		return fmt.Errorf("pinch scale must be > 0, got %v", scale)
	}
	e, ok := browserPool.get(deviceID)
	if !ok {
		return fmt.Errorf("browser-window %q not found", deviceID)
	}

	// Input.synthesizePinchGesture, NOT a hand-rolled pair of touch points.
	//
	// The first version of this dispatched two TouchPoints moving apart over 10
	// steps. Chrome delivered every one of those events to the page and NOTHING
	// ZOOMED: pinch-zoom is a compositor-level gesture, not something a page
	// derives from touch coordinates, so the events were consumed as ordinary
	// touch input and the content shifted instead of scaling. The call returned
	// 200 and the frame changed, which is exactly why it looked like it worked
	// — verified by pinching a loaded page and watching the text vanish rather
	// than grow. Enabling touch emulation did not fix it either.
	//
	// CDP already implements this gesture correctly, including the pointer
	// interleaving and compositor hand-off. Using it is the whole point of not
	// re-deriving a pinch from scratch.
	//
	// relativeSpeed converts the caller's duration into CDP's pixels/second:
	// the gesture travels roughly baseRadius*|scale-1| pixels, so speed =
	// distance/seconds keeps a longer durationMs meaning a slower pinch.
	if durationMs <= 0 {
		durationMs = 300
	}
	const baseRadius = 150.0
	distance := baseRadius * math.Abs(scale-1)
	if distance < 1 {
		distance = 1
	}
	speed := int64(distance / (float64(durationMs) / 1000.0))
	if speed < 50 {
		speed = 50
	}

	return chromedp.Run(e.browserCtx, chromedp.ActionFunc(func(c context.Context) error {
		return input.SynthesizePinchGesture(float64(x), float64(y), scale).
			WithRelativeSpeed(speed).
			Do(c)
	}))
}
