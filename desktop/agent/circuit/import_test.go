package circuit

import (
	"context"
	"math"
	"testing"
)

func TestImportKiCadDivider(t *testing.T) {
	// minimal KiCad netlist export: V1 + R1 + R2 divider
	net := `(export (version "E")
  (components
    (comp (ref "V1") (value "10"))
    (comp (ref "R1") (value "1k"))
    (comp (ref "R2") (value "1k")))
  (nets
    (net (code "1") (name "/in")
      (node (ref "V1") (pin "1"))
      (node (ref "R1") (pin "1")))
    (net (code "2") (name "/mid")
      (node (ref "R1") (pin "2"))
      (node (ref "R2") (pin "1")))
    (net (code "0") (name "GND")
      (node (ref "V1") (pin "2"))
      (node (ref "R2") (pin "2")))))`
	nl, err := ParseKiCad(net)
	if err != nil {
		t.Fatalf("kicad parse: %v", err)
	}
	if len(nl.Elements) != 3 {
		t.Fatalf("got %d elements, want 3: %+v", len(nl.Elements), nl.Elements)
	}
	res, err := NewBuiltinBackend().Simulate(context.Background(), nl, Analysis{Type: "op"})
	if err != nil {
		t.Fatalf("sim imported kicad: %v", err)
	}
	if got := res.NodeVoltages["mid"]; math.Abs(got-5) > 1e-6 {
		t.Fatalf("imported divider V(mid)=%v, want 5", got)
	}
}

func TestImportConnectionListNetGraph(t *testing.T) {
	csv := `net,component,pin
L1,K1,A1
N1,K1,A2
N1,M1,U
PE,M1,GND
`
	nl, err := ParseConnectionList(csv)
	if err != nil {
		t.Fatalf("eplan parse: %v", err)
	}
	// K1 bridges L1<->N1; M1 bridges N1<->PE → component-as-edge graph
	if len(nl.Elements) < 2 {
		t.Fatalf("expected ≥2 connection edges, got %+v", nl.Elements)
	}
	info := nl.Describe()
	if info.Simulatable {
		t.Fatal("connection list must not be simulatable")
	}
	// ERC should run cleanly enough (graph is connected via components)
	_ = RunERC(nl)
}

func TestImportConnectionListWirelist(t *testing.T) {
	csv := `From Connector,From Pin,Cable,To Connector,To Pin
X1,1,W001,X2,5
X1,2,W002,X2,6
`
	nl, err := ParseConnectionList(csv)
	if err != nil {
		t.Fatalf("wirelist parse: %v", err)
	}
	if len(nl.Elements) != 2 {
		t.Fatalf("expected 2 wires, got %d: %+v", len(nl.Elements), nl.Elements)
	}
	if nl.Elements[0].Nodes[0] != "X1:1" || nl.Elements[0].Nodes[1] != "X2:5" {
		t.Fatalf("unexpected terminals: %+v", nl.Elements[0].Nodes)
	}
}

func TestSniffFormat(t *testing.T) {
	if f := sniffFormat("(export (version E))"); f != "kicad" {
		t.Errorf("kicad sniff = %q", f)
	}
	if f := sniffFormat("V1 1 0 5\nR1 1 0 1k"); f != "spice" {
		t.Errorf("spice sniff = %q", f)
	}
	if f := sniffFormat("net,component,pin\nL1,K1,1"); f != "eplan" {
		t.Errorf("eplan sniff = %q", f)
	}
}
