package main

// autorun_coordination.go — decide whether a starting run is INDEPENDENT of the
// runs already going, or DEPENDENT on one of them.
//
// Why this exists, measured rather than assumed. On 2026-07-17 the mac mini ran
// ~19 autoruns overnight and 4 kept any code. Six died on `scope violation` at
// iteration 1 with `Verified commits kept: 0` — the runner did real work, edited
// a file its `--scope` did not list, and had the whole iteration stashed and
// thrown away. Several others lost push races to a sibling touching the same
// subsystem. See docs/architecture/AUTORUN_COLLECTIVE_SYNC_AUDIT.md.
//
// The root cause is that `scopes` is an allowlist checked AFTER the runner has
// already spent a turn (autorun_cmd.go, scope validation follows the kick). It
// answers "were those edits allowed?" — it never answers "may I start?". Two
// runs aimed at the same subsystem both burn tokens, both compile against each
// other's half-finished tree, and only find out at scope failure, gate failure,
// push rejection, or landing conflict.
//
// So admission happens here, before a goroutine is spawned:
//
//   - INDEPENDENT — owned areas are disjoint. Runs in parallel, which is the
//     whole point of a fleet.
//   - DEPENDENT — areas overlap, or the slot is already live. Refused with a
//     reason naming the holder, instead of started and failed later.
//
// Deliberately conservative: when it cannot prove two runs are disjoint it calls
// them dependent. A wrongly-serialized run costs latency; a wrongly-parallel one
// costs a night of tokens and a stashed iteration, which is what actually
// happened.
//
// This is the local, in-process tier. It is intentionally derived from the live
// session map rather than a second registry, so it cannot drift from reality or
// leak a claim when a run dies. The cross-machine tier belongs on git refs
// (update-ref CAS), not here — see the audit's Part 5.

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// autorunAdmission is the answer to "may this run start now?".
type autorunAdmission struct {
	// Allowed is false when a live run already owns something this one needs.
	Allowed bool
	// Reason is machine-readable: "" when allowed, else "slot_busy" or
	// "area_owned". Callers branch on this, never on Detail.
	Reason string
	// Detail is the human sentence, naming the holder and the contended area so
	// the answer is actionable without opening another surface.
	Detail string
	// HolderID / HolderSlot identify the run standing in the way, so a caller can
	// poll or stop it without a second lookup.
	HolderID   string
	HolderSlot string
}

// autorunOwnedAreas normalizes a run's declared scopes into comparable path
// prefixes.
//
// A scope is a glob (`desktop/agent/**`, `web/lib/*.ts`, `tasks/foo.md`). For
// overlap we only need the fixed leading directory portion: everything a glob
// can match lives under its own first wildcard. `desktop/agent/**` and
// `desktop/agent/autorun*.go` both reduce to `desktop/agent`, which is exactly
// the answer we want — they collide.
//
// An empty scope set means "unrestricted", and is represented as the root
// prefix "" so it overlaps everything. That is the correct reading: a run that
// may touch anything is dependent on every other run by definition.
func autorunOwnedAreas(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{""} // unrestricted — overlaps all
	}
	seen := map[string]bool{}
	areas := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		s = path.Clean(strings.TrimPrefix(strings.ReplaceAll(s, "\\", "/"), "./"))
		// Cut at the first wildcard: the fixed prefix is what bounds the match.
		if i := strings.IndexAny(s, "*?["); i >= 0 {
			s = s[:i]
		}
		// Back off to the last complete path segment, so `web/comp*` bounds at
		// `web` rather than inventing a `web/comp` directory that matches
		// nothing and would make two overlapping scopes look disjoint.
		if !strings.HasSuffix(s, "/") {
			if i := strings.LastIndex(s, "/"); i >= 0 {
				// Keep a fully-literal scope (a real file path) intact; only
				// truncate when we actually cut a wildcard off it.
				if strings.ContainsAny(raw, "*?[") {
					s = s[:i]
				}
			} else if strings.ContainsAny(raw, "*?[") {
				s = ""
			}
		}
		s = strings.Trim(s, "/")
		if s == "." {
			s = ""
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		areas = append(areas, s)
		if s == "" {
			// Root swallows everything else; no point carrying siblings.
			return []string{""}
		}
	}
	if len(areas) == 0 {
		return []string{""}
	}
	sort.Strings(areas)

	// Drop areas already covered by a broader sibling: `["web", "web/lib"]` is
	// just `["web"]`. Overlap answers the same either way, but this set becomes
	// a lease key and appears in status, and a redundant child there reads as a
	// second claim that does not exist. Sorting puts a parent immediately before
	// everything it contains, so one pass suffices.
	minimal := areas[:1]
	for _, a := range areas[1:] {
		if autorunAreaContains(minimal[len(minimal)-1], a) {
			continue
		}
		minimal = append(minimal, a)
	}
	return minimal
}

