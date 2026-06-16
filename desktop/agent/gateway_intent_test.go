package main

import (
	"context"
	"testing"
)

// TestGatewayIntentRouting checks the deterministic classifier routes reads,
// acts, and dev tasks correctly given a small connector catalog.
func TestGatewayIntentRouting(t *testing.T) {
	reads := []MCPCapability{
		{Connector: "transit", Capability: "card_balance", Verb: "get", Title: "transit card balance"},
		{Connector: "esarj", Capability: "station_status", Verb: "get", Title: "charger station status"},
	}
	acts := []MCPCapability{
		{Connector: "transit", Capability: "topup", Verb: "add", Risk: "financial", Title: "top up transit card"},
		{Connector: "carrier", Capability: "buy_ticket", Verb: "add", Risk: "financial", Title: "buy bus ticket"},
	}
	c := keywordIntentClassifier{}

	cases := []struct {
		utterance  string
		wantEngine IntentEngine
		wantConn   string
	}{
		{"how much is on my transit card", IntentGatewayRead, "transit"},
		{"top up my transit card by 100", IntentGatewayAct, "transit"},
		{"buy a bus ticket on carrier", IntentGatewayAct, "carrier"},
		{"fix the failing test in the auth package", IntentCode, ""},
		{"refactor the websocket reconnect logic", IntentCode, ""},
	}
	for _, tc := range cases {
		d, err := c.Classify(context.Background(), tc.utterance, reads, acts)
		if err != nil {
			t.Fatalf("%q: %v", tc.utterance, err)
		}
		if d.Engine != tc.wantEngine {
			t.Fatalf("%q → engine %q, want %q (reason: %s)", tc.utterance, d.Engine, tc.wantEngine, d.Reason)
		}
		if tc.wantConn != "" && d.Connector != tc.wantConn {
			t.Fatalf("%q → connector %q, want %q", tc.utterance, d.Connector, tc.wantConn)
		}
	}
}

// TestGatewayIntentExtractsAmount: a numeric in a top-up utterance becomes the
// amount param.
func TestGatewayIntentExtractsAmount(t *testing.T) {
	acts := []MCPCapability{{Connector: "transit", Capability: "topup", Verb: "add", Risk: "financial", Title: "top up transit card"}}
	d, err := keywordIntentClassifier{}.Classify(context.Background(), "top up my transit card by 100", nil, acts)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if d.Engine != IntentGatewayAct || d.Params["amount"] != "100" {
		t.Fatalf("decision = %+v", d)
	}
}
