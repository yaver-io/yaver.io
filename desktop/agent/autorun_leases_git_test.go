package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests run against a REAL git repository, on purpose. The entire claim of
// this file is that `git update-ref` gives us a genuine compare-and-swap, so a
// mocked git would assert nothing at all.
func newLeaseRepo(t *testing.T) *gitLeaseClient {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return newGitLeaseClient(dir)
}

func rec(holder, phase string, now time.Time, ttl int64) gitLeaseRecord {
	return gitLeaseRecord{
		Holder: holder, Slot: holder + ":codex", MachineID: "dev-" + holder, Phase: phase,
		AcquiredAt: now.Unix(), TTLSeconds: ttl,
	}
}

// The core claim: two machines cannot both hold one key.
func TestGitLeaseExcludesASecondHolder(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := buildLease("ios")

	ok, err := g.AcquireLease(ctx, k, rec("run-a", "build", now, 3600), now)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	ok, err = g.AcquireLease(ctx, k, rec("run-b", "build", now, 3600), now)
	if err != nil {
		t.Fatalf("second acquire errored instead of losing: %v", err)
	}
	if ok {
		t.Fatal("two machines must not both hold build/ios")
	}

	// The loser must be able to see WHO won, or the refusal is undiagnosable.
	held, _, found := g.ReadLease(ctx, k)
	if !found || held.Holder != "run-a" {
		t.Fatalf("read back holder=%q found=%v, want run-a", held.Holder, found)
	}
}

// Re-acquiring your own key is a renewal, not a conflict — a long build must not
// lose its toolchain by heartbeating.
func TestGitLeaseRenewalBySameHolder(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := buildLease("ios")

	if ok, _ := g.AcquireLease(ctx, k, rec("run-a", "build", now, 3600), now); !ok {
		t.Fatal("acquire")
	}
	later := now.Add(10 * time.Minute)
	ok, err := g.AcquireLease(ctx, k, rec("run-a", "build", later, 3600), later)
	if err != nil || !ok {
		t.Fatalf("renewal must succeed: ok=%v err=%v", ok, err)
	}
	held, _, _ := g.ReadLease(ctx, k)
	if held.AcquiredAt != later.Unix() {
		t.Fatalf("renewal did not extend the claim: %d vs %d", held.AcquiredAt, later.Unix())
	}
}

// A machine that dies must not hold the fleet hostage. Any actor may reap an
// expired claim — via CAS, so two reapers cannot both win.
func TestGitLeaseExpiredClaimIsReapable(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := buildLease("android")

	if ok, _ := g.AcquireLease(ctx, k, rec("dead-machine", "build", now, 60), now); !ok {
		t.Fatal("acquire")
	}
	// Before expiry: still excluded.
	if ok, _ := g.AcquireLease(ctx, k, rec("live", "build", now, 60), now.Add(30*time.Second)); ok {
		t.Fatal("a live claim must still exclude")
	}
	// After expiry: reapable.
	after := now.Add(5 * time.Minute)
	ok, err := g.AcquireLease(ctx, k, rec("live", "build", after, 60), after)
	if err != nil || !ok {
		t.Fatalf("an expired claim must be reapable: ok=%v err=%v", ok, err)
	}
	held, _, _ := g.ReadLease(ctx, k)
	if held.Holder != "live" {
		t.Fatalf("holder = %q after reap, want live", held.Holder)
	}
}

// Release is scoped by holder, exactly as in the local tier: one run must never
// be able to free another's claim.
func TestGitLeaseReleaseIsHolderScoped(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := seatLease("codex")

	if ok, _ := g.AcquireLease(ctx, k, rec("run-a", "edit", now, 3600), now); !ok {
		t.Fatal("acquire")
	}
	if g.ReleaseLease(ctx, k, "run-b") {
		t.Fatal("a non-holder must not be able to release someone else's lease")
	}
	if _, _, ok := g.ReadLease(ctx, k); !ok {
		t.Fatal("the lease was released by the wrong holder")
	}
	if !g.ReleaseLease(ctx, k, "run-a") {
		t.Fatal("the holder must be able to release")
	}
	if _, _, ok := g.ReadLease(ctx, k); ok {
		t.Fatal("lease survived its own holder's release")
	}
}

