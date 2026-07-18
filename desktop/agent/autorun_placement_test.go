package main

import (
	"strings"
	"testing"
)

// The fleet as it actually was on 2026-07-18: one Mac that can do everything
// Apple, several Linux boxes that cannot, and one offline box.
func testFleet() []MachineInfo {
	return []MachineInfo{
		{DeviceID: "mac", Name: "Mac", IsOnline: true, IsLocal: true, Capabilities: &MachineCapabilities{
			SupportsIOS: true, SupportsAndroid: true, SupportsTestFlight: true, SupportsPlayStore: true,
		}},
		{DeviceID: "linux-1", Name: "hetzner", IsOnline: true, Capabilities: &MachineCapabilities{
			SupportsAndroid: true,
		}},
		{DeviceID: "linux-2", Name: "magara", IsOnline: true, Capabilities: &MachineCapabilities{}},
		{DeviceID: "off", Name: "Ofis2", IsOnline: false, Capabilities: &MachineCapabilities{SupportsIOS: true, SupportsTestFlight: true}},
	}
}

// An iOS build belongs on the Mac and nowhere else — and the refusal has to say
// WHY, because "why did this go to CI?" is the question people actually ask.
func TestIOSBuildOnlyOnTheMac(t *testing.T) {
	p := placeAutorunTarget("ios", placementBuild, testFleet(), false)
	if p.Route != routeMachine {
		t.Fatalf("route = %s, want machine (%s)", p.Route, p.Reason)
	}
	if len(p.Eligible) != 1 || p.Eligible[0] != "mac" {
		t.Fatalf("eligible = %q, want [mac]", p.Eligible)
	}
	if got := p.Rejected["linux-2"]; got == "" {
		t.Fatal("a rejected machine must carry a reason")
	}
	if got := p.Rejected["off"]; got != "offline" {
		t.Fatalf("offline machine rejected as %q, want offline", got)
	}
}

// Every box can BUILD web; none can DEPLOY it, because the token is CI-only.
// That asymmetry is the whole point of separating build from deploy.
func TestWebBuildsAnywhereButDeploysOnlyViaCI(t *testing.T) {
	build := placeAutorunTarget("web", placementBuild, testFleet(), false)
	if build.Route != routeMachine || len(build.Eligible) != 3 {
		t.Fatalf("web build: route=%s eligible=%q — every online box can build web", build.Route, build.Eligible)
	}

	deploy := placeAutorunTarget("web", placementDeploy, testFleet(), false)
	if deploy.Route != routeCI {
		t.Fatalf("web deploy route = %s, want ci (%s)", deploy.Route, deploy.Reason)
	}
	if deploy.CIWorkflow != "release-web.yml" {
		t.Fatalf("CIWorkflow = %q", deploy.CIWorkflow)
	}
	// Routing is not failure. If this reads as an error someone goes and debugs
	// a healthy fleet.
	if !strings.Contains(deploy.Reason, "not a failure") {
		t.Fatalf("a CI-only route must not read as a failure: %q", deploy.Reason)
	}
}

// The npm CLI is the same shape: buildable anywhere, publishable only by the
// workflow that produces the signed 5-platform matrix.
func TestAgentPublishesOnlyViaCI(t *testing.T) {
	p := placeAutorunTarget("agent", placementDeploy, testFleet(), false)
	if p.Route != routeCI || p.CIWorkflow != "release-cli.yml" {
		t.Fatalf("route=%s workflow=%q, want ci/release-cli.yml", p.Route, p.CIWorkflow)
	}
}

// TestFlight has NO CI fallback on purpose — GitHub runners lack the registered
// UDIDs. If the Mac cannot do it, nothing can, and pretending otherwise sends
// the work to a runner that will fail at signing.
func TestTestFlightHasNoCIFallback(t *testing.T) {
	noMac := []MachineInfo{{DeviceID: "linux-1", IsOnline: true, Capabilities: &MachineCapabilities{}}}
	p := placeAutorunTarget("ios", placementDeploy, noMac, false)
	if p.Route != routeImpossible {
		t.Fatalf("route = %s, want impossible — CI cannot substitute for TestFlight (%s)", p.Route, p.Reason)
	}
	if p.CIWorkflow != "" {
		t.Fatalf("CIWorkflow = %q, want empty", p.CIWorkflow)
	}
}

// Exhausted quota PARKS. It must not fall through to CI or retry: TestFlight
// has no rollback, so a retry spends tomorrow's slot on the same mistake.
func TestExhaustedQuotaParksRatherThanRetries(t *testing.T) {
	p := placeAutorunTarget("ios", placementDeploy, testFleet(), true)
	if p.Route != routeParked {
		t.Fatalf("route = %s, want parked (%s)", p.Route, p.Reason)
	}
	if len(p.Eligible) != 0 {
		t.Fatal("a parked target must not report eligible machines — the Mac can do it, but it must not")
	}
}

// Android can fall back to CI, unlike iOS. The two mobile halves genuinely differ.
func TestAndroidFallsBackToCI(t *testing.T) {
	noCreds := []MachineInfo{{DeviceID: "linux-1", IsOnline: true, Capabilities: &MachineCapabilities{SupportsAndroid: true}}}
	p := placeAutorunTarget("android", placementDeploy, noCreds, false)
	if p.Route != routeCI || p.CIWorkflow != "release-mobile.yml" {
		t.Fatalf("route=%s workflow=%q, want ci/release-mobile.yml (%s)", p.Route, p.CIWorkflow, p.Reason)
	}
}

