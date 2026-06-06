package robot

import "strings"

import "testing"

func TestBuildJigSCAD(t *testing.T) {
	s := BuildJigSCAD(JigParams{Cols: 5, Rows: 2, PitchX: 15, PitchY: 15, KlemensW: 8, KlemensL: 12})
	for _, want := range []string{"cols=5", "rows=2", "pitchX=15", "difference()", "for (i = [0:cols-1])", "cube("} {
		if !strings.Contains(s, want) {
			t.Fatalf("scad missing %q:\n%s", want, s)
		}
	}
}

func TestJigDefaults(t *testing.T) {
	// zero params shouldn't panic or divide-by-zero; defaults fill in
	s := BuildJigSCAD(JigParams{})
	if !strings.Contains(s, "cols=1") || !strings.Contains(s, "difference()") {
		t.Fatalf("defaulted jig malformed:\n%s", s)
	}
}
