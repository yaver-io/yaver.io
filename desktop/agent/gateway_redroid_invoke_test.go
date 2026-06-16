package main

// gateway_redroid_invoke_test.go — tests for the redroid engine path (M-G5b).
// Drives redroidInvoke with the fakeDeviceDriver double (gateway_redroid_test.go)
// returning canned uiNode screens. No real device, no keychain, no network.
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"testing"
	"time"
)

// scriptedDriver returns a different screen on each UiTexts() call so a flow can
// be observed advancing. It embeds fakeDeviceDriver for the rest of the surface
// (Launch/Type/Tap/Frame/Snapshot) so we reuse the existing double.
type scriptedDriver struct {
	fakeDeviceDriver
	screens [][]uiNode // one per UiTexts() call; last is repeated when exhausted
	calls   int
}

func (d *scriptedDriver) UiTexts() ([]uiNode, error) {
	i := d.calls
	if i >= len(d.screens) {
		i = len(d.screens) - 1
	}
	d.calls++
	if i < 0 {
		return nil, nil
	}
	return d.screens[i], nil
}

func redroidInvokeConnector() *Connector {
	return &Connector{
		ID:      "example-charger",
		Engine:  "redroid",
		Surface: "com.example.charger",
	}
}

// TestGatewayRedroidInvokeFlowAndExtract: launch + run steps + extract the
// answerSchema (the "is this charger free / price" answer) off the final screen.
func TestGatewayRedroidInvokeFlowAndExtract(t *testing.T) {
	driver := &scriptedDriver{
		screens: [][]uiNode{
			// Entry screen (map): a search box.
			{{Text: "Find a charger", ResourceID: "search_box", Clickable: true}},
			// After typing the station name + tap: still the list.
			{{Text: "Results", ResourceID: "results_list"}, {Text: "Main St Garage", ResourceID: "row_0"}},
			// Station detail (the answer screen).
			{
				{Text: "Main St Garage", ResourceID: "station_title"},
				{Text: "Status: Available", ResourceID: "status_field"},
				{Text: "Price", ResourceID: "price_label"},
				{Text: "$0.42/kWh", ResourceID: "price_value"},
			},
		},
	}

	cap := &Capability{
		ID:   "station_status",
		Verb: "get",
		Flow: CapabilityFlow{
			Type:      "redroid",
			LaunchPkg: "com.example.charger",
			Steps: []FlowStep{
				{Action: "tap", Target: "search_box"},
				{Action: "type", Target: "search_box", Text: "{station}"},
				{Action: "tap", Target: "row_0"},
			},
		},
		AnswerSchema: map[string]string{
			"status": "Status:string", // "Status: Available" → "Available"
			"price":  "Price:string",  // "Price" label → next node "$0.42/kWh"
		},
	}

	res, err := redroidInvoke(context.Background(), redroidInvokeConnector(), cap,
		map[string]string{"station": "Main St Garage"}, Session{Kind: SessionDevice, DeviceID: "inst-1"},
		driver, false, nil, nil)
	if err != nil {
		t.Fatalf("redroidInvoke: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %s", res.Detail)
	}

	// The app was launched.
	if driver.launched != "com.example.charger" {
		t.Fatalf("launched %q, want com.example.charger", driver.launched)
	}
	// The {station} param was substituted into the type step.
	if !driver.typedContains("Main St Garage") {
		t.Fatalf("station param not typed; typed=%v", driver.typed)
	}
	// Two taps ran (search_box, row_0).
	if len(driver.taps) != 2 {
		t.Fatalf("taps=%v, want 2", driver.taps)
	}

	// answerSchema extracted the right values.
	if got := res.Answer["status"]; got != "Available" {
		t.Fatalf("status = %v, want Available", got)
	}
	if got := res.Answer["price"]; got != "$0.42/kWh" {
		t.Fatalf("price = %v, want $0.42/kWh", got)
	}
	if res.Signature == "" {
		t.Fatal("final screen signature should be set")
	}
}