// autorunAreasOverlap reports whether two owned-area sets can touch the same
// path. True when either set contains a prefix of a member of the other —
// `desktop/agent` vs `desktop/agent/autorun` overlap; `web` vs `mobile` do not.
func autorunAreasOverlap(a, b []string) (string, bool) {
	for _, x := range a {
		for _, y := range b {
			if autorunAreaContains(x, y) || autorunAreaContains(y, x) {
				contended := x
				if len(y) > len(x) {
					contended = y
				}
				if contended == "" {
					contended = "the whole repository"
				}
				return contended, true
			}
		}
	}
	return "", false
}

// autorunAreaContains reports whether outer contains inner, on path-segment
// boundaries. "" contains everything. `web` contains `web/lib` but NOT `website`
// — the segment check is what keeps two unrelated top-level dirs apart.
func autorunAreaContains(outer, inner string) bool {
	if outer == "" {
		return true
	}
	if outer == inner {
		return true
	}
	return strings.HasPrefix(inner, outer+"/")
}

// admit decides whether a run may start, given everything currently live.
//
// Callers must hold at least a read lock on the manager. It is split out from
// start() so it can be tested against a constructed session map without
// spawning goroutines or touching git.
func (m *autorunSessionManager) admitLocked(slot string, areas []string) autorunAdmission {
	for _, s := range m.sessions {
		if !autorunSessionIsLive(s) {
			continue
		}
		// Same slot means the same worktree path, branch, tmux session AND
		// $TMPDIR prompt file (all derived from task basename + seat). A second
		// run here does not race — it adopts or deletes the first one's tree.
		if s.Slot == slot {
			return autorunAdmission{
				Reason:     "slot_busy",
				Detail:     fmt.Sprintf("slot %s is already running as %s — it owns that worktree, branch and tmux session", slot, s.ID),
				HolderID:   s.ID,
				HolderSlot: s.Slot,
			}
		}
		if contended, overlaps := autorunAreasOverlap(areas, autorunOwnedAreas(s.Scopes)); overlaps {
			return autorunAdmission{
				Reason:     "area_owned",
				Detail:     fmt.Sprintf("%s (slot %s) is already working in %s — starting here would edit the same tree and one of the two iterations would be stashed and lost", s.ID, s.Slot, contended),
				HolderID:   s.ID,
				HolderSlot: s.Slot,
			}
		}
	}
	return autorunAdmission{Allowed: true}
}

// autorunAdmissionError is returned when a run is refused because something
// live already owns what it needs.
//
// A distinct type, not a plain error string, because "not now" is NOT a
// failure: nothing is broken, no work was lost, and the right response is to
// wait or pick another area — whereas a failed start means the run cannot
// happen at all. A caller that cannot tell those apart will either retry
// forever on a real error or give up on a temporary one. errors.As gives
// surfaces the machine-readable Reason without parsing prose.
type autorunAdmissionError struct {
	admission autorunAdmission
}

func (e *autorunAdmissionError) Error() string { return e.admission.Detail }

// Admission exposes the structured decision to callers holding the error.
func (e *autorunAdmissionError) Admission() autorunAdmission { return e.admission }

// autorunSessionIsLive reports whether a session still holds its resources. Only
// a running session owns a worktree and an area; a finished one is history that
// happens to still be in the map (sessions are in-memory and never evicted).
func autorunSessionIsLive(s *autorunSession) bool {
	if s == nil {
		return false
	}
	return s.Status == "running" && s.FinishedAt.IsZero()
}
