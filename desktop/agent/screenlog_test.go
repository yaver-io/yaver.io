package main

// screenlog_test.go — pure-logic coverage for the local screen-frame
// black box. We deliberately do NOT exercise the real capture path here
// (it would prompt for Screen-Recording permission and is display-bound);
// captureScreenlogFrames is covered by screenlogProbe at runtime via the
// drivers verb. These tests follow the repo's no-mock, real-bytes style.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func solidImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func gradientImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x * 255) / w)
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	return img
}

func TestScreenlogDHashAndHamming(t *testing.T) {
	g := gradientImage(64, 64)
	if h := hammingDistance(dHash(g), dHash(g)); h != 0 {
		t.Fatalf("identical image hamming = %d, want 0", h)
	}
	// A solid frame vs a gradient frame must differ substantially.
	solid := solidImage(64, 64, color.RGBA{10, 10, 10, 255})
	if h := hammingDistance(dHash(g), dHash(solid)); h < 8 {
		t.Fatalf("gradient vs solid hamming = %d, want a large distance", h)
	}
	// Near-identical (gradient + a tiny corner change) should stay close.
	g2 := gradientImage(64, 64).(*image.RGBA)
	g2.Set(0, 0, color.RGBA{0, 0, 0, 255})
	if h := hammingDistance(dHash(g), dHash(g2)); h > 6 {
		t.Fatalf("near-identical hamming = %d, want small", h)
	}
}

func TestScreenlogConfigNormalize(t *testing.T) {
	c := ScreenlogConfig{IntervalSec: 0, Format: "gif", Quality: 999, Displays: "weird", WSLTarget: "nope", MaxWidth: -5}
	c.normalize()
	if c.IntervalSec <= 0 || c.Format != "jpg" || c.Quality <= 0 || c.Quality > 100 {
		t.Fatalf("normalize left bad scalars: %+v", c)
	}
	if c.Displays != "all" || c.WSLTarget != "auto" || c.MaxWidth != 0 {
		t.Fatalf("normalize left bad enums: %+v", c)
	}
}

func TestScreenlogEncodeFrameDownscaleAndFormat(t *testing.T) {
	src := gradientImage(400, 200)

	// PNG, downscaled to 100px wide.
	enc, w, h, err := encodeFrame(src, ScreenlogConfig{Format: "png", MaxWidth: 100})
	if err != nil {
		t.Fatal(err)
	}
	if w != 100 || h != 50 {
		t.Fatalf("downscale gave %dx%d, want 100x50", w, h)
	}
	if _, err := png.Decode(bytes.NewReader(enc)); err != nil {
		t.Fatalf("png re-decode failed: %v", err)
	}

	// JPEG, full res.
	enc2, w2, _, err := encodeFrame(src, ScreenlogConfig{Format: "jpg", Quality: 80, MaxWidth: 0})
	if err != nil {
		t.Fatal(err)
	}
	if w2 != 400 {
		t.Fatalf("full-res width = %d, want 400", w2)
	}
	if _, _, err := image.Decode(bytes.NewReader(enc2)); err != nil {
		t.Fatalf("jpeg re-decode failed: %v", err)
	}
}

func TestScreenlogPersistRoundTrip(t *testing.T) {
	withTempScreenlogDir(t)
	in := &ScreenlogSession{
		ID: "slog-test1", Title: "round trip", StartedAt: 1000,
		Config: defaultScreenlogConfig(),
		Frames: []ScreenlogFrame{{Idx: 1, File: "a.png", Bytes: 10, ActiveApp: "Code"}},
	}
	if err := saveScreenlogSession(in); err != nil {
		t.Fatal(err)
	}
	out, err := loadScreenlogSession("slog-test1")
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "round trip" || len(out.Frames) != 1 || out.Frames[0].ActiveApp != "Code" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	list, err := listScreenlogSessions()
	if err != nil || len(list) != 1 {
		t.Fatalf("list returned %d sessions, err=%v", len(list), err)
	}
}