// A machine whose capabilities were never probed must be rejected, not assumed
// capable — guessing here means dispatching an Xcode build to a Raspberry Pi.
func TestUnknownCapabilitiesAreNotAssumed(t *testing.T) {
	unknown := []MachineInfo{{DeviceID: "mystery", IsOnline: true}}
	p := placeAutorunTarget("ios", placementBuild, unknown, false)
	if p.Route == routeMachine {
		t.Fatal("a machine with unprobed capabilities must not be treated as iOS-capable")
	}
	if p.Rejected["mystery"] != "capabilities unknown" {
		t.Fatalf("reject reason = %q", p.Rejected["mystery"])
	}
}

// An unknown target must fail loudly rather than silently picking a machine.
func TestUnknownTargetIsImpossibleNotSilent(t *testing.T) {
	p := placeAutorunTarget("quantum", placementBuild, testFleet(), false)
	if p.Route != routeImpossible {
		t.Fatalf("route = %s, want impossible", p.Route)
	}
}

// A whole run's worth of targets, placed together: mobile work implies BOTH
// mobile SDKs, and they route differently.
func TestPlanPlacesEveryTargetARunImplies(t *testing.T) {
	plan := autorunPlacementPlan([]string{"mobile"}, placementDeploy, testFleet(), nil)
	if len(plan) != 2 {
		t.Fatalf("mobile implies ios+android, got %d placements", len(plan))
	}
	seen := map[string]autorunPlacementRoute{}
	for _, p := range plan {
		seen[p.Target] = p.Route
	}
	if seen["ios"] != routeMachine {
		t.Fatalf("ios route = %s, want machine (the Mac has ASC creds)", seen["ios"])
	}
	if seen["android"] != routeMachine {
		t.Fatalf("android route = %s, want machine (the Mac has Play creds)", seen["android"])
	}
}

// Docs-only work implies no targets at all, so it is the easiest thing in the
// system to schedule — it must never be blocked by a compiler.
func TestDocsOnlyRunNeedsNoPlacement(t *testing.T) {
	if plan := autorunPlacementPlan([]string{"docs"}, placementBuild, testFleet(), nil); len(plan) != 0 {
		t.Fatalf("docs-only run implied %d build targets, want 0", len(plan))
	}
}

// ship names the STEP it runs ("testflight-ios"); placement names the
// CAPABILITY needed ("ios"). The audit found the two vocabularies had drifted
// with nothing bridging them, so ship spent a whole barrier — freeze, drain,
// pin — before discovering a step could not run.
func TestShipPlacementBridgesBothVocabularies(t *testing.T) {
	checks := checkShipPlacement(
		[]string{"convex", "web-cloudflare", "cli-npm", "testflight-ios"},
		testFleet(), nil,
	)
	byStep := map[string]shipPlacementCheck{}
	for _, c := range checks {
		byStep[c.Step] = c
	}

	// Local credential: runs on a machine.
	if byStep["convex"].Route != routeMachine {
		t.Errorf("convex route = %s, want machine", byStep["convex"].Route)
	}
	// CI-only credentials: routed, NOT failed.
	for _, step := range []string{"web-cloudflare", "cli-npm"} {
		c := byStep[step]
		if c.Route != routeCI {
			t.Errorf("%s route = %s, want ci", step, c.Route)
		}
		if c.Blocking {
			t.Errorf("%s is marked blocking — a CI route is a routing decision, not a failure", step)
		}
		if c.CIWorkflow == "" {
			t.Errorf("%s routed to CI without naming the workflow", step)
		}
	}
	// The Mac has ASC credentials, so TestFlight can run here.
	if byStep["testflight-ios"].Route != routeMachine {
		t.Errorf("testflight route = %s, want machine", byStep["testflight-ios"].Route)
	}
}

// The expensive discovery this avoids: freeze the fleet, drain it, pin a SHA,
// then die at the upload because the day's quota was already spent.
func TestShipPlacementBlocksOnExhaustedQuota(t *testing.T) {
	checks := checkShipPlacement([]string{"testflight-ios"}, testFleet(), map[string]bool{"ios": true})
	if len(checks) != 1 {
		t.Fatalf("got %d checks", len(checks))
	}
	if checks[0].Route != routeParked {
		t.Fatalf("route = %s, want parked", checks[0].Route)
	}
	if !checks[0].Blocking {
		t.Fatal("an exhausted quota must block the ship — TestFlight has no rollback, so a retry spends tomorrow's slot")
	}
}

// An unmapped step must be visible, never silently allowed: silence would let a
// new deploy step bypass capability and quota checks entirely.
func TestShipPlacementReportsAnUnmappedStep(t *testing.T) {
	checks := checkShipPlacement([]string{"some-new-target"}, testFleet(), nil)
	if len(checks) != 1 || checks[0].Target != "" {
		t.Fatalf("unmapped step should carry no target: %+v", checks)
	}
	if !strings.Contains(checks[0].Reason, "NOT checked") {
		t.Fatalf("an unmapped step must say its checks were skipped, got %q", checks[0].Reason)
	}
}

// iOS with no Mac cannot fall back to CI — release-mobile.yml is `if: false`
// because GitHub runners lack the registered device UDIDs.
func TestShipPlacementBlocksTestFlightWithNoMac(t *testing.T) {
	noMac := []MachineInfo{{DeviceID: "linux", IsOnline: true, Capabilities: &MachineCapabilities{}}}
	checks := checkShipPlacement([]string{"testflight-ios"}, noMac, nil)
	if !checks[0].Blocking || checks[0].Route != routeImpossible {
		t.Fatalf("route=%s blocking=%v — nothing can run TestFlight without a Mac", checks[0].Route, checks[0].Blocking)
	}
}
