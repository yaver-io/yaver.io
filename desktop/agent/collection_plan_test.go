package main

import (
	"context"
	"testing"
)

func withPlannerMachines(t *testing.T, machines []MachineInfo) {
	t.Helper()
	old := listCollectionPlannerMachines
	listCollectionPlannerMachines = func(context.Context) []MachineInfo {
		return machines
	}
	t.Cleanup(func() { listCollectionPlannerMachines = old })
}

func testMachine(id, provider, geo string, local bool) MachineInfo {
	return MachineInfo{
		DeviceID:  id,
		Name:      id,
		IsOnline:  true,
		IsLocal:   local,
		Provider:  provider,
		GeoRegion: geo,
		Capabilities: &MachineCapabilities{
			SupportsGhostWeb: true,
			SupportsAndroid:  true,
		},
	}
}

func TestCollectionPlanBlocksPolicyViolation(t *testing.T) {
	withPlannerMachines(t, []MachineInfo{testMachine("local", "local", "eu", true)})

	res := invokeVerb(t, "collection_plan", map[string]interface{}{
		"source":       "betfair.com",
		"action":       "bet",
		"jurisdiction": "TR",
	})
	if res.OK {
		t.Fatalf("policy-blocked plan must return OK=false")
	}
	m := mustOKMapFromInitial(t, res.Initial, "plan")
	plan := m["plan"].(CollectionPlan)
	if plan.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", plan.Status)
	}
	if plan.Policy.Decision != "block" {
		t.Fatalf("policy decision = %q, want block", plan.Policy.Decision)
	}
}

func TestCollectionPlanSelectsPreferredRegionRuntime(t *testing.T) {
	withPlannerMachines(t, []MachineInfo{
		testMachine("local", "local", "eu", true),
		testMachine("us-box", "hetzner", "us", false),
	})

	res := invokeVerb(t, "collection_plan", map[string]interface{}{
		"source":          "status.example.com",
		"action":          "data",
		"preferredRegion": "us",
		"needsDurable":    true,
		"needsBrowser":    true,
	})
	if !res.OK {
		t.Fatalf("plan failed: %s", res.Error)
	}
	m := mustOKMapFromInitial(t, res.Initial, "plan")
	plan := m["plan"].(CollectionPlan)
	if plan.Machine == nil || plan.Machine.DeviceID != "us-box" {
		t.Fatalf("machine = %+v, want us-box", plan.Machine)
	}
	if plan.Runtime != "self_hosted_vps" {
		t.Fatalf("runtime = %q, want self_hosted_vps", plan.Runtime)
	}
	if plan.EgressPolicy != "machine_native" {
		t.Fatalf("egressPolicy = %q, want machine_native", plan.EgressPolicy)
	}
}

func TestCollectionPlanApplyRegistersSourceAndVantage(t *testing.T) {
	resetCollectionStoreForTest("")
	withPlannerMachines(t, []MachineInfo{testMachine("local", "local", "eu", true)})

	res := invokeVerb(t, "collection_plan_apply", map[string]interface{}{
		"source":          "shop.example.com",
		"action":          "data",
		"preferredRegion": "eu",
		"needsBrowser":    true,
	})
	if !res.OK {
		t.Fatalf("apply failed: code=%s err=%s", res.Code, res.Error)
	}
	m := mustOKMapFromInitial(t, res.Initial, "apply")
	if m["sourceId"] == "" || m["vantageId"] == "" {
		t.Fatalf("expected sourceId and vantageId, got %+v", m)
	}
	src := m["source"].(*CollectionSource)
	if src.AccessState != "public_allowed" {
		t.Fatalf("accessState = %q, want public_allowed", src.AccessState)
	}
	van := m["vantage"].(*CollectionVantage)
	if van.RuntimeID != "local" || van.EgressGeo != "eu" {
		t.Fatalf("vantage = %+v, want local/eu", van)
	}
}

