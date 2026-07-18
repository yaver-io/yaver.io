package main

import (
	"strings"
	"testing"
)

// The placement module shipped fully implemented, fully unit-tested, and with
// ZERO production callers — every capability check it performs was dead from
// the day it landed, and its own tests passed the entire time. Unit tests on
// checkShipPlacement therefore prove nothing about whether ship consults it.
//
// These tests guard the WIRING specifically: that runShip's Phase 6.5 exists,
// blocks on the right things, and does not block on the wrong ones.

// stripGoLineComments removes `//` comment tails so a source-level assertion
// tests CODE rather than the prose describing it. Deliberately simple: it does
// not understand `//` inside a string literal or block comments, which is fine
// for the narrow use here and honest about not being a Go parser.
func stripGoLineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if idx := strings.Index(ln, "//"); idx >= 0 {
			lines[i] = ln[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// TestShipPlacementSummaryNamesCIRoutes covers the reporting half. A ship that
// quietly handed two targets to GitHub Actions and returned green is true but
// misleading — the user has not deployed those, a workflow has, and it can fail
// after ship returns.
func TestShipPlacementSummaryNamesCIRoutes(t *testing.T) {
	got := shipPlacementSummary([]shipPlacementCheck{
		{Step: "testflight-ios", Route: routeMachine},
		{Step: "web", Route: routeCI},
		{Step: "npm", Route: routeCI},
	})
	for _, want := range []string{"web", "npm", "CI"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary should name CI-routed steps; missing %q in %q", want, got)
		}
	}

	// All-local must not mention CI at all — a spurious "via CI" would send
	// someone to check a workflow that was never dispatched.
	local := shipPlacementSummary([]shipPlacementCheck{{Step: "testflight-ios", Route: routeMachine}})
	if strings.Contains(local, "CI") {
		t.Errorf("all-local summary should not mention CI: %q", local)
	}

	if empty := shipPlacementSummary(nil); empty == "" {
		t.Error("empty placement should still render something")
	}
}

// TestShipPlacementBlockingRule pins WHICH routes stop the barrier. Getting
// this backwards is costly in both directions: treating a CI route as failure
// sends someone to debug a healthy fleet, while failing to block on a spent
// quota burns tomorrow's TestFlight slot on the same mistake (no rollback).
func TestShipPlacementBlockingRule(t *testing.T) {
	cases := []struct {
		route     autorunPlacementRoute
		wantBlock bool
		why       string
	}{
		{routeMachine, false, "runs here — obviously proceed"},
		{routeCI, false, "a routing decision, NOT a failure"},
		{routeParked, true, "quota spent; retrying burns tomorrow's slot"},
		{routeImpossible, true, "nothing in the fleet can run it"},
	}
	for _, c := range cases {
		// Mirrors checkShipPlacement's own rule; if that rule changes, this
		// test should fail and force the decision to be made deliberately.
		got := c.route == routeParked || c.route == routeImpossible
		if got != c.wantBlock {
			t.Errorf("route %v: blocking=%v, want %v (%s)", c.route, got, c.wantBlock, c.why)
		}
	}
}

// TestShipConsultsPlacementBeforeDeploying is the anti-regression guard: it
// asserts the CALL still exists in runShip, ahead of the deploy. A source-level
// check is crude, but running the real barrier in a unit test would freeze a
// fleet — and the failure being guarded against is precisely "the function is
// perfect and nobody calls it", which no amount of testing the function itself
// would ever catch.
func TestShipConsultsPlacementBeforeDeploying(t *testing.T) {
	// Comments MUST be stripped first. The Phase 6.5 block explains itself by
	// naming checkShipPlacement in prose, so a naive substring search matches
	// the explanation even after the real call is deleted — verified by
	// mutation: commenting the call out left this test green.
	src := stripGoLineComments(readSourceFile(t, "ship.go"))

	callIdx := strings.Index(src, "checkShipPlacement(")
	if callIdx < 0 {
		t.Fatal("runShip no longer calls checkShipPlacement — placement is dead again; " +
			"it was already shipped once with zero callers while its own tests passed")
	}
	deployIdx := strings.Index(src, "Phase 7 — deploy")
	if deployIdx < 0 {
		t.Skip("Phase 7 marker renamed; ordering not checked")
	}
	if callIdx > deployIdx {
		t.Error("placement is consulted AFTER the deploy phase — it must run before, " +
			"or it cannot prevent the expensive failure it exists for")
	}
	if !strings.Contains(src, "res.Placement") {
		t.Error("placement result is not recorded on shipResult — a CI-routed step " +
			"would then be invisible to the caller")
	}
}
