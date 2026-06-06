package main

// screenlog_emulator.go — a headless, $0, no-hardware emulator for the
// screenlog pipeline (mirrors the emulated-PLC e2e pattern). It drives the
// REAL capture path (dedup → active intervals → persist → input events)
// with a SYNTHETIC activity timeline, so the whole flow — including
// analyze, export/pull, and the web/mobile viewers — can be exercised on a
// headless Linux box or a Mac with no display and no real screen grab.
//
// A scenario is a list of "the user was in App X for N seconds" segments
// with optional click/keystroke rates. The emulator generates one
// app-distinct synthetic frame per tick (identical within a segment so the
// de-dup drops them; different across segments so a new frame is kept and
// the previous interval closes — producing real "active 12:01–12:53"
// spans) plus synthetic input events, and writes a genuine on-disk session.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"time"
)

// syntheticApps drives the synthetic REAL-TIME source (env
// YAVER_SCREENLOG_SYNTH=1) used to exercise the live capture loop on a
// HEADLESS box (no display) — e.g. to measure CPU/RAM. The "app" switches
// every 10 s of wall clock so de-dup + active intervals behave realistically.
var syntheticApps = []string{"Code", "Chrome", "Slack", "Excel"}

func syntheticAppNow() string {
	return syntheticApps[(time.Now().Unix()/10)%int64(len(syntheticApps))]
}

// syntheticLiveCapture is a drop-in for captureScreenlogFrames that needs
// no display. Returns one PNG frame whose content tracks syntheticAppNow().
func syntheticLiveCapture(cfg ScreenlogConfig) ([]rawScreenlogFrame, error) {
	img := emulatedFrame(syntheticAppNow(), 640, 400)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return []rawScreenlogFrame{{display: 0, png: buf.Bytes()}}, nil
}

func syntheticWindow() (string, string) {
	return syntheticAppNow(), syntheticAppNow() + " — synthetic"
}

// maybeUseSyntheticSource swaps the capture + window sources for the
// no-display synthetic ones when YAVER_SCREENLOG_SYNTH is set. Test/dev
// only — lets `screenlog start --local` run the REAL real-time loop on a
// headless server for HW-usage measurement.
func maybeUseSyntheticSource() bool {
	if os.Getenv("YAVER_SCREENLOG_SYNTH") == "" {
		return false
	}
	screenlogCaptureFn = syntheticLiveCapture
	screenlogWindowFn = syntheticWindow
	return true
}

// ScenarioSegment is one stretch of emulated activity.
type ScenarioSegment struct {
	App          string
	Window       string
	Seconds      int
	ClicksPerMin int
	KeysPerMin   int
}

// ScreenlogScenario is a full emulated timeline.
type ScreenlogScenario struct {
	StartMs  int64 // wall-clock anchor for the first frame (0 → now-total)
	Segments []ScenarioSegment
}

// defaultEmulationScenario is a believable "morning at the desk" timeline.
func defaultEmulationScenario(scaleSeconds int) ScreenlogScenario {
	if scaleSeconds <= 0 {
		scaleSeconds = 1
	}
	s := func(app, win string, mins, cpm, kpm int) ScenarioSegment {
		return ScenarioSegment{App: app, Window: win, Seconds: mins * scaleSeconds, ClicksPerMin: cpm, KeysPerMin: kpm}
	}
	return ScreenlogScenario{
		Segments: []ScenarioSegment{
			s("Code", "screenlog_emulator.go — yaver", 30, 8, 220),
			s("Chrome", "Stack Overflow", 12, 25, 30),
			s("Slack", "#engineering", 8, 15, 90),
			s("Excel", "Q3-forecast.xlsx", 18, 40, 60),
			s("idle", "", 5, 0, 0),
		},
	}
}

