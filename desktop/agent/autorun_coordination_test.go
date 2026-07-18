package main

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAutorunOwnedAreas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		want   []string
	}{
		{"recursive glob bounds at its directory", []string{"desktop/agent/**"}, []string{"desktop/agent"}},
		{"file glob bounds at its directory", []string{"desktop/agent/autorun*.go"}, []string{"desktop/agent"}},
		{"literal file keeps its full path", []string{"tasks/merged-remaining.md"}, []string{"tasks/merged-remaining.md"}},
		{"several scopes are all kept, sorted", []string{"web/**", "mobile/**"}, []string{"mobile", "web"}},
		{"duplicates collapse", []string{"web/**", "web/lib/*.ts"}, []string{"web"}},
		{"leading ./ is normalized away", []string{"./web/**"}, []string{"web"}},

		// The dangerous cases: anything that reduces to the repo root owns
		// everything, and must swallow its siblings rather than look narrow.
		{"no scopes means unrestricted", nil, []string{""}},
		{"empty strings mean unrestricted", []string{"", "  "}, []string{""}},
		{"bare top-level glob is the root", []string{"**"}, []string{""}},
		{"root glob swallows siblings", []string{"web/**", "**"}, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := autorunOwnedAreas(tc.scopes); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("autorunOwnedAreas(%q) = %q, want %q", tc.scopes, got, tc.want)
			}
		})
	}
}