// TestGatewayRedroidScreenSignatureStable: the signature is stable across trivial
// value changes (a different price / rotating code / timestamp) but differs for a
// structurally different screen.
func TestGatewayRedroidScreenSignatureStable(t *testing.T) {
	base := &Screen{
		AppPkg: "com.example.charger",
		Nodes: []uiNode{
			{Text: "Main St Garage", ResourceID: "station_title"},
			{Text: "Status: Available", ResourceID: "status_field"},
			{Text: "Price: $0.42/kWh", ResourceID: "price_value"},
			{Text: "Updated 12:04", ResourceID: "ts"},
		},
	}
	// Trivial change: different live values (price, status text value, timestamp)
	// but SAME labels/resource-ids → SAME signature.
	trivial := &Screen{
		AppPkg: "com.example.charger",
		Nodes: []uiNode{
			{Text: "Main St Garage", ResourceID: "station_title"},
			{Text: "Status: Available", ResourceID: "status_field"},
			{Text: "Price: $0.99/kWh", ResourceID: "price_value"},
			{Text: "Updated 23:51", ResourceID: "ts"},
		},
	}
	if ScreenSignature(base) != ScreenSignature(trivial) {
		t.Fatalf("signature changed on a trivial value change:\n base=%s\n triv=%s",
			ScreenSignature(base), ScreenSignature(trivial))
	}

	// Structural change: a different screen (settings) → DIFFERENT signature.
	different := &Screen{
		AppPkg: "com.example.charger",
		Nodes: []uiNode{
			{Text: "Settings", ResourceID: "settings_title"},
			{Text: "Account", ResourceID: "account_row"},
			{Text: "Sign out", ResourceID: "signout_btn"},
		},
	}
	if ScreenSignature(base) == ScreenSignature(different) {
		t.Fatal("structurally different screen produced the same signature")
	}
	// Deterministic: same input → same output.
	if ScreenSignature(base) != ScreenSignature(base) {
		t.Fatal("signature is not deterministic")
	}
}

