package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initAutodevRepo prepares a tiny git repo + chdir's into it so
// morningHook.workDir resolves correctly. Returns a restore func.
func initAutodevRepo(t *testing.T) func() {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	run("git", "init", "-b", "main")
	run("git", "config", "user.email", "h@h")
	run("git", "config", "user.name", "h")
	if err := os.WriteFile(filepath.Join(repo, "r.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "r.txt")
	run("git", "commit", "-m", "base")

	orig, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(orig) }
}

func TestMorningHookNilWhenDisabled(t *testing.T) {
	h := newMorningHookFromPlan(autodevPlan{
		LoopName:       "test",
		Project:        "t",
		Kind:           "autodev",
		MorningSummary: false,
	})
	if h != nil {
		t.Fatal("expected nil hook when MorningSummary=false")
	}
	// nil receivers must be callable — autodev calls these
	// unconditionally.
	h.finalize("")
	h.beforeKick(1, "")
	h.afterKick(1, "", "", time.Now())
}

func TestMorningHookGracefulWithNoVideoDriver(t *testing.T) {
	restore := initAutodevRepo(t)
	defer restore()
	t.Setenv("HOME", t.TempDir())

	h := newMorningHookFromPlan(autodevPlan{
		LoopName:       "gracetest",
		Project:        "t",
		Kind:           "autodev",
		Runner:         "claude-code",
		MorningSummary: true,
		MorningVideo:   true, // asked for video but no sim/emu — must not fail
	})
	if h == nil {
		t.Fatal("expected hook when MorningSummary=true")
	}
	// Replace driver set with all-unavailable so the hook is forced to
	// skip video without any real OS capability.
	h.recMgr = NewRecordingManager(t.TempDir())
	h.recMgr.drivers = map[RecordingTarget]RecordingDriver{
		RecordingTargetIOSSim:     &fakeRecordingDriver{name: "nope-sim", available: false, reason: "no sim"},
		RecordingTargetAndroidEmu: &fakeRecordingDriver{name: "nope-emu", available: false, reason: "no emu"},
	}

	start := time.Now()
	base := h.beforeKick(1, "add hello")
	if base == "" {
		t.Fatalf("expected base SHA from real repo")
	}
	if h.recording {
		t.Fatalf("must not claim recording when no driver available")
	}

	// Make a new commit so afterKick sees non-zero git stats.
	if err := os.WriteFile("r.txt", []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "add", "-A").Run()
	exec.Command("git", "commit", "-m", "add two").Run()

	h.afterKick(1, "add hello", base, start)

	// Summary upserted with zero video metadata but non-zero git stats.
	summary, ok := h.store.Load(h.runID)
	if !ok {
		t.Fatalf("summary not written — graceful degradation should still write summary")
	}
	if len(summary.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(summary.Tasks))
	}
	task := summary.Tasks[0]
	if task.HasVideo {
		t.Errorf("HasVideo should be false when no driver is available, got true")
	}
	if task.Status != TaskStatusHighlightShipped {
		t.Errorf("status = %q, want shipped", task.Status)
	}
	if task.FilesChanged == 0 {
		t.Errorf("git stats missing — files=%d", task.FilesChanged)
	}
}

func TestMorningHookSkippedWhenNoCommit(t *testing.T) {
	restore := initAutodevRepo(t)
	defer restore()
	t.Setenv("HOME", t.TempDir())

	h := newMorningHookFromPlan(autodevPlan{
		LoopName:       "skiptest",
		Project:        "t",
		Kind:           "autodev",
		MorningSummary: true,
		MorningVideo:   false,
	})
	base := h.beforeKick(1, "no-op kick")
	// No file changes → base == head.
	h.afterKick(1, "no-op kick", base, time.Now())

	summary, _ := h.store.Load(h.runID)
	if len(summary.Tasks) != 1 {
		t.Fatalf("want 1 task, got %d", len(summary.Tasks))
	}
	if summary.Tasks[0].Status != TaskStatusHighlightSkipped {
		t.Errorf("status = %q, want skipped", summary.Tasks[0].Status)
	}
}