// A regionally-licensed book (superbet) read for DATA from TR must be allowed —
// reading public odds is fine; only funding/placing is blocked. This is the core
// "we handle blocking sites" claim: observe yes, bet no.
func TestCollectionPlanAllowsDataReadFromBlockedJurisdiction(t *testing.T) {
	withPlannerMachines(t, []MachineInfo{testMachine("local", "local", "eu", true)})

	res := invokeVerb(t, "collection_plan", map[string]interface{}{
		"source":       "superbet.rs",
		"action":       "data",
		"jurisdiction": "TR",
	})
	if !res.OK {
		t.Fatalf("data read should not be OK=false: %s", res.Error)
	}
	plan := mustOKMapFromInitial(t, res.Initial, "plan")["plan"].(CollectionPlan)
	if plan.Status == "blocked" {
		t.Fatalf("data read from TR for superbet must not be blocked, got %q", plan.Status)
	}
	if plan.Policy.Decision == "block" {
		t.Fatalf("data read policy must not be block, got %q", plan.Policy.Decision)
	}
}

// superbet bet from TR is blocked, and collection_plan_apply must REFUSE to
// register a source for a blocked plan (never automate around the policy).
func TestCollectionPlanApplyRefusesBlocked(t *testing.T) {
	resetCollectionStoreForTest("")
	withPlannerMachines(t, []MachineInfo{testMachine("local", "local", "eu", true)})

	res := invokeVerb(t, "collection_plan_apply", map[string]interface{}{
		"source":       "superbet.rs",
		"action":       "bet",
		"jurisdiction": "TR",
	})
	if res.OK {
		t.Fatalf("apply must fail for a blocked plan")
	}
	if res.Code != "policy_blocked" {
		t.Fatalf("code = %q, want policy_blocked", res.Code)
	}
	plan := mustOKMapFromInitial(t, res.Initial, "plan")["plan"].(CollectionPlan)
	if plan.Status != "blocked" {
		t.Fatalf("status = %q, want blocked", plan.Status)
	}
}

// When the preferred egress region has no machine of its own, the planner routes
// via peer-egress through the best available box (it does NOT silently collect
// from the wrong region).
func TestCollectionPlanRoutesPeerEgressOnRegionMismatch(t *testing.T) {
	withPlannerMachines(t, []MachineInfo{testMachine("local", "local", "eu", true)})

	res := invokeVerb(t, "collection_plan", map[string]interface{}{
		"source":          "status.example.com",
		"action":          "data",
		"preferredRegion": "us",
	})
	if !res.OK {
		t.Fatalf("plan failed: %s", res.Error)
	}
	plan := mustOKMapFromInitial(t, res.Initial, "plan")["plan"].(CollectionPlan)
	if plan.EgressPolicy != "peer_egress" {
		t.Fatalf("egressPolicy = %q, want peer_egress", plan.EgressPolicy)
	}
	if plan.ViaPeer == "" {
		t.Fatalf("expected ViaPeer to be set for a region mismatch")
	}
	if !sliceHasStr(plan.NextActions, "start_peer_egress_bridge") {
		t.Fatalf("nextActions = %v, want start_peer_egress_bridge", plan.NextActions)
	}
}

// No online machine satisfies a hard capability requirement → no_runtime, with a
// clear next action rather than a misleading "ready".
func TestCollectionPlanNoRuntimeWhenCapabilityUnmet(t *testing.T) {
	noAndroid := testMachine("local", "local", "eu", true)
	noAndroid.Capabilities = &MachineCapabilities{SupportsGhostWeb: true, SupportsAndroid: false}
	withPlannerMachines(t, []MachineInfo{noAndroid})

	res := invokeVerb(t, "collection_plan", map[string]interface{}{
		"source":       "shop.example.com",
		"action":       "data",
		"needsAndroid": true,
	})
	if res.OK {
		t.Fatalf("expected OK=false when no runtime matches")
	}
	plan := mustOKMapFromInitial(t, res.Initial, "plan")["plan"].(CollectionPlan)
	if plan.Status != "no_runtime" {
		t.Fatalf("status = %q, want no_runtime", plan.Status)
	}
}

func sliceHasStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// Do no harm: a source previously blocked from a datacenter vantage must be
// re-routed to the user's own-device/residential runtime, not re-hit on the same
// cloud IP class — even when a datacenter box would otherwise be selected.
func TestCollectionPlanReroutesBlockedSourceToResidential(t *testing.T) {
	resetCollectionStoreForTest("")
	src := collStore.upsertSource(CollectionSource{Name: "superbet.rs", Kind: "public_web", BaseURL: "superbet.rs", AccessState: "public_allowed"})
	van := collStore.upsertVantage(CollectionVantage{RuntimeID: "hel1", EgressPolicy: "machine_native", EgressGeo: "eu"})
	collStore.recordRun(CollectionRun{SourceID: src.SourceID, VantageID: van.VantageID, Status: "blocked_ip", BlockKind: "ip"})

	withPlannerMachines(t, []MachineInfo{
		testMachine("hel1", "hetzner", "eu", false), // datacenter — selected by needsDurable
		testMachine("laptop", "local", "eu", true),  // own-device/residential
	})

	res := invokeVerb(t, "collection_plan", map[string]interface{}{
		"source": "superbet.rs", "action": "data", "needsDurable": true,
	})
	if !res.OK {
		t.Fatalf("plan failed: %s", res.Error)
	}
	plan := mustOKMapFromInitial(t, res.Initial, "plan")["plan"].(CollectionPlan)
	if plan.Machine == nil || plan.Machine.DeviceID != "laptop" {
		t.Fatalf("expected reroute to residential 'laptop', got %+v", plan.Machine)
	}
	if plan.EgressPolicy != "machine_native" {
		t.Fatalf("egressPolicy = %q, want machine_native", plan.EgressPolicy)
	}
	if !sliceHasStr(plan.NextActions, "prefer_residential_vantage") {
		t.Fatalf("nextActions = %v, want prefer_residential_vantage", plan.NextActions)
	}
}

// When the source was blocked from a datacenter box and NO residential machine is
// online, the planner advises user-present collection rather than re-hitting it.
func TestCollectionPlanBlockedSourceAdvisesUserPresentWhenNoResidential(t *testing.T) {
	resetCollectionStoreForTest("")
	src := collStore.upsertSource(CollectionSource{Name: "superbet.rs", Kind: "public_web", BaseURL: "superbet.rs", AccessState: "public_allowed"})
	van := collStore.upsertVantage(CollectionVantage{RuntimeID: "hel1", EgressGeo: "eu"})
	collStore.recordRun(CollectionRun{SourceID: src.SourceID, VantageID: van.VantageID, Status: "blocked_geo", BlockKind: "geo"})

	withPlannerMachines(t, []MachineInfo{testMachine("hel1", "hetzner", "eu", false)}) // datacenter only

	res := invokeVerb(t, "collection_plan", map[string]interface{}{"source": "superbet.rs", "action": "data"})
	plan := mustOKMapFromInitial(t, res.Initial, "plan")["plan"].(CollectionPlan)
	if !sliceHasStr(plan.NextActions, "prefer_residential_vantage") || !sliceHasStr(plan.NextActions, "ask_user_present") {
		t.Fatalf("expected prefer_residential_vantage + ask_user_present, got %v", plan.NextActions)
	}
}

func TestCollectionPlanVerbsRegisteredOwnerOnly(t *testing.T) {
	for _, name := range []string{"collection_plan", "collection_plan_apply"} {
		opsRegistryMu.RLock()
		spec, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Fatalf("verb %q not registered", name)
		}
		if spec.AllowGuest {
			t.Fatalf("verb %q must be owner-only", name)
		}
	}
}

func mustOKMapFromInitial(t *testing.T, initial interface{}, label string) map[string]interface{} {
	t.Helper()
	m, ok := initial.(map[string]interface{})
	if !ok {
		t.Fatalf("%s initial type = %T, want map", label, initial)
	}
	return m
}