// TestGatewayRedroidChallengeRoutesToGate: a captcha screen routes to a human
// gate and is NEVER auto-solved; resolving the gate lets the read complete.
func TestGatewayRedroidChallengeRoutesToGate(t *testing.T) {
	// Reset the process-wide gate store's notifier to a recording double so we
	// don't touch a real device, and so it's isolated.
	gatewayGates = newGateStore(&recordingNotifier{})

	driver := &scriptedDriver{
		screens: [][]uiNode{
			// Entry screen IS a captcha wall.
			{{Text: "Security check", ResourceID: "challenge"}, {Text: "Select all images with traffic lights"}},
			// After the human solves it (we re-observe): the answer screen.
			{{Text: "Status: Available", ResourceID: "status_field"}},
		},
	}
	cap := &Capability{
		ID:           "station_status",
		Verb:         "get",
		Flow:         CapabilityFlow{Type: "redroid", LaunchPkg: "com.example.charger"},
		AnswerSchema: map[string]string{"status": "Status:string"},
	}

	type result struct {
		res *gatewayResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, e := redroidInvoke(context.Background(), redroidInvokeConnector(), cap, nil,
			Session{Kind: SessionDevice}, driver, false, nil, nil)
		done <- result{r, e}
	}()

	// A gate must appear (the handler suspended) — it must NOT have auto-solved.
	var gateID string
	deadline := time.After(2 * time.Second)
	for {
		gates := gatewayGates.List()
		if len(gates) == 1 {
			if gates[0].Kind != GateInteractive {
				t.Fatalf("gate kind = %q, want interactive", gates[0].Kind)
			}
			gateID = gates[0].ID
			break
		}
		select {
		case <-deadline:
			t.Fatal("captcha did not create a human gate (auto-solved?)")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Human solves it live → read resumes and completes.
	if err := gatewayGates.Resolve(gateID, Resolution{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("redroidInvoke after challenge: %v", r.err)
		}
		if r.res.Blocked {
			t.Fatalf("unexpected block: %s", r.res.Detail)
		}
		if got := r.res.Answer["status"]; got != "Available" {
			t.Fatalf("status = %v, want Available", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("redroidInvoke did not complete after the challenge was solved")
	}
}

// TestGatewayRedroidChallengeTimeoutAborts: an unresolved challenge aborts the
// read cleanly (never auto-solved, never a fabricated answer).
func TestGatewayRedroidChallengeTimeoutAborts(t *testing.T) {
	gatewayGates = newGateStore(&recordingNotifier{})
	driver := &scriptedDriver{
		screens: [][]uiNode{
			{{Text: "Please complete the captcha to continue"}},
		},
	}
	cap := &Capability{
		ID:   "x",
		Verb: "get",
		Flow: CapabilityFlow{Type: "redroid", LaunchPkg: "com.example.charger"},
	}
	// Short context so awaitHuman's ctx.Done() fires fast (no 3-min wait).
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := redroidInvoke(ctx, redroidInvokeConnector(), cap, nil, Session{}, driver, false, nil, nil)
	if err == nil {
		t.Fatal("an unresolved challenge must abort with an error, not a fabricated answer")
	}
}

// TestGatewayRedroidBlockSignalStops: a rate-limit/block screen surfaces a
// structured {blocked:true} and STOPS — no retry, no evasion.
func TestGatewayRedroidBlockSignalStops(t *testing.T) {
	driver := &scriptedDriver{
		screens: [][]uiNode{
			{{Text: "Too many requests. Try again later."}},
		},
	}
	cap := &Capability{
		ID:   "x",
		Verb: "get",
		Flow: CapabilityFlow{Type: "redroid", LaunchPkg: "com.example.charger"},
	}
	res, err := redroidInvoke(context.Background(), redroidInvokeConnector(), cap, nil,
		Session{}, driver, false, nil, nil)
	if err != nil {
		t.Fatalf("a block should be a structured result, not an error: %v", err)
	}
	if !res.Blocked {
		t.Fatal("rate-limit screen must surface Blocked=true")
	}
	if res.Answer != nil {
		t.Fatalf("a blocked read must not fabricate an answer; got %v", res.Answer)
	}
}

// TestGatewayRedroidEmptySchemaReturnsScreen: with no answerSchema the extractor
// returns the raw visible texts (raw material for a later AI extractor).
func TestGatewayRedroidEmptySchemaReturnsScreen(t *testing.T) {
	s := &Screen{Nodes: []uiNode{{Text: "Available"}, {Text: "$0.42/kWh"}, {Text: ""}}}
	out := deterministicExtractor{}.Extract(s, nil)
	texts, ok := out["screen"].([]string)
	if !ok {
		t.Fatalf("empty schema should return raw screen texts, got %v", out)
	}
	if len(texts) != 2 {
		t.Fatalf("expected 2 non-empty texts, got %v", texts)
	}
}

// TestGatewayRedroidExtractContentDesc: label-above-value + contentDesc matching.
func TestGatewayRedroidExtractContentDesc(t *testing.T) {
	s := &Screen{
		Nodes: []uiNode{
			{Text: "Available Now", ContentDesc: "Charger availability, Available Now"},
			{Text: "Connector", ResourceID: "conn_label"},
			{Text: "CCS2", ResourceID: "conn_value"},
		},
	}
	// "Connector" label → next node "CCS2".
	if v, ok := matchLabelValue(s.Nodes, "Connector"); !ok || v != "CCS2" {
		t.Fatalf("label-above-value match = %q,%v, want CCS2,true", v, ok)
	}
	// contentDesc "Charger availability, Available Now" matched by its label.
	if v, ok := matchLabelValue(s.Nodes, "Charger availability"); !ok || v != "Available Now" {
		t.Fatalf("contentDesc match = %q,%v, want 'Available Now',true", v, ok)
	}
}
