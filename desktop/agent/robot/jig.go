package robot

import (
	"fmt"
	"strings"
)

// Parametric klemens jig. The "least custom" fixturing path (docs/yaver-robot-
// fixturing-fuju.md §1) is a printed plate that seats the terminal blocks at a
// known grid inside the work area. This generates that plate as OpenSCAD from the
// SAME grid params the array program uses — so the jig the user prints and the
// program Yaver runs come from one source of truth. Render with `openscad -o
// jig.stl jig.scad` (or the OpenSCAD app), print, drop in the klemens, run the
// matching grid array anchored at the jig's first-pocket corner.

// JigParams describes the printable fixture.
type JigParams struct {
	Cols        int     `json:"cols"`
	Rows        int     `json:"rows"`
	PitchX      float64 `json:"pitchX"`      // grid spacing — match the array
	PitchY      float64 `json:"pitchY"`      // grid spacing — match the array
	KlemensW    float64 `json:"klemensW"`    // pocket width (mm, along X)
	KlemensL    float64 `json:"klemensL"`    // pocket length (mm, along Y)
	PocketDepth float64 `json:"pocketDepth"` // how deep the pocket is (mm)
	Wall        float64 `json:"wall"`        // border around the outermost pockets (mm)
	PlateH      float64 `json:"plateH"`      // base plate thickness under the pockets (mm)
	Clearance   float64 `json:"clearance"`   // added to pocket W/L for an easy drop-in fit (mm)
}

func (p *JigParams) normalize() {
	if p.Cols <= 0 {
		p.Cols = 1
	}
	if p.Rows <= 0 {
		p.Rows = 1
	}
	if p.KlemensW <= 0 {
		p.KlemensW = 8
	}
	if p.KlemensL <= 0 {
		p.KlemensL = 12
	}
	if p.PitchX <= 0 {
		p.PitchX = p.KlemensW + 4
	}
	if p.PitchY <= 0 {
		p.PitchY = p.KlemensL + 4
	}
	if p.PocketDepth <= 0 {
		p.PocketDepth = 6
	}
	if p.Wall <= 0 {
		p.Wall = 4
	}
	if p.PlateH <= 0 {
		p.PlateH = 3
	}
	if p.Clearance < 0 {
		p.Clearance = 0
	}
	if p.Clearance == 0 {
		p.Clearance = 0.4
	}
}

// BuildJigSCAD returns OpenSCAD source for the fixture. The first pocket's CENTER
// is the origin (0,0) of the grid, so a grid array built with captureOrigin (the
// tool jogged over pocket #1) lines up with every pocket.
func BuildJigSCAD(p JigParams) string {
	p.normalize()
	pw := p.KlemensW + p.Clearance
	pl := p.KlemensL + p.Clearance
	plateW := float64(p.Cols-1)*p.PitchX + pw + 2*p.Wall
	plateL := float64(p.Rows-1)*p.PitchY + pl + 2*p.Wall
	plateH := p.PlateH + p.PocketDepth

	var b strings.Builder
	fmt.Fprintf(&b, "// Yaver klemens jig — %dx%d @ %.2f x %.2f mm pitch\n", p.Cols, p.Rows, p.PitchX, p.PitchY)
	fmt.Fprintf(&b, "// Pocket #1 center is the grid origin; build the grid array with captureOrigin there.\n")
	fmt.Fprintf(&b, "cols=%d; rows=%d; pitchX=%.4f; pitchY=%.4f;\n", p.Cols, p.Rows, p.PitchX, p.PitchY)
	fmt.Fprintf(&b, "pw=%.4f; pl=%.4f; depth=%.4f; plateH=%.4f; wall=%.4f;\n", pw, pl, p.PocketDepth, plateH, p.Wall)
	// origin offset so pocket #1 center sits at (0,0); plate corner is below-left.
	fmt.Fprintf(&b, "ox = -(wall + pw/2); oy = -(wall + pl/2);\n")
	b.WriteString("difference() {\n")
	fmt.Fprintf(&b, "  translate([ox, oy, 0]) cube([%.4f, %.4f, plateH]);\n", plateW, plateL)
	b.WriteString("  for (i = [0:cols-1]) for (j = [0:rows-1])\n")
	b.WriteString("    translate([i*pitchX - pw/2, j*pitchY - pl/2, plateH - depth])\n")
	b.WriteString("      cube([pw, pl, depth + 0.1]);\n")
	b.WriteString("}\n")
	return b.String()
}
