package cell

import (
	"context"
	"errors"
	"testing"

	"github.com/yaver-io/agent/arm"
)

// fakePresenter records moves and verifies; programmable to fail/obstruct.
type fakePresenter struct {
	moves       []string // labels of waypoints moved to
	verifies    []string // expectations checked
	estops      int
	inserts     int
	failInsert  bool
	failVerify  map[string]bool // expectation -> fail
	obstructAt  string          // waypoint label that obstructs
	failMoveAt  string          // waypoint label that fails (non-obstruction)
}

func (f *fakePresenter) MoveWaypoint(_ context.Context, wp arm.Waypoint) Outcome {
	f.moves = append(f.moves, wp.Label)
	if wp.Label == f.obstructAt {
		return Outcome{OK: false, Obstruction: true, Code: "obstruction"}
	}
	if wp.Label == f.failMoveAt {
		return Outcome{OK: false, Code: "backend", Error: "move failed"}
	}
	return Outcome{OK: true}
}

func (f *fakePresenter) Verify(_ context.Context, expectation string) Outcome {
	f.verifies = append(f.verifies, expectation)
	if f.failVerify[expectation] {
		return Outcome{OK: false, Code: "verify", Error: "did not verify"}
	}
	return Outcome{OK: true}
}

func (f *fakePresenter) ForceInsert(_ context.Context, _ arm.Axis6, _, _ float64) Outcome {
	f.inserts++
	if f.failInsert {
		return Outcome{OK: false, Code: "insert", Error: "did not seat"}
	}
	return Outcome{OK: true}
}

func (f *fakePresenter) EStop(context.Context) error { f.estops++; return nil }

func wp(label string) arm.Waypoint { return arm.Waypoint{Label: label, Joints: map[string]float64{"J1": 0}} }

func ferruleStation() Station {
	return Station{
		ID:            "F3",
		Kind:          StationFerruleCrimp,
		Present:       []arm.Waypoint{wp("F3.present")},
		Withdraw:      &arm.Waypoint{Label: "F3.withdraw", Joints: map[string]float64{"J1": 0}},
		PresentExpect: "stripped end is in the F3 throat",
		VerifyExpect:  "ferrule crimped",
		Handshake:     Handshake{Trigger: TriggerManual, Done: DoneTimeout, DwellMs: 1},
	}
}

func TestServeStationHappyPath(t *testing.T) {
	p := &fakePresenter{}
	o := NewOrchestrator(p, nil)
	sr := o.ServeStation(context.Background(), ferruleStation())
	if !sr.OK || sr.Phase != PhaseWithdraw {
		t.Fatalf("want OK at withdraw, got %+v", sr)
	}
	// present then withdraw moved; present + verify expectations checked.
	if len(p.moves) != 2 {
		t.Fatalf("moves=%v", p.moves)
	}
	if p.estops != 0 {
		t.Fatalf("unexpected estop")
	}
}

func TestServeStationPresentObstructionLatchesNoTrigger(t *testing.T) {
	p := &fakePresenter{obstructAt: "F3.present"}
	var triggered bool
	o := NewOrchestrator(p, func(Station) (StationIO, error) {
		return FuncIO{TrigFn: func(context.Context) error { triggered = true; return nil }}, nil
	})
	sr := o.ServeStation(context.Background(), ferruleStation())
	if sr.OK || sr.Code != "obstruction" || sr.Phase != PhasePresent {
		t.Fatalf("want obstruction at present, got %+v", sr)
	}
	if triggered {
		t.Fatal("INVARIANT VIOLATED: station triggered after a present obstruction")
	}
}

func TestServeStationPresentVerifyFailsNoTrigger(t *testing.T) {
	st := ferruleStation()
	p := &fakePresenter{failVerify: map[string]bool{st.PresentExpect: true}}
	var triggered bool
	o := NewOrchestrator(p, func(Station) (StationIO, error) {
		return FuncIO{TrigFn: func(context.Context) error { triggered = true; return nil }}, nil
	})
	sr := o.ServeStation(context.Background(), st)
	if sr.OK || sr.Code != "present_verify_failed" {
		t.Fatalf("want present_verify_failed, got %+v", sr)
	}
	if triggered {
		t.Fatal("INVARIANT VIOLATED: triggered despite failed present verify")
	}
}

