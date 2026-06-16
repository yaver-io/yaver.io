package main

// gateway_selfheal_test.go — tests for the M-G7 self-heal loop.
//
// Everything here is in-memory (registry rooted at t.TempDir) + the fake/scripted
// device driver doubles. NO vault, NO keychain, NO network, NO native build.
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"testing"
)

// stepClock returns a deterministic, monotonically-increasing unix hint so heal
// events get stable timestamps with no time.Now dependency.
func stepClock() func() int64 {
	n := int64(1000)
	return func() int64 { n++; return n }
}

// healTestRegistry roots a ConnectorRegistry at a temp dir (no ConfigDir, no
// keychain) and seeds one redroid connector with a stale ResourceID selector.
func healTestRegistry(t *testing.T) *ConnectorRegistry {
	t.Helper()
	reg, err := newConnectorRegistryAt(t.TempDir())
	if err != nil {
		t.Fatalf("newConnectorRegistryAt: %v", err)
	}
	return reg
}

// staleConnector: a redroid connector whose step targets a now-gone ResourceID
// ("old_search_box") but the element is still reachable by Text/ContentDesc.
func staleConnector() Connector {
	return Connector{
		ID:      "example-heal",
		Engine:  "redroid",
		Surface: "com.example.heal",
		Capabilities: []Capability{{
			ID:   "station_status",
			Verb: "get",
			Flow: CapabilityFlow{
				Type:      "redroid",
				LaunchPkg: "com.example.heal",
				Steps: []FlowStep{
					{Action: "tap", Target: "old_search_box"},
				},
			},
			AnswerSchema: map[string]string{"status": "Status:string"},
		}},
	}
}

// ── locateTarget strategies ──────────────────────────────────────────────────

func TestGatewaySelfHealLocateTargetStrategies(t *testing.T) {
	screen := Screen{Nodes: []uiNode{
		{Text: "Search", ResourceID: "search_box", ContentDesc: "Find a charger"},
		{Text: "Main St Garage", ResourceID: "row_0"},
	}}

	// 1. exact ResourceID.
	if mt, n, ok := locateTarget(screen, "search_box"); !ok || mt != "search_box" || n == nil {
		t.Fatalf("resourceId locate = %q,%v", mt, ok)
	}
	// 2. exact ContentDesc.
	if mt, _, ok := locateTarget(screen, "Find a charger"); !ok || mt != "Find a charger" {
		t.Fatalf("contentDesc locate = %q,%v", mt, ok)
	}
	// 3. exact Text.
	if mt, _, ok := locateTarget(screen, "Main St Garage"); !ok || mt != "Main St Garage" {
		t.Fatalf("text locate = %q,%v", mt, ok)
	}
	// 4. normalized / case-insensitive contains → returns the node's exact Text.
	if mt, _, ok := locateTarget(screen, "main st"); !ok || mt != "Main St Garage" {
		t.Fatalf("contains locate = %q,%v", mt, ok)
	}
	// A genuinely-absent target locates nothing (vision seam is a no-op).
	if mt, _, ok := locateTarget(screen, "totally_absent_xyz"); ok {
		t.Fatalf("absent target should not locate; got %q", mt)
	}
}

func TestGatewaySelfHealVisionSeamIsNoOp(t *testing.T) {
	// The default vision locator must be a no-op so the deterministic strategies
	// alone decide success/failure in this slice.
	if _, _, ok := visionLocate(Screen{Nodes: []uiNode{{Text: "anything"}}}, "nope"); ok {
		t.Fatal("default visionLocate must be a no-op (ok=false)")
	}
}

// ── healFlowStep ──────────────────────────────────────────────────────────────

