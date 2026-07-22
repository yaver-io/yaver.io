//go:build integration

package main

// remote_runtime_browser_integration_test.go — full end-to-end loop
// for the "PC UI in glasses" browser-window target.
//
// Build-tagged "integration" because it actually:
//
//   1. Launches real headless Chromium via chromedp.
//   2. Stands up a real Pion PeerConnection on the test side.
//   3. Runs the agent's ApplyWebRTCOffer path → JPEG-DC streamer.
//   4. Waits until ≥ 1 JPEG frame arrives on the "frames" channel.
//
// Skipped from default `go test` runs because every dev box doesn't
// have Chrome on PATH. Run with:
//
//   go test -tags=integration -run TestBrowserWindowEndToEnd ./...
//
// Memory note: kivanc's mac stalls on full test suites — this is
// fenced behind a build tag for that exact reason.

import (
	"bytes"
	"context"
	"image/jpeg"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestBrowserWindowEndToEnd(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("no Chrome / Chromium binary available — skipping integration test")
	}

	// 1. Open a real headless browser window and navigate it to a
	//    data: URL so the screencast pump has something visible to
	//    capture. We use a noisy gradient so a blank screenshot can
	//    be distinguished from a real frame.
	openCtx, openCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer openCancel()
	entry, err := browserPool.open(openCtx, 640, 400)
	if err != nil {
		t.Fatalf("browserPool.open: %v", err)
	}
	t.Cleanup(func() { browserPool.close(entry.id) })

	dataURL := "data:text/html;charset=utf-8," +
		"<!doctype html><body style='margin:0;background:linear-gradient(45deg,%23ff3,%23f0a,%2306f);height:100vh'>" +
		"<h1 style='font:40px sans-serif;color:white;padding:24px'>yaver-test</h1></body>"
	if err := browserPool.navigate(entry.id, dataURL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// 2. Prime a remote-runtime session that points at the browser
	//    pool entry we just created. This skips the manager's
	//    Attach() path so we don't double-boot the browser.
	mgr := NewRemoteRuntimeManager()
	sessionID := "rr_test_browser_window"
	now := time.Now().UTC().Format(time.RFC3339)
	mgr.mu.Lock()
	mgr.sessions[sessionID] = RemoteRuntimeSession{
		ID:             sessionID,
		WorkDir:        "",
		Framework:      "browser",
		ExecutionMode:  ExecutionModeNativeWebRTC,
		TargetID:       "browser-window",
		TargetLabel:    "Browser",
		Platform:       "browser",
		TransportMode:  "direct-webrtc",
		FrameTransport: "webrtc-datachannel-jpeg-v1",
		Status:         "control-ready",
		DeviceID:       entry.id,
		CreatedAt:      now,
		UpdatedAt:      now,
		Note:           "primed by integration test",
	}
	mgr.live[sessionID] = &remoteRuntimeLiveState{
		sessionID: sessionID,
		targetID:  "browser-window",
		platform:  "browser",
		deviceID:  entry.id,
	}
	mgr.mu.Unlock()

	// 3. Stand up the "browser-side" Pion peer that the spatial VR
	//    quad would create. We don't ask for video — we want the
	//    JPEG-DC path because that's what the browser-window target
	//    publishes (CanEncodeRTPH264 returns false).
	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("client PC: %v", err)
	}
	defer clientPC.Close()
	if _, err := clientPC.CreateDataChannel("primer", nil); err != nil {
		t.Fatalf("client primer DC: %v", err)
	}

	frameCh := make(chan []byte, 4)
	eventCh := make(chan string, 16)
	clientPC.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			switch dc.Label() {
			case "frames":
				if !msg.IsString {
					select {
					case frameCh <- append([]byte(nil), msg.Data...):
					default:
					}
				}
			case "events":
				if msg.IsString {
					select {
					case eventCh <- string(msg.Data):
					default:
					}
				}
			}
		})
	})

	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gather := webrtc.GatheringCompletePromise(clientPC)
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local: %v", err)
	}
	<-gather
	finalOffer := *clientPC.LocalDescription()

	// 4. Run the production code path.
	_, answer, err := mgr.ApplyWebRTCOffer(sessionID, finalOffer)
	if err != nil {
		t.Fatalf("ApplyWebRTCOffer: %v", err)
	}
	if answer.Type != webrtc.SDPTypeAnswer {
		t.Fatalf("answer type = %s, want answer", answer.Type)
	}
	if err := clientPC.SetRemoteDescription(answer); err != nil {
		t.Fatalf("set remote: %v", err)
	}

	// 5. Wait until the frame pump emits something. 12 seconds is
	//    generous: the pump ticks at ~700ms and a fresh chromedp
	//    page typically captures < 200ms. If we time out, the JPEG
	//    encoder or the Screenshot impl regressed — both worth
	//    failing on.
	select {
	case data := <-frameCh:
		if !isProbableJPEG(data) {
			t.Fatalf("first frame is not a JPEG (len=%d, head=%v)", len(data), prefix(data, 6))
		}
		if len(data) < 1024 {
			t.Fatalf("frame too small (%d bytes) — likely a blank/black screenshot", len(data))
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("no JPEG frame after 12s — frame pump not running")
	}

	// 6. Bonus: the events channel should ship the agent's "ready"
	//    payload right after the connection completes. Verifying it
	//    catches drift in the transport string.
	deadline := time.After(3 * time.Second)
	gotReady := false
	for !gotReady {
		select {
		case ev := <-eventCh:
			if strings.Contains(ev, `"type":"ready"`) {
				gotReady = true
			}
		case <-deadline:
			t.Fatalf("never received `ready` event over `events` channel")
		}
	}
}

