package circuit

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Controller is the circuit-cell facade — the electrical analogue of
// arm.Controller. It owns the loaded netlist + engine selection and exposes
// Describe/Import/Export/Simulate/ERC/Plot to the ops layer.
type Controller struct {
	mu  sync.Mutex
	cfg Config
}

// NewController builds a controller from a (normalized) config.
func NewController(cfg Config) *Controller {
	cfg.Normalize()
	return &Controller{cfg: cfg}
}

func (c *Controller) Config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

func (c *Controller) SetConfig(cfg Config) {
	cfg.Normalize()
	c.mu.Lock()
	c.cfg = cfg
	c.mu.Unlock()
}

func (c *Controller) Netlist() Netlist {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Netlist
}

func (c *Controller) Describe() CircuitInfo {
	return c.Netlist().Describe()
}

// Import parses a circuit from text. format is "spice", "kicad", "eplan",
// "csv", or "auto" (sniffed). The parsed netlist replaces the loaded one but
// preserves any existing per-net DomainV tags for matching net names.
func (c *Controller) Import(format, text string) (CircuitInfo, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" || format == "auto" {
		format = sniffFormat(text)
	}
	var (
		nl  Netlist
		err error
	)
	switch format {
	case "kicad":
		nl, err = ParseKiCad(text)
	case "eplan", "csv", "wirelist":
		nl, err = ParseConnectionList(text)
	default:
		nl, err = ParseSPICE(text)
	}
	if err != nil {
		return CircuitInfo{}, err
	}
	if len(nl.Elements) == 0 {
		return CircuitInfo{}, fmt.Errorf("no elements parsed from %s input", format)
	}
	c.mu.Lock()
	// carry forward domain tags for nets that still exist
	if old := c.cfg.Netlist.NodeDomains; len(old) > 0 {
		if nl.NodeDomains == nil {
			nl.NodeDomains = map[string]float64{}
		}
		nets, _ := nl.Nets()
		live := map[string]bool{}
		for _, n := range nets {
			live[n] = true
		}
		for k, v := range old {
			if live[k] {
				if _, set := nl.NodeDomains[k]; !set {
					nl.NodeDomains[k] = v
				}
			}
		}
	}
	c.cfg.Netlist = nl
	c.mu.Unlock()
	return nl.Describe(), nil
}

// SetDomain tags a net with its nominal voltage for the ERC voltage-domain rule.
func (c *Controller) SetDomain(net string, volts float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Netlist.NodeDomains == nil {
		c.cfg.Netlist.NodeDomains = map[string]float64{}
	}
	c.cfg.Netlist.NodeDomains[net] = volts
}

func (c *Controller) ExportSPICE() string { return c.Netlist().EmitSPICE() }

// backend resolves the engine per config: builtin, ngspice, or auto.
func (c *Controller) backend() (Backend, error) {
	cfg := c.Config()
	switch cfg.Engine {
	case "builtin":
		return NewBuiltinBackend(), nil
	case "ngspice":
		nb := NewNgspiceBackend(cfg.NgspicePath)
		if !nb.Available() {
			return nil, fmt.Errorf("engine 'ngspice' selected but ngspice is not installed (PATH)")
		}
		return nb, nil
	default: // auto
		nb := NewNgspiceBackend(cfg.NgspicePath)
		if nb.Available() {
			return nb, nil
		}
		return NewBuiltinBackend(), nil
	}
}

// Engines reports the capability of every engine, for circuit_engines.
func (c *Controller) Engines() []Capabilities {
	cfg := c.Config()
	out := []Capabilities{NewBuiltinBackend().Capabilities()}
	out = append(out, NewNgspiceBackend(cfg.NgspicePath).Capabilities())
	return out
}

// Simulate runs an analysis on the loaded netlist with the resolved engine.
func (c *Controller) Simulate(ctx context.Context, a Analysis) (SimResult, error) {
	nl := c.Netlist()
	if len(nl.Elements) == 0 {
		return SimResult{}, fmt.Errorf("no circuit loaded — circuit_import a netlist first")
	}
	if !nl.Describe().Simulatable {
		return SimResult{}, fmt.Errorf("loaded design is a connection list (harness/EPLAN) — run circuit_erc, not simulate")
	}
	be, err := c.backend()
	if err != nil {
		return SimResult{}, err
	}
	return be.Simulate(ctx, nl, a)
}

func (c *Controller) ERC() ERCReport { return RunERC(c.Netlist()) }

// Plot runs the analysis (defaulting to config.DefaultAnalysis) and renders the
// result to a PNG; signals optionally filters which traces are drawn.
func (c *Controller) Plot(ctx context.Context, a Analysis, signals []string) ([]byte, SimResult, error) {
	if a.Type == "" {
		a = c.Config().DefaultAnalysis
	}
	if a.Type == "op" {
		a.Type = "tran" // op has no waveform; plot a transient instead
		a.Normalize()
	}
	res, err := c.Simulate(ctx, a)
	if err != nil {
		return nil, SimResult{}, err
	}
	png, err := PlotPNG(res, signals)
	if err != nil {
		return nil, res, err
	}
	return png, res, nil
}

// sniffFormat guesses the import format from the text shape.
func sniffFormat(text string) string {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	if strings.HasPrefix(t, "(export") || strings.Contains(low, "(components") || strings.Contains(low, "(nets") {
		return "kicad"
	}
	// connection list: comma/semicolon/tab-separated with a connector-ish header
	first := t
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		first = t[:i]
	}
	if strings.ContainsAny(first, ",;\t") &&
		(strings.Contains(low, "connector") || strings.Contains(low, "from") || strings.Contains(low, "net") || strings.Contains(low, "wire") || strings.Contains(low, "pin")) {
		return "eplan"
	}
	return "spice"
}
