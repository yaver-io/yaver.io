package robot

import "testing"

func TestBuildGridArray(t *testing.T) {
	p, err := BuildKlemensArray(ArrayParams{Mode: "grid", Cols: 3, Rows: 2, OriginX: 10, OriginY: 20, PitchX: 15, PitchY: 25, TargetTorqueNmm: 400, Home: true})
	if err != nil {
		t.Fatal(err)
	}
	// home + 6 klemens × (move+screw) = 1 + 12
	if len(p.Steps) != 13 {
		t.Fatalf("want 13 steps, got %d", len(p.Steps))
	}
	// first klemens move at origin, screw carries torque + XY (jig → travel)
	mv := p.Steps[1]
	if mv.Type != "move" || mv.X == nil || *mv.X != 10 || *mv.Y != 20 {
		t.Fatalf("bad first move: %+v", mv)
	}
	sc := p.Steps[2]
	if sc.Type != "screw" || sc.Torque != 400 || sc.X == nil {
		t.Fatalf("grid screw must carry XY + torque: %+v", sc)
	}
}

func TestBuildLinearArray(t *testing.T) {
	p, err := BuildKlemensArray(ArrayParams{Mode: "linear", Axis: "X", Count: 10, Origin: 5, Pitch: 8, TargetTorqueNmm: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 20 { // 10 × (move + screw)
		t.Fatalf("want 20 steps, got %d", len(p.Steps))
	}
	// linear screw has NO X/Y → plunge in place
	if sc := p.Steps[1]; sc.Type != "screw" || sc.X != nil || sc.Y != nil {
		t.Fatalf("linear screw must be plunge-in-place: %+v", sc)
	}
	// 2nd klemens at origin+pitch
	if mv := p.Steps[2]; mv.X == nil || *mv.X != 13 {
		t.Fatalf("2nd index should be at 13, got %+v", mv)
	}
}

func TestSerpentine(t *testing.T) {
	p, _ := BuildKlemensArray(ArrayParams{Mode: "grid", Cols: 2, Rows: 2, PitchX: 10, PitchY: 10, Serpentine: true})
	// row0: col0,col1 (x=0,10); row1 reversed: col1,col0 (x=10,0)
	xs := []float64{}
	for _, s := range p.Steps {
		if s.Type == "move" {
			xs = append(xs, *s.X)
		}
	}
	if len(xs) != 4 || xs[2] != 10 || xs[3] != 0 {
		t.Fatalf("serpentine row not reversed: %v", xs)
	}
}

func TestAxisStepsM92(t *testing.T) {
	if got := (&AxisSteps{X: 80, Y: 80, Z: 400}).M92(); got != "M92 X80.0000 Y80.0000 Z400.0000" {
		t.Fatalf("M92 render: %q", got)
	}
	var nilA *AxisSteps
	if nilA.M92() != "" {
		t.Fatal("nil AxisSteps should render empty")
	}
}
