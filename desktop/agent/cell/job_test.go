package cell

import (
	"context"
	"testing"

	"github.com/yaver-io/agent/arm"
)

func prepStation(id string, kind StationKind) Station {
	return Station{
		ID:        id,
		Kind:      kind,
		Present:   []arm.Waypoint{wp(id + ".present")},
		Handshake: Handshake{Trigger: TriggerManual, Done: DoneTimeout, DwellMs: 1},
	}
}

func sampleJob() (Job, map[string]Station) {
	stations := map[string]Station{
		"cut":  prepStation("cut", StationCutStrip),
		"crimp": prepStation("crimp", StationFerruleCrimp),
		"tie":  prepStation("tie", StationTest),
		"test": prepStation("test", StationTest),
	}
	lane := Lane{Index: 1, PrepStations: []string{"cut", "crimp"}, Nest: &arm.Waypoint{Label: "nest1", Joints: map[string]float64{"J1": 0}}}
	job := Job{
		ID:    "harness-A",
		Lanes: []Lane{lane},
		Routes: []RoutePath{{ID: "r1", Waypoints: []arm.Waypoint{wp("r1.a"), wp("r1.b")}}},
		TieStation:  "tie",
		TestStation: "test",
		Wires: []WireRow{
			{ID: "w1", Color: "A", SrcLane: 1, EndA: FormatFerruleSingle, PoleType: PolePushIn, RouteID: "r1"},   // auto
			{ID: "w2", Color: "B", SrcLane: 1, EndA: FormatFerruleSingle, PoleType: PoleScrew, RouteID: "r1"},    // operator (screw)
			{ID: "w3", Color: "C", SrcLane: 1, EndA: FormatFerruleTwin, PoleType: PolePushIn, TwinPartner: "w4"}, // operator (twin)
			{ID: "w4", Color: "C", SrcLane: 1, EndA: FormatFerruleTwin, PoleType: PolePushIn, TwinPartner: "w3"},
		},
	}
	return job, stations
}

func TestAutoTerminable(t *testing.T) {
	if !(WireRow{PoleType: PolePushIn, EndA: FormatFerruleSingle}).AutoTerminable() {
		t.Fatal("push-in + single should auto-terminate")
	}
	if (WireRow{PoleType: PoleScrew, EndA: FormatFerruleSingle}).AutoTerminable() {
		t.Fatal("screw must flag operator")
	}
	if (WireRow{PoleType: PolePushIn, EndA: FormatFerruleTwin}).AutoTerminable() {
		t.Fatal("twin must flag operator")
	}
	if (WireRow{PoleType: PolePushIn, EndA: FormatFerruleSingle, TwinPartner: "x"}).AutoTerminable() {
		t.Fatal("twin partner must flag operator")
	}
}

func TestValidateJob(t *testing.T) {
	job, stations := sampleJob()
	if probs := ValidateJob(job, stations); len(probs) != 0 {
		t.Fatalf("expected valid, got %v", probs)
	}
	// break a lane reference
	bad := job
	bad.Wires = append([]WireRow{}, job.Wires...)
	bad.Wires[0].SrcLane = 9
	if probs := ValidateJob(bad, stations); len(probs) == 0 {
		t.Fatal("expected a lane-not-defined problem")
	}
}

func TestRunJobSplitsAutoAndOperator(t *testing.T) {
	job, stations := sampleJob()
	p := &fakePresenter{}
	o := NewOrchestrator(p, nil)
	res := o.RunJob(context.Background(), job, stations, JobOpts{})

	if !res.OK || res.Completed != 4 {
		t.Fatalf("want 4/4 completed, got %+v", res)
	}
	// w2 (screw) + w3,w4 (twin) → operator flags; w1 → auto push-in.
	if len(res.OperatorFlags) != 3 {
		t.Fatalf("want 3 operator flags, got %v", res.OperatorFlags)
	}
	if p.inserts != 1 {
		t.Fatalf("want exactly 1 push-in insert (w1), got %d", p.inserts)
	}
	// w1 mode auto, w2 mode operator
	byID := map[string]WireResult{}
	for _, w := range res.Wires {
		byID[w.WireID] = w
	}
	if byID["w1"].Mode != "auto" || byID["w2"].Mode != "operator" {
		t.Fatalf("mode split wrong: w1=%s w2=%s", byID["w1"].Mode, byID["w2"].Mode)
	}
}

func TestRunJobPrepFailFlagsWireOnly(t *testing.T) {
	job, stations := sampleJob()
	// make the crimp present pose fail for ALL wires (fake fails any move to this label)
	p := &fakePresenter{failMoveAt: "crimp.present"}
	o := NewOrchestrator(p, nil)
	res := o.RunJob(context.Background(), job, stations, JobOpts{})
	// every wire shares lane 1 → all fail prep, but the run does not hard-stop.
	if res.OK {
		t.Fatal("expected not-OK (all wires failed prep)")
	}
	if res.Total != 4 {
		t.Fatalf("total=%d", res.Total)
	}
	for _, w := range res.Wires {
		if w.OK || w.Stage != "prep" {
			t.Fatalf("wire %s should have failed at prep, got %+v", w.WireID, w)
		}
	}
}

func TestRunJobInsertFailureFlagsWire(t *testing.T) {
	job, stations := sampleJob()
	p := &fakePresenter{failInsert: true}
	o := NewOrchestrator(p, nil)
	res := o.RunJob(context.Background(), job, stations, JobOpts{})
	byID := map[string]WireResult{}
	for _, w := range res.Wires {
		byID[w.WireID] = w
	}
	if byID["w1"].OK || byID["w1"].Stage != "terminate" {
		t.Fatalf("w1 should fail at terminate when insert fails, got %+v", byID["w1"])
	}
	// operator wires unaffected by insert failure
	if !byID["w2"].OK {
		t.Fatalf("w2 (operator) should still be OK, got %+v", byID["w2"])
	}
}

func TestRunJobDryRunNoMotion(t *testing.T) {
	job, stations := sampleJob()
	p := &fakePresenter{}
	o := NewOrchestrator(p, nil)
	res := o.RunJob(context.Background(), job, stations, JobOpts{DryRun: true})
	if !res.OK || res.Completed != 4 {
		t.Fatalf("dry run should complete all, got %+v", res)
	}
	if len(p.moves) != 0 || p.inserts != 0 {
		t.Fatalf("dry run must not move the arm: moves=%d inserts=%d", len(p.moves), p.inserts)
	}
}
