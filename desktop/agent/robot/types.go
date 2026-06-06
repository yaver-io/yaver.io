// Package robot implements the Yaver Robot Protocol (docs/robot-protocol.md):
// drive a Cartesian motion machine (Ender-3 / Marlin), then look through a
// camera and verify the commanded motion actually happened. It is transport-
// agnostic — the same logic backs the agent `robot_*` ops verbs and the
// standalone `robotd` server — and backend-agnostic (Python ender_ui bridge
// today, native serial later).
package robot

// Position is a fresh M114 readback (post-M400), never a cached value.
type Position struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Homed bool    `json:"homed"`
}

// Action describes what motion was requested, echoed back to the caller.
type Action struct {
	Kind string   `json:"kind"`           // "home" | "jog" | "move" | "tool" | "verify"
	Axis string   `json:"axis,omitempty"` // jog
	Dist float64  `json:"dist,omitempty"` // jog
	X    *float64 `json:"x,omitempty"`    // move
	Y    *float64 `json:"y,omitempty"`
	Z    *float64 `json:"z,omitempty"`
	Feed int      `json:"feed,omitempty"`
	On   *bool    `json:"on,omitempty"` // tool
}

// Verdict is the camera judgment. Mode "frames" leaves Moved/Confidence unset
// (the caller's LLM fills them from the returned frames).
type Verdict struct {
	Mode        string  `json:"mode"`                  // "agent" | "frames"
	Moved       bool    `json:"moved,omitempty"`       // agent mode
	Confidence  float64 `json:"confidence,omitempty"`  // agent mode 0..1
	Obstruction bool    `json:"obstruction,omitempty"` // agent mode
	Expectation string  `json:"expectation,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Observed    string  `json:"observed,omitempty"`
}

// CrossCheck compares what the firmware encoder reports moving vs what was asked.
type CrossCheck struct {
	ExpectedDelta map[string]float64 `json:"expectedDelta,omitempty"`
	ObservedDelta map[string]float64 `json:"observedDelta,omitempty"`
	Agree         bool               `json:"agree"`
}

// MoveResponse is the single move-and-verify result shape (docs/robot-protocol.md §3).
type MoveResponse struct {
	OK       bool        `json:"ok"`
	Code     string      `json:"code,omitempty"`
	Error    string      `json:"error,omitempty"`
	Action   *Action     `json:"action,omitempty"`
	Position *Position   `json:"position,omitempty"`
	Verify   *Verdict    `json:"verify,omitempty"`
	Frames   *Frames     `json:"frames,omitempty"`
	Cross    *CrossCheck `json:"encoderCrossCheck,omitempty"`
	TookMs   int64       `json:"tookMs"`
}

// Frames carries before/after JPEGs as data: URLs (base64). Empty when the
// caller asked verify:false.
type Frames struct {
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Status is the no-motion snapshot for robot_status / GET /robot/status.
type Status struct {
	OK        bool      `json:"ok"`
	Backend   string    `json:"backend"`
	Connected bool      `json:"connected"`
	Position  *Position `json:"position,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	EStopped  bool      `json:"estopped"`
	CameraOK  bool      `json:"cameraOk"`
}