func TestBrowserWindowTapChangesTodoBackgroundOverWebRTC(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("no Chrome / Chromium binary available — skipping integration test")
	}

	openCtx, openCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer openCancel()
	entry, err := browserPool.open(openCtx, 640, 400)
	if err != nil {
		t.Fatalf("browserPool.open: %v", err)
	}
	t.Cleanup(func() { browserPool.close(entry.id) })

	page := `<!doctype html><html><head><style>
html,body{margin:0;width:100%;height:100%;background:rgb(220,20,20)}
body.done{background:rgb(20,180,60)}
main{width:100vw;height:100vh;display:grid;place-items:center}
button{width:320px;height:96px;border:0;background:white;color:#111;font:700 22px system-ui}
</style></head><body><main><button id="todo">Ship WebRTC todo</button></main>
<script>document.body.addEventListener("click",()=>document.body.classList.add("done"));</script>
</body></html>`
	if err := browserPool.navigate(entry.id, "data:text/html;charset=utf-8,"+url.QueryEscape(page)); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	mgr := NewRemoteRuntimeManager()
	sessionID := "rr_test_browser_color"
	now := time.Now().UTC().Format(time.RFC3339)
	mgr.mu.Lock()
	mgr.sessions[sessionID] = RemoteRuntimeSession{
		ID:             sessionID,
		Framework:      "browser",
		ExecutionMode:  ExecutionModeNativeWebRTC,
		TargetID:       "browser-window",
		TargetLabel:    "Browser",
		Platform:       "browser",
		TransportMode:  "direct-webrtc",
		FrameTransport: "webrtc-datachannel-jpeg-v1",
		Status:         "control-ready",
		DeviceID:       entry.id,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	mgr.live[sessionID] = &remoteRuntimeLiveState{
		sessionID: sessionID,
		targetID:  "browser-window",
		platform:  "browser",
		deviceID:  entry.id,
	}
	mgr.mu.Unlock()

	clientPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("client PC: %v", err)
	}
	defer clientPC.Close()
	if _, err := clientPC.CreateDataChannel("primer", nil); err != nil {
		t.Fatalf("client primer DC: %v", err)
	}

	frameCh := make(chan []byte, 16)
	clientPC.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != "frames" {
			return
		}
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if !msg.IsString {
				select {
				case frameCh <- append([]byte(nil), msg.Data...):
				default:
				}
			}
		})
	})

	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gather := webrtc.GatheringCompletePromise(clientPC)
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local: %v", err)
	}
	<-gather
	_, answer, err := mgr.ApplyWebRTCOffer(sessionID, *clientPC.LocalDescription())
	if err != nil {
		t.Fatalf("ApplyWebRTCOffer: %v", err)
	}
	if err := clientPC.SetRemoteDescription(answer); err != nil {
		t.Fatalf("set remote: %v", err)
	}

	started := time.Now()
	before := waitForBrowserColorFrame(t, frameCh, isRedRGB, 12*time.Second)
	t.Logf("before tap avg rgb=%+v", before)

	tapAt := time.Now()
	if err := (browserWindowTarget{}).Tap(context.Background(), entry.id, 320, 200); err != nil {
		t.Fatalf("tap: %v", err)
	}
	after := waitForBrowserColorFrame(t, frameCh, isGreenRGB, 12*time.Second)
	tapToGreen := time.Since(tapAt)
	total := time.Since(started)
	t.Logf("after tap avg rgb=%+v tapToGreen=%s totalColorLoop=%s", after, tapToGreen.Round(time.Millisecond), total.Round(time.Millisecond))
	if tapToGreen > 3*time.Second {
		t.Fatalf("tap-to-green took %s; browser-window dev loop should stay below 3s on the JPEG data-channel path", tapToGreen.Round(time.Millisecond))
	}
}

