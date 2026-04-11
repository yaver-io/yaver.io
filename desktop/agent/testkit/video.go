package testkit

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Failure-only video capture.
//
// chromedp can stream a "screencast" feed from Chrome via CDP — one
// PNG per frame as the page changes. We start capture at the
// beginning of every spec, hold the most recent ~120 frames in a ring
// buffer, and on failure write the buffer out as a sequence of PNGs
// the dev can step through. We deliberately do NOT assemble an mp4
// here: that would require ffmpeg or cgo, both of which would
// poison the "single Go binary" promise. The mobile app's "Runs" tab
// already knows how to render a PNG sequence as a frame-by-frame
// playback, which is what most CI failure videos look like anyway.
//
// Cost: a few MB of RAM per running spec, zero on disk until the
// spec actually fails.

// FrameRing is a small in-memory ring buffer of recent screencast
// frames. The runner attaches one to each browser context.
type FrameRing struct {
	mu       sync.Mutex
	frames   [][]byte
	max      int
	captures int
}

// NewFrameRing returns a ring that holds up to `max` frames (default
// 120 — about 8s of capture at 15 fps).
func NewFrameRing(max int) *FrameRing {
	if max < 1 {
		max = 120
	}
	return &FrameRing{max: max}
}

// Push appends a frame, evicting the oldest if the ring is full.
func (r *FrameRing) Push(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, b)
	if len(r.frames) > r.max {
		r.frames = r.frames[len(r.frames)-r.max:]
	}
	r.captures++
}

// Snapshot returns a copy of the current frames so the writer can
// flush without holding the lock.
func (r *FrameRing) Snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.frames))
	copy(out, r.frames)
	return out
}

// StartScreencast subscribes to Chrome's CDP screencast events and
// pushes every frame into `ring`. Returns a stop function that the
// caller defers. The screencast is automatically stopped when the
// browser context is cancelled.
func StartScreencast(ctx context.Context, ring *FrameRing) (stop func(), err error) {
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		if e, ok := ev.(*page.EventScreencastFrame); ok {
			data, decErr := base64.StdEncoding.DecodeString(e.Data)
			if decErr == nil {
				ring.Push(data)
			}
			// CDP requires an Ack for every frame or it pauses the stream.
			sid := e.SessionID
			go func() {
				_ = chromedp.Run(ctx, page.ScreencastFrameAck(sid))
			}()
		}
	})
	if err := chromedp.Run(ctx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatPng).
			WithEveryNthFrame(2).
			WithMaxWidth(1280).
			WithMaxHeight(720),
	); err != nil {
		return func() {}, fmt.Errorf("start screencast: %w", err)
	}
	stop = func() {
		_ = chromedp.Run(ctx, page.StopScreencast())
	}
	return stop, nil
}

// FlushFrames writes the ring's contents to a per-step `frames`
// directory under the spec's artifacts dir. Used by the runner when
// a step fails so the dev can scrub through the last few seconds.
func FlushFrames(artifactDir, label string, ring *FrameRing) (string, error) {
	if ring == nil {
		return "", nil
	}
	frames := ring.Snapshot()
	if len(frames) == 0 {
		return "", nil
	}
	dir := filepath.Join(artifactDir, fmt.Sprintf("%s-frames", sanitizeName(label)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for i, f := range frames {
		path := filepath.Join(dir, fmt.Sprintf("%04d.png", i))
		if err := os.WriteFile(path, f, 0o644); err != nil {
			return dir, err
		}
	}
	// Drop a tiny manifest so the mobile player knows how many
	// frames + the approximate frame interval.
	manifestPath := filepath.Join(dir, "manifest.txt")
	manifest := fmt.Sprintf("frames=%d\nfps=15\ncaptured_at=%s\n", len(frames), time.Now().Format(time.RFC3339))
	_ = os.WriteFile(manifestPath, []byte(manifest), 0o644)
	return dir, nil
}
