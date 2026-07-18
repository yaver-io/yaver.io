package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAutorunBuildTargetsForAreas(t *testing.T) {
	for _, tc := range []struct {
		name  string
		areas []string
		want  []string
	}{
		{"agent area builds go", []string{"desktop/agent"}, []string{"agent"}},
		{"web area builds web", []string{"web"}, []string{"web"}},
		{"tvos is its own toolchain", []string{"tvos"}, []string{"tvos"}},
		{"watch is its own toolchain", []string{"watch"}, []string{"watchos"}},
		{"mobile occupies BOTH mobile SDKs", []string{"mobile"}, []string{"android", "ios"}},
		{"a narrower mobile area picks one", []string{"mobile/ios"}, []string{"ios"}},
		{"a sub-area still maps", []string{"web/lib"}, []string{"web"}},
		{"several areas union their targets", []string{"web", "desktop/agent"}, []string{"agent", "web"}},

		// The whole point of the file: these must NOT collide, so a tvOS build
		// and a web edit can run at the same time.
		{"tvos and web are disjoint", []string{"tvos"}, []string{"tvos"}},

		// Docs-only work occupies no toolchain and must never block a compile.
		{"docs take no build lease", []string{"docs"}, []string{}},
		{"tasks take no build lease", []string{"tasks"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := autorunBuildTargetsForAreas(tc.areas)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("targets(%q) = %q, want %q", tc.areas, got, tc.want)
			}
		})
	}
}

// An unrestricted run owns every toolchain, matching autorunOwnedAreas' reading
// that it conflicts with everything.
func TestUnrestrictedRunOwnsEveryBuildTarget(t *testing.T) {
	got := autorunBuildTargetsForAreas([]string{""})
	for _, want := range []string{"agent", "android", "ios", "tvos", "watchos", "web"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("unrestricted run must own %q; got %q", want, got)
		}
	}
}

// THE test for this file. A tvOS build and a web edit must run at the same time
// — that is the 15 minutes of idle machine this whole design exists to reclaim.
func TestBuildAndUnrelatedEditOverlap(t *testing.T) {
	m := newLeaseManager()

	// Run A enters `build` for tvOS: holds its area + toolchain, and — crucially
	// — does NOT hold a seat.
	aKeys := autorunPhaseLeases("build", "codex", []string{"tvos"}, autorunBuildTargetsForAreas([]string{"tvos"}), "main")
	if err := m.Acquire("run-a", "tv:codex", "build", aKeys...); err != nil {
		t.Fatalf("tvOS build could not start: %v", err)
	}

	// Run B edits web with the SAME runner seat. Today this waits on a compiler.
	bKeys := autorunPhaseLeases("edit", "codex", []string{"web"}, nil, "main")
	if err := m.Acquire("run-b", "web:codex", "edit", bKeys...); err != nil {
		t.Fatalf("web edit must not wait on a tvOS compiler: %v", err)
	}
}

// The seat is the resource a build must give back. If `build` still took it, the
// overlap above is impossible.
func TestBuildPhaseDoesNotHoldTheSeat(t *testing.T) {
	for _, k := range autorunPhaseLeases("build", "codex", []string{"web"}, []string{"web"}, "main") {
		if k.Class == leaseSeat {
			t.Fatal("build must release the runner seat — it is idle waiting on a compiler, and that seat is what a sibling's edit needs")
		}
	}
}

// ...but it must KEEP the source area, or a sibling edits the tree being
// compiled and the gate result describes a tree that no longer exists.
func TestBuildPhaseKeepsItsSourceArea(t *testing.T) {
	m := newLeaseManager()
	keys := autorunPhaseLeases("build", "codex", []string{"web"}, []string{"web"}, "main")
	if err := m.Acquire("run-a", "web:codex", "build", keys...); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	edit := autorunPhaseLeases("edit", "opencode", []string{"web"}, nil, "main")
	if err := m.Acquire("run-b", "web2:opencode", "edit", edit...); err == nil {
		t.Fatal("a second run must not edit web/ while web/ is being built")
	}
}

// Two builds of the same toolchain must serialize: concurrent Xcode/Gradle runs
// thrash the same caches and disk.
func TestSameBuildTargetSerializes(t *testing.T) {
	m := newLeaseManager()
	if err := m.Acquire("run-a", "a", "build", buildLease("ios")); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	err := m.Acquire("run-b", "b", "build", buildLease("ios"))
	if err == nil {
		t.Fatal("two iOS builds must not run at once")
	}
	c, ok := err.(leaseConflict)
	if !ok || c.Holder != "run-a" {
		t.Fatalf("the refusal must name the holder, got %v", err)
	}
}

