package main

// vibe_preview_test.go — Phase 1 unit tests for VibePreviewManager.
//
// Uses a fake browser implementation so the suite doesn't need Chrome.
// Behaviour worth pinning:
//   1. Profile resolution from name + netMode hint.
//   2. Lifecycle (start → snapshot → stop) and double-start rejection.
//   3. Ringbuffer FIFO eviction at vibePreviewRingCap.
//   4. Stable-frame collapse when the screenshot bytes don't change.
//   5. Stop closes the underlying browser session.

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── fakeBrowser ─────────────────────────────────────────────────────────────

// fakeBrowser implements vibePreviewBrowserGetter without Chrome.
// Each Screenshot() call returns the next blob from a programmable queue;
// when the queue is empty, the last blob is repeated (so stable-frame
// collapse can be exercised).
type fakeBrowser struct {
	mu       sync.Mutex
	opens    int
	closes   int
	navigates []string
	queue    [][]byte
	repeat   []byte
	openErr  error
	navErr   error
	shotErr  error
	openedID string
}

func newFakeBrowser(blobs ...[]byte) *fakeBrowser {
	return &fakeBrowser{queue: blobs}
}

func (f *fakeBrowser) OpenSession(id string, headful bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return f.openErr
	}
	f.opens++
	f.openedID = id
	return nil
}

func (f *fakeBrowser) Navigate(id, url string) (*BrowserActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.navErr != nil {
		return nil, f.navErr
	}
	f.navigates = append(f.navigates, url)
	return &BrowserActionResult{URL: url}, nil
}

func (f *fakeBrowser) Screenshot(id string) (*BrowserActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.shotErr != nil {
		return nil, f.shotErr
	}
	var b []byte
	if len(f.queue) > 0 {
		b, f.queue = f.queue[0], f.queue[1:]
		f.repeat = b
	} else {
		b = f.repeat
	}
	if b == nil {
		b = []byte("default-frame")
	}
	return &BrowserActionResult{
		ScreenshotB64: base64.StdEncoding.EncodeToString(b),
	}, nil
}

func (f *fakeBrowser) CloseSession(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// genFrame returns N distinct fake-PNG byte strings.
func genFrames(n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = []byte(fmt.Sprintf("frame-%05d", i))
	}
	return out
}

// ─── ProfileFor ──────────────────────────────────────────────────────────────

func TestProfileFor_explicitName(t *testing.T) {
	p := ProfileFor("live-relay-cell", "")
	if p.Name != "live-relay-cell" || p.FPS != 2 {
		t.Fatalf("expected live-relay-cell @ 2 FPS, got %+v", p)
	}
}

func TestProfileFor_netModeHint(t *testing.T) {
	cases := []struct {
		netMode string
		want    string
	}{
		{"direct", "live-direct"},
		{"relay-wifi", "live-relay-wifi"},
		{"relay-cell", "live-relay-cell"},
		{"cellular", "live-relay-cell"}, // alias
		{"", "live-relay-wifi"},          // default
		{"unknown", "live-relay-wifi"},   // unknown → default
	}
	for _, c := range cases {
		got := ProfileFor("", c.netMode)
		if got.Name != c.want {
			t.Errorf("netMode=%q: want %s, got %s", c.netMode, c.want, got.Name)
		}
	}
}

func TestProfileFor_explicitOverridesNetMode(t *testing.T) {
	p := ProfileFor("live-direct", "relay-cell")
	if p.Name != "live-direct" {
		t.Fatalf("explicit profile should win, got %s", p.Name)
	}
}

// ─── Lifecycle ───────────────────────────────────────────────────────────────

func TestStart_requiresProjectAndTarget(t *testing.T) {
	mgr := NewVibePreviewManager(newFakeBrowser())
	if _, err := mgr.Start(VibePreviewStartOpts{TargetURL: "http://x"}); err == nil {
		t.Fatal("expected error for missing project")
	}
	if _, err := mgr.Start(VibePreviewStartOpts{Project: "p"}); err == nil {
		t.Fatal("expected error for missing targetUrl")
	}
}

func TestStart_nilBrowserManager(t *testing.T) {
	mgr := NewVibePreviewManager(nil)
	_, err := mgr.Start(VibePreviewStartOpts{Project: "p", TargetURL: "http://x"})
	if err == nil || !strings.Contains(err.Error(), "browser automation unavailable") {
		t.Fatalf("expected browser-unavailable error, got %v", err)
	}
}

func TestStart_doubleStart_rejected(t *testing.T) {
	fb := newFakeBrowser(genFrames(10)...)
	mgr := NewVibePreviewManager(fb)

	// summary-only mode prevents the live capture loop from spinning up;
	// initial capture still fires (covered by snapshot tests below).
	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "alpha", TargetURL: "http://x", Mode: VibePreviewModeSummaryOnly,
	}); err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer mgr.StopAll()

	_, err := mgr.Start(VibePreviewStartOpts{
		Project: "alpha", TargetURL: "http://x", Mode: VibePreviewModeSummaryOnly,
	})
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected already-active error, got %v", err)
	}
}

