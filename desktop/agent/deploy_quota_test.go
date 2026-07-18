package main

import (
	"strings"
	"testing"
	"time"
)

// These protect a resource that cannot be refunded. TestFlight allows ~15-20
// uploads/app/day and has NO rollback — a bad build can only be superseded by
// spending another slot. Getting the count wrong in the permissive direction
// burns tomorrow's allowance; getting it wrong in the strict direction only
// defers a ship. The asymmetry is why the rules below lean conservative.

func quotaHistory(t *testing.T, runs []DeployRun) *DeployHistory {
	t.Helper()
	h := NewDeployHistory(1000)
	for i := range runs {
		r := runs[i] // copy: h.runs holds pointers, so a loop-var address would alias
		h.runs = append(h.runs, &r)
	}
	return h
}

func iosRun(target string, at time.Time, ok bool) DeployRun {
	return DeployRun{Target: target, StartedAt: at.UnixMilli(), OK: ok}
}

// A FAILED upload still consumes an Apple slot. Treating it as free would
// overshoot the cap exactly when someone is retrying a broken build — the worst
// possible moment to be wrong.
func TestDeployQuotaCountsFailedUploadsToo(t *testing.T) {
	now := time.Now()
	h := quotaHistory(t, []DeployRun{
		iosRun("testflight-ios", now.Add(-1*time.Hour), true),
		iosRun("testflight-ios", now.Add(-2*time.Hour), false), // failed — still counts
		iosRun("testflight-ios", now.Add(-3*time.Hour), false),
	})
	if got := deployQuotaUsage(h, now)["ios"]; got != 3 {
		t.Fatalf("ios usage = %d, want 3 — a failed upload still spends an Apple slot", got)
	}
}

// The window is rolling, not calendar. A "since midnight" reset would hand out
// a fresh allowance Apple does not honour.
func TestDeployQuotaWindowIsRolling(t *testing.T) {
	now := time.Now()
	h := quotaHistory(t, []DeployRun{
		iosRun("testflight-ios", now.Add(-23*time.Hour), true),        // inside
		iosRun("testflight-ios", now.Add(-25*time.Hour), true),        // outside
		iosRun("testflight-ios", now.Add(-deployQuotaWindow*2), true), // well outside
	})
	if got := deployQuotaUsage(h, now)["ios"]; got != 1 {
		t.Fatalf("ios usage = %d, want 1 — only uploads inside the rolling 24h window count", got)
	}
}

// Exhaustion must fire AT the limit, not past it.
func TestDeployQuotaExhaustsAtTheLimit(t *testing.T) {
	now := time.Now()
	limit := deployQuotaLimits["ios"]

	var justUnder []DeployRun
	for i := 0; i < limit-1; i++ {
		justUnder = append(justUnder, iosRun("testflight-ios", now.Add(-time.Duration(i)*time.Minute), true))
	}
	if deployQuotaExhausted(quotaHistory(t, justUnder), now)["ios"] {
		t.Errorf("%d uploads should NOT exhaust a limit of %d — parking early wastes a usable slot",
			limit-1, limit)
	}

	atLimit := append(justUnder, iosRun("testflight-ios", now, true))
	if !deployQuotaExhausted(quotaHistory(t, atLimit), now)["ios"] {
		t.Errorf("%d uploads MUST exhaust a limit of %d — one more spends tomorrow's allowance",
			limit, limit)
	}
}

// Step names ("testflight-ios") must map to capability targets ("ios"), and
// unmetered steps must map to nothing. A step wrongly mapped to "ios" would
// park a ship that consumes no Apple quota at all.
func TestDeployQuotaTargetMapping(t *testing.T) {
	for _, in := range []string{"testflight-ios", "TestFlight-iOS", "ios", "appstore"} {
		if got := deployQuotaTargetFor(in); got != "ios" {
			t.Errorf("deployQuotaTargetFor(%q) = %q, want ios", in, got)
		}
	}
	for _, in := range []string{"web", "npm", "convex", "playstore", "android", ""} {
		if got := deployQuotaTargetFor(in); got != "" {
			t.Errorf("deployQuotaTargetFor(%q) = %q, want \"\" — only metered targets may park a ship", in, got)
		}
	}
}

// Only targets with a REAL externally-enforced cap may appear. An invented
// limit would park ships for no reason.
func TestDeployQuotaLimitsOnlyCoverMeteredTargets(t *testing.T) {
	for target, limit := range deployQuotaLimits {
		if limit <= 0 {
			t.Errorf("%s has a non-positive limit (%d) — that would park every ship", target, limit)
		}
	}
	if _, ok := deployQuotaLimits["web"]; ok {
		t.Error("web deploys are not daily-capped by anyone; a limit here would park ships for no reason")
	}
	if _, ok := deployQuotaLimits["android"]; ok {
		t.Error("Play has no comparable daily upload cap; do not invent one")
	}
}

// The summary must warn BEFORE the wall, not only at it.
func TestDeployQuotaSummaryWarnsBeforeExhaustion(t *testing.T) {
	now := time.Now()
	limit := deployQuotaLimits["ios"]
	var runs []DeployRun
	for i := 0; i < limit-1; i++ {
		runs = append(runs, iosRun("testflight-ios", now.Add(-time.Duration(i)*time.Minute), true))
	}
	s := deployQuotaSummary(quotaHistory(t, runs), now)
	if !strings.Contains(s, "1 left") {
		t.Errorf("summary should say how many slots remain, got %q", s)
	}

	runs = append(runs, iosRun("testflight-ios", now, true))
	s = deployQuotaSummary(quotaHistory(t, runs), now)
	if !strings.Contains(strings.ToLower(s), "spent") {
		t.Errorf("an exhausted quota should say so plainly, got %q", s)
	}

	if empty := deployQuotaSummary(quotaHistory(t, nil), now); empty == "" {
		t.Error("summary must render something even with no deploys")
	}
}

// A nil history must not panic — ship runs on boxes that have never deployed.
func TestDeployQuotaHandlesNilHistory(t *testing.T) {
	if got := deployQuotaUsage(nil, time.Now()); len(got) != 0 {
		t.Errorf("nil history should report no usage, got %v", got)
	}
	if deployQuotaExhausted(nil, time.Now())["ios"] {
		t.Error("nil history must never report a quota as exhausted — that would park every ship on a fresh box")
	}
}
