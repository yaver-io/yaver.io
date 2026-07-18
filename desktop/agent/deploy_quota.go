package main

// deploy_quota.go — the missing half of placement.
//
// checkShipPlacement has always taken a `quotaExhausted` map and nothing has
// ever produced one, so it was called with nil: the capability half worked
// ("can this machine build iOS?") while the quota half silently answered "not
// exhausted" for every target, forever.
//
// That gap is not academic. TestFlight allows roughly 15-20 uploads per app per
// day and has NO ROLLBACK — a bad build can only be superseded by another
// upload. So the failure mode is asymmetric in the worst direction: a ship that
// discovers the quota is spent has already frozen the fleet, drained every
// autorun, pinned a SHA, and burned 20 minutes of archive time. And the natural
// reflex — retry — spends TOMORROW's slot on the same mistake.
//
// Placement's routeParked exists precisely for this: park, tell the user when
// it resets, and do not touch the barrier. This file finally lets it fire.
//
// Counting is deliberately conservative in one direction: an upload that FAILED
// still counts. Apple counts what you sent, not what succeeded, so treating a
// failed upload as a free slot would overshoot the cap exactly when a user is
// retrying a broken build — the worst moment to be wrong.

import (
	"strconv"
	"strings"
	"time"
)

// deployQuotaLimits maps a placement target to its daily ceiling.
//
// Only targets with a REAL, externally-enforced daily cap belong here. Adding a
// target with an invented number would park a ship for no reason, which is
// worse than not checking at all — the whole point is to avoid burning a real
// resource, not to invent a fake one.
var deployQuotaLimits = map[string]int{
	// Apple's documented soft cap is ~15-20/app/day; 15 is the conservative
	// end. Being early by a couple of slots costs a deferred ship; being late
	// costs a day.
	"ios": 15,
}

// deployQuotaWindow is how far back an upload still counts against the cap.
// Apple's window is a rolling day, not a calendar day, so this is 24h from now
// rather than "since midnight" — the latter would hand out a fresh allowance at
// local midnight that Apple does not honour.
const deployQuotaWindow = 24 * time.Hour

// deployQuotaTargetFor maps a deploy-history Target onto a placement target.
// History records the STEP that ran ("testflight-ios"); placement speaks in
// capabilities ("ios"). Returns "" when a step consumes no metered quota.
func deployQuotaTargetFor(historyTarget string) string {
	t := strings.ToLower(strings.TrimSpace(historyTarget))
	switch {
	case strings.Contains(t, "testflight"), strings.Contains(t, "ios"), strings.Contains(t, "appstore"):
		return "ios"
	}
	return ""
}

// deployQuotaUsage counts uploads inside the window, per placement target.
func deployQuotaUsage(h *DeployHistory, now time.Time) map[string]int {
	used := map[string]int{}
	if h == nil {
		return used
	}
	cutoff := now.Add(-deployQuotaWindow).UnixMilli()
	// A generous window: enough history to cover a day of uploads without
	// walking the whole file.
	for _, run := range h.List(500, "") {
		if run.StartedAt < cutoff {
			continue
		}
		target := deployQuotaTargetFor(run.Target)
		if target == "" {
			continue
		}
		// Counted whether or not it succeeded — see the file header.
		used[target]++
	}
	return used
}

// deployQuotaExhausted is what checkShipPlacement wants: target -> spent?
func deployQuotaExhausted(h *DeployHistory, now time.Time) map[string]bool {
	usage := deployQuotaUsage(h, now)
	out := map[string]bool{}
	for target, limit := range deployQuotaLimits {
		out[target] = usage[target] >= limit
	}
	return out
}

// deployQuotaSummary is a human line for the ship phase log: what is spent and
// what remains. Surfaced even when nothing is exhausted, because "14 of 15
// used" is the warning worth seeing BEFORE the barrier, not after.
func deployQuotaSummary(h *DeployHistory, now time.Time) string {
	usage := deployQuotaUsage(h, now)
	var parts []string
	for target, limit := range deployQuotaLimits {
		used := usage[target]
		if used == 0 {
			continue
		}
		parts = append(parts, targetQuotaPhrase(target, used, limit))
	}
	if len(parts) == 0 {
		return "no metered deploys in the last 24h"
	}
	return strings.Join(parts, "; ")
}

func targetQuotaPhrase(target string, used, limit int) string {
	remaining := limit - used
	if remaining <= 0 {
		return target + ": daily upload quota spent — a retry would consume tomorrow's allowance"
	}
	return target + ": " + strconv.Itoa(used) + "/" + strconv.Itoa(limit) + " uploads used in the last 24h, " + strconv.Itoa(remaining) + " left"
}
