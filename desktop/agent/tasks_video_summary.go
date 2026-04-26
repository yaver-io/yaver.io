package main

// tasks_video_summary.go — bridges the Task lifecycle to the vibe-
// preview clip recorder. When a task is created with VideoEnabled, the
// post-finish hook auto-spawns a short MP4 demonstration of the result
// (sim/emulator MP4 for mobile, browser frame burst for web). The
// task's VideoClipID is then surfaced in the task JSON so mobile + web
// task views can render a "▶ Watch demo" button.
//
// Wired in main.go's runServe by setting taskMgr.OnTaskDone to
// MaybeRecordTaskSummary, AFTER the vibePreviewMgr is constructed.

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaybeRecordTaskSummary is the OnTaskDone callback that owns the
// task → clip bridge. Idempotent + non-blocking — kicks off the
// recorder in a goroutine and returns. The recorder's own state
// machine (vibe_preview_clip.go) handles teardown, finalization,
// and the clip_ready SSE event.
//
// Tasks without VideoEnabled return immediately. Tasks that already
// have VideoClipID set (re-entry from auto-retry, etc.) skip too.
func MaybeRecordTaskSummary(t *Task) {
	if t == nil || !t.VideoEnabled || t.VideoClipID != "" {
		return
	}
	mgr := ActiveVibePreviewManager()
	if mgr == nil {
		log.Printf("[task-video] %s: vibe-preview manager not initialised — skipping clip", t.ID)
		return
	}

	// Don't record on failure unless explicitly requested. A crash
	// or stuck task usually doesn't have a meaningful "before/after"
	// to demo — better to skip the LLM/disk cost.
	if t.Status != TaskStatusFinished {
		log.Printf("[task-video] %s: task status %q is not finished — skipping clip", t.ID, t.Status)
		return
	}

	source := autoDetectVideoSource(t)
	project := videoProjectForTask(t)

	rec, err := mgr.StartClip(VibeClipStartOpts{
		Project:        project,
		Source:         VibeClipSource(source),
		DurationMaxSec: 12, // demos should be short — long clips kill mobile bandwidth
		ExerciseHint:   t.Title,
	})
	if err != nil {
		log.Printf("[task-video] %s: StartClip failed: %v", t.ID, err)
		t.VideoStatus = "failed"
		return
	}

	// Stash the clip id on the in-memory copy. The TaskManager's
	// OnTaskDone copies the task before invoking the callback (see
	// fireTaskDone) so we can't mutate the persisted task from
	// here directly. The mobile + web clients refresh their task
	// view periodically; the clip_ready SSE event is the
	// authoritative signal for "video is watchable now".
	t.VideoClipID = rec.ID
	t.VideoStatus = "recording"
	log.Printf("[task-video] %s: started clip %s (source=%s, project=%s)",
		t.ID, rec.ID, source, project)
}

// autoDetectVideoSource picks a reasonable VideoSource when the task
// didn't specify one explicitly. Detection rules:
//   - Explicit task.VideoSource → honour it
//   - WorkDir contains app.json + ios/        → sim-ios
//   - WorkDir contains build.gradle + android → sim-android
//   - WorkDir has package.json with next/vite → browser
//   - Otherwise                              → browser (safe default
//     because chromedp on the agent will at least navigate to localhost).
func autoDetectVideoSource(t *Task) string {
	if s := strings.TrimSpace(t.VideoSource); s != "" {
		return s
	}
	wd := strings.TrimSpace(t.WorkDir)
	if wd == "" {
		return string(VibeClipSourceBrowser)
	}
	if hasFile(wd, "app.json") && hasDir(wd, "ios") {
		return string(VibeClipSourceSimIOS)
	}
	if hasFile(wd, "build.gradle") || hasDir(wd, "android") {
		// Prefer sim-ios when both ios/ and android/ exist (Yaver's
		// own fleet skews iOS-first); detect that here so a typical
		// RN project doesn't accidentally route to adb.
		if hasDir(wd, "ios") {
			return string(VibeClipSourceSimIOS)
		}
		return string(VibeClipSourceSimAndroid)
	}
	return string(VibeClipSourceBrowser)
}

// videoProjectForTask derives a vibe-preview project key from the task.
// Used as the per-session bucket for frames + clips. Same project key
// across multiple tasks groups their demos in one timeline, which is
// what the user wants when iterating on the same workspace.
func videoProjectForTask(t *Task) string {
	if wd := strings.TrimSpace(t.WorkDir); wd != "" {
		base := filepath.Base(wd)
		if base != "" && base != "." && base != "/" {
			return base
		}
	}
	if t.ID != "" {
		return "task-" + t.ID[:min8(len(t.ID))]
	}
	return "task-untitled"
}

func min8(n int) int {
	if n < 8 {
		return n
	}
	return 8
}

// chainTaskDoneCallbacks wraps an existing OnTaskDone (if any) with
// MaybeRecordTaskSummary so we can be wired in addition to whatever
// other lifecycle observers main.go already attaches.
func chainTaskDoneCallbacks(prev func(*Task)) func(*Task) {
	return func(t *Task) {
		MaybeRecordTaskSummary(t)
		if prev != nil {
			prev(t)
		}
	}
}

// reapInactiveTaskClips runs from a goroutine in main.go's runServe to
// flip VideoStatus to "stale" if a clip is still in "recording" state
// past the recorder's max duration window. Defensive — the clip
// recorder usually emits clip_ready promptly; this catches edge cases
// where the recorder process dies without finalizing.
func reapInactiveTaskClips(tm *TaskManager) {
	if tm == nil {
		return
	}
	go func() {
		t := time.NewTicker(2 * time.Minute)
		defer t.Stop()
		for range t.C {
			tm.mu.Lock()
			now := time.Now()
			for _, task := range tm.tasks {
				if task.VideoClipID == "" || task.VideoStatus != "recording" {
					continue
				}
				if task.FinishedAt == nil {
					continue
				}
				if now.Sub(*task.FinishedAt) > 5*time.Minute {
					// Recorder should have finished long ago. Mark stale.
					task.VideoStatus = "stale"
				}
			}
			tm.mu.Unlock()
		}
	}()
	_ = os.Getpid // keep the import in case the file ever needs it
}