func TestStart_initialCaptureFires(t *testing.T) {
	fb := newFakeBrowser(genFrames(2)...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	sess, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if sess.Project != "p" {
		t.Fatalf("session project mismatch: %s", sess.Project)
	}
	// Mode change-only suppresses the loop, but the initial capture is
	// always taken so the modal can render *something* immediately.
	latest := mgr.LatestFrame("p")
	if latest == nil {
		t.Fatal("expected an initial frame after Start")
	}
	if latest.Seq != 1 {
		t.Fatalf("expected seq=1, got %d", latest.Seq)
	}
}

func TestStop_unknownProject(t *testing.T) {
	mgr := NewVibePreviewManager(newFakeBrowser())
	if err := mgr.Stop("nope"); err == nil {
		t.Fatal("expected error stopping unknown project")
	}
}

func TestStop_closesBrowserSession(t *testing.T) {
	fb := newFakeBrowser(genFrames(3)...)
	mgr := NewVibePreviewManager(fb)

	_, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := mgr.Stop("p"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	fb.mu.Lock()
	closes := fb.closes
	fb.mu.Unlock()
	if closes != 1 {
		t.Fatalf("expected 1 browser CloseSession, got %d", closes)
	}
}

// ─── Ringbuffer + stable-frame collapse ──────────────────────────────────────

func TestRingbuffer_evictsAtCap(t *testing.T) {
	// Push (cap + 5) distinct frames via Snapshot. After eviction, oldest
	// should be gone and FrameCount should reflect every successful capture.
	frames := genFrames(vibePreviewRingCap + 5)
	fb := newFakeBrowser(frames...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	// change-only avoids the periodic loop fighting our snapshot calls.
	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Initial capture already fired during Start (consumes frames[0]).
	// Take cap+4 more to push past the limit.
	for i := 0; i < vibePreviewRingCap+4; i++ {
		if _, err := mgr.Snapshot("p"); err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
	}

	mgr.mu.Lock()
	ringLen := len(mgr.ring["p"])
	mgr.mu.Unlock()
	if ringLen != vibePreviewRingCap {
		t.Fatalf("expected ring at cap (%d), got %d", vibePreviewRingCap, ringLen)
	}

	// FrameCount should equal total non-stable captures: 1 initial + cap+4.
	stat := mgr.Status()
	if len(stat) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(stat))
	}
	want := uint64(1 + vibePreviewRingCap + 4)
	if stat[0].FrameCount != want {
		t.Fatalf("FrameCount: want %d, got %d", want, stat[0].FrameCount)
	}
}

func TestStableFrameCollapse(t *testing.T) {
	// Queue one unique frame, then exhaust the queue so subsequent
	// screenshots all return the same blob → stable collapse.
	fb := newFakeBrowser([]byte("only-frame"))
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := mgr.Snapshot("p"); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
	}

	stat := mgr.Status()
	if len(stat) != 1 {
		t.Fatalf("expected 1 session, got %d", len(stat))
	}
	if stat[0].FrameCount != 1 {
		t.Fatalf("FrameCount should be 1 (only one unique hash), got %d", stat[0].FrameCount)
	}
	if stat[0].StableHits != 5 {
		t.Fatalf("StableHits should be 5, got %d", stat[0].StableHits)
	}

	mgr.mu.Lock()
	ringLen := len(mgr.ring["p"])
	mgr.mu.Unlock()
	if ringLen != 1 {
		t.Fatalf("ring should hold 1 frame after collapse, got %d", ringLen)
	}
}

func TestFrameByHash(t *testing.T) {
	frames := genFrames(3)
	fb := newFakeBrowser(frames...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	rec, _ := mgr.Snapshot("p")
	got := mgr.FrameByHash("p", rec.Hash)
	if got == nil || got.Seq != rec.Seq {
		t.Fatalf("FrameByHash should return the same record (got %+v, want seq=%d)", got, rec.Seq)
	}
	if mgr.FrameByHash("p", "nopehash") != nil {
		t.Fatal("FrameByHash should return nil for unknown hash")
	}
}

func TestStopAll_concurrent(t *testing.T) {
	fb := newFakeBrowser(genFrames(50)...)
	mgr := NewVibePreviewManager(fb)
	for i := 0; i < 5; i++ {
		_, err := mgr.Start(VibePreviewStartOpts{
			Project:   fmt.Sprintf("p%d", i),
			TargetURL: "http://x",
			Mode:      VibePreviewModeChangeOnly,
		})
		if err != nil {
			t.Fatalf("start p%d: %v", i, err)
		}
	}
	mgr.StopAll()
	if got := len(mgr.Status()); got != 0 {
		t.Fatalf("expected 0 sessions after StopAll, got %d", got)
	}
	fb.mu.Lock()
	closes := fb.closes
	fb.mu.Unlock()
	if closes != 5 {
		t.Fatalf("expected 5 browser closes, got %d", closes)
	}
}

// ─── Live-loop smoke (no real browser) ───────────────────────────────────────

func TestLiveLoop_capturesFramesOverTime(t *testing.T) {
	// Use a high-FPS profile so the loop captures multiple frames in a
	// short test window. The fake browser cycles through 30 distinct
	// blobs then repeats the last one.
	frames := genFrames(30)
	fb := newFakeBrowser(frames...)
	mgr := NewVibePreviewManager(fb)
	defer mgr.StopAll()

	// Force the live-direct profile (8 FPS = ~125 ms interval) and live mode.
	if _, err := mgr.Start(VibePreviewStartOpts{
		Project:   "p",
		TargetURL: "http://x",
		Mode:      VibePreviewModeLive,
		Profile:   "live-direct",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait until at least 5 captures have happened (initial + 4 ticks at
	// 125 ms ≈ 500 ms total). Polling avoids tying the test to wall clock.
	pollUntil(t, 3000, func() bool {
		stat := mgr.Status()
		return len(stat) == 1 && stat[0].FrameCount >= 5
	}, "expected >=5 frames captured by the live loop")
}

// pollUntil polls cond every 25 ms up to maxMs total, fatal-failing with msg
// on timeout. Keeps the live-loop test deterministic on slow CI without
// hard-coding a fixed sleep that's either flaky or wasteful.
func pollUntil(t *testing.T, maxMs int, cond func() bool, msg string) {
	t.Helper()
	steps := maxMs / 25
	for i := 0; i < steps; i++ {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s (waited %d ms)", msg, maxMs)
}
