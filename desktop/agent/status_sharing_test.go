package main

import (
	"strings"
	"testing"
	"time"
)

func TestSummarizeGuestAccessDefaults(t *testing.T) {
	got := summarizeGuestAccess(nil)
	want := "default: unlimited, all runners, all shared devices"
	if got != want {
		t.Fatalf("summarizeGuestAccess(nil) = %q, want %q", got, want)
	}
}

func TestSummarizeGuestAccessConfigured(t *testing.T) {
	limit := 3600
	shareAllDevices := true
	useHostKeys := true
	allowGuestKeys := false
	requireIsolation := true
	cfg := &GuestConfig{
		UsageMode:                 "scheduled",
		DailyTokenLimit:           &limit,
		AllowedRunners:            []string{"claude", "codex"},
		ResourcePreset:            "desktop-control",
		ShareAllDevices:           &shareAllDevices,
		UseHostAPIKeys:            &useHostKeys,
		AllowGuestProvidedAPIKeys: &allowGuestKeys,
		RequireIsolation:          &requireIsolation,
	}

	got := summarizeGuestAccess(cfg)
	for _, wantPart := range []string{
		"mode=scheduled",
		"limit=3600s/day",
		"runners=claude,codex",
		"preset=desktop-control",
		"devices=all",
		"hostkeys=true",
		"guestkeys=false",
		"isolation=true",
	} {
		if !strings.Contains(got, wantPart) {
			t.Fatalf("summary %q missing %q", got, wantPart)
		}
	}
}

func TestStatusUnixMilliOrDash(t *testing.T) {
	if got := statusUnixMilliOrDash(0); got != "-" {
		t.Fatalf("statusUnixMilliOrDash(0) = %q, want -", got)
	}
	ts := time.Date(2026, time.April, 17, 10, 0, 0, 0, time.UTC).UnixMilli()
	if got := statusUnixMilliOrDash(ts); got != "2026-04-17" {
		t.Fatalf("statusUnixMilliOrDash(valid) = %q", got)
	}
}

func TestStatusTimeOrDash(t *testing.T) {
	if got := statusTimeOrDash(""); got != "-" {
		t.Fatalf("statusTimeOrDash(empty) = %q, want -", got)
	}
	if got := statusTimeOrDash("not-a-time"); got != "not-a-time" {
		t.Fatalf("statusTimeOrDash(invalid) = %q, want passthrough", got)
	}
	got := statusTimeOrDash("2026-04-17T10:30:00Z")
	if !strings.HasPrefix(got, "2026-04-17 ") {
		t.Fatalf("statusTimeOrDash(valid) = %q, want formatted local date-time", got)
	}
}

func TestShortStatusUserID(t *testing.T) {
	if got := shortStatusUserID("user1234"); got != "user1234" {
		t.Fatalf("shortStatusUserID(short) = %q", got)
	}
	if got := shortStatusUserID("1234567890"); got != "12345678..." {
		t.Fatalf("shortStatusUserID(long) = %q", got)
	}
}
