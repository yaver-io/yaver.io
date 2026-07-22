package main

// remote_runtime_browser_test.go — interface-contract, capability probe and
// honest-skip e2e tests for the browser-window runtime target. The e2e launches
// a real browser when DiscoverChromeBinary finds one; otherwise it skips with a
// clear missing-capability reason rather than pretending the lane rendered.

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserWindowTargetImplementsRuntimeTarget(t *testing.T) {
	// Compile-time guarantee — assigning to a runtimeTarget variable
	// fails at build if the interface drifts.
	var _ runtimeTarget = browserWindowTarget{}
}

func TestBrowserWindowTargetRegistered(t *testing.T) {
	tgt, err := runtimeTargetFor("browser-window")
	if err != nil {
		t.Fatalf("runtimeTargetFor(browser-window) returned err: %v", err)
	}
	if _, ok := tgt.(browserWindowTarget); !ok {
		t.Fatalf("unexpected target type %T — expected browserWindowTarget", tgt)
	}
}

func TestBrowserFrameworkCapabilities(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/whatever", "browser")
	if !caps.RemoteRuntimeEligible {
		t.Fatalf("browser framework should be RemoteRuntimeEligible — capabilities = %+v", caps)
	}
	if caps.ExecutionMode != ExecutionModeNativeWebRTC {
		t.Fatalf("browser framework should be ExecutionModeNativeWebRTC, got %q", caps.ExecutionMode)
	}
	if len(caps.Targets) == 0 || caps.Targets[0].ID != "browser-window" {
		t.Fatalf("expected single browser-window target, got %+v", caps.Targets)
	}
}

func TestProbeBrowserWindowTargetShape(t *testing.T) {
	target := probeBrowserWindowTarget()
	if target.ID != "browser-window" {
		t.Fatalf("ID = %q, want browser-window", target.ID)
	}
	if target.Platform != "browser" {
		t.Fatalf("Platform = %q, want browser", target.Platform)
	}
	// Enabled depends on the test box. Reason must be populated when
	// disabled so the dashboard can render a usable error.
	if !target.Enabled && strings.TrimSpace(target.Reason) == "" {
		t.Fatalf("disabled target must carry a Reason explaining why")
	}
}

func TestBrowserPoolListEmpty(t *testing.T) {
	// Snapshot the pool count before our test ran (parallel test
	// processes won't share state but defensive coding helps when
	// somebody adds a t.Parallel() above).
	pool := &browserWindowPool{entries: map[string]*browserWindowEntry{}}
	if got := pool.list(); len(got) != 0 {
		t.Fatalf("fresh pool.list() = %v, want empty", got)
	}
}

func TestBrowserPoolCloseUnknown(t *testing.T) {
	pool := &browserWindowPool{entries: map[string]*browserWindowEntry{}}
	if pool.close("does-not-exist") {
		t.Fatalf("close on unknown id should return false")
	}
}

func TestOpsGlassPCVerbsRegistered(t *testing.T) {
	wantVerbs := []string{
		"glass_pc_open",
		"glass_pc_navigate",
		"glass_pc_focus",
		"glass_pc_close",
		"glass_pc_list",
		"glass_hud",
	}
	for _, name := range wantVerbs {
		opsRegistryMu.RLock()
		_, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Errorf("verb %q not registered in opsRegistry", name)
		}
	}
}

func TestHUDPayloadClampers(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := hudClampLine(long)
	if n := len([]rune(got)); n > hudMaxLineLen {
		t.Fatalf("hudClampLine returned %d runes (want ≤ %d)", n, hudMaxLineLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("hudClampLine should mark truncation with …; got %q", got)
	}
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line"
	}
	if got := hudClampLines(lines); len(got) != hudMaxLines {
		t.Fatalf("hudClampLines returned %d lines, want %d", len(got), hudMaxLines)
	}
}

func TestOpsGlassHUDBadView(t *testing.T) {
	// Stub server with no blackbox manager → handler should return
	// blackbox_missing. Confirms the early-exit path before we burn
	// payload unmarshal time.
	c := OpsContext{Server: &HTTPServer{}}
	body, _ := json.Marshal(map[string]any{"view": "notification", "payload": map[string]any{}})
	res := opsGlassHUDHandler(c, body)
	if res.OK {
		t.Fatalf("expected NOT OK when blackbox manager is nil; got %+v", res)
	}
	if res.Code != "blackbox_missing" {
		t.Fatalf("expected code blackbox_missing, got %q", res.Code)
	}
}

