package robot

import (
	"context"
	"testing"
)

func TestScrewParamsFromCalibration(t *testing.T) {
	c := &Controller{Env: DefaultEnvelope, ZSafe: 25, ZEngage: 6, MaxPlunge: 10, TargetTorqueNmm: 400}
	// no overrides → use calibration
	p := c.screwParamsFromCalibration(110, 110, 0, 0, 0)
	if p.Zapproach != 6 || p.Zsafe != 25 || p.TargetTorqueNmm != 400 {
		t.Fatalf("calibration not applied: %+v", p)
	}
	if p.Zmax != -4 { // 6 - 10, above Zmin(0)? -4 < 0 → clamps to Zmin
		// engage 6 - plunge 10 = -4, below Zmin 0 → clamp to 0
		if p.Zmax != c.Env.Zmin {
			t.Fatalf("Zmax should clamp to Zmin=%.0f, got %.2f", c.Env.Zmin, p.Zmax)
		}
	}
	// overrides win
	p2 := c.screwParamsFromCalibration(0, 0, 12, 30, 200)
	if p2.Zapproach != 12 || p2.Zsafe != 30 || p2.TargetTorqueNmm != 200 {
		t.Fatalf("overrides ignored: %+v", p2)
	}
}

func TestTorqueNoCompanion(t *testing.T) {
	c := &Controller{Env: DefaultEnvelope}
	if _, err := c.Torque(context.Background()); err == nil {
		t.Fatal("Torque should error without a companion")
	}
}