func TestGatewaySelfHealFlowStepRelocates(t *testing.T) {
	// Step targets a label that is GONE verbatim (the app reworded it), but the
	// same control is still findable by a normalized/contains Text match. The old
	// exact target is NOT present (no node text/contentDesc/resource-id equals it),
	// so this exercises the fuzzy re-locate path → heal.
	screen := Screen{
		AppPkg: "com.example.heal",
		Nodes: []uiNode{
			{Text: "Search for a charger", ResourceID: "search_v2"}, // expanded label
			{Text: "Results"},
		},
	}
	// The old selector "Search" is no longer a verbatim/exact match for any node
	// (the label expanded to "Search for a charger"), but it is a normalized
	// substring → the fuzzy strategy re-locates it.
	step := FlowStep{Action: "tap", Target: "Search"}
	healed, ok := healFlowStep(screen, step)
	if !ok {
		t.Fatal("expected the step to heal (element findable by normalized Text)")
	}
	// It promoted the fuzzy match to the node's exact Text for next time.
	if healed.Target != "Search for a charger" {
		t.Fatalf("healed target = %q, want the node's exact Text", healed.Target)
	}
	if healed.Action != "tap" {
		t.Fatalf("heal must preserve Action, got %q", healed.Action)
	}
	if healed.ExpectSignature == "" || healed.ExpectSignature != ScreenSignature(&screen) {
		t.Fatalf("heal must refresh ExpectSignature to the observed screen sig")
	}
}


func TestGatewaySelfHealFlowStepAbsentDoesNotHeal(t *testing.T) {
	screen := Screen{Nodes: []uiNode{{Text: "Nothing relevant", ResourceID: "other"}}}
	step := FlowStep{Action: "tap", Target: "old_search_box"}
	got, ok := healFlowStep(screen, step)
	if ok {
		t.Fatal("a genuinely-absent target must NOT heal")
	}
	if got.Target != "old_search_box" {
		t.Fatalf("non-heal must return the original step unchanged, got %q", got.Target)
	}
}

func TestGatewaySelfHealFlowStepPresentTargetIsNoop(t *testing.T) {
	// When the original target still resolves, nothing heals.
	screen := Screen{Nodes: []uiNode{{Text: "Search", ResourceID: "search_box"}}}
	step := FlowStep{Action: "tap", Target: "search_box"}
	if _, ok := healFlowStep(screen, step); ok {
		t.Fatal("a present target must not heal")
	}
}

// ── persistHealedFlow: versioned rewrite + log ────────────────────────────────

func TestGatewaySelfHealPersistRewritesAndVersions(t *testing.T) {
	reg := healTestRegistry(t)
	conn := staleConnector()
	if err := reg.Store(conn); err != nil {
		t.Fatalf("seed connector: %v", err)
	}

	updated := []FlowStep{{Action: "tap", Target: "search_v2", ExpectSignature: "sig:abc"}}
	after, err := persistHealedFlow(reg, conn.ID, "station_status", updated)
	if err != nil {
		t.Fatalf("persistHealedFlow: %v", err)
	}
	// Version bumped from 0 → 1.
	if after.Capabilities[0].Flow.Version != 1 {
		t.Fatalf("version = %d, want 1", after.Capabilities[0].Flow.Version)
	}
	// New target persisted.
	if after.Capabilities[0].Flow.Steps[0].Target != "search_v2" {
		t.Fatalf("new target not persisted: %q", after.Capabilities[0].Flow.Steps[0].Target)
	}

	// Round-trip from disk: the rewrite is durable.
	reloaded, err := reg.Get(conn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Capabilities[0].Flow.Steps[0].Target != "search_v2" ||
		reloaded.Capabilities[0].Flow.Version != 1 {
		t.Fatalf("durable rewrite missing: %+v", reloaded.Capabilities[0].Flow)
	}
}

func TestGatewaySelfHealLogRecordsEvent(t *testing.T) {
	log := newHealLog(8)
	clock := stepClock()
	log.append(healEvent{
		Connector: "c", Capability: "cap", StepIndex: 2,
		OldTarget: "old", NewTarget: "new", Strategy: "text", AtUnixHint: clock(),
	})
	snap := log.snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 event, got %d", len(snap))
	}
	ev := snap[0]
	if ev.OldTarget != "old" || ev.NewTarget != "new" || ev.StepIndex != 2 || ev.Strategy != "text" {
		t.Fatalf("event fields wrong: %+v", ev)
	}
	if ev.AtUnixHint != 1001 {
		t.Fatalf("deterministic clock hint = %d, want 1001", ev.AtUnixHint)
	}
}

