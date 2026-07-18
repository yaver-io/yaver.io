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

func TestAutorunStopAllCancelsEveryRunningSessionOnly(t *testing.T) {
	m := &autorunSessionManager{sessions: make(map[string]*autorunSession)}
	running, stopRunning := context.WithCancel(context.Background())
	defer stopRunning()
	m.sessions["live"] = &autorunSession{ID: "live", Status: "running", cancel: stopRunning}
	m.sessions["done"] = &autorunSession{ID: "done", Status: "completed"} // cancel==nil: already finished

	previous := autorunSessions
	autorunSessions = m
	defer func() { autorunSessions = previous }()

	res := opsAutorunStopAllHandler(OpsContext{}, json.RawMessage(`{}`))
	if !res.OK {
		t.Fatalf("stop_all failed: %#v", res)
	}
	initial, ok := res.Initial.(map[string]interface{})
	if !ok || initial["count"] != 1 {
		t.Fatalf("only the running session should be reported stopped: %#v", res.Initial)
	}
	if running.Err() == nil {
		t.Fatal("running session was not cancelled")
	}
	if m.sessions["done"].Status != "completed" {
		t.Fatalf("a finished session must not be re-marked: %q", m.sessions["done"].Status)
	}
}

func TestAutorunOpsRegisteredOwnerOnly(t *testing.T) {
	for _, name := range []string{"autorun_start", "autorun_status", "autorun_stop", "autorun_stop_all"} {
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

func TestAutorunSessionViewCarriesMaxIters(t *testing.T) {
	m := &autorunSessionManager{sessions: make(map[string]*autorunSession)}
	s := &autorunSession{
		ID:       "autorun-1",
		Slot:     "widget:codex",
		Task:     "/repo/tasks/widget.md",
		MaxIters: 9,
	}
	if got := m.view(s); got.MaxIters != 9 {
		t.Fatalf("view MaxIters = %d, want 9", got.MaxIters)
	}
}
