package main

import (
	"strings"
	"testing"
	"time"
)

// A fixed slot needs a stable address. These pin the two properties a UI relies
// on to give an agent a permanent home: the key is the same every run, and the
// order never moves under a status change.

// The session ID is a timestamp — new every run. The slot key must not be.
func TestAutorunSlotKeyIsStableAcrossRuns(t *testing.T) {
	first := autorunSlotKey("/repo/tasks/fix-gate.md", "codex")
	second := autorunSlotKey("/repo/tasks/fix-gate.md", "codex")
	if first != second {
		t.Fatalf("slot key is not stable: %q vs %q", first, second)
	}
	if first != "fix-gate:codex" {
		t.Fatalf("slot key = %q; want a human-meaningful task:seat address", first)
	}
	// The same task on two seats is two agents with two homes — that is the
	// whole point of the master/doer split being visible.
	if master := autorunSlotKey("/repo/tasks/fix-gate.md", "claude"); master == first {
		t.Fatal("two seats on one task must not collapse into one slot")
	}
	// Different tasks never share a slot.
	if other := autorunSlotKey("/repo/tasks/other.md", "codex"); other == first {
		t.Fatal("two tasks must not share a slot")
	}
	// An unspecified seat still gets a usable address rather than a dangling colon.
	if auto := autorunSlotKey("/repo/tasks/fix-gate.md", ""); auto != "fix-gate:auto" {
		t.Fatalf("empty seat = %q; want a resolvable address", auto)
	}
}

// The tmux session name and the slot key are the same agent seen from two ends.
// If they can disagree, "attach to the thing I tapped" silently attaches to
// something else.
func TestAutorunSlotKeyAgreesWithTmuxSessionName(t *testing.T) {
	const task, runner = "/repo/tasks/fix-gate.md", "codex"
	slot := autorunSlotKey(task, runner)
	session := autorunTmuxSessionName(task, runner)
	name, seat, _ := strings.Cut(slot, ":")
	if !strings.Contains(session, name) || !strings.Contains(session, seat) {
		t.Fatalf("slot %q and tmux session %q do not describe the same agent", slot, session)
	}
}

// THE regression this exists for: recency order made an agent's position a
// function of time, so any session starting or finishing renumbered every row.
// Slot order must be unaffected by status, finish time, and map iteration.
func TestAutorunViewOrderDoesNotMoveWhenStatusChanges(t *testing.T) {
	now := time.Now().UTC()
	views := []autorunSessionView{
		{ID: "autorun-3", Slot: "widget:codex", Status: "running", StartedAt: now},
		{ID: "autorun-1", Slot: "alpha:claude", Status: "running", StartedAt: now.Add(-2 * time.Hour)},
		{ID: "autorun-2", Slot: "beta:opencode", Status: "running", StartedAt: now.Add(-time.Hour)},
	}
	sortAutorunViewsBySlot(views)
	before := []string{views[0].Slot, views[1].Slot, views[2].Slot}
	if before[0] != "alpha:claude" || before[1] != "beta:opencode" || before[2] != "widget:codex" {
		t.Fatalf("slot order is not deterministic: %v", before)
	}

	// The oldest agent finishes — under recency order this would have jumped it
	// to the end and shifted everything else. Under slot order nothing moves.
	views[0].Status = "completed"
	views[0].FinishedAt = now.Add(time.Minute)
	views[2].Status = "failed"
	sortAutorunViewsBySlot(views)
	after := []string{views[0].Slot, views[1].Slot, views[2].Slot}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("a status change moved the slots: %v -> %v", before, after)
		}
	}
}

// Go randomizes map iteration, so two polls over identical state could return
// two different orders — the list would shuffle while nothing changed.
func TestAutorunViewOrderIsTotalForRepeatedSlots(t *testing.T) {
	views := []autorunSessionView{
		{ID: "autorun-9", Slot: "widget:codex"},
		{ID: "autorun-2", Slot: "widget:codex"},
	}
	sortAutorunViewsBySlot(views)
	if views[0].ID != "autorun-2" {
		t.Fatalf("two runs of one slot must order deterministically, got %q first", views[0].ID)
	}
}

// A session must carry its slot to the client. Without it every consumer
// re-derives the address from a path, and they will disagree.
func TestAutorunSessionViewCarriesItsSlot(t *testing.T) {
	m := &autorunSessionManager{sessions: make(map[string]*autorunSession)}
	s := &autorunSession{
		ID:   "autorun-1",
		Slot: autorunSlotKey("/repo/tasks/widget.md", "codex"),
		Task: "/repo/tasks/widget.md",
	}
	if got := m.view(s); got.Slot != "widget:codex" {
		t.Fatalf("view dropped the slot: %q", got.Slot)
	}
}
