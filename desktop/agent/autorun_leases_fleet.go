package main

// autorun_leases_fleet.go — the bridge that makes the two lease tiers one
// answer.
//
// Without this, autorun_leases.go excludes runs on ONE box and
// autorun_leases_git.go excludes them across the fleet, and nothing consults
// both — so two machines would each be locally certain they may build iOS.
//
// The order is deliberate, and it is the whole design:
//
//	1. LOCAL first. It is in-process and instant, and most conflicts are local
//	   (siblings on one box). Rejecting here costs no network at all.
//	2. FETCH before trusting "free". A lease is only as fresh as the last fetch,
//	   so skipping this turns the remote into a cache that lies.
//	3. GIT CAS. The remote arbitrates. This is the authoritative answer.
//	4. RELEASE LOCAL if the CAS lost. This is the step that is easy to omit and
//	   expensive to omit: a run that keeps a local claim it lost remotely holds
//	   a phantom lease, blocking its own siblings on behalf of a key it does not
//	   own.
//
// Degrade-to-local is chosen on purpose. When git is unreachable the fleet tier
// cannot answer, and the alternatives are to block every run until the network
// returns, or to proceed with local exclusion only. Blocking is worse: a box
// that cannot reach the remote is exactly the box you want to keep working, and
// the failure mode of proceeding is two machines editing one area — which the
// scope check and the landing rebase already catch downstream. The choice is
// recorded on the result so a caller never has to guess which tier answered.

import (
	"context"
	"log"
	"time"
)

// fleetAcquireResult says who granted the claim, and — when refused — which
// tier refused it. "Denied by whom" is the question a stuck run needs answered.
type fleetAcquireResult struct {
	OK bool
	// Tier is "local", "fleet", or "local-only" when the remote was unreachable
	// and we proceeded on local exclusion alone.
	Tier string
	// Conflict is set when a tier refused. Nil on success.
	Conflict error
	// Degraded is true when the fleet tier could not be consulted. The claim is
	// real locally and unverified globally; a caller that cares (a build target,
	// a deploy) may choose to wait, while an edit can reasonably proceed.
	Degraded bool
}

// fleetLeaseCoordinator holds both tiers. The git client is optional: a box with
// no repo remote still gets local exclusion, which is strictly better than none.
type fleetLeaseCoordinator struct {
	local  *leaseManager
	git    *gitLeaseClient
	remote string
	// machineID identifies THIS box in a published record. Never a hostname or
	// a path — the privacy contract forbids both from leaving the machine.
	machineID string
}

func newFleetLeaseCoordinator(local *leaseManager, git *gitLeaseClient, remote, machineID string) *fleetLeaseCoordinator {
	return &fleetLeaseCoordinator{local: local, git: git, remote: remote, machineID: machineID}
}

