package circuit

import "strings"

// Config is the vault/file-backed definition of the circuit cell. Like the arm
// cell it is fully parametric — the loaded netlist (from SPICE/KiCad/EPLAN) IS
// the circuit; the config only selects the engine and remembers the source +
// default analysis. Stored in vault project "circuit", name "circuit-config",
// with a ~/.yaver/circuit-config.json fallback.
type Config struct {
	// Engine: "auto" (ngspice if installed, else builtin), "builtin", "ngspice".
	Engine string `json:"engine,omitempty"`
	// NgspicePath overrides the ngspice binary location (default: PATH lookup).
	NgspicePath string `json:"ngspicePath,omitempty"`

	// Netlist is the currently-loaded circuit. Persisted so the cell survives a
	// daemon restart. Empty until the first circuit_import.
	Netlist Netlist `json:"netlist,omitempty"`

	// DefaultAnalysis is pre-filled in the UI / used when a verb omits it.
	DefaultAnalysis Analysis `json:"defaultAnalysis,omitempty"`

	Label     string `json:"label,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

func (c *Config) Normalize() {
	c.Engine = strings.ToLower(strings.TrimSpace(c.Engine))
	if c.Engine == "" {
		c.Engine = "auto"
	}
	if c.DefaultAnalysis.Type == "" {
		c.DefaultAnalysis.Type = "op"
	}
	c.DefaultAnalysis.Normalize()
	if c.Netlist.NodeDomains == nil {
		c.Netlist.NodeDomains = map[string]float64{}
	}
}

// Enabled reports whether the circuit cell has a circuit loaded. The built-in
// engine is always available, so a cell is "enabled" the moment a netlist with
// at least one element exists.
func (c Config) Enabled() bool {
	return len(c.Netlist.Elements) > 0
}
