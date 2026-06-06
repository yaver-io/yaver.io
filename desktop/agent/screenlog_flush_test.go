package main

import (
	"image"
	"image/color"
	"testing"
	"time"
)

func newTestRecorder(id string) *activeScreenlog {
	sess := &ScreenlogSession{ID: id, Config: defaultScreenlogConfig(), Frames: []ScreenlogFrame{}}
	_ = saveScreenlogSession(sess)
	return &activeScreenlog{
		session:      sess,
		lastKept:     map[int]uint64{},
		lastKeptAt:   map[int]int64{},
		lastKeptSlot: map[int]int{},
		nextIdx:      1,
	}
}

func grayImage(c uint8) image.Image {
	return solidImage(16, 16, color.RGBA{c, c, c, 255})
}

// TestScreenlogIndexFlushesOnFirstKeptFrame is the regression for the bug that
// left ahmet's box with jpgs on disk but an empty index.json frames[] (so the
// scrubber showed 0 frames): the index must be persisted on the FIRST kept
// frame, not only after screenlogSaveEvery frames or a clean stop.
func TestScreenlogIndexFlushesOnFirstKeptFrame(t *testing.T) {
	withTempScreenlogDir(t)
	a := newTestRecorder("slog-flush1")
	cfg := defaultScreenlogConfig()

	a.ingestFrame(time.Now().UnixMilli(), 0, grayImage(120), "TestApp", "win", cfg)

	// Read the index back FROM DISK (not the in-memory slice) — this is what
	// the viewer/scrubber and a post-reboot resume actually see.
	loaded, err := loadScreenlogSession("slog-flush1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Frames) != 1 {
		t.Fatalf("first kept frame must be flushed to index.json on disk; got %d frames", len(loaded.Frames))
	}
	if loaded.Frames[0].File == "" {
		t.Error("flushed frame should reference its jpg file")
	}
}

// TestScreenlogIndexFlushesOnTimeBound verifies the time-based flush: even when
// fewer than screenlogSaveEvery frames are kept (dedup-heavy static screen),
// the index is re-persisted once screenlogSaveMaxIntervalMs of capture-time has
// passed — so a crash loses at most a few seconds, not the whole session.
func TestScreenlogIndexFlushesOnTimeBound(t *testing.T) {
	withTempScreenlogDir(t)
	a := newTestRecorder("slog-flush2")
	cfg := defaultScreenlogConfig()
	cfg.Dedup = false // keep every frame so we control the count, not dedup

	base := time.Now().UnixMilli()
	a.ingestFrame(base, 0, grayImage(10), "A", "", cfg) // first → flush, lastSaveAt=base
	// A second frame only a moment later, well under screenlogSaveEvery: should
	// NOT have advanced past 1 on disk yet (still within the time window).
	a.ingestFrame(base+100, 0, grayImage(20), "A", "", cfg)
	if loaded, _ := loadScreenlogSession("slog-flush2"); len(loaded.Frames) != 1 {
		t.Fatalf("within the time window the index should still show 1, got %d", len(loaded.Frames))
	}
	// Now jump capture-time past the interval → the next frame must flush both.
	a.ingestFrame(base+screenlogSaveMaxIntervalMs+1, 0, grayImage(30), "A", "", cfg)
	loaded, _ := loadScreenlogSession("slog-flush2")
	if len(loaded.Frames) != 3 {
		t.Fatalf("time-bound flush should persist all 3 frames, got %d", len(loaded.Frames))
	}
}