// Acquire takes a key on this box and, when possible, across the fleet.
func (f *fleetLeaseCoordinator) Acquire(ctx context.Context, holder, slot, phase string, k leaseKey) fleetAcquireResult {
	// 1. Local first — cheapest possible rejection.
	if err := f.local.Acquire(holder, slot, phase, k); err != nil {
		return fleetAcquireResult{Tier: "local", Conflict: err}
	}

	if f.git == nil {
		// No git tier at all. Local exclusion is the whole answer, and saying so
		// beats implying a guarantee we are not making.
		return fleetAcquireResult{OK: true, Tier: "local-only", Degraded: true}
	}

	now := time.Now()

	// 2. Refresh before trusting anything — a stale namespace makes "free" a lie.
	//
	// Only when there is a remote to refresh FROM. The CAS below is what
	// actually excludes, and it works on any repository two actors share; the
	// remote is how that repository reaches other boxes, not a precondition for
	// arbitration. Gating the whole tier on a remote (as this first did) meant
	// two machines sharing a repo got no exclusion whatsoever — the local
	// managers are separate, so both would have proceeded.
	if f.remote != "" {
		if err := f.git.FetchLeases(ctx, f.remote); err != nil {
			log.Printf("[autorun-lease] fleet tier unreachable (%v) — proceeding on local exclusion for %s", err, k)
			return fleetAcquireResult{OK: true, Tier: "local-only", Degraded: true}
		}
	}

	// 3. The remote arbitrates.
	rec := gitLeaseRecord{
		Key: k.String(), Holder: holder, Slot: slot, MachineID: f.machineID, Phase: phase,
		AcquiredAt: now.Unix(), TTLSeconds: int64(autorunLeaseTTL / time.Second),
	}
	ok, err := f.git.AcquireLease(ctx, k, rec, now)
	if err != nil {
		f.local.Release(holder, k)
		return fleetAcquireResult{Tier: "fleet", Conflict: err}
	}
	if !ok {
		// 4. We lost. Give the local claim back — holding it would block our own
		// siblings on behalf of a key another machine owns.
		f.local.Release(holder, k)
		held, _, found := f.git.ReadLease(ctx, k)
		conflict := leaseConflict{Key: k, Holder: "another machine"}
		if found {
			conflict = leaseConflict{Key: k, Holder: held.Holder, Slot: held.Slot, Phase: held.Phase}
		}
		return fleetAcquireResult{Tier: "fleet", Conflict: conflict}
	}

	// Publishing is what makes the claim visible to the rest of the fleet. A
	// failure here is NOT a lost claim — the CAS already succeeded locally, and
	// the next publish or the holder's renewal will carry it — so it is logged
	// rather than treated as a refusal.
	if f.remote != "" {
		if err := f.git.PublishLeases(ctx, f.remote); err != nil {
			log.Printf("[autorun-lease] holding %s locally; publish deferred: %v", k, err)
		}
	}
	return fleetAcquireResult{OK: true, Tier: "fleet"}
}

// Release drops a key in both tiers. Local first so a sibling on this box can
// proceed immediately, without waiting on the network.
func (f *fleetLeaseCoordinator) Release(ctx context.Context, holder string, k leaseKey) {
	f.local.Release(holder, k)
	if f.git == nil {
		return
	}
	if !f.git.ReleaseLease(ctx, k, holder) {
		return // not ours, or already gone — either way nothing to publish
	}
	if f.remote == "" {
		return
	}
	if err := f.git.PublishLeases(ctx, f.remote); err != nil {
		// The ref is deleted locally and the record has a TTL, so the worst
		// case is that the fleet frees this key on expiry instead of now.
		log.Printf("[autorun-lease] released %s locally; publish deferred: %v", k, err)
	}
}

// ReleaseAll drops every claim a holder owns, in both tiers. Called when a run
// ends however it ends — TTL is the backstop for a process that dies before
// reaching here, not the mechanism.
func (f *fleetLeaseCoordinator) ReleaseAll(ctx context.Context, holder string) {
	held := f.local.Snapshot()
	f.local.ReleaseAll(holder)
	if f.git == nil {
		return
	}
	published := false
	for _, h := range held {
		if h.Holder != holder {
			continue
		}
		if f.git.ReleaseLease(ctx, h.Key, holder) {
			published = true
		}
	}
	if published && f.remote != "" {
		if err := f.git.PublishLeases(ctx, f.remote); err != nil {
			log.Printf("[autorun-lease] released %s's claims locally; publish deferred: %v", holder, err)
		}
	}
}

// Renew extends both tiers. A long build must not lose its toolchain to TTL
// while it is legitimately still compiling — on either tier.
func (f *fleetLeaseCoordinator) Renew(ctx context.Context, holder, slot, phase string) {
	f.local.Renew(holder)
	if f.git == nil {
		return
	}
	now := time.Now()
	for _, h := range f.local.Snapshot() {
		if h.Holder != holder {
			continue
		}
		rec := gitLeaseRecord{
			Key: h.Key.String(), Holder: holder, Slot: slot, MachineID: f.machineID, Phase: phase,
			AcquiredAt: now.Unix(), TTLSeconds: int64(autorunLeaseTTL / time.Second),
		}
		// Re-acquiring our own key IS the renewal path (autorun_leases_git.go).
		if _, err := f.git.AcquireLease(ctx, h.Key, rec, now); err != nil {
			log.Printf("[autorun-lease] renew %s: %v", h.Key, err)
		}
	}
	if f.remote != "" {
		if err := f.git.PublishLeases(ctx, f.remote); err != nil {
			log.Printf("[autorun-lease] renewed %s locally; publish deferred: %v", holder, err)
		}
	}
}
