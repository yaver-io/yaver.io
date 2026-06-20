package main

// browser_video.go — record a browser session to an MP4 by capturing the
// agent's OWN chromedp session at a steady cadence and piping the frames into
// ffmpeg. This is the browser-source clip recorder the task runner uses to
// return a video of a web run, identically in self-hosted and managed-cloud
// deployments.
//
// Why this and not Playwright recordVideo: the frames come from the SAME
// chromedp context that browser_navigate/click/type drive, so the video is
// exactly what the agent did — no separate replay, no extra runtime dep.
//
// Headless-safe: CDP CaptureScreenshot renders offscreen, so no X server / Xvfb
// is needed; the same path runs on a laptop and on a provisioned cloud box
// (once chromium + ffmpeg are in the image). The produced MP4 is registered as
// a VibeClipRecord, so it reuses the existing /vibing/preview/clip/<id>
// range-serving, poster, and listing — no new transport.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	// browserClipQuality is the JPEG quality CDP encodes each captured frame at.
	browserClipQuality = 60
	// browserClipFPS is the capture cadence (frames/sec). ffmpeg paces output by
	// wall-clock arrival, so jitter below this rate still yields real-time video.
	browserClipFPS = 10
	// browserClipMaxSec is a safety cap so a session left open without an
	// explicit browser_close still finalizes its clip. A normal run is stopped
	// by browser_close well before this.
	browserClipMaxSec = 600
)

// BrowserVideoRecorder captures a browser session's viewport at a steady
// cadence and encodes it to MP4 via ffmpeg. One per recording session.
type BrowserVideoRecorder struct {
	clipID     string
	project    string
	mp4Path    string
	posterPath string

	vpm        *VibePreviewManager
	rec        *VibeClipRecord
	browserCtx context.Context

	ffmpeg *exec.Cmd
	stdin  io.WriteCloser

	startedAt time.Time
	durMaxSec int

	done      chan struct{} // closed by Stop to signal the capture loop to exit
	loopDone  chan struct{} // closed by the capture loop when it has exited
	stopOnce  sync.Once
	stopTimer *time.Timer
}

// startBrowserRecording begins recording browserCtx to an MP4. It registers a
// VibeClipRecord (status=recording) immediately and returns the recorder; the
// caller surfaces rec.clipID. Stop() — or the safety duration cap — finalizes
// the file (status ready/failed + size + poster).
func startBrowserRecording(vpm *VibePreviewManager, browserCtx context.Context, project string, durMaxSec int) (*BrowserVideoRecorder, error) {
	if vpm == nil {
		return nil, fmt.Errorf("recording unavailable: preview manager not initialised")
	}
	if browserCtx == nil {
		return nil, fmt.Errorf("recording unavailable: no browser context")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH — required for browser video recording")
	}
	if project == "" {
		project = "browser"
	}
	if durMaxSec <= 0 || durMaxSec > browserClipMaxSec {
		durMaxSec = browserClipMaxSec
	}

	clipID := newClipID()
	dir := filepath.Join(vpm.resolveDiskRoot(), "clips", sanitizeBranchName(project))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir clips dir: %w", err)
	}
	mp4Path := filepath.Join(dir, clipID+".mp4")
	posterPath := filepath.Join(dir, clipID+".poster.jpg")

	// ffmpeg reads concatenated JPEG frames from stdin (image2pipe). Each frame
	// is timestamped by wall-clock arrival, then resampled to a constant FPS, so
	// idle gaps hold the last frame instead of collapsing — the MP4 plays in
	// real time on every player. yuv420p + even dims for broad compatibility.
	ff := exec.Command("ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "image2pipe",
		"-use_wallclock_as_timestamps", "1",
		"-i", "-",
		"-an",
		"-vf", fmt.Sprintf("fps=%d,scale=trunc(iw/2)*2:trunc(ih/2)*2", browserClipFPS),
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "28",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		mp4Path,
	)
	stdin, err := ff.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdin: %w", err)
	}
	if err := ff.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	now := vpm.nowFn()
	rec := &VibeClipRecord{
		ID:        clipID,
		Project:   project,
		Source:    string(VibeClipSourceBrowser),
		StartedAt: now,
		Status:    "recording",
		Path:      mp4Path,
	}
	r := &BrowserVideoRecorder{
		clipID:     clipID,
		project:    project,
		mp4Path:    mp4Path,
		posterPath: posterPath,
		vpm:        vpm,
		rec:        rec,
		browserCtx: browserCtx,
		ffmpeg:     ff,
		stdin:      stdin,
		startedAt:  now,
		durMaxSec:  durMaxSec,
		done:       make(chan struct{}),
		loopDone:   make(chan struct{}),
	}

	vpm.RegisterClip(project, rec)
	vpm.EmitClipEvent(project, VibePreviewEvent{
		Type:      "clip_started",
		Project:   project,
		ClipID:    clipID,
		Source:    string(VibeClipSourceBrowser),
		DurationS: float64(durMaxSec),
	})

	go r.captureLoop()
	// Safety cap: finalize even if browser_close never arrives.
	r.stopTimer = time.AfterFunc(time.Duration(durMaxSec)*time.Second, func() { r.Stop() })

	return r, nil
}

