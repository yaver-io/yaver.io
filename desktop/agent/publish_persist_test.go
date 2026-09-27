package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishRunsPersistAcrossAgentRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := NewPublishManager(nil, nil, home)
	run := &PublishRun{
		ID: "receipt-1", ProjectDir: "/project", TargetID: "npm-sdk",
		TargetKind: "npm", Status: PublishRunCompleted,
		StartedAt: "2026-09-25T10:00:00Z", FinishedAt: "2026-09-25T10:01:00Z",
	}
	pm.mu.Lock()
	pm.runs[run.ID] = run
	pm.persistRunLocked(run)
	pm.mu.Unlock()

	state := filepath.Join(home, ".yaver", "publishes", "receipt-1.json")
	dirInfo, err := os.Stat(filepath.Dir(state))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("receipt directory must be owner-only: info=%v err=%v", dirInfo, err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatalf("persisted receipt missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt permissions = %o, want 600", info.Mode().Perm())
	}

	restarted := NewPublishManager(nil, nil, home)
	got, ok := restarted.GetRun("receipt-1")
	if !ok || got.Status != PublishRunCompleted || got.TargetID != "npm-sdk" {
		t.Fatalf("persisted run not restored: ok=%v run=%+v", ok, got)
	}
}

func TestPublishRestartFailsIndeterminateRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pm := NewPublishManager(nil, nil, home)
	run := &PublishRun{ID: "in-flight", Status: PublishRunRunning, StartedAt: "2026-09-25T10:00:00Z"}
	pm.mu.Lock()
	pm.runs[run.ID] = run
	pm.persistRunLocked(run)
	pm.mu.Unlock()

	restarted := NewPublishManager(nil, nil, home)
	got, ok := restarted.GetRun("in-flight")
	if !ok || got.Status != PublishRunFailed || !strings.Contains(got.Error, "inspect the store before retrying") {
		t.Fatalf("indeterminate run must fail closed after restart: ok=%v run=%+v", ok, got)
	}
}
