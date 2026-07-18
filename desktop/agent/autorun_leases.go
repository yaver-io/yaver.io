package main

// autorun_leases.go — typed, expiring claims so runs that need different things
// can work at the same time.
//
// The problem this exists for, observed while deploying on 2026-07-18: a tvOS
// archive held Xcode, most of the CPU and a chunk of disk for ~15 minutes.
// Nothing about that compile prevented editing web/ or desktop/agent/ — different
// files, different toolchain. But an autorun holds its runner seat, worktree and
// slot for the ENTIRE run, so a queued web task waits on a tvOS compiler.
//
// The unit of scheduling is the run; it should be the step. A run is at least
// four phases and they need almost disjoint resources:
//
//	orient  — seat, read-only tree
//	edit    — seat, EXCLUSIVE source area
//	build   — EXCLUSIVE build target, CPU/RAM/disk   (the seat is idle, waiting)
//	land    — the landing lease                       (seat and toolchain idle)
//
// During build the expensive subscription seat sits idle waiting on a compiler;
// during edit the toolchain sits idle waiting on a model. Releasing what a phase
// does not need is what lets "build tvOS" and "develop web UI" overlap.
//
// Scope of THIS file: the local, in-process tier — typed keys, exclusivity, TTL,
// and all-or-nothing acquisition. The cross-machine tier rides git-ref CAS and
// the bus (see AUTORUN_COLLECTIVE_SYNC_AUDIT.md Part 5); the key strings here are
// chosen to be usable verbatim as ref names so the two tiers cannot disagree
// about what a claim is called.
//
// See docs/architecture/AUTORUN_TASK_GRAPH.md.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// leaseClass is what KIND of thing is claimed. Class decides exclusivity, and
// getting that wrong in either direction is expensive: too strict serializes
// work that could overlap, too loose loses an iteration to a stash.
type leaseClass string

const (
	// leaseSource — one writer per source area. Two runners editing one area
	// lose an iteration to a scope stash; measured six times in one night.
	leaseSource leaseClass = "path"
	// leaseBuild — one build per toolchain target. Concurrent Xcode/Gradle runs
	// thrash the same caches and disk, and one run's `go clean -cache` wipes the
	// cache every other run and the human on that box depend on.
	leaseBuild leaseClass = "build"
	// leaseSeat — one conversation per runner seat. This is the one a build
	// phase must RELEASE; it is the whole point of the file.
	leaseSeat leaseClass = "seat"
	// leaseLand — serializes landing onto a base branch. autorunLandMu already
	// does this within one process; the lease is how it extends across them.
	leaseLand leaseClass = "land"
)

// autorunBuildTargetRule maps a repo path prefix to the BUILD toolchain it
// occupies.
//
// Deliberately a sibling of shipTargetRules (ship_targets.go), not a reuse of
// it: that table answers "what must be DEPLOYED?" and this one answers "what
// toolchain is BUSY?". They are close but not equal — desktop/agent/ deploys as
// cli-npm yet builds with Go, and mobile/ deploys to two stores while occupying
// two different SDKs. Collapsing them would make one of the two questions wrong.
//
// Ordered most-specific-first; first match wins, mirroring shipTargetRules so
// the two stay legible side by side.
var autorunBuildTargetRules = []struct {
	Prefix  string
	Targets []string
}{
	{Prefix: "tvos/", Targets: []string{"tvos"}},
	{Prefix: "watch/", Targets: []string{"watchos"}},
	{Prefix: "wear/", Targets: []string{"android"}},
	{Prefix: "mobile/ios/", Targets: []string{"ios"}},
	{Prefix: "mobile/android/", Targets: []string{"android"}},
	{Prefix: "mobile/", Targets: []string{"ios", "android"}},
	{Prefix: "web/", Targets: []string{"web"}},
	{Prefix: "backend/", Targets: []string{"convex"}},
	{Prefix: "desktop/agent/", Targets: []string{"agent"}},
	{Prefix: "desktop/", Targets: []string{"agent"}},
	{Prefix: "relay/", Targets: []string{"agent"}},
	{Prefix: "cli/", Targets: []string{"agent"}},
	{Prefix: "sdk/", Targets: []string{"sdk"}},
}