func TestServeStationDoneTimeoutLatchesEStop(t *testing.T) {
	st := ferruleStation()
	st.Handshake = Handshake{Trigger: TriggerModbus, Done: DoneModbus, TimeoutMs: 30, PollMs: 5}
	p := &fakePresenter{}
	o := NewOrchestrator(p, func(Station) (StationIO, error) {
		return FuncIO{DoneFn: func(context.Context) (bool, error) { return false, nil }}, nil // never done
	})
	sr := o.ServeStation(context.Background(), st)
	if sr.OK || sr.Code != "done_timeout" || sr.Phase != PhaseActuating {
		t.Fatalf("want done_timeout at actuating, got %+v", sr)
	}
	if p.estops != 1 {
		t.Fatalf("want 1 estop on timeout, got %d", p.estops)
	}
}

func TestServeStationDoneSenseError(t *testing.T) {
	st := ferruleStation()
	st.Handshake = Handshake{Trigger: TriggerModbus, Done: DoneModbus, TimeoutMs: 100, PollMs: 5}
	p := &fakePresenter{}
	o := NewOrchestrator(p, func(Station) (StationIO, error) {
		return FuncIO{DoneFn: func(context.Context) (bool, error) { return false, errors.New("modbus exception 4") }}, nil
	})
	sr := o.ServeStation(context.Background(), st)
	if sr.OK || sr.Code != "done_timeout" {
		t.Fatalf("want done_timeout (wrapping sense error), got %+v", sr)
	}
	if p.estops != 1 {
		t.Fatalf("want estop on done-sense error")
	}
}

func TestValidateSequenceTubeBeforeCrimp(t *testing.T) {
	// crimp MustFollow tube-apply; an order [crimp, tube] violates it.
	crimp := Station{ID: "crimp", Kind: StationTerminalCrimp, Constraints: SeqConstraints{MustFollow: []StationKind{StationTubeApply}}}
	tube := Station{ID: "tube", Kind: StationTubeApply}
	stations := map[string]Station{"crimp": crimp, "tube": tube}

	bad := CellProgram{SKU: "x", Leads: []Lead{{EndA: []EndStep{{StationID: "crimp"}, {StationID: "tube"}}}}}
	if probs := ValidateSequence(bad, stations); len(probs) == 0 {
		t.Fatal("expected a constraint violation for crimp-before-tube")
	}
	good := CellProgram{SKU: "x", Leads: []Lead{{EndA: []EndStep{{StationID: "tube"}, {StationID: "crimp"}}}}}
	if probs := ValidateSequence(good, stations); len(probs) != 0 {
		t.Fatalf("expected valid order, got %v", probs)
	}
}

func TestRunProgramOverLead(t *testing.T) {
	p := &fakePresenter{}
	o := NewOrchestrator(p, nil)
	st := ferruleStation()
	stations := map[string]Station{st.ID: st}
	prog := CellProgram{
		SKU:   "demo-sku",
		Leads: []Lead{{Color: "Sarı/Yeşil", EndA: []EndStep{{StationID: "F3"}}, EndB: []EndStep{{StationID: "F3"}}}},
	}
	res := o.Run(context.Background(), prog, stations, false)
	if !res.OK || res.Completed != 1 {
		t.Fatalf("want 1/1 completed, got %+v", res)
	}
	if len(res.Leads[0].Stations) != 2 { // endA + endB
		t.Fatalf("want 2 station visits, got %d", len(res.Leads[0].Stations))
	}
}

func TestRunProgramUnknownStation(t *testing.T) {
	o := NewOrchestrator(&fakePresenter{}, nil)
	prog := CellProgram{SKU: "x", Leads: []Lead{{EndA: []EndStep{{StationID: "ghost"}}}}}
	res := o.Run(context.Background(), prog, map[string]Station{}, false)
	if res.OK || res.Leads[0].Stations[0].Code != "unknown_station" {
		t.Fatalf("want unknown_station, got %+v", res)
	}
}
