package main

// vibe_preview_clip.go — Phase 2.5: short MP4 demo clips of the running
// app, recorded directly from the simulator/emulator the agent already
// drives. Reuses the in-memory clip ringbuffer + per-session SSE channel
// that Phase 2 set up.
//
// Sources:
//   sim-ios     — `xcrun simctl io booted recordVideo --codec=h264 <out>.mp4`
//   sim-android — `adb shell screenrecord --time-limit=N /sdcard/<id>.mp4` + adb pull
//   browser     — Phase 2 frame stream, no clip (a frame burst already exists)
//   phone       — driven from the Yaver mobile app side (Phase 5)

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VibeClipSource enumerates where the recording comes from.
type VibeClipSource string

const (
	VibeClipSourceSimIOS     VibeClipSource = "sim-ios"
	VibeClipSourceSimAndroid VibeClipSource = "sim-android"
	VibeClipSourceBrowser    VibeClipSource = "browser"
	VibeClipSourcePhone      VibeClipSource = "phone"
)

// VibeClipStartOpts is the input for StartClip.
type VibeClipStartOpts struct {
	Project        string         `json:"project"`
	Source         VibeClipSource `json:"source"`         // "" → auto-detect
	DurationMaxSec int            `json:"durationMaxSec"` // default 12, max 30
	ExerciseHint   string         `json:"exerciseHint"`   // free-form text the exercise driver may use
}

// VibeClipRecorder holds the per-session machinery for an active recording.
// One per active clip; the manager keeps these in clipRecorders by clip ID.
type VibeClipRecorder struct {
	clipID    string
	project   string
	source    VibeClipSource
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	startedAt time.Time
	durMaxSec int

	// On-disk targets
	mp4Path    string
	posterPath string

	// adb-only: the screenrecord file lives on the device first; adb pull
	// after stop. iOS recordVideo writes directly to host disk.
	devicePath string

	doneCh chan struct{}
}

// vibeClipRecorders is a process-wide registry of active recorders.
var (
	vibeClipRecMu sync.Mutex
	vibeClipRec   = make(map[string]*VibeClipRecorder)
)

// vibeClipDurationDefaultSec is the fallback when the caller doesn't specify.
const vibeClipDurationDefaultSec = 12

// vibeClipDurationMaxSec caps recordings to avoid producing huge files
// that bust the relay 200 MB cap on a single response.
const vibeClipDurationMaxSec = 30

// ─── Source detection ────────────────────────────────────────────────────────

// resolveClipSource picks the recording strategy. Explicit wins; otherwise
// platform + tooling presence + obvious environmental hints.
func resolveClipSource(opts VibeClipStartOpts) (VibeClipSource, error) {
	if opts.Source != "" {
		return opts.Source, nil
	}
	// macOS + xcrun → sim-ios. Anywhere with adb attached → sim-android.
	// Both → sim-ios (most yaver users on Mac, RN/Expo flow defaults to iOS).
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("xcrun"); err == nil {
			return VibeClipSourceSimIOS, nil
		}
	}
	if _, err := exec.LookPath("adb"); err == nil {
		return VibeClipSourceSimAndroid, nil
	}
	return "", fmt.Errorf("no clip source available: install Xcode (for iOS) or platform-tools/adb (for Android), or pass --source explicitly")
}

// ─── Recorder lifecycle ──────────────────────────────────────────────────────

