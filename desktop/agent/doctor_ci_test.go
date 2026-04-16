package main

import "testing"

func TestParseKeyValueLines(t *testing.T) {
	out := `
 sleep                0
 disksleep            0
 autorestart          1
 powernap             0
`
	got := parseKeyValueLines(out)
	want := map[string]string{
		"sleep":       "0",
		"disksleep":   "0",
		"autorestart": "1",
		"powernap":    "0",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("parseKeyValueLines()[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestPMSetIntValue(t *testing.T) {
	values := map[string]string{
		"sleep": "0",
		"bad":   "x",
	}
	if got := pmsetIntValue(values, "sleep"); got != 0 {
		t.Fatalf("pmsetIntValue(sleep) = %d, want 0", got)
	}
	if got := pmsetIntValue(values, "bad"); got != -1 {
		t.Fatalf("pmsetIntValue(bad) = %d, want -1", got)
	}
	if got := pmsetIntValue(values, "missing"); got != -1 {
		t.Fatalf("pmsetIntValue(missing) = %d, want -1", got)
	}
}
