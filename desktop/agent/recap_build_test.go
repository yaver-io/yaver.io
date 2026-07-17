package main

// recap_build_test.go — end-to-end: real JPEG frames on disk → a real MP4.
//
// Separate from recap_test.go because these need ffmpeg and actually encode.
// They skip cleanly on a box without it, matching the soft-dependency rule the
// builder itself follows.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func requireFfmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed — recap encoding is a soft dependency")
	}
}

// withTempScreenlogDir lives in screenlog_test.go.

// writeTestJPEG writes a real, decodable JPEG so ffmpeg has something to chew.
func writeTestJPEG(t *testing.T, path string, shade uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{R: shade, G: uint8(x % 256), B: uint8(y % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedScreenlogSession builds a session on disk with n real frames on the
// given display, 2s apart, each with a closed interval.
func seedScreenlogSession(t *testing.T, id string, display, n int, startMs int64) *ScreenlogSession {
	t.Helper()
	dir, err := screenlogSessionDir(id)
	if err != nil {
		t.Fatal(err)
	}
	sess := &ScreenlogSession{ID: id, StartedAt: startMs, Config: defaultScreenlogConfig()}
	for i := 0; i < n; i++ {
		at := startMs + int64(i)*2000
		name := fmt.Sprintf("%06d_d%d_%d.jpg", i, display, at)
		writeTestJPEG(t, filepath.Join(dir, name), uint8(i*20))
		sess.Frames = append(sess.Frames, ScreenlogFrame{
			Idx: i, Display: display, CapturedAt: at, ActiveToMs: at + 2000,
			File: name, Width: 320, Height: 200,
			ActiveApp: "Terminal", ActiveWindow: "autorun — doer",
		})
	}
	sess.StoppedAt = startMs + int64(n)*2000
	if err := saveScreenlogSession(sess); err != nil {
		t.Fatal(err)
	}
	return sess
}

func ffprobeFloat(t *testing.T, path string, entry string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", entry, "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", entry, err)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("ffprobe %s returned %q: %v", entry, out, err)
	}
	return v
}

// The headline test: frames in, watchable video out.
func TestBuildRecap_endToEnd(t *testing.T) {
	requireFfmpeg(t)
	withTempRecapDir(t)
	withTempScreenlogDir(t)

	start := time.Now().Add(-time.Hour).UnixMilli()
	seedScreenlogSession(t, "slog-e2e", 0, 12, start)

	rec, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID:    "slog-e2e",
		AutorunID:    "autorun-e2e",
		Task:         "nightly",
		Tag:          RecapTagNightly,
		Display:      0,
		TargetSec:    6,
		MaxWidth:     320,
		FinishReason: autorunReasonDone,
		Iterations:   4,
		Commits:      3,
		FinalCommit:  "abc1234",
		Verified:     true,
	})
	if err != nil {
		t.Fatalf("BuildRecap: %v", err)
	}
	if rec.Status != RecapStatusReady {
		t.Fatalf("status = %s (%s)", rec.Status, rec.Error)
	}
	if rec.Frames != 12 {
		t.Errorf("frames = %d, want 12", rec.Frames)
	}

	dir, err := recapDir(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	video := recapVideoPath(dir)
	st, err := os.Stat(video)
	if err != nil {
		t.Fatalf("no video produced: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("video is empty")
	}
	if rec.SizeBytes != st.Size() {
		t.Errorf("recorded SizeBytes %d != actual %d", rec.SizeBytes, st.Size())
	}

	// It must be a real, probeable H.264 video whose length matches the pacing.
	dur := ffprobeFloat(t, video, "format=duration")
	if dur <= 0 {
		t.Fatalf("ffprobe reports duration %.2f", dur)
	}
	if delta := dur - rec.DurationSec; delta > 1.5 || delta < -1.5 {
		t.Errorf("encoded duration %.2fs disagrees with recorded %.2fs — the pacing did not survive encoding", dur, rec.DurationSec)
	}

	// The poster is what a listing shows before the video is pulled.
	if _, err := os.Stat(recapPosterPath(dir)); err != nil {
		t.Errorf("no poster: %v", err)
	}

	// The intermediate concat list must not survive into the artifact dir.
	if _, err := os.Stat(filepath.Join(dir, "frames.txt")); !os.IsNotExist(err) {
		t.Error("frames.txt leaked into the recap dir")
	}

	// It must be listable by its run — the join the whole feature exists for.
	byRun, err := listRecaps(RecapFilter{AutorunID: "autorun-e2e"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byRun) != 1 || byRun[0].ID != rec.ID {
		t.Fatalf("recap not findable by autorun id: %+v", byRun)
	}
}

// faststart moves the moov atom to the front. Without it, tvOS/VR/web all
// stall until the whole file downloads, and Range seeking is useless — which
// is the entire reason this is served with http.ServeContent.
func TestBuildRecap_isStreamable(t *testing.T) {
	requireFfmpeg(t)
	withTempRecapDir(t)
	withTempScreenlogDir(t)
	seedScreenlogSession(t, "slog-fs", 0, 6, time.Now().Add(-time.Minute).UnixMilli())

	rec, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID: "slog-fs", Tag: RecapTagManual, Display: 0, TargetSec: 3, MaxWidth: 320,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := recapDir(rec.ID)
	b, err := os.ReadFile(recapVideoPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	moov := bytes.Index(b, []byte("moov"))
	mdat := bytes.Index(b, []byte("mdat"))
	if moov < 0 || mdat < 0 {
		t.Fatalf("not an MP4? moov=%d mdat=%d", moov, mdat)
	}
	if moov > mdat {
		t.Errorf("moov atom (%d) is after mdat (%d) — -movflags +faststart is not taking effect, so seeking and Range playback will stall", moov, mdat)
	}
}

// Trap 1, proven against a real encode: two monitors in one directory must
// never be spliced into one video.
func TestBuildRecap_multiDisplaySessionPicksOneMonitor(t *testing.T) {
	requireFfmpeg(t)
	withTempRecapDir(t)
	withTempScreenlogDir(t)

	start := time.Now().Add(-time.Minute).UnixMilli()
	// Seed display 0 with 8 frames, then append display 1's frames into the
	// SAME session dir — exactly how screenlog stores a two-monitor machine.
	sess := seedScreenlogSession(t, "slog-multi", 0, 8, start)
	dir, _ := screenlogSessionDir("slog-multi")
	for i := 0; i < 3; i++ {
		at := start + int64(i)*2000
		name := fmt.Sprintf("%06d_d%d_%d.jpg", 100+i, 1, at)
		writeTestJPEG(t, filepath.Join(dir, name), 250)
		sess.Frames = append(sess.Frames, ScreenlogFrame{
			Idx: 100 + i, Display: 1, CapturedAt: at, ActiveToMs: at + 2000, File: name,
		})
	}
	if err := saveScreenlogSession(sess); err != nil {
		t.Fatal(err)
	}

	rec, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID: "slog-multi", Tag: RecapTagManual, Display: 0, TargetSec: 3, MaxWidth: 320,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Frames != 8 {
		t.Errorf("display-0 recap used %d frames, want 8 — display 1 leaked in", rec.Frames)
	}
}

// A window that catches no frames must fail with a message that says what to
// do, not a bare "no frames".
func TestBuildRecap_emptyWindowExplainsItself(t *testing.T) {
	withTempRecapDir(t)
	withTempScreenlogDir(t)
	seedScreenlogSession(t, "slog-empty", 0, 4, time.Now().Add(-time.Hour).UnixMilli())

	_, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID: "slog-empty", Tag: RecapTagManual, Display: 0,
		SinceMs: 1, UntilMs: 2, // a window a decade before the frames
	})
	if err == nil {
		t.Fatal("want an error for an empty window")
	}
	// The two real causes are a display mismatch and a bad window; the message
	// must let the caller tell which.
	for _, want := range []string{"display", "displays"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the available displays, got: %v", err)
		}
	}
}

func TestBuildRecap_rejectsUnsafeTag(t *testing.T) {
	withTempRecapDir(t)
	withTempScreenlogDir(t)
	seedScreenlogSession(t, "slog-tag", 0, 2, time.Now().UnixMilli())
	_, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID: "slog-tag", Display: 0, Tag: "fix /Users/bob/api",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid tag") {
		t.Fatalf("a free-text tag must be rejected before it can leak a path, got: %v", err)
	}
}

