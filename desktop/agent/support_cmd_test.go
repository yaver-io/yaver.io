package main

import "testing"

func TestSupportRelayBasesIncludesCachedAndDedupes(t *testing.T) {
	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{HttpURL: "https://relay-a.example.com/"},
			{HttpURL: "https://relay-b.example.com"},
		},
		CachedRelayServers: []RelayServerConfig{
			{HttpURL: "https://relay-b.example.com/"},
			{HttpURL: "https://relay-c.example.com"},
			{HttpURL: ""},
		},
	}

	got := supportRelayBases(cfg)
	want := []string{
		"https://relay-a.example.com",
		"https://relay-b.example.com",
		"https://relay-c.example.com",
	}
	if len(got) != len(want) {
		t.Fatalf("supportRelayBases() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supportRelayBases()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