// All-or-nothing is the deadlock guard: a partial hold is how two runs end up
// each waiting on what the other took.
func TestAcquireIsAllOrNothing(t *testing.T) {
	m := newLeaseManager()
	if err := m.Acquire("run-a", "a", "build", buildLease("ios")); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// run-b wants a free area AND the taken toolchain.
	err := m.Acquire("run-b", "b", "build", sourceLease("mobile"), buildLease("ios"))
	if err == nil {
		t.Fatal("expected refusal")
	}
	// The free one must NOT have been taken on the way through.
	if err := m.Acquire("run-c", "c", "edit", sourceLease("mobile")); err != nil {
		t.Fatalf("a failed acquire must leave nothing behind, but mobile was held: %v", err)
	}
}

// Asking again for what you already hold is a renewal. Phases overlap at their
// boundary and a run must not lose its own claim by re-requesting it.
func TestReacquiringOwnLeaseIsNotAConflict(t *testing.T) {
	m := newLeaseManager()
	if err := m.Acquire("run-a", "a", "edit", sourceLease("web"), seatLease("codex")); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := m.Acquire("run-a", "a", "build", sourceLease("web"), buildLease("web")); err != nil {
		t.Fatalf("a run must not conflict with itself across a phase change: %v", err)
	}
}

// A crashed run must not block the fleet forever. TTL is the backstop; releasing
// on end is the mechanism.
func TestExpiredLeaseStopsBlocking(t *testing.T) {
	m := newLeaseManager()
	base := time.Now()
	m.now = func() time.Time { return base }
	if err := m.Acquire("dead-run", "a", "build", buildLease("ios")); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	m.now = func() time.Time { return base.Add(autorunLeaseTTL + time.Minute) }
	if err := m.Acquire("live-run", "b", "build", buildLease("ios")); err != nil {
		t.Fatalf("an expired claim must not block a live run: %v", err)
	}
	for _, h := range m.Snapshot() {
		if h.Holder == "dead-run" {
			t.Fatal("Snapshot must reap expired holds, or a dead holder appears to still own things")
		}
	}
}

func TestRenewKeepsALongBuildAlive(t *testing.T) {
	m := newLeaseManager()
	base := time.Now()
	m.now = func() time.Time { return base }
	if err := m.Acquire("run-a", "a", "build", buildLease("ios")); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	m.now = func() time.Time { return base.Add(autorunLeaseTTL - time.Minute) }
	if n := m.Renew("run-a"); n != 1 {
		t.Fatalf("renewed %d leases, want 1", n)
	}
	m.now = func() time.Time { return base.Add(autorunLeaseTTL + time.Minute) }
	if err := m.Acquire("run-b", "b", "build", buildLease("ios")); err == nil {
		t.Fatal("a renewed build must still hold its toolchain — a long compile is not a dead run")
	}
}

func TestReleaseAllFreesACrashedRun(t *testing.T) {
	m := newLeaseManager()
	keys := autorunPhaseLeases("edit", "codex", []string{"web", "desktop/agent"}, nil, "main")
	if err := m.Acquire("run-a", "a", "edit", keys...); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	m.ReleaseAll("run-a")
	if got := len(m.Snapshot()); got != 0 {
		t.Fatalf("ReleaseAll left %d holds", got)
	}
	if err := m.Acquire("run-b", "b", "edit", keys...); err != nil {
		t.Fatalf("after ReleaseAll everything must be free: %v", err)
	}
}

// Release must be scoped to the holder, or one run can free another's claim.
func TestReleaseOnlyAffectsOwnHolds(t *testing.T) {
	m := newLeaseManager()
	if err := m.Acquire("run-a", "a", "build", buildLease("ios")); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	m.Release("run-b", buildLease("ios")) // not the holder
	if err := m.Acquire("run-c", "c", "build", buildLease("ios")); err == nil {
		t.Fatal("a non-holder must not be able to release someone else's lease")
	}
}

// The lease key doubles as a git ref path when this grows a cross-machine tier,
// so it must not contain anything a ref name forbids.
func TestLeaseKeyIsRefSafe(t *testing.T) {
	for _, k := range []leaseKey{
		sourceLease("desktop/agent"), buildLease("ios"), seatLease("codex"), landLease("main"), sourceLease(""),
	} {
		s := k.String()
		if s == "" || strings.ContainsAny(s, " ~^:?*[\\") || strings.Contains(s, "..") {
			t.Fatalf("lease key %q is not usable as a git ref", s)
		}
	}
}
