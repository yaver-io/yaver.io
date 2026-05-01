package main

import "testing"

func TestFilterHostShareSessionsForDeviceFiltersAndSortsNewestActivityFirst(t *testing.T) {
	sessions := []HostShareSessionInfo{
		{
			SessionID:      "older-match",
			HostDeviceID:   "device-a",
			LastActivityAt: 100,
			StartedAt:      10,
		},
		{
			SessionID:      "other-device",
			HostDeviceID:   "device-b",
			LastActivityAt: 999,
			StartedAt:      20,
		},
		{
			SessionID:      "newer-match",
			HostDeviceID:   "device-a",
			LastActivityAt: 200,
			StartedAt:      30,
		},
	}

	got := filterHostShareSessionsForDevice(sessions, "device-a")
	if len(got) != 2 {
		t.Fatalf("len(filtered) = %d, want 2", len(got))
	}
	if got[0].SessionID != "newer-match" {
		t.Fatalf("first session = %q, want %q", got[0].SessionID, "newer-match")
	}
	if got[1].SessionID != "older-match" {
		t.Fatalf("second session = %q, want %q", got[1].SessionID, "older-match")
	}
}

func TestFilterHostShareSessionsForDeviceIgnoresBlankDeviceID(t *testing.T) {
	got := filterHostShareSessionsForDevice([]HostShareSessionInfo{
		{SessionID: "sess-1", HostDeviceID: "device-a"},
	}, "")
	if len(got) != 0 {
		t.Fatalf("len(filtered) = %d, want 0", len(got))
	}
}
