package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The verdict is the line an operator acts on, so it must never soften a hard
// death into something reassuring.
func TestFlightVerdictReportsHardDeath(t *testing.T) {
	newestFirst := []FlightEvent{
		{Kind: flightKindUncleanStop, Detail: "macOS shutdown cause -128: power loss or hard reset"},
		{Kind: flightKindBoot, Detail: "agent started"},
	}
	got := flightVerdict(newestFirst)
	if !strings.Contains(got, "DIED HARD") {
		t.Errorf("verdict must say the box died hard, got %q", got)
	}
	if !strings.Contains(got, "power loss") {
		t.Errorf("verdict must carry the OS cause through, got %q", got)
	}
}

func TestFlightVerdictReportsGracefulStop(t *testing.T) {
	got := flightVerdict([]FlightEvent{
		{Kind: flightKindShutdown, Detail: "agent stopped on terminated"},
		{Kind: flightKindBoot, Detail: "agent started"},
	})
	if !strings.Contains(got, "GRACEFUL") {
		t.Errorf("a recorded shutdown is a graceful stop, got %q", got)
	}
	if strings.Contains(got, "DIED HARD") {
		t.Errorf("a graceful stop must never be reported as a hard death, got %q", got)
	}
}

// A `boot` as the newest record means the agent is up — OR that it went silent
// without warning. The verdict must not claim the box is healthy.
func TestFlightVerdictBootIsNotAClaimOfHealth(t *testing.T) {
	got := flightVerdict([]FlightEvent{{Kind: flightKindBoot, Detail: "agent started"}})
	if !strings.Contains(got, "RUNNING") {
		t.Errorf("expected the running case, got %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "silent") {
		t.Errorf("verdict must warn that a silent box stopped without warning, got %q", got)
	}
}

func TestFlightVerdictEmptyHistory(t *testing.T) {
	if got := flightVerdict(nil); !strings.Contains(got, "No verdict") {
		t.Errorf("empty history must not invent a verdict, got %q", got)
	}
}

// Ages are what make the history readable; an absolute stamp alone forces the
// reader to do arithmetic at 3am.
func TestRoundFlightAge(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{25 * time.Minute, "25m"},
		{5 * time.Hour, "5h"},
		{72 * time.Hour, "3d"},
	} {
		if got := roundFlightAge(tc.d); got != tc.want {
			t.Errorf("roundFlightAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatFlightTimeFallsBackOnUnparseableStamp(t *testing.T) {
	if got := formatFlightTime("not-a-time"); got != "not-a-time" {
		t.Errorf("an unparseable stamp must be shown verbatim rather than dropped, got %q", got)
	}
}

// A verb that isn't reachable through dispatchOps is not shipped, no matter
// what init() registered. This is the phone's path to the black box.
func TestFlightEventsVerbIsDispatchable(t *testing.T) {
	res := dispatchOps(OpsContext{
		Ctx:    context.Background(),
		Caller: "owner",
	}, OpsRequest{
		Machine: "auto",
		Verb:    "flight_events",
		Payload: json.RawMessage(`{}`),
	})
	if !res.OK {
		t.Fatalf("flight_events must dispatch, got %s (%s)", res.Error, res.Code)
	}
	initial, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("expected a map payload, got %T", res.Initial)
	}
	// The verdict is the whole value of the verb — events without an
	// interpretation just move the guesswork to the reader.
	if _, present := initial["verdict"]; !present {
		t.Error("flight_events must return a verdict, not just raw events")
	}
	if _, present := initial["events"]; !present {
		t.Error("flight_events must return the events")
	}
}

// An empty payload is the common call ("just show me") and must not be an error.
func TestFlightEventsVerbAcceptsEmptyPayload(t *testing.T) {
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"},
		OpsRequest{Machine: "auto", Verb: "flight_events"})
	if !res.OK {
		t.Fatalf("an omitted payload must be accepted, got %s (%s)", res.Error, res.Code)
	}
}

// A limit above the cap must clamp, not over-serve.
func TestFlightEventsVerbRejectsGarbagePayload(t *testing.T) {
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"},
		OpsRequest{Machine: "auto", Verb: "flight_events", Payload: json.RawMessage(`{"limit":"lots"}`)})
	if res.OK {
		t.Error("a non-integer limit must be rejected rather than silently ignored")
	}
}

// An alias is what a user actually types (`yaver ssh mac-mini`), and the --device
// help promises it. The first cut matched only name/deviceId, so `--device
// mac-mini` fell through as a raw id and 404'd against a device that exists.
func TestFlightDeviceAliasResolvesExactly(t *testing.T) {
	devices := []DeviceInfo{
		{DeviceID: "229aeb03-b877-41aa-ba60-2daf785cd4a5", Name: "Some-Mac-mini.local", Alias: "mac-mini"},
		{DeviceID: "6e8db080-0000-0000-0000-000000000000", Name: "mac-mini", Alias: "laptop"},
	}
	// The alias must win over another device whose NAME collides with it —
	// otherwise a coincidental name makes the alias ambiguous.
	got, err := resolveFlightDeviceFrom(devices, "mac-mini")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "229aeb03-b877-41aa-ba60-2daf785cd4a5" {
		t.Errorf("alias must resolve to its own device, got %q", got)
	}
}

func TestFlightDeviceIDResolvesExactly(t *testing.T) {
	devices := []DeviceInfo{{DeviceID: "abc-123", Name: "box", Alias: "b"}}
	got, err := resolveFlightDeviceFrom(devices, "abc-123")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "abc-123" {
		t.Errorf("exact deviceId must win, got %q", got)
	}
}

func TestFlightUnknownDevicePassesThrough(t *testing.T) {
	devices := []DeviceInfo{{DeviceID: "abc-123", Name: "box", Alias: "b"}}
	// An unknown string may still be a raw id the list didn't show; let the
	// backend decide rather than refusing locally.
	got, err := resolveFlightDeviceFrom(devices, "zzz")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "zzz" {
		t.Errorf("unknown device must pass through to the backend, got %q", got)
	}
}
