package main

// gateway_device_invoke_test.go — tests for the engine:"device" dispatch and the
// integrity-block detection (M-G5c). Drives deviceInvoke with the scriptedDriver /
// fakeDeviceDriver doubles returning canned screens. No real device, no keychain,
// no network, no native build.
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"testing"
	"time"
)

// gateChallengeCalled fails the test if the human gate is ever consulted (an
// integrity block must NEVER route to a gate). It swaps the process-wide gate
// store for one whose notifier records, then asserts zero gates appeared.

// TestGatewayIntegrityBlockStopsNoHeal: a Play Integrity / attestation wall on the
// entry screen → IntegrityBlocked + Blocked, STOPS immediately, no answer, no
// self-heal step ran, and NO human gate was created.
func TestGatewayIntegrityBlockStopsNoHeal(t *testing.T) {
	// Isolate the gate store so we can assert no gate was created.
	gatewayGates = newGateStore(&recordingNotifier{})

	driver := &scriptedDriver{
		screens: [][]uiNode{
			// Entry screen is an integrity/attestation wall.
			{
				{Text: "This device isn't supported", ResourceID: "integrity_title"},
				{Text: "Play Integrity check failed. Update Google Play services to continue."},
			},
			// A would-be answer screen behind it — must NEVER be reached.
			{{Text: "Status: Available", ResourceID: "status_field"}},
		},
	}
	// A flow with a step whose target is absent from the wall — if the path kept
	// going it would try to self-heal it. It must NOT.
	cap := &Capability{
		ID:   "x",
		Verb: "get",
		Flow: CapabilityFlow{
			Type:      "redroid",
			LaunchPkg: "com.example.app",
			Steps:     []FlowStep{{Action: "tap", Target: "definitely_absent_selector"}},
		},
		AnswerSchema: map[string]string{"status": "Status:string"},
	}

	res, err := deviceInvoke(context.Background(), redroidInvokeConnector(), cap, nil,
		Session{Kind: SessionDevice}, driver, false, nil, nil)
	if err != nil {
		t.Fatalf("an integrity block must be a structured result, not an error: %v", err)
	}
	if !res.IntegrityBlocked {
		t.Fatal("integrity wall must set IntegrityBlocked=true")
	}
	if !res.Blocked {
		t.Fatal("an integrity block is also a block (Blocked=true)")
	}
	if res.Answer != nil {
		t.Fatalf("an integrity-blocked read must not fabricate an answer; got %v", res.Answer)
	}
	if len(res.NeedsHeal) != 0 {
		t.Fatalf("an integrity block must STOP before any self-heal; got NeedsHeal=%v", res.NeedsHeal)
	}
	// No step ran (we stopped on the entry observation before the flow loop).
	if len(driver.taps) != 0 {
		t.Fatalf("an integrity block must stop before running any step; taps=%v", driver.taps)
	}
	// No human gate was created (integrity is never routed to a human).
	if gates := gatewayGates.List(); len(gates) != 0 {
		t.Fatalf("an integrity block must NOT route to a human gate; got %d gate(s)", len(gates))
	}
}

// TestGatewayDeviceEngineDispatchSamePath: an engine:"device" connector dispatches
// through the SAME deviceInvoke path as redroid and extracts an answer off a
// normal screen.
func TestGatewayDeviceEngineDispatchSamePath(t *testing.T) {
	driver := &scriptedDriver{
		screens: [][]uiNode{
			// Entry screen.
			{{Text: "Home", ResourceID: "home"}},
			// Answer screen after the single tap.
			{
				{Text: "Status: Available", ResourceID: "status_field"},
				{Text: "Price", ResourceID: "price_label"},
				{Text: "$0.42/kWh", ResourceID: "price_value"},
			},
		},
	}
	conn := &Connector{
		ID:      "real-phone-charger",
		Engine:  "device", // <-- real paired phone, same invoke path as redroid
		Surface: "com.example.charger",
	}
	cap := &Capability{
		ID:   "station_status",
		Verb: "get",
		Flow: CapabilityFlow{
			Type:      "redroid",
			LaunchPkg: "com.example.charger",
			Steps:     []FlowStep{{Action: "tap", Target: "home"}},
		},
		AnswerSchema: map[string]string{
			"status": "Status:string",
			"price":  "Price:string",
		},
	}

	res, err := deviceInvoke(context.Background(), conn, cap, nil,
		Session{Kind: SessionDevice, DeviceID: "phone-1"}, driver, false, nil, nil)
	if err != nil {
		t.Fatalf("deviceInvoke (engine=device): %v", err)
	}
	if res.Blocked || res.IntegrityBlocked {
		t.Fatalf("normal screen must not block: %s", res.Detail)
	}
	if driver.launched != "com.example.charger" {
		t.Fatalf("launched %q, want com.example.charger", driver.launched)
	}
	if got := res.Answer["status"]; got != "Available" {
		t.Fatalf("status = %v, want Available", got)
	}
	if got := res.Answer["price"]; got != "$0.42/kWh" {
		t.Fatalf("price = %v, want $0.42/kWh", got)
	}
	// redroidInvoke alias drives the same body — sanity-check it still resolves.
	if _, err := redroidInvoke(context.Background(), conn, cap, nil,
		Session{Kind: SessionDevice}, &scriptedDriver{screens: driver.screens}, false, nil, nil); err != nil {
		t.Fatalf("redroidInvoke alias must still work: %v", err)
	}
}

