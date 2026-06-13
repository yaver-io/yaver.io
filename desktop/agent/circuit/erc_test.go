package circuit

import "testing"

func findRule(rep ERCReport, rule string) *ERCFinding {
	for i := range rep.Findings {
		if rep.Findings[i].Rule == rule {
			return &rep.Findings[i]
		}
	}
	return nil
}

func TestERCClean(t *testing.T) {
	nl, _ := ParseSPICE(`* clean
V1 1 0 5
R1 1 2 1k
R2 2 0 1k
.end`)
	rep := RunERC(nl)
	if !rep.OK {
		t.Fatalf("expected clean ERC, got %+v", rep.Findings)
	}
}

func TestERCNoGround(t *testing.T) {
	nl, _ := ParseSPICE(`* float
V1 1 2 5
R1 1 2 1k
.end`)
	rep := RunERC(nl)
	if findRule(rep, "no-ground") == nil {
		t.Fatalf("expected no-ground finding, got %+v", rep.Findings)
	}
	if rep.OK {
		t.Fatal("expected ERC to fail without ground")
	}
}

func TestERCDanglingNet(t *testing.T) {
	nl, _ := ParseSPICE(`* dangle
V1 1 0 5
R1 1 2 1k
R2 2 0 1k
R3 2 3 1k
.end`)
	rep := RunERC(nl)
	d := findRule(rep, "dangling-net")
	if d == nil || d.Net != "3" {
		t.Fatalf("expected dangling net 3, got %+v", rep.Findings)
	}
}

func TestERCVoltageDomainMismatch(t *testing.T) {
	nl, _ := ParseSPICE(`* domains
V1 mains 0 230
V2 selv 0 12
R1 mains selv 1k
.end`)
	nl.NodeDomains = map[string]float64{"mains": 230, "selv": 12}
	rep := RunERC(nl)
	f := findRule(rep, "voltage-domain-mismatch")
	if f == nil {
		t.Fatalf("expected voltage-domain-mismatch, got %+v", rep.Findings)
	}
	if f.Severity != SevError {
		t.Fatalf("expected error severity, got %v", f.Severity)
	}
}

func TestERCDomainOKThroughCap(t *testing.T) {
	// a coupling capacitor between domains is an allowed (non-DC) bridge
	nl, _ := ParseSPICE(`* coupled
V1 a 0 230
V2 b 0 12
C1 a b 1u
.end`)
	nl.NodeDomains = map[string]float64{"a": 230, "b": 12}
	rep := RunERC(nl)
	if findRule(rep, "voltage-domain-mismatch") != nil {
		t.Fatalf("capacitor coupling should not trip domain mismatch: %+v", rep.Findings)
	}
}
