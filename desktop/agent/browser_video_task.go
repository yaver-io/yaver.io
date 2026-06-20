package main

// browser_video_task.go — cross-process glue that lets a TASK return a video of
// the agent's browser work (P2). The agent runs in a `yaver mcp` stdio child
// (a separate process from the daemon that serves clips), so two seams need
// bridging:
//
//   1. Serving: a clip the agent's process recorded to the canonical on-disk
//      layout (~/.yaver/vibe-preview/clips/<project>/<id>.mp4) must be servable
//      by the daemon even though it isn't in the daemon's in-memory clip map.
//      findClipOnDisk + the serve-handler fallback close that gap.
//
//   2. Attribution: the agent's process knows YAVER_TASK_ID and the clip id it
//      created; the daemon (which owns the persisted Task) does not. A small
//      marker file keyed by task id carries the clip id across, read by the
//      daemon's OnTaskDone hook (MaybeRecordTaskSummary).
//
// Recording itself is auto-enabled when the task runner sets
// YAVER_TASK_RECORD_BROWSER=1 (see taskEnv); browser_open then records without
// the agent having to opt in per call.

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// vibePreviewRoot mirrors VibePreviewManager.resolveDiskRoot()'s default so a
// process without a manager handle still finds the canonical clip directory.
func vibePreviewRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".yaver", "vibe-preview")
}

// findClipOnDisk locates a finalized clip's MP4 (and poster, if present) by id
// under the canonical clips layout, regardless of which process wrote it. Clip
// ids are c_<hex> by construction; the caller has already rejected path
// separators, so a glob on the id is safe.
func findClipOnDisk(id string) (mp4Path, posterPath string, ok bool) {
	if id == "" {
		return "", "", false
	}
	matches, _ := filepath.Glob(filepath.Join(vibePreviewRoot(), "clips", "*", id+".mp4"))
	if len(matches) == 0 {
		return "", "", false
	}
	mp4Path = matches[0]
	poster := strings.TrimSuffix(mp4Path, ".mp4") + ".poster.jpg"
	if st, err := os.Stat(poster); err == nil && st.Size() > 0 {
		posterPath = poster
	}
	if st, err := os.Stat(mp4Path); err != nil || st.Size() == 0 {
		return "", "", false
	}
	return mp4Path, posterPath, true
}

// taskClipMarkerPath is where a recording process records "task X → clip Y".
func taskClipMarkerPath(taskID string) string {
	return filepath.Join(vibePreviewRoot(), "task-clips", sanitizeMarkerKey(taskID)+".clip")
}

// writeTaskClipMarker records the clip id a recording process created for a
// task, so the daemon can attribute it later. Best-effort.
func writeTaskClipMarker(taskID, clipID string) {
	taskID = strings.TrimSpace(taskID)
	clipID = strings.TrimSpace(clipID)
	if taskID == "" || clipID == "" {
		return
	}
	p := taskClipMarkerPath(taskID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(clipID), 0o600)
}

// readTaskClipMarker returns the clip id a recording process attributed to this
// task, if any.
func readTaskClipMarker(taskID string) (string, bool) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", false
	}
	b, err := os.ReadFile(taskClipMarkerPath(taskID))
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", false
	}
	return id, true
}

// browserClipBucket is the object-storage bucket recorded clips are uploaded to
// for durable, box-independent sharing (P4).
const browserClipBucket = "yaver-clips"

// maybeShareClipDurably uploads a finalized clip to object storage and returns a
// presigned URL that outlives the box. Best-effort + opt-in: if MinIO/object
// storage isn't running on this host it returns "" instantly (isRunning is a
// quick docker-ps / TCP probe), so the no-storage path costs nothing. The
// always-available path remains the relay-served /vibing/preview/clip/<id>.
func maybeShareClipDurably(mp4Path, clipID string) string {
	if mp4Path == "" || clipID == "" {
		return ""
	}
	sm := NewStorageManager()
	if sm == nil || !sm.isRunning() {
		return ""
	}
	// CreateBucket is idempotent enough for our purpose — ignore "already exists".
	_, _ = sm.CreateBucket(browserClipBucket)
	key := clipID + ".mp4"
	if _, err := sm.Upload(browserClipBucket, key, mp4Path); err != nil {
		return ""
	}
	url, err := sm.Presign(browserClipBucket, key, 7*24*time.Hour)
	if err != nil {
		return ""
	}
	return url
}

// sanitizeMarkerKey keeps a task id safe as a filename component.
func sanitizeMarkerKey(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "task"
	}
	return out
}
