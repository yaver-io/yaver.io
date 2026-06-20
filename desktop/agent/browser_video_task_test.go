package main

// browser_video_task_test.go — P2 cross-process glue: a clip recorded by a task
// agent's separate process must be (a) servable by the daemon by id from disk,
// and (b) linkable back to the task via the marker on completion.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedClipOnDisk writes a fake finalized clip into the canonical layout under
// the (test-isolated) HOME and returns its id.
func seedClipOnDisk(t *testing.T, project, id string, body []byte) {
	t.Helper()
	dir := filepath.Join(vibePreviewRoot(), "clips", project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".mp4"), body, 0o600); err != nil {
		t.Fatalf("write clip: %v", err)
	}
}

func TestFindClipOnDisk_AndMarkerRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const proj, id = "demo-proj", "c_abc123def456"
	seedClipOnDisk(t, proj, id, []byte("not-a-real-mp4-but-nonempty"))

	mp4, _, ok := findClipOnDisk(id)
	if !ok {
		t.Fatal("findClipOnDisk did not find the seeded clip")
	}
	if filepath.Base(mp4) != id+".mp4" {
		t.Fatalf("resolved wrong path: %s", mp4)
	}
	if _, _, ok := findClipOnDisk("c_missing"); ok {
		t.Fatal("findClipOnDisk reported a missing clip as present")
	}

	// Empty file must NOT resolve (mirrors the recorder's empty-mp4 → failed).
	seedClipOnDisk(t, proj, "c_empty", nil)
	if _, _, ok := findClipOnDisk("c_empty"); ok {
		t.Fatal("findClipOnDisk resolved an empty clip")
	}

	writeTaskClipMarker("task-xyz", id)
	got, ok := readTaskClipMarker("task-xyz")
	if !ok || got != id {
		t.Fatalf("marker round-trip: got %q ok=%v, want %q", got, ok, id)
	}
	if _, ok := readTaskClipMarker("task-none"); ok {
		t.Fatal("readTaskClipMarker returned a marker that was never written")
	}
}

func TestClipServeDiskFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const proj, id = "demo-proj", "c_servefallback01"
	body := []byte("FAKEMP4DATA-0123456789")
	seedClipOnDisk(t, proj, id, body)

	// A daemon whose in-memory clip map has NO record of this clip (it was
	// recorded by another process) must still serve it from disk.
	s := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/vibing/preview/clip/"+id, nil)
	rw := httptest.NewRecorder()
	s.handleVibePreviewClip(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rw.Code, rw.Body.String())
	}
	if rw.Body.Len() != len(body) {
		t.Fatalf("served %d bytes, want %d", rw.Body.Len(), len(body))
	}
	if ct := rw.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("content-type = %q, want video/mp4", ct)
	}

	// Unknown id → 404.
	rw2 := httptest.NewRecorder()
	s.handleVibePreviewClip(rw2, httptest.NewRequest(http.MethodGet, "/vibing/preview/clip/c_nope", nil))
	if rw2.Code != http.StatusNotFound {
		t.Fatalf("unknown clip status = %d, want 404", rw2.Code)
	}
}

func TestMaybeShareClipDurably_NoStorageIsNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// With no object storage running (the default), durable share is a no-op
	// and must return "" fast — the relay-served clip URL remains the path.
	if url := maybeShareClipDurably("", "c_x"); url != "" {
		t.Fatalf("empty inputs should yield no url, got %q", url)
	}
	seedClipOnDisk(t, "p", "c_share01", []byte("FAKEMP4"))
	mp4, _, _ := findClipOnDisk("c_share01")
	if url := maybeShareClipDurably(mp4, "c_share01"); url != "" {
		t.Fatalf("no storage configured → want empty url, got %q", url)
	}
}

func TestMaybeRecordTaskSummary_LinksBrowserClip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// MaybeRecordTaskSummary needs an active manager to pass its nil-guard.
	prev := ActiveVibePreviewManager()
	bm := NewBrowserManager()
	defer bm.Stop()
	SetActiveVibePreviewManager(NewVibePreviewManager(bm))
	defer SetActiveVibePreviewManager(prev)

	const taskID, clipID = "tsk-link-1", "c_linkme0001"
	seedClipOnDisk(t, "browser", clipID, []byte("FAKEMP4-finalized"))
	writeTaskClipMarker(taskID, clipID)

	fin := time.Now()
	tk := &Task{
		ID:           taskID,
		Status:       TaskStatusFinished,
		VideoEnabled: true,
		FinishedAt:   &fin,
		// no WorkDir → autoDetectVideoSource → browser
	}
	MaybeRecordTaskSummary(tk)

	if tk.VideoClipID != clipID {
		t.Fatalf("VideoClipID = %q, want %q", tk.VideoClipID, clipID)
	}
	if tk.VideoStatus != "ready" {
		t.Fatalf("VideoStatus = %q, want ready (clip exists on disk)", tk.VideoStatus)
	}

	// No marker → no link (agent never opened a recorded browser).
	tk2 := &Task{ID: "tsk-none", Status: TaskStatusFinished, VideoEnabled: true, FinishedAt: &fin}
	MaybeRecordTaskSummary(tk2)
	if tk2.VideoClipID != "" {
		t.Fatalf("VideoClipID = %q, want empty (no marker)", tk2.VideoClipID)
	}
}
