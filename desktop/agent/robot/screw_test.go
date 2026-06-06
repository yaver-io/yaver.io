package robot

import (
	"context"
	"testing"
)

// fakeCompanion models a load-cell/current sensor: torque rises as the carriage
// plunges below the (simulated) screw-head height. Reads the fakeBridge's live
// Z so the closed loop is exercised end to end.
type fakeCompanion struct {
	fb       *fakeBridge
	headZ    float64 // screw head height — torque starts climbing below this
	nmmPerMm float64 // torque gain (N·mm per mm of plunge past the head)
	zeroed   bool
	gpio     map[int]bool
}

func newFakeCompanion(fb *fakeBridge) *fakeCompanion {
	return &fakeCompanion{fb: fb, headZ: 5.0, nmmPerMm: 200, gpio: map[int]bool{}}
}

func (f *fakeCompanion) Ping(ctx context.Context) error { return nil }
func (f *fakeCompanion) Zero(ctx context.Context) error { f.zeroed = true; return nil }
func (f *fakeCompanion) Close() error                   { return nil }
func (f *fakeCompanion) GPIO(ctx context.Context, pin int, on bool) error {
	f.gpio[pin] = on
	return nil
}
func (f *fakeCompanion) Sense(ctx context.Context) (SenseReading, error) {
	f.fb.mu.Lock()
	z := f.fb.z
	f.fb.mu.Unlock()
	tq := (f.headZ - z) * f.nmmPerMm
	if tq < 0 {
		tq = 0
	}
	return SenseReading{TorqueNmm: tq, CurrentmA: tq / 4}, nil
}

func newScrewController(t *testing.T) (*Controller, *fakeBridge, *fakeCompanion) {
	t.Helper()
	c, fb := newTestController(t)
	comp := newFakeCompanion(fb)
	c.Companion = comp
	return c, fb, comp
}

func screwParams() ScrewParams {
	return ScrewParams{
		X: 110, Y: 110, Zapproach: 8, Zmax: 2, Step: 0.3, Feed: 60,
		Zsafe: 20, TargetTorqueNmm: 400, ToolPin: -1, DwellMs: 0, TimeoutSec: 30,
	}
}

func TestDriveScrewSeatsAtTorque(t *testing.T) {
	c, _, comp := newScrewController(t)
	ctx := context.Background()
	if h := c.Home(ctx, "", "off", ""); !h.OK {
		t.Fatalf("home: %s", h.Error)
	}
	res := c.DriveScrew(ctx, screwParams(), "off", "")
	if !res.OK {
		t.Fatalf("screw failed: [%s] %s", res.Code, res.Error)
	}
	if !res.Seated {
		t.Fatalf("expected seated; measured=%.0f target=%.0f finalZ=%.2f", res.MeasuredTorqueNmm, res.TargetTorqueNmm, res.FinalZ)
	}
	// Target 400 N·mm at gain 200 N·mm/mm below headZ=5 → seats at z≈3.
	if res.MeasuredTorqueNmm < 400 {
		t.Fatalf("measured torque %.0f below target", res.MeasuredTorqueNmm)
	}
	if res.FinalZ > 3.05 {
		t.Fatalf("should have seated by z≈3, stopped at %.2f", res.FinalZ)
	}
	if !comp.zeroed {
		t.Fatalf("companion should have been tared before plunge")
	}
	// Retracted to safe height afterwards.
	if res.Position == nil || res.Position.Z != 20 {
		t.Fatalf("should retract to zsafe=20, got %+v", res.Position)
	}
}

func TestDriveScrewNotSeated(t *testing.T) {
	c, _, _ := newScrewController(t)
	ctx := context.Background()
	_ = c.Home(ctx, "", "off", "")
	p := screwParams()
	p.TargetTorqueNmm = 100000 // unreachable in the plunge window
	res := c.DriveScrew(ctx, p, "off", "")
	if !res.OK || res.Seated || res.Code != "not_seated" {
		t.Fatalf("expected ok+not_seated, got ok=%v seated=%v code=%s", res.OK, res.Seated, res.Code)
	}
	if res.FinalZ > 2.0001 {
		t.Fatalf("should have plunged to Zmax=2, finalZ=%.2f", res.FinalZ)
	}
}

func TestDriveScrewNeedsCompanion(t *testing.T) {
	c, _ := newTestController(t) // no companion attached
	ctx := context.Background()
	_ = c.Home(ctx, "", "off", "")
	res := c.DriveScrew(ctx, screwParams(), "off", "")
	if res.OK || res.Code != "no_companion" {
		t.Fatalf("expected no_companion, got %+v", res)
	}
}