// StartClip kicks off a recording for project. Returns the clip metadata
// immediately (status=recording). The MP4 lands on disk + status flips to
// "ready" when the recording stops (either time-cap hits, StopClip is
// called, or the underlying tool exits on its own).
func (m *VibePreviewManager) StartClip(opts VibeClipStartOpts) (*VibeClipRecord, error) {
	if m == nil {
		return nil, fmt.Errorf("vibe-preview manager not initialised")
	}
	if strings.TrimSpace(opts.Project) == "" {
		return nil, fmt.Errorf("project is required")
	}
	dur := opts.DurationMaxSec
	if dur <= 0 {
		dur = vibeClipDurationDefaultSec
	}
	if dur > vibeClipDurationMaxSec {
		dur = vibeClipDurationMaxSec
	}

	source, err := resolveClipSource(opts)
	if err != nil {
		return nil, err
	}

	clipID := newClipID()
	now := m.nowFn()

	dir := filepath.Join(m.resolveDiskRoot(), "clips", sanitizeBranchName(opts.Project))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir clips dir: %w", err)
	}
	mp4Path := filepath.Join(dir, clipID+".mp4")
	posterPath := filepath.Join(dir, clipID+".poster.jpg")

	rec := &VibeClipRecord{
		ID:        clipID,
		Project:   opts.Project,
		Source:    string(source),
		StartedAt: now,
		Status:    "recording",
		Path:      mp4Path,
	}

	// Build + spawn the per-source command. Failure to spawn → rec stays
	// out of the registry; caller gets the error.
	recorder, err := newClipRecorder(clipID, opts.Project, source, mp4Path, posterPath, dur)
	if err != nil {
		return nil, err
	}

	vibeClipRecMu.Lock()
	vibeClipRec[clipID] = recorder
	vibeClipRecMu.Unlock()

	m.RegisterClip(opts.Project, rec)
	m.EmitClipEvent(opts.Project, VibePreviewEvent{
		Type:      "clip_started",
		Project:   opts.Project,
		ClipID:    clipID,
		Source:    string(source),
		DurationS: float64(dur),
	})

	// Phase 7: drive the running app while the clip records, so the
	// MP4 captures interaction instead of an idle home screen. The
	// exerciser is a no-op when Maestro isn't installed — recorder
	// still produces a clip in that case.
	if source == VibeClipSourceSimIOS || source == VibeClipSourceSimAndroid {
		exerciseCtx, exerciseCancel := context.WithTimeout(context.Background(), time.Duration(dur)*time.Second)
		go func() {
			defer exerciseCancel()
			<-recorder.doneCh // tie the exercise lifetime to the recorder
		}()
		m.ExerciseClip(exerciseCtx, clipID, opts.Project, opts.ExerciseHint, opts.ExerciseHint)
	}

	// Watch the recorder asynchronously. When the process exits (timeout
	// or explicit Stop), finalize the on-disk artifacts + emit clip_ready.
	go m.watchClipRecorder(recorder, rec, dur)

	return rec, nil
}

// StopClip signals an active recording to wrap up early. Idempotent on a
// clip that already finished.
func (m *VibePreviewManager) StopClip(clipID string) error {
	vibeClipRecMu.Lock()
	recorder, ok := vibeClipRec[clipID]
	vibeClipRecMu.Unlock()
	if !ok {
		return fmt.Errorf("clip %q not active (or already finished)", clipID)
	}
	if recorder.cancel != nil {
		recorder.cancel()
	}
	// Wait briefly for the watcher to finalize so the immediate
	// /clips/<id> GET sees status=ready.
	select {
	case <-recorder.doneCh:
	case <-time.After(3 * time.Second):
		// Took longer than expected — caller can poll; we don't block forever.
	}
	return nil
}

// watchClipRecorder reaps the recorder process, finalizes the file, and
// emits clip_ready. Runs in its own goroutine.
func (m *VibePreviewManager) watchClipRecorder(rec *VibeClipRecorder, meta *VibeClipRecord, durMaxSec int) {
	defer close(rec.doneCh)

	waitErr := rec.cmd.Wait()

	// adb screenrecord wrote to the device, not the host — pull it now.
	if rec.source == VibeClipSourceSimAndroid && rec.devicePath != "" {
		pull := exec.Command("adb", "shell", "ls", "-l", rec.devicePath)
		_ = pull.Run() // sanity check; ignored
		pullCmd := exec.Command("adb", "pull", rec.devicePath, rec.mp4Path)
		if out, err := pullCmd.CombinedOutput(); err != nil {
			meta.Status = "failed"
			meta.Err = fmt.Sprintf("adb pull: %v: %s", err, out)
		}
		// Best-effort cleanup on the device.
		_ = exec.Command("adb", "shell", "rm", rec.devicePath).Run()
	}

	// Stat the final MP4. If the file is missing or empty, mark failed.
	st, statErr := os.Stat(rec.mp4Path)
	if statErr != nil || (st != nil && st.Size() == 0) {
		meta.Status = "failed"
		if statErr != nil {
			meta.Err = fmt.Sprintf("stat: %v", statErr)
		} else {
			meta.Err = "empty mp4"
		}
		if waitErr != nil && meta.Err == "" {
			meta.Err = waitErr.Error()
		}
	} else {
		meta.Status = "ready"
		meta.SizeBytes = st.Size()
		meta.DurationSec = float64(time.Since(meta.StartedAt).Seconds())
		// Cap reported duration at requested max + 0.5 s slack to account
		// for tool startup/teardown.
		maxDur := float64(durMaxSec) + 0.5
		if meta.DurationSec > maxDur {
			meta.DurationSec = maxDur
		}
		// Lazy poster: extract first frame via ffmpeg if available. Best
		// effort — if ffmpeg isn't installed, the mobile UI falls back to
		// the most recent frame from the live stream.
		if posterErr := extractClipPoster(rec.mp4Path, rec.posterPath); posterErr == nil {
			meta.PosterPath = rec.posterPath
		}
	}
	meta.EndedAt = m.nowFn()

	// Push the final record back into the manager so /clips/list sees the
	// updated status; emit the SSE event.
	m.RegisterClip(meta.Project, meta)
	m.EmitClipEvent(meta.Project, VibePreviewEvent{
		Type:      "clip_ready",
		Project:   meta.Project,
		ClipID:    meta.ID,
		Source:    meta.Source,
		DurationS: meta.DurationSec,
		Size:      int(meta.SizeBytes),
		Message:   meta.Err,
	})

	// Drop from the active registry.
	vibeClipRecMu.Lock()
	delete(vibeClipRec, rec.clipID)
	vibeClipRecMu.Unlock()
}

