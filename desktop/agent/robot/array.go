package robot

import (
	"fmt"
	"strings"
)

// Klemens (terminal-block) array generator. The fixturing question — "how do I
// present N terminal blocks to the robot" — has two software-supported answers,
// both of which this turns into a runnable fastening Program:
//
//   - "grid"   : a jig holding klemens at a known grid INSIDE the Cartesian XY
//                work area (Ender-3, or a rigid Fuju-driven 3-axis). The tool
//                travels to each (x,y) and drives the screw home. Least custom
//                hardware — the robot you have does it.
//   - "linear" : a single linear rail (Fuju driver) indexes a strip past a FIXED
//                screwdriver. The rail moves to each position; the screw plunges
//                in place (AtCurrent). For long strips that don't fit the jig.
//
// Either way the output is the same teach-and-repeat Program, so replay, camera
// verify, encoder cross-check, and torque seating all apply unchanged.

// ArrayParams describes a klemens layout to fasten.
type ArrayParams struct {
	Name string `json:"name"`
	Mode string `json:"mode"` // "grid" | "linear"

	// grid (Cartesian jig): first klemens at (OriginX,OriginY), Cols×Rows at pitch.
	OriginX    float64 `json:"originX"`
	OriginY    float64 `json:"originY"`
	Cols       int     `json:"cols"`
	Rows       int     `json:"rows"`
	PitchX     float64 `json:"pitchX"`
	PitchY     float64 `json:"pitchY"`
	Serpentine bool    `json:"serpentine"` // alternate row direction → less travel

	// linear (single rail): Count klemens at Pitch along Axis, starting at Origin.
	Axis   string  `json:"axis"`   // "X" | "Y" (rail axis); default X
	Origin float64 `json:"origin"` // first klemens position along the axis (mm)
	Pitch  float64 `json:"pitch"`
	Count  int     `json:"count"`

	TargetTorqueNmm float64 `json:"targetTorqueNmm"`
	Home            bool    `json:"home"` // home (reference) before the run
}

// BuildKlemensArray expands the layout into an ordered fastening Program: optional
// home, then for each klemens a travel + drive-home-to-torque.
func BuildKlemensArray(p ArrayParams) (Program, error) {
	prog := Program{Name: strings.TrimSpace(p.Name)}
	if prog.Name == "" {
		prog.Name = "klemens-array"
	}
	if p.Home {
		prog.Steps = append(prog.Steps, Step{Type: "home", Label: "reference"})
	}

	switch strings.ToLower(strings.TrimSpace(p.Mode)) {
	case "linear":
		if p.Count <= 0 || p.Pitch == 0 {
			return prog, fmt.Errorf("linear array needs count>0 and pitch≠0")
		}
		axis := strings.ToUpper(strings.TrimSpace(p.Axis))
		if axis == "" {
			axis = "X"
		}
		for i := 0; i < p.Count; i++ {
			pos := p.Origin + float64(i)*p.Pitch
			mv := Step{Type: "move", Feed: 3000, Label: fmt.Sprintf("index klemens %d", i+1)}
			switch axis {
			case "Y":
				mv.Y = &[]float64{pos}[0]
			default:
				mv.X = &[]float64{pos}[0]
			}
			prog.Steps = append(prog.Steps, mv)
			// no X/Y on the screw → plunge in place under the fixed screwdriver
			prog.Steps = append(prog.Steps, Step{Type: "screw", Torque: p.TargetTorqueNmm, Label: fmt.Sprintf("drive klemens %d", i+1)})
		}
	default: // grid
		if p.Cols <= 0 || p.Rows <= 0 {
			return prog, fmt.Errorf("grid array needs cols>0 and rows>0")
		}
		n := 0
		for r := 0; r < p.Rows; r++ {
			y := p.OriginY + float64(r)*p.PitchY
			for cc := 0; cc < p.Cols; cc++ {
				col := cc
				if p.Serpentine && r%2 == 1 {
					col = p.Cols - 1 - cc // snake back on odd rows
				}
				x := p.OriginX + float64(col)*p.PitchX
				n++
				xv, yv := x, y
				prog.Steps = append(prog.Steps,
					Step{Type: "move", X: &xv, Y: &yv, Feed: 3000, Label: fmt.Sprintf("to klemens %d", n)},
					Step{Type: "screw", X: &xv, Y: &yv, Torque: p.TargetTorqueNmm, Label: fmt.Sprintf("drive klemens %d", n)},
				)
			}
		}
	}
	return prog, nil
}
