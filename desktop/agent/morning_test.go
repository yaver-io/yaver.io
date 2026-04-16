package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeMorningIDRejectsPathEscape(t *testing.T) {
	// "." and ".." alone sanitize to "unknown". Slashes become underscores
	// which guarantees no escape out of the store root (filepath.Join
	// joins non-slash segments safely).
	cases := map[string]string{
		"":              "unknown",
		".":             "unknown",
		"..":            "unknown",
		"../etc":        ".._etc",
		"a/b":           "a_b",
		"run-123.abc":   "run-123.abc",
		"r/../etc":      "r_.._etc",
		"a" + strings.Repeat("b", 100): "a" + strings.Repeat("b", 63),
	}
	for in, want := range cases {
		if got := sanitizeMorningID(in); got != want {
			t.Errorf("sanitizeMorningID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMorningStoreRoundTrip(t *testing.T) {
	store := NewMorningStore(t.TempDir())
	start := time.Now().UTC().Truncate(time.Second)

	task := TaskHighlight{
		TaskID:     "t1",
		RunnerID:   "claude",
		Title:      "Add survey form",
		Status:     TaskStatusHighlightShipped,
		StartedAt:  start,
		FinishedAt: start.Add(2 * time.Minute),
		CostUSD:    0.21,
		HeadSHA:    "a1b2c3",
		CommitSHAs: []string{"a1b2c3"},
	}
	if _, err := store.UpsertTask("run-1", "proj", "/tmp/proj", task); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}

	loaded, ok := store.Load("run-1")
	if !ok {
		t.Fatal("expected run-1 to load")
	}
	if len(loaded.Tasks) != 1 || loaded.Tasks[0].TaskID != "t1" {
		t.Fatalf("tasks = %+v", loaded.Tasks)
	}
	if loaded.Stats.TasksShipped != 1 {
		t.Errorf("shipped count = %d, want 1", loaded.Stats.TasksShipped)
	}
	if loaded.Stats.TotalCostUSD != 0.21 {
		t.Errorf("cost = %v, want 0.21", loaded.Stats.TotalCostUSD)
	}

	// Incremental update: add video metadata without clobbering title/runner.
	update := TaskHighlight{
		TaskID:          "t1",
		HasVideo:        true,
		VideoDurationMs: 4200,
		VideoSizeBytes:  123456,
	}
	if _, err := store.UpsertTask("run-1", "", "", update); err != nil {
		t.Fatalf("UpsertTask update: %v", err)
	}
	loaded, _ = store.Load("run-1")
	got := loaded.Tasks[0]
	if got.Title != "Add survey form" || got.RunnerID != "claude" || got.CostUSD != 0.21 {
		t.Fatalf("earlier fields clobbered: %+v", got)
	}
	if !got.HasVideo || got.VideoDurationMs != 4200 || got.VideoSizeBytes != 123456 {
		t.Fatalf("video merge lost: %+v", got)
	}

	// Rollback: flips status + stamps RevertSHA.
	if _, err := store.MarkRollback("run-1", "t1", "r3v3rt"); err != nil {
		t.Fatalf("MarkRollback: %v", err)
	}
	loaded, _ = store.Load("run-1")
	got = loaded.Tasks[0]
	if got.Status != TaskStatusHighlightRolledBack {
		t.Errorf("status = %q, want rolled-back", got.Status)
	}
	if got.RevertSHA != "r3v3rt" || got.RolledBackAt == nil {
		t.Errorf("rollback fields missing: %+v", got)
	}
	if loaded.Stats.TasksRolledBack != 1 || loaded.Stats.TasksShipped != 0 {
		t.Errorf("rollback did not recompute stats: %+v", loaded.Stats)
	}
}

func TestMorningStoreListNewestFirst(t *testing.T) {
	store := NewMorningStore(t.TempDir())
	oldStart := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	newStart := time.Now().UTC().Truncate(time.Second)

	_, _ = store.UpsertTask("old", "p", "/w", TaskHighlight{TaskID: "a", Status: TaskStatusHighlightShipped, StartedAt: oldStart})
	_, _ = store.UpsertTask("new", "p", "/w", TaskHighlight{TaskID: "b", Status: TaskStatusHighlightShipped, StartedAt: newStart})

	runs := store.List(0)
	if len(runs) != 2 {
		t.Fatalf("list len = %d", len(runs))
	}
	if runs[0].RunID != "new" {
		t.Fatalf("newest-first broken: first = %s", runs[0].RunID)
	}
}

func TestMorningStoreWriteIsAtomic(t *testing.T) {
	// If the Save call is interrupted we should never observe the tmp
	// file via Load — Save must rename only after the bytes are on disk.
	store := NewMorningStore(t.TempDir())
	if _, err := store.UpsertTask("run", "p", "/w", TaskHighlight{TaskID: "t", Status: TaskStatusHighlightShipped}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entries, _ := os.ReadDir(store.runDir("run"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("tmp file survived rename: %s", e.Name())
		}
	}
}

func TestGitHelpersAgainstRealRepo(t *testing.T) {
	dir := t.TempDir()
	runCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	runCmd("git", "init", "-b", "main")
	runCmd("git", "config", "user.email", "morning@test")
	runCmd("git", "config", "user.name", "Morning Test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd("git", "add", "a.txt")
	runCmd("git", "commit", "-m", "base")
	base := GitHeadSHA(dir)
	if base == "" {
		t.Fatal("base HEAD empty")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd("git", "add", "-A")
	runCmd("git", "commit", "-m", "edit")
	head := GitHeadSHA(dir)
	if head == "" || head == base {
		t.Fatalf("head did not advance: base=%s head=%s", base, head)
	}

	shas := GitCommitSHAsBetween(dir, base, head)
	if len(shas) != 1 || shas[0] != head {
		t.Fatalf("shas = %v, want [%s]", shas, head)
	}
	files, added, removed := GitDiffStats(dir, base, head)
	if files != 2 || added == 0 {
		t.Fatalf("stats (files=%d added=%d removed=%d) look wrong", files, added, removed)
	}
	_ = removed
}
