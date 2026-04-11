package testkit

import (
	"testing"
	"time"
)

func TestAutoFixLogRecordAndList(t *testing.T) {
	dir := t.TempDir()
	log := NewAutoFixLog(dir)

	for i := 0; i < 3; i++ {
		log.Record(AutoFix{
			SpecName:    "spec",
			Strategy:    "selector_replace",
			Description: "fix click target",
		})
		// Stagger so the timestamps differ.
		time.Sleep(2 * time.Millisecond)
	}

	got := log.List(0)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for _, e := range got {
		if e.State != AutoFixApplied {
			t.Errorf("default state = %s, want applied", e.State)
		}
		if e.ID == "" {
			t.Error("ID should be auto-generated")
		}
		if e.CreatedAt.IsZero() {
			t.Error("CreatedAt should be set")
		}
	}
	// Newest first.
	if !got[0].CreatedAt.After(got[2].CreatedAt) {
		t.Error("expected newest first ordering")
	}
}

func TestAutoFixLogMarkUndone(t *testing.T) {
	dir := t.TempDir()
	log := NewAutoFixLog(dir)
	rec := log.Record(AutoFix{SpecName: "spec", Strategy: "selector_replace"})

	updated, err := log.MarkUndone(rec.ID)
	if err != nil {
		t.Fatalf("MarkUndone: %v", err)
	}
	if updated.State != AutoFixRolledBack {
		t.Errorf("state = %s, want rolled_back", updated.State)
	}
	if updated.UndoneAt == nil {
		t.Error("UndoneAt should be set")
	}

	// Idempotent guard.
	if _, err := log.MarkUndone(rec.ID); err == nil {
		t.Error("expected error on second undo")
	}

	// Unknown ID.
	if _, err := log.MarkUndone("does-not-exist"); err == nil {
		t.Error("expected error on unknown id")
	}
}

func TestAutoFixLogPersistence(t *testing.T) {
	dir := t.TempDir()
	log1 := NewAutoFixLog(dir)
	rec := log1.Record(AutoFix{SpecName: "persistent", Strategy: "selector_replace"})

	log2 := NewAutoFixLog(dir)
	got := log2.List(0)
	if len(got) != 1 {
		t.Fatalf("reload returned %d entries", len(got))
	}
	if got[0].ID != rec.ID {
		t.Errorf("reload mismatch: %s vs %s", got[0].ID, rec.ID)
	}
}

func TestAutoFixLogAppliedCount(t *testing.T) {
	dir := t.TempDir()
	log := NewAutoFixLog(dir)
	a := log.Record(AutoFix{SpecName: "a"})
	log.Record(AutoFix{SpecName: "b"})
	if log.AppliedCount() != 2 {
		t.Errorf("AppliedCount = %d, want 2", log.AppliedCount())
	}
	_, _ = log.MarkUndone(a.ID)
	if log.AppliedCount() != 1 {
		t.Errorf("after undo: AppliedCount = %d, want 1", log.AppliedCount())
	}
}