// A failed build must leave a record saying so, not an orphan directory. The
// autorun hook has no caller to return an error to, so the record IS the
// error report.
func TestBuildRecap_recordsFailureVisibly(t *testing.T) {
	withTempRecapDir(t)
	withTempScreenlogDir(t)

	// Seed a session whose frame files are referenced but absent, so ffmpeg
	// fails while everything upstream succeeds.
	dir, err := screenlogSessionDir("slog-broken")
	if err != nil {
		t.Fatal(err)
	}
	_ = dir
	sess := &ScreenlogSession{ID: "slog-broken", StartedAt: time.Now().UnixMilli(), Config: defaultScreenlogConfig()}
	for i := 0; i < 3; i++ {
		at := sess.StartedAt + int64(i)*2000
		sess.Frames = append(sess.Frames, ScreenlogFrame{
			Idx: i, Display: 0, CapturedAt: at, ActiveToMs: at + 2000,
			File: fmt.Sprintf("%06d_d0_%d.jpg", i, at), // never written
		})
	}
	sess.StoppedAt = sess.StartedAt + 6000
	if err := saveScreenlogSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	rec, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID: "slog-broken", Tag: RecapTagManual, Display: 0, TargetSec: 3,
	})
	if err == nil {
		t.Fatal("want an error when the frames are missing")
	}
	if rec == nil {
		t.Fatal("a failed build must still return its record")
	}
	if rec.Status != RecapStatusFailed || rec.Error == "" {
		t.Errorf("failure must be recorded on the record: status=%s err=%q", rec.Status, rec.Error)
	}
	// And it must be visible in a listing rather than silently absent.
	got, _ := listRecaps(RecapFilter{})
	if len(got) != 1 || got[0].Status != RecapStatusFailed {
		t.Errorf("a failed recap must be listable so the failure is visible: %+v", got)
	}
}

