package main

// vibe_preview_integration_test.go — exercises the full /vibing/preview
// stack end-to-end through MobileClient. Two skip gates:
//
//   • If chromedp can't launch (no Chrome on the box) → skip. The same
//     gate browser_test.go uses.
//   • If YAVER_TEST_BASE_URL is set, talks to that remote agent
//     (typically the Hetzner ephemeral box) instead of an in-process
//     server. Lets `bash /opt/yaver/ci/remote/verify.sh` invoke this
//     test against the deployed binary.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVibePreview_HeadlessE2E(t *testing.T) {
	if os.Getenv("YAVER_TEST_SKIP_CHROME") == "1" {
		t.Skip("YAVER_TEST_SKIP_CHROME set — skipping chromedp-dependent path")
	}

	// Spin up a tiny static dev server the chromedp browser can navigate
	// to. Independent of any framework — just an HTML page that re-renders
	// a counter so frame hashes change, exercising the dedup path.
	devServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html><head><title>vibe-preview test</title></head>
<body style="margin:0;background:#222;color:#eee;font-family:monospace">
<h1 id=t style="padding:32px">vibe-preview e2e</h1>
<script>
let n=0; setInterval(()=>{n++; document.getElementById('t').textContent='vibe-preview e2e — ' + n}, 200)
</script>
</body></html>`))
	}))
	defer devServer.Close()

	baseURL, authToken, agentSrv, agentMgr := acquireAgent(t)
	defer agentSrv.Close()

	// Sanity: chromedp can actually launch on this box. Sub-test so a
	// missing-Chrome environment fails this assertion + skips cleanly.
	if agentMgr.browserMgr == nil {
		t.Skip("BrowserManager not initialised on this agent; skipping chromedp-dependent test")
	}

	client := NewMobileClient(baseURL, authToken, &http.Client{Timeout: 60 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	project := "vibe-preview-itest"

	// Best-effort cleanup if a prior failed run left a session active.
	_ = client.StopVibePreview(ctx, project)

	resp, err := client.StartVibePreview(ctx, VibePreviewStartOptsHeadless{
		Project:   project,
		TargetURL: devServer.URL,
		Mode:      "live",
		Profile:   "live-direct",
	})
	if err != nil {
		// Graceful skip when Chrome simply isn't available.
		if strings.Contains(err.Error(), "browser automation unavailable") ||
			strings.Contains(err.Error(), "launch chrome") {
			t.Skipf("chromedp not available on this box: %v", err)
		}
		t.Fatalf("StartVibePreview: %v", err)
	}
	if resp == nil || resp["session"] == nil {
		t.Fatalf("StartVibePreview returned no session: %+v", resp)
	}
	t.Cleanup(func() {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		_ = client.StopVibePreview(ctx2, project)
	})

	// Poll status until at least one frame has been captured. Live-direct
	// is 8 FPS so we should see the first frame in well under a second,
	// but we allow up to 10 s to absorb a cold Chrome boot.
	deadline := time.Now().Add(10 * time.Second)
	var snap map[string]any
	for time.Now().Before(deadline) {
		s, err := client.VibePreviewSnapshot(ctx, project)
		if err == nil && s != nil && s["hash"] != nil {
			snap = s
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if snap == nil {
		t.Fatal("snapshot did not return a hash within 10 s")
	}
	hash, _ := snap["hash"].(string)
	if hash == "" {
		t.Fatalf("snapshot hash empty: %+v", snap)
	}

	// Fetch the frame bytes; must be non-empty PNG.
	bytes, err := client.FetchVibeFrame(ctx, project, hash)
	if err != nil {
		t.Fatalf("FetchVibeFrame: %v", err)
	}
	if len(bytes) < 100 {
		t.Fatalf("frame bytes suspiciously small: %d", len(bytes))
	}
	// PNG magic: \x89PNG
	if !(bytes[0] == 0x89 && bytes[1] == 'P' && bytes[2] == 'N' && bytes[3] == 'G') {
		t.Fatalf("frame is not a PNG (first 8 bytes: % x)", bytes[:8])
	}

	// Status must show the active session.
	st, err := client.VibePreviewStatus(ctx)
	if err != nil {
		t.Fatalf("VibePreviewStatus: %v", err)
	}
	sessions, _ := st["sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatalf("expected >=1 session in status, got %+v", st)
	}
}

// acquireAgent returns either a local in-process agent (the default) or
// a remote agent reachable via YAVER_TEST_BASE_URL + YAVER_TEST_AUTH_TOKEN.
// In remote mode the returned httptest.Server is a no-op shim — the
// agentMgr is the real one running over there, accessed through HTTP.
func acquireAgent(t *testing.T) (baseURL, authToken string, srv *httptest.Server, agentMgr *HTTPServer) {
	t.Helper()
	if remote := os.Getenv("YAVER_TEST_BASE_URL"); remote != "" {
		token := os.Getenv("YAVER_TEST_AUTH_TOKEN")
		if token == "" {
			t.Fatal("YAVER_TEST_BASE_URL set but YAVER_TEST_AUTH_TOKEN empty")
		}
		// No in-process httptest.Server — return a placeholder. The
		// "agentMgr" returned here is a stub with browserMgr set so the
		// chromedp gate passes; the real test asserts behavior over HTTP.
		stub := &HTTPServer{browserMgr: NewBrowserManager()}
		shim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "shim — not used", http.StatusNotImplemented)
		}))
		return strings.TrimRight(remote, "/"), token, shim, stub
	}
	return startLocalAgentForTest(t)
}

// startLocalAgentForTest boots an in-process HTTPServer with the vibe-
// preview routes mounted. Mirrors startTestServer's surface but is
// scoped to just the routes this test exercises so we don't pull in
// Convex sync or the heartbeat ticker.
func startLocalAgentForTest(t *testing.T) (string, string, *httptest.Server, *HTTPServer) {
	t.Helper()
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	hs := NewHTTPServer(0, "test-token", "test-user", "test-device", "", "test-host", tm)
	hs.browserMgr = NewBrowserManager()
	hs.vibePreviewMgr = NewVibePreviewManager(hs.browserMgr)
	hs.vibePreviewMgr.SetDiskRoot(t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/health", hs.handleHealth)
	mux.HandleFunc("/vibing/preview/start", hs.auth(hs.handleVibePreviewStart))
	mux.HandleFunc("/vibing/preview/stop", hs.auth(hs.handleVibePreviewStop))
	mux.HandleFunc("/vibing/preview/status", hs.auth(hs.handleVibePreviewStatus))
	mux.HandleFunc("/vibing/preview/snapshot", hs.auth(hs.handleVibePreviewSnapshot))
	mux.HandleFunc("/vibing/preview/events", hs.auth(hs.handleVibePreviewEvents))
	mux.HandleFunc("/vibing/preview/frames/", hs.auth(hs.handleVibePreviewFrame))
	mux.HandleFunc("/vibing/preview/clip/start", hs.auth(hs.handleVibePreviewClipStart))
	mux.HandleFunc("/vibing/preview/clip/stop", hs.auth(hs.handleVibePreviewClipStop))
	mux.HandleFunc("/vibing/preview/clips", hs.auth(hs.handleVibePreviewClips))
	mux.HandleFunc("/vibing/preview/clip/", hs.auth(hs.handleVibePreviewClip))

	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		hs.vibePreviewMgr.StopAll()
		hs.browserMgr.Stop()
		srv.Close()
	})
	return srv.URL, "test-token", srv, hs
}