// TestGatewayGenericBlockNotIntegrity: a generic rate-limit/locked block still
// routes through detectBlockSignal (Blocked, NOT IntegrityBlocked) — it is not
// misclassified as an integrity block.
func TestGatewayGenericBlockNotIntegrity(t *testing.T) {
	driver := &scriptedDriver{
		screens: [][]uiNode{
			{{Text: "Too many requests. Try again later."}},
		},
	}
	cap := &Capability{
		ID:   "x",
		Verb: "get",
		Flow: CapabilityFlow{Type: "redroid", LaunchPkg: "com.example.app"},
	}
	res, err := deviceInvoke(context.Background(), redroidInvokeConnector(), cap, nil,
		Session{}, driver, false, nil, nil)
	if err != nil {
		t.Fatalf("a generic block should be a structured result, not an error: %v", err)
	}
	if !res.Blocked {
		t.Fatal("rate-limit screen must surface Blocked=true")
	}
	if res.IntegrityBlocked {
		t.Fatal("a generic rate-limit block must NOT be misclassified as IntegrityBlocked")
	}
	if res.Answer != nil {
		t.Fatalf("a blocked read must not fabricate an answer; got %v", res.Answer)
	}
}

// TestGatewayChallengeNotIntegrity: a normal human-solvable challenge screen still
// routes to the human gate — integrity detection did not over-trigger and steal it.
func TestGatewayChallengeNotIntegrity(t *testing.T) {
	gatewayGates = newGateStore(&recordingNotifier{})

	driver := &scriptedDriver{
		screens: [][]uiNode{
			// A captcha challenge — solvable by a human, NOT an integrity wall.
			{{Text: "Security check", ResourceID: "challenge"}, {Text: "Select all images with traffic lights"}},
			// Answer screen after the human solves it.
			{{Text: "Status: Available", ResourceID: "status_field"}},
		},
	}
	cap := &Capability{
		ID:           "x",
		Verb:         "get",
		Flow:         CapabilityFlow{Type: "redroid", LaunchPkg: "com.example.app"},
		AnswerSchema: map[string]string{"status": "Status:string"},
	}

	type result struct {
		res *gatewayResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, e := deviceInvoke(context.Background(), redroidInvokeConnector(), cap, nil,
			Session{Kind: SessionDevice}, driver, false, nil, nil)
		done <- result{r, e}
	}()

	// A human gate must appear (the challenge was NOT swallowed by integrity).
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
			t.Fatal("a normal challenge must create a human gate (integrity over-triggered?)")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if err := gatewayGates.Resolve(gateID, Resolution{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("deviceInvoke after challenge: %v", r.err)
		}
		if r.res.IntegrityBlocked {
			t.Fatal("a challenge must not be classified as an integrity block")
		}
		if got := r.res.Answer["status"]; got != "Available" {
			t.Fatalf("status = %v, want Available", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deviceInvoke did not complete after the challenge was solved")
	}
}

// TestGatewayDetectIntegrityBlockKeywords: the detector recognizes attestation
// phrasing and does NOT fire on ordinary challenge/block/normal copy.
func TestGatewayDetectIntegrityBlockKeywords(t *testing.T) {
	hits := []string{
		"Play Integrity check failed",
		"This device failed SafetyNet attestation",
		"Your device is rooted",
		"This device isn't secure",
		"Device not supported",
		"We can't verify it's you on this device",
		"Update Google Play services to continue",
	}
	for _, h := range hits {
		s := Screen{Nodes: []uiNode{{Text: h}}}
		if blocked, _ := detectIntegrityBlock(s); !blocked {
			t.Fatalf("expected integrity block for %q", h)
		}
	}
	misses := []string{
		"Too many requests. Try again later.",
		"Select all images with traffic lights",
		"Status: Available",
		"Sign in to continue",
	}
	for _, m := range misses {
		s := Screen{Nodes: []uiNode{{Text: m}}}
		if blocked, reason := detectIntegrityBlock(s); blocked {
			t.Fatalf("integrity over-triggered on %q (reason %q)", m, reason)
		}
	}
}
