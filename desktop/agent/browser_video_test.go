package main

// browser_video_test.go — end-to-end proof that a recorded browser session
// produces a playable MP4 clip, served through the existing clip store. Mirrors
// the manual Playwright-on-a-box proof, but in-process and against a LOCAL
// httptest server (no third-party traffic, per the repo do-no-harm rule).
//
// Skips cleanly when ffmpeg or Chrome/Chromium isn't available (CI without a
// browser), so it never blocks the build.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserVideoRecorder_E2E(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH — skipping browser video e2e")
	}

	// A tiny self-contained page that visibly changes, so the recording has
	// motion to encode (not a single static frame).
	const pageHTML = `<!doctype html><html><head><meta charset=utf-8>
<title>Yaver Recorder Test</title>
<style>body{font:48px system-ui;margin:0;display:grid;place-items:center;height:100vh}</style></head>
<body><div id=c>0</div>
<script>let n=0;setInterval(()=>{n++;document.getElementById('c').textContent=n;
document.body.style.background='hsl('+(n*30%360)+',70%,60%)';},120);</script>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, pageHTML)
	}))
	defer srv.Close()

	bm := NewBrowserManager()
	defer bm.Stop()
	vpm := NewVibePreviewManager(bm)
	bm.SetVibePreviewManager(vpm)

	const sid = "rec-e2e"
	if err := bm.OpenSession(sid, false); err != nil {
		t.Skipf("Chrome/Chromium unavailable, skipping: %v", err)
	}

	clipID, err := bm.StartRecording(sid, 30)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if clipID == "" {
		t.Fatal("StartRecording returned empty clip id")
	}

	// While recording, drive the page so the video captures real activity.
	if _, err := bm.Navigate(sid, srv.URL); err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := bm.Evaluate(sid, "window.scrollBy(0,10)"); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	// CloseSession finalizes the recording (flushes ffmpeg) before tearing down.
	if err := bm.CloseSession(sid); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	rec := vpm.ClipByID(clipID)
	if rec == nil {
		t.Fatal("clip not registered in store")
	}
	if rec.Status != "ready" {
		t.Fatalf("clip status = %q (err=%q), want ready", rec.Status, rec.Err)
	}
	if rec.SizeBytes <= 0 {
		t.Fatalf("clip is empty (%d bytes)", rec.SizeBytes)
	}
	if rec.Source != string(VibeClipSourceBrowser) {
		t.Fatalf("clip source = %q, want browser", rec.Source)
	}
	if st, statErr := os.Stat(rec.Path); statErr != nil || st.Size() <= 0 {
		t.Fatalf("mp4 missing/empty on disk at %s: %v", rec.Path, statErr)
	}
	// Sanity-check it's a real MP4 ffmpeg can probe (best-effort if ffprobe present).
	if _, err := exec.LookPath("ffprobe"); err == nil {
		if out, perr := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
			"-show_entries", "stream=codec_name", "-of", "csv=p=0", rec.Path).CombinedOutput(); perr != nil {
			t.Fatalf("ffprobe could not read mp4: %v: %s", perr, out)
		}
	}
	t.Logf("recorded %d bytes, %.1fs, source=%s at %s", rec.SizeBytes, rec.DurationSec, rec.Source, rec.Path)

	// Cleanup the test artifact.
	_ = os.Remove(rec.Path)
	if rec.PosterPath != "" {
		_ = os.Remove(rec.PosterPath)
	}
}
