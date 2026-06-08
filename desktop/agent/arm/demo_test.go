package arm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yaver-io/agent/robot"
)

func TestDemoRecorder(t *testing.T) {
	dir := t.TempDir()
	rec := NewDemoRecorder(dir)
	c := newFakeController()

	if err := rec.Start(context.Background(), c, "seat-wire", "seat the white wire", 30); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if active, name, _ := rec.Active(); !active || name != "seat-wire" {
		t.Errorf("Active = %v %q", active, name)
	}
	// double-start must fail
	if err := rec.Start(context.Background(), c, "other", "", 10); err == nil {
		t.Error("concurrent recording should be refused")
	}

	time.Sleep(250 * time.Millisecond)
	meta, err := rec.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if meta.Frames < 3 {
		t.Errorf("expected several frames at 30fps over 250ms, got %d", meta.Frames)
	}
	if meta.Prompt != "seat the white wire" || meta.Episode != "episode_000" {
		t.Errorf("meta = %+v", meta)
	}

	// files on disk
	epDir := filepath.Join(dir, "seat-wire", "episode_000")
	if _, err := os.Stat(filepath.Join(epDir, "meta.json")); err != nil {
		t.Errorf("meta.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(epDir, "states.jsonl")); err != nil {
		t.Errorf("states.jsonl missing: %v", err)
	}
	frames, _ := os.ReadDir(filepath.Join(epDir, "frames"))
	if len(frames) != meta.Frames {
		t.Errorf("frame files = %d, meta says %d", len(frames), meta.Frames)
	}

	// List + next-episode allocation
	if err := rec.Start(context.Background(), c, "seat-wire", "", 20); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	m2, _ := rec.Stop()
	if m2.Episode != "episode_001" {
		t.Errorf("second episode = %q, want episode_001", m2.Episode)
	}
	list, err := rec.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("List = %d episodes, want 2", len(list))
	}

	if active, _, _ := rec.Active(); active {
		t.Error("should be idle after Stop")
	}
}

func TestDemoRecorderNeedsCamera(t *testing.T) {
	rec := NewDemoRecorder(t.TempDir())
	c := NewController(newFakeBackend(), nil, robot.VisionConfig{}, Config{})
	if err := rec.Start(context.Background(), c, "x", "", 10); err == nil {
		t.Error("recording without a camera must fail")
	}
}
