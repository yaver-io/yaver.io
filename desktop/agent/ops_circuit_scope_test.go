package main

import (
	"context"
	"encoding/json"
	"testing"
)

// The circuit capability scope is the auth layer that lets an external product
// (Talos, OCPP) drive ONLY the circuit simulator on a Yaver box and nothing
// else. These tests pin that isolation: a "circuit"-scoped guest can invoke
// circuit_* verbs and is refused every other verb — including verbs that are
// AllowGuest for normal guest tiers.

func TestCapabilityScopeAuthzMatrix(t *testing.T) {
	ownerOnly := opsVerbSpec{Name: "exec_command", AllowGuest: false}
	guestOK := opsVerbSpec{Name: "feedback_submit", AllowGuest: true}
	circuitVerb := opsVerbSpec{Name: "circuit_simulate", AllowGuest: false}

	cases := []struct {
		scope string
		verb  string
		spec  opsVerbSpec
		want  bool
	}{
		// circuit scope: ONLY circuit_* verbs, nothing else
		{"circuit", "circuit_simulate", circuitVerb, true},
		{"circuit", "circuit_erc", circuitVerb, true},
		{"circuit", "exec_command", ownerOnly, false},  // no host exec
		{"circuit", "feedback_submit", guestOK, false}, // not even AllowGuest verbs
		{"circuit", "vault_get", ownerOnly, false},     // no vault
		// non-capability tiers keep the legacy AllowGuest behavior unchanged
		{"full", "feedback_submit", guestOK, true},
		{"full", "exec_command", ownerOnly, false},
		{"", "feedback_submit", guestOK, true},
		{"", "circuit_simulate", circuitVerb, false}, // a plain guest still can't reach circuit
		{"deploy", "exec_command", ownerOnly, false},
	}
	for _, c := range cases {
		if got := guestVerbAllowed(c.scope, c.verb, c.spec); got != c.want {
			t.Errorf("guestVerbAllowed(scope=%q verb=%q allowGuest=%v) = %v, want %v", c.scope, c.verb, c.spec.AllowGuest, got, c.want)
		}
	}

	if !isCapabilityScope("circuit") {
		t.Error("circuit should be a capability scope")
	}
	if isCapabilityScope("full") || isCapabilityScope("deploy") || isCapabilityScope("") {
		t.Error("broad tiers must NOT be capability scopes")
	}
}

// End-to-end through dispatchOps: a circuit-scoped guest is refused both an
// owner-only verb AND an AllowGuest verb (the denial is decided at the gate,
// before any machine routing). Proves the OpsContext.Scope wiring is live.
func TestCircuitScopeDispatchIsolation(t *testing.T) {
	registerOpsVerb(opsVerbSpec{Name: "zzz_probe_owner_iso", AllowGuest: false, Handler: func(OpsContext, json.RawMessage) OpsResult { return OpsResult{OK: true} }})
	registerOpsVerb(opsVerbSpec{Name: "zzz_probe_guest_iso", AllowGuest: true, Handler: func(OpsContext, json.RawMessage) OpsResult { return OpsResult{OK: true} }})

	circuitGuest := OpsContext{Ctx: context.Background(), Server: &HTTPServer{}, Caller: "guest", Scope: "circuit"}

	// owner-only verb → refused
	if r := dispatchOps(circuitGuest, OpsRequest{Verb: "zzz_probe_owner_iso", Machine: "local"}); r.Code != "unauthorized" {
		t.Fatalf("circuit guest reached owner verb: code=%q ok=%v", r.Code, r.OK)
	}
	// AllowGuest verb → STILL refused (capability scope is a strict allowlist)
	if r := dispatchOps(circuitGuest, OpsRequest{Verb: "zzz_probe_guest_iso", Machine: "local"}); r.Code != "unauthorized" {
		t.Fatalf("circuit guest reached a non-circuit AllowGuest verb: code=%q ok=%v", r.Code, r.OK)
	}

	// a normal full guest may still use the AllowGuest verb (no regression)
	fullGuest := OpsContext{Ctx: context.Background(), Server: &HTTPServer{}, Caller: "guest", Scope: "full"}
	if r := dispatchOps(fullGuest, OpsRequest{Verb: "zzz_probe_guest_iso", Machine: "local"}); r.Code == "unauthorized" {
		t.Fatalf("full guest wrongly denied an AllowGuest verb: %q", r.Error)
	}
}