func TestBrowserPoolListReportsOpened(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("no Chrome / Chromium binary available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entry, err := browserPool.open(ctx, 0, 0)
	if err != nil {
		t.Fatalf("browserPool.open: %v", err)
	}
	t.Cleanup(func() { browserPool.close(entry.id) })

	listed := browserPool.list()
	found := false
	for _, row := range listed {
		if id, ok := row["id"].(string); ok && id == entry.id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("opened window %s not visible in browserPool.list()", entry.id)
	}
}

func TestBrowserScreenshotProducesPNG(t *testing.T) {
	if !browserBinaryAvailable() {
		t.Skip("no Chrome / Chromium binary available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entry, err := browserPool.open(ctx, 320, 240)
	if err != nil {
		t.Fatalf("browserPool.open: %v", err)
	}
	t.Cleanup(func() { browserPool.close(entry.id) })

	if err := browserPool.navigate(entry.id, "data:text/html,<body bgcolor=red>OK"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	tmpDir := t.TempDir()
	pngPath := tmpDir + "/frame.png"
	tgt := browserWindowTarget{}
	if err := tgt.Screenshot(ctx, entry.id, pngPath); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	data := readTestFile(t, pngPath)
	if len(data) < 100 {
		t.Fatalf("PNG too short (%d bytes) — capture likely failed", len(data))
	}
	// 0x89 'P' 'N' 'G' is the PNG magic.
	if !(data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G') {
		t.Fatalf("written file is not PNG (head=%v)", prefix(data, 6))
	}
}

func isProbableJPEG(b []byte) bool {
	return len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF
}

type avgRGB struct {
	R int
	G int
	B int
}

func waitForBrowserColorFrame(t *testing.T, frameCh <-chan []byte, pred func(avgRGB) bool, timeout time.Duration) avgRGB {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case data := <-frameCh:
			rgb, err := averageJPEGCenter(data)
			if err != nil {
				t.Fatalf("decode JPEG frame: %v", err)
			}
			if pred(rgb) {
				return rgb
			}
		case <-deadline:
			t.Fatalf("timed out waiting for matching color frame")
		}
	}
}

func averageJPEGCenter(data []byte) (avgRGB, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return avgRGB{}, err
	}
	b := img.Bounds()
	cropW := min(240, b.Dx())
	cropH := min(240, b.Dy())
	startX := b.Min.X + (b.Dx()-cropW)/2
	startY := b.Min.Y + (b.Dy()-cropH)/2
	var r, g, bl uint64
	var count uint64
	for y := startY; y < startY+cropH; y++ {
		for x := startX; x < startX+cropW; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r += uint64(cr >> 8)
			g += uint64(cg >> 8)
			bl += uint64(cb >> 8)
			count++
		}
	}
	return avgRGB{R: int(r / count), G: int(g / count), B: int(bl / count)}, nil
}

func isRedRGB(rgb avgRGB) bool {
	return rgb.R >= 150 && rgb.G <= 120 && rgb.B <= 120 && rgb.R > rgb.G+40
}

func isGreenRGB(rgb avgRGB) bool {
	return rgb.G >= 130 && rgb.R <= 140 && rgb.B <= 140 && rgb.G > rgb.R+40
}

func prefix(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
