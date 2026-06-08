// Package cell is the wire-harness CELL layer: it makes the robot arm the
// "transfer system" a turnkey harness machine builds in steel, and every other
// operation a cheap semiautomatic station the arm SERVES. A Station is a machine
// (ferrule crimp, terminal press, seal insert, heatshrink apply/heat, mark,
// klemens/terminal-block insert, connector insert, pull-test) modeled uniformly
// as: a taught present-pose set + a trigger/done handshake + a vision verify
// expectation. The Orchestrator drives the arm through a deterministic rendezvous
// state machine that owns pinch-safety.
//
// See docs/yaver-arm-served-harness-cell.md. This package is LLM-free; it depends
// on the arm package (for Waypoint/Pose) and the machine package (for the
// Driver seam used by modbus handshakes), but reaches both through small
// interfaces (Presenter/StationIO) so it is fully unit-testable with fakes.
package cell

import "github.com/yaver-io/agent/arm"

// StationKind is the operation a station performs.
type StationKind string

const (
	StationCutStrip      StationKind = "cut_strip"        // the one feed machine (length+strip)
	StationFerruleCrimp  StationKind = "ferrule_crimp"    // JCW F3, bandolier ferrule feed
	StationTerminalCrimp StationKind = "terminal_crimp"   // benchtop / pneumatic terminal press
	StationSealInsert    StationKind = "seal_insert"      // single-wire seal / grommet (before crimp)
	StationTubeApply     StationKind = "heatshrink_apply" // slide heatshrink tube onto the end
	StationTubeHeat      StationKind = "heatshrink_heat"  // hot-air / oven shrink
	StationMark          StationKind = "mark_print"       // inkjet / hot-stamp / laser marking
	StationBlockInsert   StationKind = "block_insert"     // klemens / terminal block insert + screw
	StationConnInsert    StationKind = "connector_insert" // connector-housing cavity insert
	StationTest          StationKind = "test"             // pull-test / continuity
)

// TriggerKind: how the arm tells the station to actuate (replaces the foot pedal).
type TriggerKind string

const (
	TriggerNone   TriggerKind = "none"   // arm-only present (e.g. dwell at a heat zone)
	TriggerManual TriggerKind = "manual" // operator/assist actuates; we wait for done
	TriggerModbus TriggerKind = "modbus" // assert a coil/register via a machine.Driver
)

// DoneKind: how the station signals the cycle finished.
type DoneKind string

const (
	DoneTimeout DoneKind = "timeout" // deterministic-cycle machine: wait DwellMs/TimeoutMs
	DoneManual  DoneKind = "manual"  // operator confirms (UI); headless run treats as timeout
	DoneModbus  DoneKind = "modbus"  // poll a done/counter register via a machine.Driver
	DoneVision  DoneKind = "vision"  // read the machine's lamp/screen via arm vision
)

// Handshake is the trigger/done contract for one station (design §6). The modbus
// fields resolve against the station's DriverID in the agent's machine engine.
type Handshake struct {
	Trigger TriggerKind `json:"trigger,omitempty"`
	Done    DoneKind    `json:"done,omitempty"`

	TriggerTag   string  `json:"triggerTag,omitempty"`   // modbus: coil/register to assert
	TriggerValue float64 `json:"triggerValue,omitempty"` // modbus: value to write (default 1)
	DoneTag      string  `json:"doneTag,omitempty"`      // modbus: done/counter register
	DoneValue    float64 `json:"doneValue,omitempty"`    // modbus: value meaning "done" (0 → any non-zero)
	DoneExpect   string  `json:"doneExpect,omitempty"`   // vision: expectation meaning done ("green OK lamp lit")

	DwellMs   int `json:"dwellMs,omitempty"`   // settle dwell at present, and timeout-done duration
	TimeoutMs int `json:"timeoutMs,omitempty"` // max wait for done before fault (default 30000)
	PollMs    int `json:"pollMs,omitempty"`    // poll interval for modbus/vision done (default 250)
}

// SeqConstraints encode ordering rules (design §9), checked at save time. e.g. a
// heatshrink-apply station MustPrecede the crimp; a crimp MustFollow seal_insert.
type SeqConstraints struct {
	MustFollow  []StationKind `json:"mustFollow,omitempty"`  // must appear AFTER these kinds on the same end
	MustPrecede []StationKind `json:"mustPrecede,omitempty"` // ...and BEFORE these kinds
}

// Station is one semiautomatic machine the arm serves.
type Station struct {
	ID    string      `json:"id"`
	Kind  StationKind `json:"kind"`
	Label string      `json:"label,omitempty"`

	// DriverID resolves to a machine.Driver in the agent's machine engine, used
	// for modbus trigger/done. Empty for manual/timeout/vision stations.
	DriverID string `json:"driverId,omitempty"`

	Approach *arm.Waypoint  `json:"approach,omitempty"` // optional clear pre-pose
	Present  []arm.Waypoint `json:"present,omitempty"`  // taught present pose(s)
	Withdraw *arm.Waypoint  `json:"withdraw,omitempty"` // optional retract pose

	PresentExpect string `json:"presentExpect,omitempty"` // vision: lead end is in the jaw mouth
	VerifyExpect  string `json:"verifyExpect,omitempty"`  // vision: good result after the cycle

	Handshake   Handshake      `json:"handshake"`
	Constraints SeqConstraints `json:"constraints,omitempty"`
}
