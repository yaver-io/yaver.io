package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestDispatchOpsUnknownVerb — an agent calling an undefined verb should
// get a structured error with the "unknown_verb" code, not a panic
// and not a generic 500. Stable code is the contract agents branch on.
func TestDispatchOpsUnknownVerb(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "does_not_exist"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "unknown_verb" {
		t.Fatalf("want code=unknown_verb, got %q (err=%q)", res.Code, res.Error)
	}
	if !strings.Contains(res.Error, "does_not_exist") {
		t.Fatalf("error should mention the verb, got %q", res.Error)
	}
}

// TestDispatchOpsRemoteRouting — cross-machine routing now tries to
// proxy through the existing peer-proxy plumbing. In a unit test
// (no Convex, no relay) that resolution fails, and the dispatcher
// must return a typed `remote_failed` error rather than leaking the
// underlying transport message or panicking. Agents branch on the
// stable code to decide whether to retry / use a different machine.
func TestDispatchOpsRemoteRouting(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "some-device-id", Verb: "info"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	// Either remote_failed (transport error — the common case in tests)
	// or remote_malformed (if a proxy somehow returned junk). Both are
	// acceptable fail-fast modes; what we want to prove is that the
	// dispatcher doesn't fall through to local execution silently.
	if res.Code != "remote_failed" && res.Code != "remote_malformed" && res.Code != "unauthorized" {
		t.Fatalf("want remote_failed|remote_malformed|unauthorized, got %q (err=%q)", res.Code, res.Error)
	}
}

// TestDispatchOpsPrimaryAliasUnset — asking for the primary device
// when the user hasn't set one returns a typed error rather than
// falling through to local. This is the default state for fresh
// installs and the check catches the "forgot to set primary" UX.
func TestDispatchOpsPrimaryAliasUnset(t *testing.T) {
	// No HTTPServer context — resolver fails cleanly.
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "primary", Verb: "info"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "invalid_machine" {
		t.Fatalf("want invalid_machine, got %q", res.Code)
	}
}

// TestOpsInfoLocal — the built-in info verb should work against a nil
// server (specs only), returning the baseline machine fingerprint so
// agents can orient themselves before any further ops.
func TestOpsInfoLocal(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "info"})
	if !res.OK {
		t.Fatalf("info failed: %s (%s)", res.Error, res.Code)
	}
	m, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("initial not a map, got %T", res.Initial)
	}
	for _, k := range []string{"hostname", "platform", "arch", "numCPU", "localIPs"} {
		if _, present := m[k]; !present {
			t.Errorf("info output missing %q", k)
		}
	}
}

// TestOpsGuestScope — verbs that don't opt in to AllowGuest must be
// refused for guest callers with the "unauthorized" code. The default
// is owner-only; opt-in AllowGuest is how we get case-by-case sharing
// without accidentally giving guests a shell.
func TestOpsGuestScope(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "guest"}
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "run"})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "unauthorized" {
		t.Fatalf("want code=unauthorized, got %q", res.Code)
	}
}

// TestListOpsVerbs — the registry must be deterministic (sorted) so
// mobile-headless and the CLI render a stable list, and it must
// include the verbs this cycle ships.
func TestListOpsVerbs(t *testing.T) {
	got := map[string]bool{}
	prev := ""
	for _, v := range listOpsVerbs() {
		if prev != "" && v.Name <= prev {
			t.Errorf("verbs not sorted: %q came after %q", v.Name, prev)
		}
		prev = v.Name
		got[v.Name] = true
	}
	for _, want := range []string{"info", "run"} {
		if !got[want] {
			t.Errorf("verb %q missing from registry", want)
		}
	}
}

// TestOpsBadPayload — run with malformed JSON payload returns a
// typed bad_payload error rather than a 500 or a panic.
func TestOpsBadPayload(t *testing.T) {
	octx := OpsContext{Ctx: context.Background(), Caller: "owner"}
	// Payload is "garbage" — not valid JSON for opsRunPayload.
	res := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "run", Payload: json.RawMessage(`"garbage"`)})
	if res.OK {
		t.Fatal("expected OK=false")
	}
	if res.Code != "bad_payload" {
		t.Fatalf("want code=bad_payload, got %q (err=%q)", res.Code, res.Error)
	}
}