// captureLoop grabs a JPEG viewport frame at browserClipFPS and writes it to
// ffmpeg's stdin. It is the single writer to stdin, so no locking is needed.
// It exits when Stop closes done, or when the browser context is canceled
// (session closing under it).
func (r *BrowserVideoRecorder) captureLoop() {
	defer close(r.loopDone)
	ticker := time.NewTicker(time.Second / time.Duration(browserClipFPS))
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-r.browserCtx.Done():
			return
		case <-ticker.C:
			var buf []byte
			ctx, cancel := context.WithTimeout(r.browserCtx, 3*time.Second)
			err := chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
				d, e := page.CaptureScreenshot().
					WithFormat(page.CaptureScreenshotFormatJpeg).
					WithQuality(browserClipQuality).
					Do(c)
				if e != nil {
					return e
				}
				buf = d
				return nil
			}))
			cancel()
			if err != nil || len(buf) == 0 {
				if r.browserCtx.Err() != nil {
					return // session tearing down
				}
				continue // transient (mid-navigation paint, etc.) — skip a frame
			}
			if _, werr := r.stdin.Write(buf); werr != nil {
				return // ffmpeg gone
			}
		}
	}
}

// Stop ends the recording: halts the capture loop, flushes + waits ffmpeg, and
// finalizes the VibeClipRecord (status ready/failed, size, poster). Idempotent;
// safe to call from browser_close, the idle reaper, and the safety timer.
// Call this BEFORE canceling the browser context so in-flight frames flush.
func (r *BrowserVideoRecorder) Stop() {
	r.stopOnce.Do(func() {
		if r.stopTimer != nil {
			r.stopTimer.Stop()
		}
		close(r.done)
		<-r.loopDone        // capture loop has stopped writing
		_ = r.stdin.Close() // EOF → ffmpeg flushes and exits
		_ = r.ffmpeg.Wait()

		st, statErr := os.Stat(r.mp4Path)
		if statErr != nil || st == nil || st.Size() == 0 {
			r.rec.Status = "failed"
			if statErr != nil {
				r.rec.Err = statErr.Error()
			} else {
				r.rec.Err = "empty mp4"
			}
		} else {
			r.rec.Status = "ready"
			r.rec.SizeBytes = st.Size()
			r.rec.DurationSec = time.Since(r.startedAt).Seconds()
			if posterErr := extractClipPoster(r.mp4Path, r.posterPath); posterErr == nil {
				r.rec.PosterPath = r.posterPath
			}
			// P4: best-effort durable share URL (instant no-op when object
			// storage isn't running). Synchronous so it completes before the
			// agent's process can exit on stdin EOF.
			if url := maybeShareClipDurably(r.mp4Path, r.clipID); url != "" {
				r.rec.ShareURL = url
			}
		}
		r.rec.EndedAt = r.vpm.nowFn()
		r.vpm.RegisterClip(r.rec.Project, r.rec)
		r.vpm.EmitClipEvent(r.rec.Project, VibePreviewEvent{
			Type:      "clip_ready",
			Project:   r.rec.Project,
			ClipID:    r.rec.ID,
			Source:    r.rec.Source,
			DurationS: r.rec.DurationSec,
			Size:      int(r.rec.SizeBytes),
			Message:   r.rec.Err,
		})
	})
}
