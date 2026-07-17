package main

import (
	"path/filepath"
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

func TestAutorunBranchNameTracksTheSlotWithoutUsingColon(t *testing.T) {
	branch := autorunBranchName("/repo/tasks/fix-gate.md", "codex")
	if branch != "autorun/fix-gate/codex" {
		t.Fatalf("branch name = %q", branch)
	}
	if strings.Contains(branch, ":") {
		t.Fatalf("git branch names cannot contain a colon: %q", branch)
	}
}

func TestAutorunWorkspaceForUsesStableSlotPath(t *testing.T) {
	autorunIsolateHome(t)
	ws, err := autorunWorkspaceFor("/repo/tasks/fix-gate.md", "/repo", "codex")
	if err != nil {
		t.Fatal(err)
	}
	// The slot keeps its colon: it is an identity string, and the UIs key off it.
	if got, want := ws.Slot, "fix-gate:codex"; got != want {
		t.Fatalf("slot = %q, want %q", got, want)
	}
	// The PATH must not. This assertion used to demand "fix-gate:codex" on disk
	// and so guarded the bug it should have caught: a colon is the PATH/IFS
	// separator, and every runner launched into that worktree died resolving
	// its own cwd (codex: "No such file or directory (os error 2)") before it
	// could print a word. Because all seats shared the flaw, the loop walked
	// the whole fallback chain and blamed whichever runner happened to be last.
	if got, want := ws.WorkDir, filepath.Join(filepath.Dir(filepath.Dir(ws.WorkDir)), "worktrees", "fix-gate-codex"); got != want {
		t.Fatalf("worktree path = %q, want %q", got, want)
	}
	if got, want := ws.TaskPath, filepath.Join(ws.WorkDir, "tasks", "fix-gate.md"); got != want {
		t.Fatalf("task path = %q, want %q", got, want)
	}
}

// A worktree path is handed to git, to a shell, and to whatever runner binary
// we exec inside it. Anything that needs quoting to survive that trip does not
// belong in the name.
func TestAutorunWorktreePathIsShellSafe(t *testing.T) {
	autorunIsolateHome(t)
	for _, seat := range []string{"codex", "claude", "opencode", "glm", ""} {
		ws, err := autorunWorkspaceFor("/repo/tasks/fix-gate.md", "/repo", seat)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(ws.WorkDir)
		for _, bad := range []string{":", " ", "$", "\"", "'", "\\", "*", "?", "|", "&", ";", "(", ")", "<", ">", "\t", "\n"} {
			if strings.Contains(base, bad) {
				t.Fatalf("seat %q: worktree dir %q contains %q — it will not survive being cd'd into", seat, base, bad)
			}
		}
	}
}

// Two seats on one task are two agents and must stay two worktrees. Sanitizing
// the path must not collapse them into one — that would have two runners
// editing the same checkout.
func TestAutorunWorktreePathsStayDistinctPerSeat(t *testing.T) {
	autorunIsolateHome(t)
	seen := map[string]string{}
	for _, seat := range []string{"codex", "claude", "opencode", "glm"} {
		ws, err := autorunWorkspaceFor("/repo/tasks/fix-gate.md", "/repo", seat)
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := seen[ws.WorkDir]; dup {
			t.Fatalf("seats %q and %q collapsed onto one worktree %q", prev, seat, ws.WorkDir)
		}
		seen[ws.WorkDir] = seat
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
