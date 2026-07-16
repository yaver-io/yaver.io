package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAutorunSessionContextOutlivesRequestAndStopsExplicitly(t *testing.T) {
	type contextKey string
	request, cancelRequest := context.WithCancel(context.WithValue(context.Background(), contextKey("trace"), "kept"))
	session, stopSession := autorunSessionContext(request)

	cancelRequest()
	if err := session.Err(); err != nil {
		t.Fatalf("session inherited request cancellation: %v", err)
	}
	if got := session.Value(contextKey("trace")); got != "kept" {
		t.Fatalf("session lost request context value: %v", got)
	}

	stopSession()
	if err := session.Err(); err != context.Canceled {
		t.Fatalf("explicit stop did not cancel session: %v", err)
	}
}

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