func TestAutorunAreasOverlap(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []string
		want bool
	}{
		// Independent — these MUST run in parallel or the fleet is pointless.
		{"different top-level dirs", []string{"web"}, []string{"mobile"}, false},
		{"sibling packages", []string{"desktop/agent"}, []string{"desktop/installer"}, false},
		{"prefix that is not a path boundary", []string{"web"}, []string{"website"}, false},

		// Dependent — starting both is what cost a night of tokens.
		{"identical areas", []string{"desktop/agent"}, []string{"desktop/agent"}, true},
		{"parent contains child", []string{"desktop"}, []string{"desktop/agent"}, true},
		{"child inside parent, order reversed", []string{"desktop/agent"}, []string{"desktop"}, true},
		{"one shared area among several", []string{"web", "desktop/agent"}, []string{"mobile", "desktop/agent"}, true},
		{"unrestricted overlaps a narrow scope", []string{""}, []string{"web"}, true},
		{"unrestricted overlaps unrestricted", []string{""}, []string{""}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := autorunAreasOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("overlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// The four scope-violation deaths of 2026-07-17, replayed. Each of those tasks
// legitimately spanned desktop/agent + web + mobile; run together they collide,
// and the loser's finished work was stashed and discarded.
func TestAdmissionRefusesTheOverlapThatKilledFourRuns(t *testing.T) {
	m := &autorunSessionManager{sessions: map[string]*autorunSession{
		"autorun-1": {
			ID: "autorun-1", Slot: "ci-one-bus:codex", Status: "running",
			Scopes: []string{"desktop/agent/**", "web/**"},
		},
	}}

	adm := m.admitLocked("ci-review-gate:codex", autorunOwnedAreas([]string{"desktop/agent/**", "web/**"}))
	if adm.Allowed {
		t.Fatal("two runs both owning desktop/agent must not both start")
	}
	if adm.Reason != "area_owned" {
		t.Fatalf("Reason = %q, want area_owned", adm.Reason)
	}
	if adm.HolderID != "autorun-1" {
		t.Fatalf("HolderID = %q — the refusal must name who holds it", adm.HolderID)
	}
}

func TestAdmissionAllowsGenuinelyIndependentRuns(t *testing.T) {
	m := &autorunSessionManager{sessions: map[string]*autorunSession{
		"autorun-1": {ID: "autorun-1", Slot: "a:codex", Status: "running", Scopes: []string{"web/**"}},
	}}
	if adm := m.admitLocked("b:codex", autorunOwnedAreas([]string{"mobile/**"})); !adm.Allowed {
		t.Fatalf("disjoint areas must run in parallel, got refusal: %s", adm.Detail)
	}
}

func TestAdmissionRefusesBusySlot(t *testing.T) {
	m := &autorunSessionManager{sessions: map[string]*autorunSession{
		"autorun-1": {ID: "autorun-1", Slot: "merged-remaining:codex", Status: "running", Scopes: []string{"web/**"}},
	}}
	// Disjoint areas, same slot: still refused. The slot IS the worktree path,
	// branch, tmux session and prompt file — a second run adopts or deletes the
	// first one's tree, which is how a relaunch inherited a finished run's
	// "already converged" handoff and did nothing.
	adm := m.admitLocked("merged-remaining:codex", autorunOwnedAreas([]string{"mobile/**"}))
	if adm.Allowed {
		t.Fatal("a busy slot must be refused even when the areas are disjoint")
	}
	if adm.Reason != "slot_busy" {
		t.Fatalf("Reason = %q, want slot_busy", adm.Reason)
	}
}

// A finished run owns nothing. Sessions are in-memory and never evicted, so if
// admission counted them the box would refuse every start after a day's work.
func TestAdmissionIgnoresFinishedRuns(t *testing.T) {
	for _, status := range []string{"completed", "failed", "stopped"} {
		t.Run(status, func(t *testing.T) {
			m := &autorunSessionManager{sessions: map[string]*autorunSession{
				"old": {
					ID: "old", Slot: "same:codex", Status: status,
					FinishedAt: time.Now().UTC(), Scopes: []string{"desktop/agent/**"},
				},
			}}
			if adm := m.admitLocked("same:codex", autorunOwnedAreas([]string{"desktop/agent/**"})); !adm.Allowed {
				t.Fatalf("a %s run must not block a new start: %s", status, adm.Detail)
			}
		})
	}
}

// A run recorded as "running" but already finished is stale bookkeeping, not a
// live claim. Both conditions are required.
func TestAdmissionIgnoresRunningButFinished(t *testing.T) {
	m := &autorunSessionManager{sessions: map[string]*autorunSession{
		"stale": {
			ID: "stale", Slot: "same:codex", Status: "running",
			FinishedAt: time.Now().UTC(), Scopes: []string{"desktop/agent/**"},
		},
	}}
	if adm := m.admitLocked("same:codex", autorunOwnedAreas([]string{"desktop/agent/**"})); !adm.Allowed {
		t.Fatalf("a finished-but-running-flagged session must not hold a claim: %s", adm.Detail)
	}
}

// An unscoped run owns the repo. Letting one start beside anything else
// reintroduces exactly the collision this file exists to prevent.
func TestAdmissionTreatsUnscopedRunAsOwningEverything(t *testing.T) {
	m := &autorunSessionManager{sessions: map[string]*autorunSession{
		"broad": {ID: "broad", Slot: "broad:codex", Status: "running", Scopes: nil},
	}}
	if adm := m.admitLocked("narrow:codex", autorunOwnedAreas([]string{"web/**"})); adm.Allowed {
		t.Fatal("an unscoped live run owns the repo; nothing may start beside it")
	}

	m2 := &autorunSessionManager{sessions: map[string]*autorunSession{
		"narrow": {ID: "narrow", Slot: "narrow:codex", Status: "running", Scopes: []string{"web/**"}},
	}}
	if adm := m2.admitLocked("broad:codex", autorunOwnedAreas(nil)); adm.Allowed {
		t.Fatal("an unscoped STARTING run would own the repo; it must not start beside a live run")
	}
}

func TestAdmissionErrorIsDistinguishable(t *testing.T) {
	err := error(&autorunAdmissionError{admission: autorunAdmission{
		Reason: "slot_busy", Detail: "slot x is already running", HolderID: "autorun-1",
	}})
	var adm *autorunAdmissionError
	if !errors.As(err, &adm) {
		t.Fatal("callers must be able to tell 'not now' from a real failure via errors.As")
	}
	if adm.Admission().Reason != "slot_busy" {
		t.Fatalf("Reason = %q, want slot_busy", adm.Admission().Reason)
	}
	if err.Error() == "" {
		t.Fatal("the error must still carry a human sentence")
	}
}
