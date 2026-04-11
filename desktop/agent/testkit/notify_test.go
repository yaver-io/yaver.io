package testkit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNotificationCenterAppendAndList(t *testing.T) {
	dir := t.TempDir()
	nc := NewNotificationCenter(filepath.Join(dir, "n.jsonl"), 16)

	for i := 0; i < 3; i++ {
		nc.Append(Notification{
			ID:        "x" + string(rune('a'+i)),
			Kind:      "test_failed",
			SpecName:  "spec",
			Error:     "err",
			CreatedAt: time.Now(),
		})
	}
	got := nc.List(10)
	if len(got) != 3 {
		t.Fatalf("List = %d, want 3", len(got))
	}
	// Newest first
	if got[0].ID != "xc" || got[2].ID != "xa" {
		t.Errorf("order wrong: %v", []string{got[0].ID, got[1].ID, got[2].ID})
	}
}

func TestNotificationCenterRingBuffer(t *testing.T) {
	dir := t.TempDir()
	// Constructor floors at 16 to keep the ring useful; pass enough to
	// prove the trim logic actually runs.
	nc := NewNotificationCenter(filepath.Join(dir, "n.jsonl"), 16)
	for i := 0; i < 30; i++ {
		nc.Append(Notification{ID: string(rune('a' + i)), CreatedAt: time.Now()})
	}
	got := nc.List(0)
	if len(got) != 16 {
		t.Fatalf("ring should hold 16, got %d", len(got))
	}
}

func TestNotificationCenterPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n.jsonl")
	nc := NewNotificationCenter(path, 16)
	nc.Append(Notification{ID: "first", Kind: "test_failed", CreatedAt: time.Now()})

	// Simulate agent restart.
	nc2 := NewNotificationCenter(path, 16)
	got := nc2.List(0)
	if len(got) != 1 || got[0].ID != "first" {
		t.Errorf("reload failed: %v", got)
	}
}

func TestPublishSuiteFailures(t *testing.T) {
	suite := &Suite{
		StartedAt: time.Now(),
		Results: []*Result{
			{
				Spec:   &Spec{Name: "ok", Path: "/tmp/ok.test.yaml"},
				Passed: true,
			},
			{
				Spec:   &Spec{Name: "broken", Path: "/tmp/broken.test.yaml"},
				Passed: false,
				Steps: []StepResult{
					{Index: 1, Description: "click x", Phase: "step", Err: errFake("could not find node")},
				},
			},
		},
		FinishedAt: time.Now(),
	}
	dir := t.TempDir()
	nc := NewNotificationCenter(filepath.Join(dir, "n.jsonl"), 16)
	PublishSuiteFailures(nc, suite, "abc1234", "main")
	got := nc.List(0)
	if len(got) != 1 {
		t.Fatalf("expected 1 failure notification, got %d", len(got))
	}
	if got[0].SpecName != "broken" {
		t.Errorf("wrong spec: %s", got[0].SpecName)
	}
	if got[0].GitBranch != "main" {
		t.Errorf("git branch not propagated: %s", got[0].GitBranch)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
