package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAutorunOpsRegisteredOwnerOnly(t *testing.T) {
	for _, name := range []string{"autorun_start", "autorun_status", "autorun_stop"} {
		opsRegistryMu.RLock()
		spec, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if spec.AllowGuest {
			t.Fatalf("%s must be owner-only", name)
		}
	}
}

func TestAutorunStartRejectsMissingSafetyBoundary(t *testing.T) {
	res := opsAutorunStartHandler(OpsContext{Ctx: context.Background()}, json.RawMessage(`{"task":"task.md","gate":"go test ./..."}`))
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected missing scopes to be rejected, got %#v", res)
	}
}

func TestAutorunStatusAndStopUnknownSession(t *testing.T) {
	status := opsAutorunStatusHandler(OpsContext{}, json.RawMessage(`{"id":"missing"}`))
	if status.OK || status.Code != "not_found" {
		t.Fatalf("unexpected status result: %#v", status)
	}
	stop := opsAutorunStopHandler(OpsContext{}, json.RawMessage(`{"id":"missing"}`))
	if stop.OK || stop.Code != "not_found" {
		t.Fatalf("unexpected stop result: %#v", stop)
	}
}
