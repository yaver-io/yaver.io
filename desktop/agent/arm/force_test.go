package arm

import (
	"context"
	"testing"

	"github.com/yaver-io/agent/robot"
)

// fakeForceBackend implements Backend + ForceBackend with canned force behaviour.
type fakeForceBackend struct {
	wrench   Wrench
	seatAt   float64 // travel (mm) at which |force| crosses the limit
	moveCnt  int
}

func (f *fakeForceBackend) Name() string                              { return "fake-force" }
func (f *fakeForceBackend) Connect(context.Context) error             { return nil }
func (f *fakeForceBackend) Close() error                              { return nil }
func (f *fakeForceBackend) Describe(context.Context) (ArmInfo, error) { return sixCartInfo(), nil }
func (f *fakeForceBackend) Status(context.Context) (ArmStatus, error) {
	return ArmStatus{OK: true, Backend: f.Name(), Connected: true, Enabled: true}, nil
}
func (f *fakeForceBackend) Enable(context.Context, bool) error { return nil }
func (f *fakeForceBackend) JointState(context.Context) ([]JointState, error) {
	return []JointState{{Name: "J1", Position: 0, Unit: "deg"}}, nil
}
func (f *fakeForceBackend) Pose(context.Context) (Pose, error)                         { return Pose{}, nil }
func (f *fakeForceBackend) MoveJoints(context.Context, map[string]float64, int, int) error { return nil }
func (f *fakeForceBackend) MoveLinear(context.Context, Pose, int, int) error               { return nil }
func (f *fakeForceBackend) WaitIdle(context.Context) error                                 { return nil }
func (f *fakeForceBackend) Stop(context.Context) error                                     { return nil }
func (f *fakeForceBackend) EStop(context.Context) error                                    { return nil }
func (f *fakeForceBackend) FreeDrive(context.Context, bool) error                          { return nil }
func (f *fakeForceBackend) Raw(context.Context, string) (string, error)                    { return "", nil }
func (f *fakeForceBackend) Wrench(context.Context) (Wrench, error)                         { return f.wrench, nil }
func (f *fakeForceBackend) ForceMove(_ context.Context, dir Axis6, limitN, maxDistMm float64, _ int) (ForceResult, error) {
	axis, _, err := ParseAxis6(dir)
	if err != nil {
		return ForceResult{Code: "bad_payload"}, err
	}
	_ = axis
	if f.seatAt > 0 && f.seatAt <= maxDistMm {
		return ForceResult{OK: true, Seated: true, PeakForceN: limitN, TravelMm: f.seatAt}, nil
	}
	return ForceResult{OK: true, Seated: false, PeakForceN: limitN / 2, TravelMm: maxDistMm}, nil
}

func sixCartInfo() ArmInfo {
	js := make([]JointSpec, 6)
	for i := range js {
		js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: -180, Max: 180, Unit: "deg"}
	}
	return ArmInfo{Joints: js, HasCartesian: true, DOF: 6, Source: "config"}
}

func TestForceMoveSeats(t *testing.T) {
	b := &fakeForceBackend{wrench: Wrench{Fz: 3.0}, seatAt: 4.0}
	ctrl := NewController(b, nil, robot.VisionConfig{}, Config{Info: sixCartInfo()})
	fr := ctrl.ForceMove(context.Background(), "z", 20, 50, 0)
	if !fr.OK || !fr.Seated {
		t.Fatalf("expected seated, got %+v", fr)
	}
	if fr.TravelMm != 4.0 {
		t.Fatalf("travel=%.1f want 4.0", fr.TravelMm)
	}
}

func TestForceMoveGuards(t *testing.T) {
	b := &fakeForceBackend{}
	ctrl := NewController(b, nil, robot.VisionConfig{}, Config{Info: sixCartInfo()})
	ctx := context.Background()

	if fr := ctrl.ForceMove(ctx, "z", 999, 50, 0); fr.OK || fr.Code != "out_of_range" {
		t.Fatalf("over-force should be refused, got %+v", fr)
	}
	if fr := ctrl.ForceMove(ctx, "z", 20, 9999, 0); fr.OK || fr.Code != "out_of_range" {
		t.Fatalf("over-travel should be refused, got %+v", fr)
	}
	if fr := ctrl.ForceMove(ctx, "wobble", 20, 50, 0); fr.OK || fr.Code != "bad_payload" {
		t.Fatalf("bad axis should be refused, got %+v", fr)
	}
	// e-stop latched → refuse
	_ = ctrl.EStop(ctx)
	if fr := ctrl.ForceMove(ctx, "z", 20, 50, 0); fr.OK || fr.Code != "estopped" {
		t.Fatalf("estopped should refuse, got %+v", fr)
	}
}

func TestForceMoveNoForceBackend(t *testing.T) {
	// generic backend has no force support → ErrNoForce path
	addr, _ := fakeLineRobot(t)
	cfg := Config{Driver: "generic_tcp", Addr: addr, Info: sixJointInfo()}
	cfg.Normalize()
	gb, err := NewGenericArmBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := NewController(gb, nil, robot.VisionConfig{}, cfg)
	if _, werr := ctrl.Wrench(context.Background()); werr != ErrNoForce {
		t.Fatalf("want ErrNoForce, got %v", werr)
	}
	if fr := ctrl.ForceMove(context.Background(), "z", 20, 50, 0); fr.OK || fr.Code != "no_force" {
		t.Fatalf("want no_force, got %+v", fr)
	}
}