// emulatedFrame builds an app-distinct synthetic image. The gradient
// DIRECTION derives from the app name, so two different apps yield
// different perceptual hashes (a new kept frame) while frames within one
// app are identical (de-duped). A solid color would NOT work — its dHash
// is all-zero for every color.
func emulatedFrame(app string, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var hsh uint32 = 2166136261
	for _, b := range []byte(app) {
		hsh = (hsh ^ uint32(b)) * 16777619
	}
	ax := int(hsh%7) + 1
	ay := int((hsh>>3)%7) + 1
	base := color.RGBA{uint8(hsh), uint8(hsh >> 8), uint8(hsh >> 16), 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8((x*ax + y*ay) % 256)
			img.Set(x, y, color.RGBA{base.R ^ v, base.G ^ v, base.B ^ v, 255})
		}
	}
	return img
}

// runScreenlogEmulation builds a real session from a scenario. The result
// is on disk under ~/.yaver/screenlog/ — analyzable, viewable, pullable.
func runScreenlogEmulation(title string, sc ScreenlogScenario, cfg ScreenlogConfig) (*ScreenlogSession, error) {
	cfg.normalize()
	total := 0
	for _, seg := range sc.Segments {
		total += seg.Seconds
	}
	if total == 0 {
		return nil, fmt.Errorf("empty scenario")
	}
	start := sc.StartMs
	if start == 0 {
		start = time.Now().UnixMilli() - int64(total)*1000
	}

	host, _ := os.Hostname()
	sess := &ScreenlogSession{
		ID:        "slog-emu-" + randomFormID(),
		Title:     title,
		Host:      host,
		StartedAt: start,
		Config:    cfg,
		Frames:    []ScreenlogFrame{},
	}
	if err := saveScreenlogSession(sess); err != nil {
		return nil, err
	}
	a := &activeScreenlog{
		session:      sess,
		lastKept:     map[int]uint64{},
		lastKeptAt:   map[int]int64{},
		lastKeptSlot: map[int]int{},
		nextIdx:      1,
		startedAt:    time.Now(),
	}

	const w, h = 320, 200 // small synthetic frames keep emulation fast
	now := start
	for _, seg := range sc.Segments {
		segEnd := now + int64(seg.Seconds)*1000
		img := emulatedFrame(seg.App, w, h)
		clickEvery, keyEvery := rateEvery(seg.ClicksPerMin), rateEvery(seg.KeysPerMin)
		var nextClick, nextKey int64 = now + clickEvery, now + keyEvery
		for t := now; t < segEnd; t += int64(cfg.IntervalSec) * 1000 {
			a.ingestFrame(t, 0, img, seg.App, seg.Window, cfg)
			// Synthetic input events within the tick window.
			if cfg.CaptureInput {
				var batch []InputEvent
				for clickEvery > 0 && nextClick <= t {
					batch = append(batch, InputEvent{T: nextClick, Type: "click", Button: "left", X: int(nextClick % int64(w)), Y: int(nextClick % int64(h)), ScreenW: w, ScreenH: h})
					nextClick += clickEvery
				}
				for keyEvery > 0 && nextKey <= t {
					batch = append(batch, InputEvent{T: nextKey, Type: "key", Key: "a"})
					nextKey += keyEvery
				}
				if len(batch) > 0 {
					_, _ = ingestInputEvents(sess.ID, batch, !cfg.AllowRawText)
				}
			}
		}
		now = segEnd
	}

	// Close the final open interval + persist.
	a.mu.Lock()
	for i := range a.session.Frames {
		if a.session.Frames[i].ActiveToMs == 0 {
			a.session.Frames[i].ActiveToMs = now
		}
	}
	a.session.StoppedAt = now
	_ = saveScreenlogSession(a.session)
	out := *a.session
	a.mu.Unlock()
	return &out, nil
}

// rateEvery converts a per-minute rate into a millisecond interval (0 = off).
func rateEvery(perMin int) int64 {
	if perMin <= 0 {
		return 0
	}
	return int64(60000 / perMin)
}
