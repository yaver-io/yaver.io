package main

// ops_jig.go — generate a printable fixture/jig as OpenSCAD from grid parameters.
// Fixtures can be BOUGHT (openGrid/Gridfinity plates, industrial tooling grid
// plates, vendor connector nests) or PRINTED from this. Either way the grid's
// known hole/pocket pitch doubles as the cell's metric WORLD FRAME: every fixture
// seats at an integer (col,row), so the harness layout IS the coordinate system
// the robot and a demonstration video share (see
// docs/yaver-video-to-policy-harness-cell.md). The first pocket center is the grid
// origin, matching the arm grid-array's captureOrigin.

import (
	"encoding/json"

	"github.com/yaver-io/agent/robot"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "robot_jig_generate",
		Description: "Generate a printable fixture/jig as OpenSCAD from grid params {cols, rows, pitchX, pitchY, klemensW, klemensL, pocketDepth, wall, plateH, clearance}. Pocket #1 center = grid origin (matches the arm grid-array captureOrigin). Render with `openscad -o jig.stl jig.scad`, or buy an equivalent openGrid/Gridfinity/tooling plate — the grid pitch is the cell's world frame either way.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cols":        map[string]interface{}{"type": "integer"},
				"rows":        map[string]interface{}{"type": "integer"},
				"pitchX":      map[string]interface{}{"type": "number", "description": "grid spacing X (mm) — match the arm array"},
				"pitchY":      map[string]interface{}{"type": "number", "description": "grid spacing Y (mm)"},
				"klemensW":    map[string]interface{}{"type": "number", "description": "pocket width (mm)"},
				"klemensL":    map[string]interface{}{"type": "number", "description": "pocket length (mm)"},
				"pocketDepth": map[string]interface{}{"type": "number"},
				"wall":        map[string]interface{}{"type": "number"},
				"plateH":      map[string]interface{}{"type": "number"},
				"clearance":   map[string]interface{}{"type": "number", "description": "added to pocket W/L for drop-in fit (mm)"},
			},
			"additionalProperties": false,
		},
		Handler: func(_ OpsContext, payload json.RawMessage) OpsResult {
			var p robot.JigParams
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &p); err != nil {
					return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
				}
			}
			scad := robot.BuildJigSCAD(p)
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"scad":   scad,
				"render": "openscad -o jig.stl jig.scad",
				"note":   "first pocket center is the grid origin; the grid pitch is the cell's world frame (anchor fiducials at the corners).",
			}}
		},
		AllowGuest: false,
	})
}