func TestScreenlogDiskBudgetEviction(t *testing.T) {
	withTempScreenlogDir(t)
	id := "slog-budget"
	dir, _ := screenlogSessionDir(id)
	// Three 1 MB frames on disk; budget = 2 MB → oldest one evicted.
	mk := func(name string) {
		os.WriteFile(filepath.Join(dir, name), make([]byte, 1024*1024), 0o600)
	}
	mk("f1.png")
	mk("f2.png")
	mk("f3.png")
	a := &activeScreenlog{
		session: &ScreenlogSession{ID: id, Frames: []ScreenlogFrame{
			{Idx: 1, File: "f1.png", Bytes: 1024 * 1024},
			{Idx: 2, File: "f2.png", Bytes: 1024 * 1024},
			{Idx: 3, File: "f3.png", Bytes: 1024 * 1024},
		}},
		totalBytes: 3 * 1024 * 1024,
	}
	a.enforceDiskBudgetLocked(ScreenlogConfig{MaxDiskMB: 2}, dir)
	if len(a.session.Frames) != 2 || a.session.Frames[0].File != "f2.png" {
		t.Fatalf("eviction left frames %+v", a.session.Frames)
	}
	if _, err := os.Stat(filepath.Join(dir, "f1.png")); !os.IsNotExist(err) {
		t.Fatalf("oldest frame f1.png should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "f2.png")); err != nil {
		t.Fatalf("f2.png should survive: %v", err)
	}
}

func TestWinPathToWSLFallback(t *testing.T) {
	// On macOS/Linux-without-wslpath this exercises the string fallback.
	got := winPathToWSL(`C:\Users\dev\AppData\Local\Temp\yaver-slog.png`)
	want := "/mnt/c/Users/dev/AppData/Local/Temp/yaver-slog.png"
	// If a real wslpath happens to exist it may differ; only assert the
	// fallback shape when wslpath is absent.
	if !lookPathOK("wslpath") && got != want {
		t.Fatalf("winPathToWSL = %q, want %q", got, want)
	}
}

func TestScreenlogFieldsAreConvexForbidden(t *testing.T) {
	forbidden := map[string]bool{}
	for _, k := range fieldsWeForbidInAnyConvexPayload {
		forbidden[k] = true
	}
	for _, must := range []string{"activeWindow", "phash", "frameBytes", "frameJpeg"} {
		if !forbidden[must] {
			t.Errorf("%q MUST be on the Convex forbidden list — screenlog frames are local-only", must)
		}
	}
}

func TestActivityReportBreakdown(t *testing.T) {
	base := int64(1_700_000_000_000)
	samples := []ActivitySample{
		{Start: base, End: base + 60_000, Category: "Code", Label: "main.go"},
		{Start: base + 60_000, End: base + 90_000, Category: "Chrome", Label: "docs"},
		{Start: base + 90_000, End: base + 120_000, Category: "Code", Label: "main.go"},
		{Start: base + 120_000, End: base + 420_000, Category: "idle", Idle: true},
	}
	r := buildActivityReport(samples, "screen", "dads-pc")
	if r.ActiveSec != 120 || r.IdleSec != 300 {
		t.Fatalf("active=%d idle=%d, want 120/300", r.ActiveSec, r.IdleSec)
	}
	if len(r.ByCategory) == 0 || r.ByCategory[0].Name != "Code" || r.ByCategory[0].Seconds != 90 {
		t.Fatalf("top category wrong: %+v", r.ByCategory)
	}
	if r.NarrativePrompt() == "" {
		t.Fatal("empty narrative prompt")
	}
}

func TestScreenlogAnalyzeSession(t *testing.T) {
	withTempScreenlogDir(t)
	base := int64(1_700_000_000_000)
	sess := &ScreenlogSession{
		ID: "slog-an", Host: "dads-pc", StartedAt: base,
		Config: ScreenlogConfig{IntervalSec: 2},
		Frames: []ScreenlogFrame{
			{Idx: 1, CapturedAt: base, ActiveApp: "Excel"},
			{Idx: 2, CapturedAt: base + 2_000, ActiveApp: "Excel"},
			{Idx: 3, CapturedAt: base + 4_000, ActiveApp: "Chrome"},
			// Big gap → idle tail.
			{Idx: 4, CapturedAt: base + 600_000, ActiveApp: "Excel"},
		},
	}
	if err := saveScreenlogSession(sess); err != nil {
		t.Fatal(err)
	}
	rep, _, err := analyzeScreenlogSession("slog-an", 120, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ByCategory) == 0 || rep.ByCategory[0].Name != "Excel" {
		t.Fatalf("expected Excel top, got %+v", rep.ByCategory)
	}
	if rep.IdleSec == 0 {
		t.Fatalf("expected idle from the 10-min gap, got 0")
	}
}

func TestScreenlogEnforcePolicy(t *testing.T) {
	base := defaultScreenlogPolicy()

	// Master kill-switch.
	off := base
	off.Enabled = false
	if ok, _ := screenlogEnforce(off, screenlogCaller{}); ok {
		t.Fatal("disabled policy should deny even local")
	}
	// Local always allowed when enabled.
	if ok, _ := screenlogEnforce(base, screenlogCaller{}); !ok {
		t.Fatal("local owner should be allowed by default")
	}
	// Remote gated.
	noRemote := base
	noRemote.AllowRemoteControl = false
	if ok, _ := screenlogEnforce(noRemote, screenlogCaller{Remote: true}); ok {
		t.Fatal("remote should be denied when allowRemoteControl=false")
	}
	if ok, _ := screenlogEnforce(base, screenlogCaller{Remote: true}); !ok {
		t.Fatal("remote should be allowed by default")
	}
	// Mesh peer requires a grant.
	if ok, _ := screenlogEnforce(base, screenlogCaller{Remote: true, Mesh: true, PeerID: "peerX"}); ok {
		t.Fatal("ungranted mesh peer should be denied")
	}
	granted := base
	granted.AllowedPeers = []string{"peerX"}
	if ok, _ := screenlogEnforce(granted, screenlogCaller{Remote: true, Mesh: true, PeerID: "peerX"}); !ok {
		t.Fatal("granted mesh peer should be allowed")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:5050": true,
		"[::1]:5050":     true,
		"":               true,
		"192.168.1.4:80": false,
		"10.0.0.2:1":     false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestSampleKeyframes(t *testing.T) {
	sess := &ScreenlogSession{}
	for i := 0; i < 100; i++ {
		sess.Frames = append(sess.Frames, ScreenlogFrame{Idx: i})
	}
	got := sampleKeyframes(sess, 5)
	if len(got) != 5 {
		t.Fatalf("got %d keyframes, want 5", len(got))
	}
	if got[0].Idx != 0 || got[4].Idx != 99 {
		t.Fatalf("keyframe span wrong: first=%d last=%d", got[0].Idx, got[4].Idx)
	}
}

func TestScreenlogInputEventsAndRedaction(t *testing.T) {
	withTempScreenlogDir(t)
	id := "slog-input"
	if err := saveScreenlogSession(&ScreenlogSession{ID: id, StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	events := []InputEvent{
		{T: 1000, Type: "click", X: 840, Y: 210, Button: "left", ScreenW: 2560, ScreenH: 1440},
		{T: 1100, Type: "key", Key: "Enter"},
		{T: 1200, Type: "key", Key: "p"},         // printable → redacted
		{T: 1300, Type: "text", Text: "hunter2"}, // secret → redacted to length
		{T: 0, Type: "key", Key: "x"},            // invalid t → skipped
		{T: 1400, Type: "bogus", Key: "y"},       // invalid type → skipped
	}
	n, err := ingestInputEvents(id, events, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("ingested %d, want 4 (2 skipped)", n)
	}
	got, err := readInputEvents(id, 0)
	if err != nil || len(got) != 4 {
		t.Fatalf("read %d events, err=%v", len(got), err)
	}
	// Redaction: named key survives, printable redacted, text → length form.
	if got[1].Key != "Enter" {
		t.Errorf("named key should survive, got %q", got[1].Key)
	}
	if got[2].Key != "•" {
		t.Errorf("printable key should be redacted, got %q", got[2].Key)
	}
	if got[3].Text != "[redacted:7]" {
		t.Errorf("secret text should be redacted to length, got %q", got[3].Text)
	}
	st := inputStats(got)
	if st["clicks"].(int) != 1 || st["keys"].(int) != 2 {
		t.Errorf("stats wrong: %+v", st)
	}
}

func TestScreenlogInputRawTextKept(t *testing.T) {
	withTempScreenlogDir(t)
	id := "slog-raw"
	saveScreenlogSession(&ScreenlogSession{ID: id, StartedAt: 1})
	_, _ = ingestInputEvents(id, []InputEvent{{T: 1, Type: "text", Text: "verbatim"}}, false)
	got, _ := readInputEvents(id, 0)
	if len(got) != 1 || got[0].Text != "verbatim" {
		t.Fatalf("raw text should be kept when redact=false, got %+v", got)
	}
}

func TestScreenlogPolicyInputGate(t *testing.T) {
	// AllowInputCapture defaults OFF — the strongest gate.
	if defaultScreenlogPolicy().AllowInputCapture {
		t.Fatal("AllowInputCapture must default to false")
	}
}

func TestInputCaptureManagerWithFakeProducer(t *testing.T) {
	withTempScreenlogDir(t)
	id := "slog-cap"
	if err := saveScreenlogSession(&ScreenlogSession{ID: id, StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	// Fake producer: emits two JSON-line events then exits.
	testInputProducer = func() *exec.Cmd {
		return exec.Command("sh", "-c",
			`printf '{"t":1,"type":"click","button":"left","x":5,"y":6}\n{"t":2,"type":"key","key":"Return"}\n'`)
	}
	t.Cleanup(func() { testInputProducer = nil; stopInputCapture() })

	note, ok := startInputCapture(id, defaultScreenlogConfig(), true)
	if !ok {
		t.Fatalf("startInputCapture failed: %s", note)
	}
	// Let the pump read + flush (producer exits → done → flush).
	deadline := time.Now().Add(2 * time.Second)
	var got []InputEvent
	for time.Now().Before(deadline) {
		got, _ = readInputEvents(id, 0)
		if len(got) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	stopInputCapture()
	if len(got) != 2 || got[0].Type != "click" || got[1].Key != "Return" {
		t.Fatalf("manager ingested wrong events: %+v", got)
	}
}

func TestScreenlogEmulationEndToEnd(t *testing.T) {
	withTempScreenlogDir(t)
	cfg := defaultScreenlogConfig()
	cfg.CaptureInput = true
	cfg.IntervalSec = 1
	sc := ScreenlogScenario{
		StartMs: 1_700_000_000_000,
		Segments: []ScenarioSegment{
			{App: "Code", Window: "main.go", Seconds: 30, KeysPerMin: 120},
			{App: "Chrome", Window: "docs", Seconds: 10, ClicksPerMin: 60},
			{App: "Code", Window: "main.go", Seconds: 20, KeysPerMin: 120},
		},
	}
	sess, err := runScreenlogEmulation("emu", sc, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Frames) < 3 {
		t.Fatalf("expected ≥3 kept frames (one per app switch), got %d", len(sess.Frames))
	}
	// Active intervals must be closed ("on from X to Y").
	for _, f := range sess.Frames {
		if f.ActiveToMs <= f.CapturedAt {
			t.Fatalf("frame %d interval not closed: %+v", f.Idx, f)
		}
	}
	// Analyze: Code (30+20s) should beat Chrome (10s).
	rep, _, err := analyzeScreenlogSession(sess.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ByCategory) == 0 || rep.ByCategory[0].Name != "Code" {
		t.Fatalf("expected Code top, got %+v", rep.ByCategory)
	}
	// Input events were emitted.
	events, _ := readInputEvents(sess.ID, 0)
	if len(events) == 0 {
		t.Fatal("expected synthetic input events")
	}
	// Export bundles into a non-empty tar.gz.
	var buf bytes.Buffer
	dir, _ := screenlogSessionDir(sess.ID)
	if err := writeSessionTarGz(&buf, dir, sess.ID); err != nil || buf.Len() == 0 {
		t.Fatalf("export failed: err=%v size=%d", err, buf.Len())
	}
}

func TestScreenlogFrameCapBoundsMemory(t *testing.T) {
	withTempScreenlogDir(t)
	id := "slog-cap-mem"
	if err := saveScreenlogSession(&ScreenlogSession{ID: id, StartedAt: 1}); err != nil {
		t.Fatal(err)
	}
	cfg := defaultScreenlogConfig()
	cfg.MaxFrames = 20
	cfg.Dedup = false // every frame distinct anyway; keep them all up to the cap
	a := &activeScreenlog{
		session:      &ScreenlogSession{ID: id},
		lastKept:     map[int]uint64{},
		lastKeptAt:   map[int]int64{},
		lastKeptSlot: map[int]int{},
		nextIdx:      1,
	}
	// Feed 200 distinct frames; the in-memory index must stay ≤ MaxFrames.
	for i := 0; i < 200; i++ {
		img := emulatedFrame(fmt.Sprintf("app%d", i), 48, 48)
		a.ingestFrame(int64(1000+i*1000), 0, img, fmt.Sprintf("app%d", i), "", cfg)
	}
	if len(a.session.Frames) > 20 {
		t.Fatalf("frame cap breached: %d frames in memory (want ≤20)", len(a.session.Frames))
	}
	// The surviving window must be the most-recent frames.
	last := a.session.Frames[len(a.session.Frames)-1]
	if last.ActiveApp != "app199" {
		t.Fatalf("newest frame should be app199, got %s", last.ActiveApp)
	}
}

// withTempScreenlogDir points the package-global screenlog base dir at a
// throwaway temp dir for the duration of the test.
func withTempScreenlogDir(t *testing.T) {
	t.Helper()
	screenlogMu.Lock()
	prev := screenlogBaseDir
	screenlogBaseDir = t.TempDir()
	screenlogMu.Unlock()
	t.Cleanup(func() {
		screenlogMu.Lock()
		screenlogBaseDir = prev
		screenlogMu.Unlock()
	})
}
