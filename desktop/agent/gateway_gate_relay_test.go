package main

import (
	"context"
	"testing"
	"time"
)

// TestResolveByConnector verifies the seamless OTP relay: a login blocked on an
// enter_code gate for a connector resolves when the phone forwards a code by
// connector name (no gate id), and the blocked goroutine gets the answer.
func TestResolveByConnector(t *testing.T) {
	store := newGateStore(nil)

	got := make(chan Resolution, 1)
	go func() {
		res, _ := store.awaitHuman(context.Background(), GateRequest{
			ConnectorID: "misli",
			Kind:        GateEnterCode,
			Prompt:      "enter the code",
			Timeout:     2 * time.Second,
		})
		got <- res
	}()

	// Wait for the waiter to register (awaitHuman runs in the goroutine above).
	deadline := time.Now().Add(time.Second)
	for len(store.List()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	id, err := store.ResolveByConnector("misli", GateEnterCode, Resolution{Answer: "123456"})
	if err != nil {
		t.Fatalf("ResolveByConnector: %v", err)
	}
	if id == "" {
		t.Fatal("expected a resolved gate id")
	}

	select {
	case res := <-got:
		if res.Status != GateResolved || res.Answer != "123456" {
			t.Fatalf("blocked flow got wrong resolution: %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked flow never received the relayed code")
	}
}

// TestResolveByConnectorNoMatch confirms a stray/duplicate forward (no waiting
// login) is a clean error, not a wrong-gate resolution.
func TestResolveByConnectorNoMatch(t *testing.T) {
	store := newGateStore(nil)
	if _, err := store.ResolveByConnector("nobody", GateEnterCode, Resolution{Answer: "000000"}); err == nil {
		t.Fatal("expected error when no gate is pending for the connector")
	}
}

// TestResolveByConnectorKindFilter confirms a forwarded code does NOT resolve a
// non-code gate (e.g. a push approval) for the same connector.
func TestResolveByConnectorKindFilter(t *testing.T) {
	store := newGateStore(nil)
	go func() {
		_, _ = store.awaitHuman(context.Background(), GateRequest{
			ConnectorID: "bank",
			Kind:        GateApprovePush, // a push, not a code
			Prompt:      "approve the push",
			Timeout:     500 * time.Millisecond,
		})
	}()
	deadline := time.Now().Add(time.Second)
	for len(store.List()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := store.ResolveByConnector("bank", GateEnterCode, Resolution{Answer: "111111"}); err == nil {
		t.Fatal("a forwarded code must not resolve a push-approval gate")
	}
}

// TestProvideOTPMCP exercises the MCP entrypoint against the process-wide store.
func TestProvideOTPMCP(t *testing.T) {
	isolateHome(t)
	if err := saveGatewayConsent(GatewayConsent{AutoRelayOtp: true}); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	// mcpGatewayProvideOTP targets the package-global gatewayGates; drive a waiter
	// onto it and relay through the MCP function.
	go func() {
		_, _ = gatewayGates.awaitHuman(context.Background(), GateRequest{
			ConnectorID: "transit",
			Kind:        GateEnterCode,
			Prompt:      "code?",
			Timeout:     2 * time.Second,
		})
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		found := false
		for _, g := range gatewayGates.List() {
			if g.ConnectorID == "transit" {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	res := mcpGatewayProvideOTP("transit", "654321")
	m, _ := res.(map[string]interface{})
	if ok, _ := m["ok"].(bool); !ok {
		t.Fatalf("expected ok relay, got %+v", res)
	}

	// Missing args → error.
	bad := mcpGatewayProvideOTP("", "123")
	if mb, _ := bad.(map[string]interface{}); mb["error"] == nil {
		t.Fatalf("expected error for empty connector, got %+v", bad)
	}
}

// TestProvideOTPRequiresConsent confirms the relay is refused without the grant.
func TestProvideOTPRequiresConsent(t *testing.T) {
	isolateHome(t) // no consent granted
	res := mcpGatewayProvideOTP("misli", "123456")
	m, _ := res.(map[string]interface{})
	if nc, _ := m["needsConsent"].(bool); !nc {
		t.Fatalf("expected needsConsent without a grant, got %+v", res)
	}
}
