package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestGatewayOpsRegistered(t *testing.T) {
	want := map[string]bool{
		"gateway_query":       false,
		"gateway_intent":      false,
		"gateway_act":         false,
		"gateway_act_confirm": false,
	}
	for _, v := range listOpsVerbs() {
		if _, ok := want[v.Name]; ok {
			want[v.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing ops verb %s", name)
		}
	}
}

func TestGatewayOpsMissingRequiredFieldsReturnGatewayError(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	cases := []string{"gateway_query", "gateway_intent", "gateway_act", "gateway_act_confirm"}
	for _, verb := range cases {
		res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: verb, Payload: json.RawMessage(`{}`)})
		if res.OK {
			t.Fatalf("%s expected OK=false", verb)
		}
		if res.Code != "gateway_error" {
			t.Fatalf("%s expected gateway_error, got %#v", verb, res)
		}
	}
}

func TestGatewayOpsRejectMalformedJSON(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "gateway_query", Payload: json.RawMessage(`"bad"`)})
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload, got %#v", res)
	}
}

func TestGatewayOpsResultMapsError(t *testing.T) {
	res := gatewayOpsResult(map[string]interface{}{"error": "nope", "detail": "x"})
	if res.OK || res.Code != "gateway_error" || res.Error != "nope" {
		t.Fatalf("unexpected gateway error mapping: %#v", res)
	}
	res = gatewayOpsResult(map[string]interface{}{"ok": true})
	if !res.OK {
		t.Fatalf("expected OK=true, got %#v", res)
	}
}
