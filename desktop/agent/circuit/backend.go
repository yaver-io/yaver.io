package circuit

import "context"

// Backend is the pluggable simulation engine — the electrical analogue of
// arm.Backend. Two ship in-tree: the dependency-free built-in MNA solver
// (always available) and an ngspice pass-through (available when the binary is
// on PATH). New engines (Xyce, LTspice-batch, …) implement this same interface.
type Backend interface {
	Name() string
	Available() bool
	Capabilities() Capabilities
	Simulate(ctx context.Context, nl Netlist, a Analysis) (SimResult, error)
}

// Capabilities advertises what an engine can do, for the circuit_engines verb.
type Capabilities struct {
	Engine    string   `json:"engine"`
	Available bool     `json:"available"`
	Analyses  []string `json:"analyses"`
	Elements  []string `json:"elements"`
	Nonlinear bool     `json:"nonlinear"`
	Note      string   `json:"note,omitempty"`
}

// ErrUnsupported is returned for an element/analysis the engine cannot handle.
var ErrUnsupported = errConst("unsupported by this circuit engine")
