package main

import (
	"context"
	"testing"
)

// Two coordinators sharing ONE git repo stand in for two machines sharing one
// remote. That is the situation the whole file exists for.
func twoMachines(t *testing.T) (*fleetLeaseCoordinator, *fleetLeaseCoordinator) {
	t.Helper()
	shared := newLeaseRepo(t) // real git repo; see autorun_leases_git_test.go
	a := newFleetLeaseCoordinator(newLeaseManager(), shared, "", "machine-a")
	b := newFleetLeaseCoordinator(newLeaseManager(), shared, "", "machine-b")
	return a, b
}

// A conflict on this box must be answered without touching the network at all.
func TestFleetLocalConflictShortCircuits(t *testing.T) {
	a, _ := twoMachines(t)
	ctx := context.Background()
	k := buildLease("ios")

	if r := a.Acquire(ctx, "run-1", "a:codex", "build", k); !r.OK {
		t.Fatalf("first acquire failed: %v", r.Conflict)
	}
	r := a.Acquire(ctx, "run-2", "b:codex", "build", k)
	if r.OK {
		t.Fatal("two runs on one box must not both hold build/ios")
	}
	if r.Tier != "local" {
		t.Fatalf("tier = %q, want local — a local conflict must not cost a network round trip", r.Tier)
	}
}

// THE test: two different machines, one key. Local exclusion cannot see across
// boxes, so without the git tier both would proceed.
func TestFleetSecondMachineIsRefused(t *testing.T) {
	a, b := twoMachines(t)
	ctx := context.Background()
	k := buildLease("ios")

	if r := a.Acquire(ctx, "run-a", "task:codex", "build", k); !r.OK {
		t.Fatalf("machine A could not acquire: %v", r.Conflict)
	}
	r := b.Acquire(ctx, "run-b", "task:codex", "build", k)
	if r.OK {
		t.Fatal("two MACHINES must not both hold build/ios — this is what the fleet tier is for")
	}
	if r.Tier != "fleet" {
		t.Fatalf("tier = %q, want fleet", r.Tier)
	}
	// The refusal has to name the winner, or a stuck run has nowhere to look.
	if c, ok := r.Conflict.(leaseConflict); !ok || c.Holder != "run-a" {
		t.Fatalf("conflict = %v, want it to name run-a", r.Conflict)
	}
}

// The step that is easy to omit: losing the remote CAS must give the local claim
// back, or the loser blocks its own siblings on behalf of a key it does not own.
func TestFleetLosingTheRaceReleasesTheLocalClaim(t *testing.T) {
	a, b := twoMachines(t)
	ctx := context.Background()
	k := buildLease("ios")

	a.Acquire(ctx, "run-a", "task:codex", "build", k)
	if r := b.Acquire(ctx, "run-b", "task:codex", "build", k); r.OK {
		t.Fatal("expected machine B to lose")
	}
	// B lost. Its LOCAL manager must not still be holding the key.
	for _, h := range b.local.Snapshot() {
		if h.Key.String() == k.String() {
			t.Fatal("machine B kept a phantom local claim for a key it lost remotely")
		}
	}
	// Proof it is really free locally: a sibling on B can take it once A frees it.
	a.Release(ctx, "run-a", k)
	if r := b.Acquire(ctx, "run-b2", "task:codex", "build", k); !r.OK {
		t.Fatalf("after A released, B must be able to acquire: %v", r.Conflict)
	}
}

// Disjoint keys never contend across machines — a tvOS build on one box and a
// web edit on another is the point of the whole system.
func TestFleetDisjointKeysRunOnBothMachines(t *testing.T) {
	a, b := twoMachines(t)
	ctx := context.Background()

	if r := a.Acquire(ctx, "run-a", "tv:codex", "build", buildLease("tvos")); !r.OK {
		t.Fatalf("tvos build: %v", r.Conflict)
	}
	if r := b.Acquire(ctx, "run-b", "web:opencode", "edit", sourceLease("web")); !r.OK {
		t.Fatalf("web edit on another machine must not contend: %v", r.Conflict)
	}
	if r := b.Acquire(ctx, "run-b", "web:opencode", "edit", seatLease("opencode")); !r.OK {
		t.Fatalf("a different runner seat must be free: %v", r.Conflict)
	}
}

// A box with no remote still gets local exclusion, and must SAY that its answer
// is local-only rather than implying a fleet-wide guarantee.
func TestFleetWithoutRemoteDegradesHonestly(t *testing.T) {
	solo := newFleetLeaseCoordinator(newLeaseManager(), nil, "", "machine-solo")
	ctx := context.Background()
	k := buildLease("ios")

	r := solo.Acquire(ctx, "run-a", "a:codex", "build", k)
	if !r.OK {
		t.Fatalf("a box with no remote must still work: %v", r.Conflict)
	}
	if r.Tier != "local-only" || !r.Degraded {
		t.Fatalf("tier=%q degraded=%v — an unverified claim must not look fleet-wide", r.Tier, r.Degraded)
	}
	// Local exclusion must still hold.
	if r := solo.Acquire(ctx, "run-b", "b:codex", "build", k); r.OK {
		t.Fatal("local exclusion must still apply without a remote")
	}
}

// ReleaseAll must clear both tiers, so a finished run frees the fleet and not
// just its own box.
func TestFleetReleaseAllFreesBothTiers(t *testing.T) {
	a, b := twoMachines(t)
	ctx := context.Background()

	a.Acquire(ctx, "run-a", "task:codex", "edit", sourceLease("web"))
	a.Acquire(ctx, "run-a", "task:codex", "edit", seatLease("codex"))
	a.ReleaseAll(ctx, "run-a")

	if n := len(a.local.Snapshot()); n != 0 {
		t.Fatalf("local tier still holds %d claims after ReleaseAll", n)
	}
	if r := b.Acquire(ctx, "run-b", "task:codex", "edit", sourceLease("web")); !r.OK {
		t.Fatalf("another machine must be able to take a released key: %v", r.Conflict)
	}
}
