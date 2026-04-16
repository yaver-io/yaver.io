package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeRecordingDriver is a no-op recorder used to exercise the manager
// without shelling out to ffmpeg/xcrun/adb. Writes a small mp4 stub so
// statMp4 passes.
type fakeRecordingDriver struct {
	name      string
	available bool
	reason    string
	mu        sync.Mutex
	started   []string
	stopped   []string
}

func (d *fakeRecordingDriver) Name() string            { return d.name }
func (d *fakeRecordingDriver) Available() (bool, string) {
	return d.available, d.reason
}

func (d *fakeRecordingDriver) Start(ctx context.Context, outPath, runID, taskID string) (driverState, error) {
	d.mu.Lock()
	d.started = append(d.started, runID+":"+taskID)
	d.mu.Unlock()
	// Write a stub mp4 so Stop's statMp4 succeeds.
	if err := os.WriteFile(outPath, []byte("stub"), 0o600); err != nil {
		return driverState{}, err
	}
	return driverState{OutPath: outPath}, nil
}

func (d *fakeRecordingDriver) Stop(state driverState) error {
	d.mu.Lock()
	d.stopped = append(d.stopped, state.OutPath)
	d.mu.Unlock()
	return nil
}

func newManagerWithFakes(root string, drivers map[RecordingTarget]RecordingDriver) *RecordingManager {
	m := NewRecordingManager(root)
	m.drivers = drivers
	return m
}

func TestRecordingManagerPicksRequestedTargetWhenAvailable(t *testing.T) {
	screen := &fakeRecordingDriver{name: "fake-screen", available: true}
	sim := &fakeRecordingDriver{name: "fake-sim", available: true}

	m := newManagerWithFakes(t.TempDir(), map[RecordingTarget]RecordingDriver{
		RecordingTargetScreen: screen,
		RecordingTargetIOSSim: sim,
	})
	h, err := m.Start(context.Background(), "run1", "task1", RecordingTargetIOSSim)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.Driver != "fake-sim" || h.Target != RecordingTargetIOSSim {
		t.Fatalf("did not pick requested target: %+v", h)
	}
	if _, err := m.Stop("run1", "task1"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRecordingManagerExplicitTargetDoesNotFallBack(t *testing.T) {
	// Explicit requests are honored; they do NOT silently fall back to
	// screen even if screen is ready. The caller asked for ios-sim,
	// and if the sim isn't ready we return a clear error.
	screen := &fakeRecordingDriver{name: "fake-screen", available: true}
	sim := &fakeRecordingDriver{name: "fake-sim", available: false, reason: "no booted sim"}

	m := newManagerWithFakes(t.TempDir(), map[RecordingTarget]RecordingDriver{
		RecordingTargetScreen: screen,
		RecordingTargetIOSSim: sim,
	})
	if _, err := m.Start(context.Background(), "run1", "task1", RecordingTargetIOSSim); err == nil {
		t.Fatal("expected error when explicit ios-sim is unavailable — no silent fallback to screen")
	}
}

func TestRecordingManagerAutoPickNeverChoosesScreen(t *testing.T) {
	// The morning reel is a product demo. Recording the whole
	// desktop would show the IDE — which is explicitly not what the
	// user wants ("video about the finished product, not how you
	// coded"). Auto-pick must refuse screen even when nothing else
	// is available, so autodev can log-and-skip instead.
	screen := &fakeRecordingDriver{name: "fake-screen", available: true}
	sim := &fakeRecordingDriver{name: "fake-sim", available: false, reason: "no booted sim"}
	emu := &fakeRecordingDriver{name: "fake-emu", available: false, reason: "no emu"}

	m := newManagerWithFakes(t.TempDir(), map[RecordingTarget]RecordingDriver{
		RecordingTargetScreen:     screen,
		RecordingTargetIOSSim:     sim,
		RecordingTargetAndroidEmu: emu,
	})
	if _, err := m.Start(context.Background(), "run1", "task1", ""); err == nil {
		t.Fatal("expected auto-pick to refuse when no app-demo driver is ready")
	}
	if m.HasAnyAppDemoDriver() {
		t.Fatal("HasAnyAppDemoDriver() should be false when sim + emu are not ready")
	}
}

func TestRecordingManagerAutoPickPrefersIOSSimOverAndroid(t *testing.T) {
	sim := &fakeRecordingDriver{name: "fake-sim", available: true}
	emu := &fakeRecordingDriver{name: "fake-emu", available: true}

	m := newManagerWithFakes(t.TempDir(), map[RecordingTarget]RecordingDriver{
		RecordingTargetIOSSim:     sim,
		RecordingTargetAndroidEmu: emu,
	})
	h, err := m.Start(context.Background(), "run", "task", "")
	if err != nil {
		t.Fatalf("auto-pick: %v", err)
	}
	if h.Target != RecordingTargetIOSSim {
		t.Fatalf("expected ios-sim to win auto-pick, got %s", h.Target)
	}
	_, _ = m.Stop("run", "task")
}

func TestRecordingManagerRejectsDoubleStart(t *testing.T) {
	sim := &fakeRecordingDriver{name: "fake-sim", available: true}
	m := newManagerWithFakes(t.TempDir(), map[RecordingTarget]RecordingDriver{
		RecordingTargetIOSSim: sim,
	})
	if _, err := m.Start(context.Background(), "run1", "t1", RecordingTargetIOSSim); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := m.Start(context.Background(), "run1", "t1", RecordingTargetIOSSim); err == nil {
		t.Fatal("expected error on double-start")
	}
	_, _ = m.Stop("run1", "t1")
}

func TestRecordingManagerStopWithoutStartIsError(t *testing.T) {
	m := newManagerWithFakes(t.TempDir(), map[RecordingTarget]RecordingDriver{
		RecordingTargetScreen: &fakeRecordingDriver{name: "f", available: true},
	})
	if _, err := m.Stop("run1", "missing"); err == nil {
		t.Fatal("expected error stopping a recording that was never started")
	}
}

func TestRecordingManagerWritesToExpectedPath(t *testing.T) {
	root := t.TempDir()
	sim := &fakeRecordingDriver{name: "fake-sim", available: true}
	m := newManagerWithFakes(root, map[RecordingTarget]RecordingDriver{
		RecordingTargetIOSSim: sim,
	})
	h, err := m.Start(context.Background(), "my-run", "my-task", RecordingTargetIOSSim)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	expect := filepath.Join(root, "my-run", "my-task", "video.mp4")
	if h.Path != expect {
		t.Fatalf("path = %q, want %q", h.Path, expect)
	}
	if _, err := os.Stat(expect); err != nil {
		t.Fatalf("stub mp4 not created: %v", err)
	}
	_, _ = m.Stop("my-run", "my-task")
}