// After a release the key is free for anyone — the normal handoff.
func TestGitLeaseFreedKeyIsReacquirable(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := sourceLease("web")

	if ok, _ := g.AcquireLease(ctx, k, rec("run-a", "edit", now, 3600), now); !ok {
		t.Fatal("acquire")
	}
	g.ReleaseLease(ctx, k, "run-a")
	if ok, err := g.AcquireLease(ctx, k, rec("run-b", "edit", now, 3600), now); !ok || err != nil {
		t.Fatalf("a freed key must be acquirable: ok=%v err=%v", ok, err)
	}
}

// Disjoint keys never contend — this is what lets a tvOS build and a web edit
// run on two different machines at the same time.
func TestGitLeaseDisjointKeysDoNotContend(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()

	if ok, _ := g.AcquireLease(ctx, buildLease("tvos"), rec("run-a", "build", now, 3600), now); !ok {
		t.Fatal("tvos")
	}
	if ok, err := g.AcquireLease(ctx, sourceLease("web"), rec("run-b", "edit", now, 3600), now); !ok || err != nil {
		t.Fatalf("web edit must not contend with a tvos build across machines: ok=%v err=%v", ok, err)
	}
	if ok, err := g.AcquireLease(ctx, seatLease("opencode"), rec("run-b", "edit", now, 3600), now); !ok || err != nil {
		t.Fatalf("a different runner seat must be free: ok=%v err=%v", ok, err)
	}
}

// The fleet view lists live claims and hides expired ones, so a dead machine
// never appears to still own anything.
func TestGitLeaseListHidesExpired(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()

	g.AcquireLease(ctx, buildLease("ios"), rec("live", "build", now, 3600), now)
	g.AcquireLease(ctx, buildLease("android"), rec("dead", "build", now, 60), now)

	live := g.ListLeases(ctx, now.Add(10*time.Minute))
	if len(live) != 1 || live[0].Holder != "live" {
		t.Fatalf("ListLeases = %+v, want only the live holder", live)
	}
}

// The record must never carry work-derived data. The privacy contract forbids
// paths, prompts and diffs leaving the box, and a lease genuinely does not need
// them: exclusion is about WHICH key is held, not what is being done with it.
func TestGitLeaseRecordCarriesNoWorkDerivedData(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := sourceLease("desktop/agent")

	g.AcquireLease(ctx, k, rec("run-a", "edit", now, 3600), now)
	held, _, ok := g.ReadLease(ctx, k)
	if !ok {
		t.Fatal("read back")
	}
	for _, forbidden := range []string{"/Users/", "/home/", "/root/", "C:\\Users\\"} {
		for _, field := range []string{held.Holder, held.Slot, held.MachineID, held.Phase, held.Key} {
			if strings.Contains(field, forbidden) {
				t.Fatalf("lease record leaked an absolute path (%q in %q)", forbidden, field)
			}
		}
	}
}

// A ref that exists but holds garbage must not wedge the key forever.
func TestGitLeaseUnparseableRecordDoesNotWedgeTheKey(t *testing.T) {
	g := newLeaseRepo(t)
	ctx := context.Background()
	now := time.Now()
	k := buildLease("web")

	// Point the ref at a blob that is not a lease record.
	blob := exec.Command("sh", "-c", "printf 'not json' | git -C "+g.repoDir+" hash-object -w --stdin")
	out, err := blob.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	oid := strings.TrimSpace(string(out))
	if o, err := exec.Command("git", "-C", g.repoDir, "update-ref", autorunLeaseRef(k), oid).CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v: %s", err, o)
	}

	if ok, err := g.AcquireLease(ctx, k, rec("run-a", "build", now, 3600), now); !ok || err != nil {
		t.Fatalf("an unreadable claim must not wedge the key: ok=%v err=%v", ok, err)
	}
}