func TestBrowserWindowCreateRendersNonBlankAndAcceptsInput(t *testing.T) {
	if DiscoverChromeBinary() == "" {
		t.Skip("no usable Chrome/Chromium binary available — browser-window e2e requires a real browser")
	}
	port, stop := startBrowserLaneTestServer(t)
	defer stop()

	workDir := t.TempDir()
	mgr := NewRemoteRuntimeManager()
	mgr.SetDevServerManager(&fakeDevServer{
		running: true,
		workDir: workDir,
		devPort: port,
	})

	caps := remoteRuntimeCapabilitiesForProject(workDir, "flutter")
	if caps.FeedbackSurface != string(FeedbackInAppSDK) {
		t.Fatalf("browser-rendered flutter feedback surface = %q, want %q", caps.FeedbackSurface, FeedbackInAppSDK)
	}

	session, err := mgr.Create(workDir, "flutter", "browser-window", "direct-webrtc")
	if err != nil {
		t.Fatalf("Create browser-window: %v", err)
	}
	if session.DeviceID == "" {
		t.Fatalf("Create must attach a browser device; session=%+v", session)
	}
	t.Cleanup(func() { browserPool.close(session.DeviceID) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pngPath := t.TempDir() + "/browser-frame.png"
	if err := (browserWindowTarget{}).Screenshot(ctx, session.DeviceID, pngPath); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	img := readPNG(t, pngPath)
	if isUniformImage(img) {
		t.Fatalf("browser frame is uniform/blank after Create→Navigate; note=%q", session.Note)
	}

	if err := (browserWindowTarget{}).Text(ctx, session.DeviceID, "sdk-ok"); err != nil {
		t.Fatalf("Text input: %v", err)
	}
	entry, ok := browserPool.get(session.DeviceID)
	if !ok {
		t.Fatalf("browser pool lost session device %q", session.DeviceID)
	}
	var inputValue, transport string
	if err := chromedp.Run(entry.browserCtx,
		chromedp.Value(`#feedback-note`, &inputValue),
		chromedp.Evaluate(`window.__yaverFeedbackTransport`, &transport),
	); err != nil {
		t.Fatalf("inspect browser app state: %v", err)
	}
	if inputValue != "sdk-ok" {
		t.Fatalf("browser control input did not reach the app: input value=%q", inputValue)
	}
	if transport != string(FeedbackInAppSDK) {
		t.Fatalf("feedback transport marker = %q, want %q", transport, FeedbackInAppSDK)
	}
}

func startBrowserLaneTestServer(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test server: %v", err)
	}
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		t.Fatalf("split test server addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		ln.Close()
		t.Fatalf("parse test server port %q: %v", portStr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head>
  <title>Yaver Browser Lane Dogfood</title>
  <script>window.__yaverFeedbackTransport = "in-app-sdk";</script>
  <style>
    html, body { margin: 0; min-height: 100%; font: 24px system-ui, sans-serif; }
    body { background: linear-gradient(135deg, #0b5fff, #18b26b 48%, #f2c94c); color: white; }
    main { padding: 48px; }
    input { font: inherit; padding: 12px; width: 360px; }
  </style>
</head>
<body>
  <main>
    <h1>Yaver browser control</h1>
    <input id="feedback-note" autofocus aria-label="feedback note" />
  </main>
</body>
</html>`))
	})
	srv := &http.Server{Handler: mux}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ln)
		close(done)
	}()
	return port, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open png %s: %v", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode png %s: %v", path, err)
	}
	return img
}

func isUniformImage(img image.Image) bool {
	b := img.Bounds()
	if b.Empty() {
		return true
	}
	first := color.NRGBAModel.Convert(img.At(b.Min.X, b.Min.Y))
	for y := b.Min.Y; y < b.Max.Y; y += max(1, b.Dy()/24) {
		for x := b.Min.X; x < b.Max.X; x += max(1, b.Dx()/24) {
			if color.NRGBAModel.Convert(img.At(x, y)) != first {
				return false
			}
		}
	}
	return true
}