// The subtitle sidecar is what makes muting the narrator work. It must be
// produced from the same cues the record carries.
func TestBuildRecap_writesSubtitlesMatchingCues(t *testing.T) {
	requireFfmpeg(t)
	withTempRecapDir(t)
	withTempScreenlogDir(t)
	seedScreenlogSession(t, "slog-vtt", 0, 10, time.Now().Add(-30*time.Minute).UnixMilli())

	rec, err := BuildRecap(context.Background(), RecapBuildOpts{
		SessionID: "slog-vtt", Tag: RecapTagNightly, Task: "nightly", Display: 0,
		TargetSec: 5, MaxWidth: 320,
		FinishReason: autorunReasonGate, Iterations: 2,
	})
	if err != nil {
		t.Fatalf("BuildRecap: %v", err)
	}
	if !rec.HasSubtitles || len(rec.Cues) == 0 {
		t.Fatalf("want subtitles + cues, got HasSubtitles=%v cues=%d", rec.HasSubtitles, len(rec.Cues))
	}
	if rec.HasAudio {
		t.Error("narration was not requested; HasAudio must be false")
	}
	dir, _ := recapDir(rec.ID)
	b, err := os.ReadFile(recapSubtitlesPath(dir))
	if err != nil {
		t.Fatalf("no VTT written: %v", err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "WEBVTT") {
		t.Errorf("not a VTT file:\n%s", s)
	}
	// The gate failed, so the closer must say so — this is the honesty rule
	// surviving all the way to the artifact a user reads.
	if !strings.Contains(strings.ToLower(s), "gate failed") {
		t.Errorf("the subtitles must report the real outcome:\n%s", s)
	}
	for _, c := range rec.Cues {
		if !strings.Contains(s, strings.ReplaceAll(strings.TrimSpace(c.Text), "\n", " ")) {
			t.Errorf("cue %q is in the record but not in the VTT", c.Text)
		}
	}

	// The record on disk must round-trip the cues — a surface that draws
	// captions as 3D geometry reads them from here, not from the VTT.
	raw, err := os.ReadFile(recapJSONPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var back RecapRecord
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Cues) != len(rec.Cues) {
		t.Errorf("recap.json lost cues: %d vs %d", len(back.Cues), len(rec.Cues))
	}
}
