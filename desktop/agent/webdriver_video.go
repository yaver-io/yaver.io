package main

// webdriver_video.go records any W3C WebDriver session (Chrome, Firefox, or
// real Safari) through the same Vibe clip contract used by CDP browser_open.
// The frame source is the operation itself: periodic WebDriver screenshots.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/yaver-io/agent/testkit"
)

type WebDriverVideoRecorder struct {
	clipID, mp4Path, posterPath string
	vpm                         *VibePreviewManager
	rec                         *VibeClipRecord
	driver                      testkit.WebDriver
	ffmpeg                      *exec.Cmd
	stdin                       io.WriteCloser
	startedAt                   time.Time
	done, loopDone              chan struct{}
	stopOnce                    sync.Once
	stopTimer                   *time.Timer
}

func startWebDriverRecording(vpm *VibePreviewManager, driver testkit.WebDriver, project string, maxSec int) (*WebDriverVideoRecorder, error) {
	if vpm == nil || driver == nil {
		return nil, fmt.Errorf("recording unavailable: webdriver clip sink is not initialized")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found on PATH — required for browser video recording")
	}
	if project == "" {
		project = "webdriver"
	}
	if maxSec <= 0 || maxSec > browserClipMaxSec {
		maxSec = browserClipMaxSec
	}
	clipID := newClipID()
	dir := filepath.Join(vpm.resolveDiskRoot(), "clips", sanitizeBranchName(project))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	mp4 := filepath.Join(dir, clipID+".mp4")
	poster := filepath.Join(dir, clipID+".poster.jpg")
	ff := exec.Command("ffmpeg",
		"-y", "-loglevel", "error", "-f", "image2pipe", "-use_wallclock_as_timestamps", "1", "-i", "-", "-an",
		"-vf", "fps=5,scale=trunc(iw/2)*2:trunc(ih/2)*2", "-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
		"-pix_fmt", "yuv420p", "-movflags", "+faststart", mp4,
	)
	stdin, err := ff.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := ff.Start(); err != nil {
		return nil, err
	}
	now := vpm.nowFn()
	rec := &VibeClipRecord{ID: clipID, Project: project, Source: string(VibeClipSourceBrowser), StartedAt: now, Status: "recording", Path: mp4}
	r := &WebDriverVideoRecorder{
		clipID: clipID, mp4Path: mp4, posterPath: poster, vpm: vpm, rec: rec, driver: driver,
		ffmpeg: ff, stdin: stdin, startedAt: now, done: make(chan struct{}), loopDone: make(chan struct{}),
	}
	vpm.RegisterClip(project, rec)
	vpm.EmitClipEvent(project, VibePreviewEvent{Type: "clip_started", Project: project, ClipID: clipID, Source: rec.Source, DurationS: float64(maxSec)})
	go r.captureLoop()
	r.stopTimer = time.AfterFunc(time.Duration(maxSec)*time.Second, r.Stop)
	return r, nil
}

func (r *WebDriverVideoRecorder) captureLoop() {
	defer close(r.loopDone)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			frame, err := r.driver.Screenshot(ctx)
			cancel()
			if err != nil || len(frame) == 0 {
				continue
			}
			if _, err := r.stdin.Write(frame); err != nil {
				return
			}
		}
	}
}

func (r *WebDriverVideoRecorder) Stop() {
	r.stopOnce.Do(func() {
		if r.stopTimer != nil {
			r.stopTimer.Stop()
		}
		close(r.done)
		<-r.loopDone
		_ = r.stdin.Close()
		_ = r.ffmpeg.Wait()
		st, err := os.Stat(r.mp4Path)
		if err != nil || st.Size() == 0 {
			r.rec.Status = "failed"
			if err != nil {
				r.rec.Err = err.Error()
			} else {
				r.rec.Err = "empty mp4"
			}
		} else {
			r.rec.Status, r.rec.SizeBytes, r.rec.DurationSec = "ready", st.Size(), time.Since(r.startedAt).Seconds()
			if extractClipPoster(r.mp4Path, r.posterPath) == nil {
				r.rec.PosterPath = r.posterPath
			}
			if url := maybeShareClipDurably(r.mp4Path, r.clipID); url != "" {
				r.rec.ShareURL = url
			}
		}
		r.rec.EndedAt = r.vpm.nowFn()
		r.vpm.RegisterClip(r.rec.Project, r.rec)
		r.vpm.EmitClipEvent(r.rec.Project, VibePreviewEvent{Type: "clip_ready", Project: r.rec.Project, ClipID: r.rec.ID, Source: r.rec.Source, DurationS: r.rec.DurationSec, Size: int(r.rec.SizeBytes), Message: r.rec.Err})
	})
}