// ─── Per-source command construction ──────────────────────────────────────────

// newClipRecorder builds + spawns the recording command. The returned
// recorder is ready to be Wait()-ed.
func newClipRecorder(clipID, project string, source VibeClipSource, mp4Path, posterPath string, durMaxSec int) (*VibeClipRecorder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(durMaxSec+5)*time.Second)

	rec := &VibeClipRecorder{
		clipID:     clipID,
		project:    project,
		source:     source,
		cancel:     cancel,
		startedAt:  time.Now(),
		durMaxSec:  durMaxSec,
		mp4Path:    mp4Path,
		posterPath: posterPath,
		doneCh:     make(chan struct{}),
	}

	switch source {
	case VibeClipSourceSimIOS:
		// xcrun simctl io booted recordVideo emits an MP4 directly.
		// --codec h264 keeps the file playable by every platform's
		// default video element. There's no built-in time cap, so we
		// rely on context.WithTimeout above to SIGINT the process.
		args := []string{
			"simctl", "io", "booted", "recordVideo",
			"--codec=h264",
			"--mask=ignored",
			mp4Path,
		}
		rec.cmd = exec.CommandContext(ctx, "xcrun", args...)

	case VibeClipSourceSimAndroid:
		// screenrecord runs on the device, so we record to /sdcard then
		// adb pull on Wait. --time-limit caps duration on the device.
		devicePath := "/sdcard/yaver-vibe-" + clipID + ".mp4"
		rec.devicePath = devicePath
		rec.cmd = exec.CommandContext(ctx, "adb", "shell",
			"screenrecord", "--time-limit", strconv.Itoa(durMaxSec),
			devicePath,
		)

	case VibeClipSourcePhone:
		// Phone-side capture is driven from the mobile app — the agent
		// just allocates the clip ID + on-disk path and waits for an
		// upload (Phase 5). For now, treat the recorder as a stub: the
		// process is a sleep that exits at durMaxSec.
		rec.cmd = exec.CommandContext(ctx, "sleep", strconv.Itoa(durMaxSec+5))

	case VibeClipSourceBrowser:
		cancel()
		return nil, fmt.Errorf("browser source uses the frame stream — record via /vibing/preview/start mode=live")
	default:
		cancel()
		return nil, fmt.Errorf("unknown clip source %q", source)
	}

	if err := rec.cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s recording: %w", source, err)
	}
	return rec, nil
}

// extractClipPoster pulls the first frame as JPEG via ffmpeg. Best-effort.
func extractClipPoster(mp4Path, posterPath string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return err // skip silently if ffmpeg missing
	}
	cmd := exec.Command("ffmpeg",
		"-y", "-loglevel", "error",
		"-i", mp4Path,
		"-vframes", "1",
		"-q:v", "3",
		posterPath,
	)
	return cmd.Run()
}

// ─── ID generation ────────────────────────────────────────────────────────────

func newClipID() string {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Crypto-rand failure shouldn't happen; fall back to time-based.
		return fmt.Sprintf("c_%d", time.Now().UnixNano())
	}
	return "c_" + hex.EncodeToString(b[:])
}