// ── BOUNDED-IMPROVEMENT GUARD ─────────────────────────────────────────────────

// A heal must never touch verb/auth/engine or the capability set.
func TestGatewaySelfHealGuardRejectsVerbChange(t *testing.T) {
	before := staleConnector()
	after := staleConnector()
	after.Capabilities[0].Verb = "delete" // an ACT verb — forbidden
	if err := assertHealIsBounded(&before, &after, "station_status"); err == nil {
		t.Fatal("guard must reject a verb change (read → act)")
	}
}

func TestGatewaySelfHealGuardRejectsAuthChange(t *testing.T) {
	before := staleConnector()
	after := staleConnector()
	after.Auth.LoginRef = "gateway/heal/login" // auth change — forbidden
	if err := assertHealIsBounded(&before, &after, "station_status"); err == nil {
		t.Fatal("guard must reject an auth change")
	}
}

func TestGatewaySelfHealGuardRejectsCapabilityAdd(t *testing.T) {
	before := staleConnector()
	after := staleConnector()
	after.Capabilities = append(after.Capabilities, Capability{ID: "transfer", Verb: "add"})
	if err := assertHealIsBounded(&before, &after, "station_status"); err == nil {
		t.Fatal("guard must reject adding a capability")
	}
}

func TestGatewaySelfHealGuardRejectsStepActionChange(t *testing.T) {
	before := staleConnector()
	after := staleConnector()
	after.Capabilities[0].Flow.Steps[0].Action = "type" // changed what the step DOES
	if err := assertHealIsBounded(&before, &after, "station_status"); err == nil {
		t.Fatal("guard must reject changing a step's Action")
	}
}

func TestGatewaySelfHealGuardAllowsSelectorOnlyRewrite(t *testing.T) {
	before := staleConnector()
	after := staleConnector()
	// Selector-only change (Target + ExpectSignature) + a version bump = allowed.
	after.Capabilities[0].Flow.Steps[0].Target = "search_v2"
	after.Capabilities[0].Flow.Steps[0].ExpectSignature = "sig:new"
	after.Capabilities[0].Flow.Version = 1
	if err := assertHealIsBounded(&before, &after, "station_status"); err != nil {
		t.Fatalf("guard must allow a selector-only rewrite: %v", err)
	}
}

