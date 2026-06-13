package circuit

import (
	"math"
	"testing"
)

func TestParseValueSuffixes(t *testing.T) {
	cases := map[string]float64{
		"1k": 1e3, "10k": 1e4, "4.7k": 4700, "1meg": 1e6, "2.2m": 2.2e-3,
		"100u": 1e-4, "1n": 1e-9, "10p": 1e-11, "1f": 1e-15, "1g": 1e9,
		"3.3": 3.3, "1k5": 1e3, // trailing junk ignored after suffix
	}
	for in, want := range cases {
		got, err := parseValue(in)
		if err != nil {
			t.Fatalf("parseValue(%q): %v", in, err)
		}
		if math.Abs(got-want) > math.Abs(want)*1e-9+1e-18 {
			t.Errorf("parseValue(%q)=%v want %v", in, got, want)
		}
	}
}

func TestNetlistRoundTrip(t *testing.T) {
	deck := `* round trip
V1 1 0 DC 12
R1 1 2 4.7k
C1 2 0 100n
L1 2 3 10m
.end`
	nl, err := ParseSPICE(deck)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(nl.Elements) != 4 {
		t.Fatalf("got %d elements, want 4", len(nl.Elements))
	}
	// re-parse the emitted deck and compare values
	nl2, err := ParseSPICE(nl.EmitSPICE())
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if len(nl2.Elements) != 4 {
		t.Fatalf("round-trip got %d elements, want 4", len(nl2.Elements))
	}
	byName := map[string]Element{}
	for _, e := range nl2.Elements {
		byName[e.Name] = e
	}
	if math.Abs(byName["R1"].Value-4700) > 1e-6 {
		t.Errorf("R1 round-trip value = %v", byName["R1"].Value)
	}
	if math.Abs(byName["C1"].Value-100e-9) > 1e-18 {
		t.Errorf("C1 round-trip value = %v", byName["C1"].Value)
	}
}

func TestDescribeCounts(t *testing.T) {
	nl, _ := ParseSPICE(`* d
V1 in 0 5
R1 in out 1k
R2 out 0 2k
.end`)
	info := nl.Describe()
	if !info.HasGround {
		t.Error("expected ground")
	}
	if !info.Simulatable {
		t.Error("expected simulatable")
	}
	if info.ElementCount != 3 {
		t.Errorf("element count = %d", info.ElementCount)
	}
	if len(info.Sources) != 1 || info.Sources[0] != "V1" {
		t.Errorf("sources = %v", info.Sources)
	}
}
