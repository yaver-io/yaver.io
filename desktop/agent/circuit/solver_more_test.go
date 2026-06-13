package circuit

import (
	"context"
	"math"
	"testing"
)

// two DC sources into a node via equal resistors → superposition midpoint.
func TestSuperposition(t *testing.T) {
	// V1=10 thru 1k, V2=0 thru 1k into node m → V(m)=5
	res := simOp(t, `* superpos
V1 a 0 10
V2 b 0 0
R1 a m 1k
R2 b m 1k
.end`)
	if got := res.NodeVoltages["m"]; math.Abs(got-5) > 1e-6 {
		t.Fatalf("V(m)=%v, want 5", got)
	}
}

// DC sweep of a divider should be perfectly linear: V(out)=0.5*Vin.
func TestDCSweepLinear(t *testing.T) {
	nl := mustParse(t, `* sweep
V1 in 0 0
R1 in out 1k
R2 out 0 1k
.end`)
	res, err := NewBuiltinBackend().Simulate(context.Background(), nl,
		Analysis{Type: "dc", SweepSrc: "V1", SweepStart: 0, SweepStop: 10, SweepStep: 1})
	if err != nil {
		t.Fatalf("dc sweep: %v", err)
	}
	if len(res.Samples) != 11 {
		t.Fatalf("got %d sweep points, want 11", len(res.Samples))
	}
	// columns: [V1, V(in), V(out)] (sorted nets in, out)
	outCol := -1
	for i, s := range res.Signals {
		if s == "V(out)" {
			outCol = i
		}
	}
	for _, row := range res.Samples {
		if math.Abs(row[outCol]-0.5*row[0]) > 1e-9 {
			t.Fatalf("nonlinear sweep: Vin=%v Vout=%v", row[0], row[outCol])
		}
	}
}

// RLC transient must stay finite and settle (no NaN/Inf blow-up).
func TestRLCTransientStable(t *testing.T) {
	nl := mustParse(t, `* rlc
V1 1 0 PULSE(0 1 0 1e-9 0 1 2)
R1 1 2 100
L1 2 3 1m
C1 3 0 1u
.end`)
	res, err := NewBuiltinBackend().Simulate(context.Background(), nl, Analysis{Type: "tran", TStop: 10e-3, TStep: 5e-6})
	if err != nil {
		t.Fatalf("rlc tran: %v", err)
	}
	last := res.Samples[len(res.Samples)-1]
	for i, v := range last {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("non-finite sample col %d: %v", i, v)
		}
	}
	// final V(3) should settle near the 1 V drive (series R/L, cap holds it)
	col := -1
	for i, s := range res.Signals {
		if s == "V(3)" {
			col = i
		}
	}
	if math.Abs(last[col]-1) > 0.1 {
		t.Fatalf("RLC final V(3)=%v, want ≈1", last[col])
	}
}

// RC high-pass: at f≫fc gain→0 dB, at f≪fc gain rolls off.
func TestACHighPass(t *testing.T) {
	nl := mustParse(t, `* hp
V1 in 0 AC 1
C1 in out 100n
R1 out 0 1k
.end`)
	res, err := NewBuiltinBackend().Simulate(context.Background(), nl, Analysis{Type: "ac", FStart: 1, FStop: 1e6, Points: 30})
	if err != nil {
		t.Fatalf("ac: %v", err)
	}
	col := -1
	for i, s := range res.Signals {
		if s == "V(out)dB" {
			col = i
		}
	}
	first := res.Samples[0][col]                 // low freq → attenuated
	last := res.Samples[len(res.Samples)-1][col] // high freq → ~0 dB
	if !(last > first+20) {
		t.Fatalf("high-pass not rising: low=%v dB high=%v dB", first, last)
	}
	if math.Abs(last) > 1.0 {
		t.Fatalf("high-pass passband gain=%v dB, want ≈0", last)
	}
}

// a pure connection-list (EPLAN) must refuse simulation but allow ERC.
func TestConnectionListNotSimulatable(t *testing.T) {
	nl, err := ParseConnectionList("net,component,pin\nL1,K1,1\nN1,K1,2\nN1,M1,A\nPE,M1,B\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := NewController(Config{Engine: "builtin", Netlist: nl})
	if _, err := c.Simulate(context.Background(), Analysis{Type: "op"}); err == nil {
		t.Fatal("expected simulate to refuse a connection list")
	}
	_ = c.ERC() // must not panic
}
