package main

// vibe_preview_exercise_test.go — Phase 7 tests. Don't invoke maestro
// itself (no maestro binary on most CI runners). Instead, exercise the
// flow-detection + canned-yaml-generation paths, which is where most
// of the regression risk lives.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindExistingExerciseFlow_picksProjectSpecific(t *testing.T) {
	dir := t.TempDir()
	flow := filepath.Join(dir, "e2e", "myapp.flow.yaml")
	if err := os.MkdirAll(filepath.Dir(flow), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(flow, []byte("appId: foo"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := findExistingExerciseFlow(dir, "myapp")
	if got != flow {
		t.Fatalf("expected %s, got %s", flow, got)
	}
}

func TestFindExistingExerciseFlow_fallsBackToSmoke(t *testing.T) {
	dir := t.TempDir()
	smoke := filepath.Join(dir, "e2e", "smoke.flow.yaml")
	if err := os.MkdirAll(filepath.Dir(smoke), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(smoke, []byte("appId: x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := findExistingExerciseFlow(dir, "myapp")
	if got != smoke {
		t.Fatalf("expected smoke fallback, got %s", got)
	}
}

func TestFindExistingExerciseFlow_returnsEmptyWhenNothingMatches(t *testing.T) {
	dir := t.TempDir()
	if got := findExistingExerciseFlow(dir, "myapp"); got != "" {
		t.Fatalf("expected empty, got %s", got)
	}
}

func TestWriteCannedExerciseFlow_isValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exercises", "myapp", "test.yaml")
	if err := writeCannedExerciseFlow(path, "kick added pricing card"); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(data)
	for _, want := range []string{"appId:", "launchApp", "swipe", "tapOn", "kick added pricing card"} {
		if !strings.Contains(body, want) {
			t.Errorf("canned flow missing %q\n---\n%s", want, body)
		}
	}
}

func TestSeedExerciseFromContext(t *testing.T) {
	mgr := NewVibePreviewManager(newFakeBrowser())
	mgr.SetDiskRoot(t.TempDir())
	defer mgr.StopAll()

	path, err := mgr.SeedExerciseFromContext("p", "first", "test seed")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !strings.HasSuffix(path, "/exercises/p/first.yaml") {
		t.Errorf("unexpected path %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("seeded file should exist: %v", err)
	}
}

func TestExerciseClip_noopWhenMaestroMissing(t *testing.T) {
	// Force the unset path even on a host where maestro is installed.
	saved := maestroPath
	maestroPath = ""
	t.Cleanup(func() { maestroPath = saved })

	mgr := NewVibePreviewManager(newFakeBrowser())
	mgr.SetDiskRoot(t.TempDir())
	defer mgr.StopAll()

	// Should return immediately without panic + without writing anything.
	mgr.ExerciseClip(context.Background(), "c1", "p", "", "")

	if MaestroAvailable() {
		t.Error("MaestroAvailable should be false in this test")
	}
}
