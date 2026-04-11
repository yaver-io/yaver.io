package testkit

// Safari smoke test — guarded by env YAVER_SAFARI_SMOKE=1 so GH
// Actions never runs it (Safari requires `sudo safaridriver --enable`
// once on the host, which GH Actions containers can't do). On a dev's
// own macOS box this is the single-command verification that the W3C
// client, the driver spawn, and `about:blank` navigation all line up.
//
// Run: YAVER_SAFARI_SMOKE=1 go test -run TestSafariSmoke ./...

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestSafariSmoke(t *testing.T) {
	if os.Getenv("YAVER_SAFARI_SMOKE") != "1" {
		t.Skip("set YAVER_SAFARI_SMOKE=1 to run the Safari smoke test (macOS only, needs `sudo safaridriver --enable`)")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("safari smoke test is macOS-only")
	}

	// Serve a tiny page we can point Safari at so the test doesn't
	// depend on network reachability or any real website.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body><h1>yaver-test-sdk</h1></body></html>`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	d, err := NewSafariDriver(ctx)
	if err != nil {
		t.Fatalf("NewSafariDriver: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	if err := NewSafariSession(ctx, d, true, 1024, 768); err != nil {
		t.Fatalf("NewSafariSession: %v", err)
	}

	if err := d.Navigate(ctx, srv.URL); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	png, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("screenshot is empty — Safari returned no bytes")
	}
	// PNGs start with the 8-byte signature \x89PNG\r\n\x1a\n.
	if len(png) < 8 ||
		png[0] != 0x89 || png[1] != 'P' || png[2] != 'N' || png[3] != 'G' {
		t.Fatalf("screenshot is not a PNG — first bytes: %x", png[:minInt(8, len(png))])
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