// persistHealedFlow must refuse to heal a non-read (ACT) capability — only READ
// flows self-heal (docs §8). Seed a connector carrying a valid ACT capability and
// try to heal IT; the bounded-improvement guard must reject it, leaving the
// manifest untouched.
func TestGatewaySelfHealPersistRefusesNonRead(t *testing.T) {
	reg := healTestRegistry(t)
	conn := Connector{
		ID:      "example-act",
		Engine:  "redroid",
		Surface: "com.example.act",
		Capabilities: []Capability{{
			ID:   "do_thing",
			Verb: "add",  // ACT verb
			Risk: "high", // valid risk tier so Store accepts the manifest
			Flow: CapabilityFlow{
				Type:      "redroid",
				LaunchPkg: "com.example.act",
				Steps:     []FlowStep{{Action: "tap", Target: "confirm_btn"}},
			},
		}},
	}
	if err := reg.Store(conn); err != nil {
		t.Fatalf("seed act connector: %v", err)
	}
	// Attempt to heal the ACT capability's selector — the guard must refuse.
	updated := []FlowStep{{Action: "tap", Target: "confirm_v2"}}
	if _, err := persistHealedFlow(reg, conn.ID, "do_thing", updated); err == nil {
		t.Fatal("persistHealedFlow must refuse to heal a non-read (ACT) capability")
	}
	// The on-disk manifest is untouched (selector unchanged, version not bumped).
	reloaded, err := reg.Get(conn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Capabilities[0].Flow.Steps[0].Target != "confirm_btn" ||
		reloaded.Capabilities[0].Flow.Version != 0 {
		t.Fatalf("refused heal must not mutate the manifest: %+v", reloaded.Capabilities[0].Flow)
	}
}

// ── end-to-end: redroidInvoke recovers via heal ───────────────────────────────

// TestGatewaySelfHealRedroidInvokeRecovers drives redroidInvoke end-to-end with a
// scriptedDriver whose screen has DRIFTED: the flow's step targets a gone
// ResourceID, but the same element is present by Text. The invoke must re-locate,
// proceed, extract the answer, persist a versioned rewrite, and log the heal.
func TestGatewaySelfHealRedroidInvokeRecovers(t *testing.T) {
	// Isolate the process-wide heal log so the assertion is deterministic.
	gatewayHealLog = newHealLog(16)

	reg := healTestRegistry(t)
	conn := staleConnector()
	// The seeded step targets the label "Search"; the live app reworded it.
	conn.Capabilities[0].Flow.Steps[0].Target = "Search"
	if err := reg.Store(conn); err != nil {
		t.Fatalf("seed: %v", err)
	}

	driver := &scriptedDriver{
		screens: [][]uiNode{
			// Entry screen: the old verbatim label "Search" is GONE; the
			// control is now "Search for a charger" — recoverable by a normalized
			// contains match, not by the exact old selector.
			{{Text: "Search for a charger", ResourceID: "search_v2", Clickable: true}},
			// After the (re-located) tap: the answer screen.
			{
				{Text: "Main St Garage", ResourceID: "station_title"},
				{Text: "Status: Available", ResourceID: "status_field"},
			},
		},
	}

	cap := &conn.Capabilities[0]
	res, err := redroidInvoke(context.Background(), &conn, cap, nil,
		Session{Kind: SessionDevice, DeviceID: "inst-1"}, driver, false, reg, stepClock())
	if err != nil {
		t.Fatalf("redroidInvoke: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Detail)
	}
	// It recovered: the answer was extracted off the screen the heal reached.
	if got := res.Answer["status"]; got != "Available" {
		t.Fatalf("status = %v, want Available (heal should have advanced the flow)", got)
	}
	// A heal was recorded in the result.
	if len(res.NeedsHeal) == 0 {
		t.Fatal("expected a NeedsHeal/heal record")
	}

	// The rewrite persisted with a bumped version + the new target.
	reloaded, err := reg.Get(conn.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	step0 := reloaded.Capabilities[0].Flow.Steps[0]
	if step0.Target == "Search" {
		t.Fatalf("step target was not healed on disk: %q", step0.Target)
	}
	if step0.Target != "Search for a charger" {
		t.Fatalf("healed target on disk = %q, want %q", step0.Target, "Search for a charger")
	}
	if reloaded.Capabilities[0].Flow.Version != 1 {
		t.Fatalf("flow version not bumped on disk: %d", reloaded.Capabilities[0].Flow.Version)
	}
	// The heal log captured the event with selector strings only (no creds/paths).
	events := gatewayHealLog.snapshot()
	if len(events) == 0 {
		t.Fatal("heal log should have recorded the relocate")
	}
	found := false
	for _, ev := range events {
		if ev.Connector == conn.ID && ev.Capability == cap.ID && ev.OldTarget == "Search" {
			found = true
			if ev.NewTarget == "Search" {
				t.Fatal("heal event new target should differ from old")
			}
		}
	}
	if !found {
		t.Fatalf("heal event not logged: %+v", events)
	}

	// The verb + auth on disk are UNCHANGED — the heal never touched ACT/auth.
	if !isReadVerb(reloaded.Capabilities[0].Verb) {
		t.Fatal("heal must leave the capability read-only")
	}
	if !connectorAuthEqual(conn.Auth, reloaded.Auth) {
		t.Fatal("heal must not change auth")
	}
}
