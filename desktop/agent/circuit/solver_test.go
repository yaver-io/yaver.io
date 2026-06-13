package circuit

import (
	"context"
	"math"
	"testing"
)

func mustParse(t *testing.T, deck string) Netlist {
	t.Helper()
	nl, err := ParseSPICE(deck)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return nl
}

func simOp(t *testing.T, deck string) SimResult {
	t.Helper()
	res, err := NewBuiltinBackend().Simulate(context.Background(), mustParse(t, deck), Analysis{Type: "op"})
	if err != nil {
		t.Fatalf("op sim: %v", err)
	}
	return res
}

func TestResistiveDivider(t *testing.T) {
	res := simOp(t, `* divider
V1 1 0 10
R1 1 2 1k
R2 2 0 1k
.end`)
	if got := res.NodeVoltages["2"]; math.Abs(got-5) > 1e-6 {
		t.Fatalf("V(2)=%v, want 5", got)
	}
	if got := res.NodeVoltages["1"]; math.Abs(got-10) > 1e-6 {
		t.Fatalf("V(1)=%v, want 10", got)
	}
	// source current = 10V / 2k = 5 mA, flowing out of + node through source
	if got := math.Abs(res.BranchCurrents["V1"]); math.Abs(got-5e-3) > 1e-9 {
		t.Fatalf("I(V1)=%v, want 5e-3", got)
	}
}

func TestUnevenDivider(t *testing.T) {
	// 10V across 2k + 8k → V(mid) = 10 * 8k/10k = 8
	res := simOp(t, `* uneven
V1 in 0 10
R1 in mid 2k
R2 mid 0 8k
.end`)
	if got := res.NodeVoltages["mid"]; math.Abs(got-8) > 1e-6 {
		t.Fatalf("V(mid)=%v, want 8", got)
	}
}

func TestCurrentSourceIntoResistor(t *testing.T) {
	// 1 mA into a 1k resistor to ground → 1 V
	res := simOp(t, `* isrc
I1 0 a 1m
R1 a 0 1k
.end`)
	if got := res.NodeVoltages["a"]; math.Abs(got-1) > 1e-6 {
		t.Fatalf("V(a)=%v, want 1", got)
	}
}

func TestRCTransientTimeConstant(t *testing.T) {
	// step 0→10 V through 1k into 1u → tau = 1 ms; at t=tau V = 10(1-1/e)=6.321
	deck := `* rc
V1 1 0 PULSE(0 10 0 1e-9 0 1 2)
R1 1 2 1k
C1 2 0 1u
.end`
	res, err := NewBuiltinBackend().Simulate(context.Background(), mustParse(t, deck),
		Analysis{Type: "tran", TStop: 5e-3, TStep: 2e-6})
	if err != nil {
		t.Fatalf("tran: %v", err)
	}
	// find column for V(2)
	col := -1
	for i, s := range res.Signals {
		if s == "V(2)" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no V(2) signal in %v", res.Signals)
	}
	// sample nearest t = 1ms
	want := 10 * (1 - 1/math.E)
	var got float64
	bestDt := math.Inf(1)
	for _, row := range res.Samples {
		if dt := math.Abs(row[0] - 1e-3); dt < bestDt {
			bestDt, got = dt, row[col]
		}
	}
	if math.Abs(got-want) > 0.15 {
		t.Fatalf("V(2)@1ms=%v, want ≈%v", got, want)
	}
	// final value approaches 10
	last := res.Samples[len(res.Samples)-1][col]
	if math.Abs(last-10) > 0.1 {
		t.Fatalf("V(2) final=%v, want ≈10", last)
	}
}

func TestACLowPassCutoff(t *testing.T) {
	// RC low-pass, fc = 1/(2π·1k·1u) ≈ 159.15 Hz → -3.01 dB at fc
	deck := `* lp
V1 1 0 DC 0 AC 1
R1 1 2 1k
C1 2 0 1u
.end`
	res, err := NewBuiltinBackend().Simulate(context.Background(), mustParse(t, deck),
		Analysis{Type: "ac", FStart: 1, FStop: 1e5, Points: 50})
	if err != nil {
		t.Fatalf("ac: %v", err)
	}
	col := -1
	for i, s := range res.Signals {
		if s == "V(2)dB" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no V(2)dB signal in %v", res.Signals)
	}
	fc := 1 / (2 * math.Pi * 1e3 * 1e-6)
	var got float64
	bestDf := math.Inf(1)
	for _, row := range res.Samples {
		if df := math.Abs(row[0] - fc); df < bestDf {
			bestDf, got = df, row[col]
		}
	}
	if math.Abs(got-(-3.01)) > 0.4 {
		t.Fatalf("gain@fc=%v dB, want ≈-3.01", got)
	}
}

func TestDiodeForwardDrop(t *testing.T) {
	// 1 V through 1k into a diode to ground → Vd in the silicon range (~0.4-0.75)
	res := simOp(t, `* diode
V1 1 0 1
R1 1 2 1k
D1 2 0 Dmod
.end`)
	vd := res.NodeVoltages["2"]
	if vd < 0.3 || vd > 0.8 {
		t.Fatalf("diode drop V(2)=%v, want 0.3..0.8", vd)
	}
	// current consistency: (1 - Vd)/1k should be > 0
	if (1-vd)/1e3 <= 0 {
		t.Fatalf("non-conducting diode? V(2)=%v", vd)
	}
}

func TestInductorDCShort(t *testing.T) {
	// at DC an inductor is a wire → V(2) == V(1) == 5 (divider through R then L to gnd? )
	// V1 1 0 5 ; R1 1 2 1k ; L1 2 0 1m → DC: L shorts node2 to gnd → V(2)=0
	res := simOp(t, `* ind
V1 1 0 5
R1 1 2 1k
L1 2 0 1m
.end`)
	if got := res.NodeVoltages["2"]; math.Abs(got) > 1e-6 {
		t.Fatalf("V(2)=%v, want 0 (inductor shorts to gnd at DC)", got)
	}
}