// autorunBuildTargetsForAreas returns every toolchain a run's owned areas can
// occupy. Empty means "no build target we model" — docs-only work, which must
// never take a build lease and therefore never blocks a compile.
//
// An unrestricted run (the root area "") owns every target, matching
// autorunOwnedAreas' reading that a run which may touch anything conflicts with
// everything.
func autorunBuildTargetsForAreas(areas []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, area := range areas {
		if area == "" {
			all := map[string]bool{}
			for _, r := range autorunBuildTargetRules {
				for _, t := range r.Targets {
					all[t] = true
				}
			}
			out = out[:0]
			for t := range all {
				out = append(out, t)
			}
			sort.Strings(out)
			return out
		}
		probe := strings.TrimSuffix(area, "/") + "/"
		// The two match directions need OPPOSITE handling, and conflating them
		// is a real bug: area `mobile` matched the more specific `mobile/ios/`
		// rule first and stopped, so a mobile run never locked the Android
		// toolchain and a sibling could start a Gradle build underneath it.
		//
		//   area BROADER than rule  (`mobile` ⊃ `mobile/ios/`, `mobile/android/`)
		//       → collect EVERY rule beneath it, keep scanning.
		//   area NARROWER or equal  (`web/lib` ⊂ `web/`)
		//       → the first (most specific) match is the answer; stop.
		for _, r := range autorunBuildTargetRules {
			broader := strings.HasPrefix(r.Prefix, probe)
			narrower := strings.HasPrefix(probe, r.Prefix)
			if !broader && !narrower {
				continue
			}
			for _, t := range r.Targets {
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
			if narrower {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// leaseKey is one claim. Its String() is deliberately a valid git ref path, so
// the same identifier works as a local map key and as refs/yaver/lease/<key>
// when this grows a cross-machine tier.
type leaseKey struct {
	Class leaseClass
	Name  string
}

func (k leaseKey) String() string { return string(k.Class) + "/" + k.Name }

func sourceLease(area string) leaseKey {
	if area == "" {
		area = "_root"
	}
	return leaseKey{Class: leaseSource, Name: area}
}
func buildLease(target string) leaseKey { return leaseKey{Class: leaseBuild, Name: target} }
func seatLease(runner string) leaseKey  { return leaseKey{Class: leaseSeat, Name: runner} }
func landLease(base string) leaseKey    { return leaseKey{Class: leaseLand, Name: base} }

type leaseHold struct {
	Key      leaseKey  `json:"key"`
	Holder   string    `json:"holder"` // autorun session ID
	Slot     string    `json:"slot"`
	Phase    string    `json:"phase"`
	Acquired time.Time `json:"acquired"`
	Expires  time.Time `json:"expires"`
}

func (h *leaseHold) expired(now time.Time) bool {
	return !h.Expires.IsZero() && now.After(h.Expires)
}

// autorunLeaseTTL bounds how long a claim survives without renewal.
//
// Sized against the loop's own limits: one runner turn is capped at 30 minutes
// (autorunKickTimeout), so a TTL below that would expire a healthy run
// mid-thought. Above it, a crashed agent's claims strand siblings for longer
// than the work could possibly have taken. 45m leaves headroom without leaving
// the fleet stuck for an hour.
const autorunLeaseTTL = 45 * time.Minute

type leaseManager struct {
	mu    sync.Mutex
	holds map[string]*leaseHold
	now   func() time.Time // injectable so TTL behaviour is testable without sleeping
}

func newLeaseManager() *leaseManager {
	return &leaseManager{holds: map[string]*leaseHold{}, now: time.Now}
}

var autorunLeases = newLeaseManager()

// leaseConflict names one claim that blocked an acquisition, including who holds
// it — a refusal that cannot say who is in the way just moves the debugging to a
// second surface.
type leaseConflict struct {
	Key    leaseKey `json:"key"`
	Holder string   `json:"holder"`
	Slot   string   `json:"slot"`
	Phase  string   `json:"phase"`
}

func (c leaseConflict) Error() string {
	return fmt.Sprintf("%s is held by %s (slot %s, phase %s)", c.Key, c.Holder, c.Slot, c.Phase)
}

// Acquire takes every requested key or none of them.
//
// All-or-nothing is the deadlock guard: a caller that could take a subset would
// hold a source area while waiting for a build target another run holds while
// waiting for that area. With no partial holds there is no wait-for cycle, so
// the scheduler needs no deadlock detection at all — it just retries later.
//
// Re-acquiring a key this holder already owns is a renewal, not a conflict:
// phases overlap at their boundary and a run must not lose its own claim by
// asking for it twice.
func (m *leaseManager) Acquire(holder, slot, phase string, keys ...leaseKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()

	for _, k := range keys {
		h, ok := m.holds[k.String()]
		if !ok || h.expired(now) || h.Holder == holder {
			continue
		}
		return leaseConflict{Key: k, Holder: h.Holder, Slot: h.Slot, Phase: h.Phase}
	}
	for _, k := range keys {
		m.holds[k.String()] = &leaseHold{
			Key: k, Holder: holder, Slot: slot, Phase: phase,
			Acquired: now, Expires: now.Add(autorunLeaseTTL),
		}
	}
	return nil
}

// Release drops only the named keys, and only if this holder owns them. This is
// what a phase transition calls: entering `build` releases the seat so a sibling
// task's `edit` can use it, while keeping the source area so nobody edits the
// tree being compiled.
func (m *leaseManager) Release(holder string, keys ...leaseKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		if h, ok := m.holds[k.String()]; ok && h.Holder == holder {
			delete(m.holds, k.String())
		}
	}
}

// ReleaseAll drops everything a holder owns. Called when a run ends — including
// when it ends badly, which is the case that matters: a crashed run that keeps
// its claims blocks every sibling until TTL, and TTL is the backstop, not the
// mechanism.
func (m *leaseManager) ReleaseAll(holder string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, h := range m.holds {
		if h.Holder == holder {
			delete(m.holds, key)
		}
	}
}

// Renew extends this holder's claims. A long build must not lose its build
// target to TTL while it is legitimately still compiling.
func (m *leaseManager) Renew(holder string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	n := 0
	for _, h := range m.holds {
		if h.Holder == holder && !h.expired(now) {
			h.Expires = now.Add(autorunLeaseTTL)
			n++
		}
	}
	return n
}

// Snapshot is the fleet-visible view: live holds only, sorted, with expired ones
// reaped on read so a dead holder never appears to still own anything.
func (m *leaseManager) Snapshot() []leaseHold {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	out := []leaseHold{}
	for key, h := range m.holds {
		if h.expired(now) {
			delete(m.holds, key)
			continue
		}
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

// autorunPhaseLeases is the contract between a phase and what it may hold.
//
// This table IS the overlap policy, so it is written in one place rather than
// spread across the loop:
//
//   - orient — seat only. Reading needs no exclusivity.
//   - edit   — seat + source areas. Nobody else writes here.
//   - build  — source areas + build targets, and NOT the seat. Keeping the areas
//     is what stops a sibling editing the tree being compiled; dropping the seat
//     is what lets a sibling think while we compile.
//   - land   — the landing lease only.
func autorunPhaseLeases(phase, runner string, areas, buildTargets []string, base string) []leaseKey {
	var keys []leaseKey
	switch phase {
	case "orient":
		keys = append(keys, seatLease(runner))
	case "edit":
		keys = append(keys, seatLease(runner))
		for _, a := range areas {
			keys = append(keys, sourceLease(a))
		}
	case "build":
		for _, a := range areas {
			keys = append(keys, sourceLease(a))
		}
		for _, t := range buildTargets {
			keys = append(keys, buildLease(t))
		}
	case "land":
		if base == "" {
			base = "main"
		}
		keys = append(keys, landLease(base))
	}
	return keys
}
