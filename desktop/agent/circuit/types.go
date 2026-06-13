// Package circuit is Yaver's generic, domain-agnostic electrical-circuit layer:
// a parametric netlist model + a dependency-free pure-Go simulator (modified
// nodal analysis) that runs on ANY self-hosted device with zero installs, plus
// an optional ngspice pass-through for full SPICE device models when the binary
// is present. It is the electrical sibling of the arm/robot cells — one Backend
// interface, a Controller facade, driven by the circuit_* ops verbs, the
// circuit_plot first-class MCP tool, and the web/mobile circuit cells.
//
// Everything is DATA: nodes, elements and analyses come from an imported netlist
// (SPICE, KiCad, or an EPLAN/CSV connection list), never hardcoded. Netlists are
// user work-derived, so — like arm programs — they live agent-local + P2P only
// and NEVER touch Convex.
package circuit

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ElementKind is the SPICE-style first letter of an element card.
type ElementKind string

const (
	KindResistor  ElementKind = "R"
	KindCapacitor ElementKind = "C"
	KindInductor  ElementKind = "L"
	KindVSource   ElementKind = "V" // independent voltage source
	KindISource   ElementKind = "I" // independent current source
	KindDiode     ElementKind = "D"
	KindVCVS      ElementKind = "E" // voltage-controlled voltage source
	KindVCCS      ElementKind = "G" // voltage-controlled current source
	// Connection is a non-electrical wire/link used by harness/EPLAN imports: a
	// pure connectivity edge (connector pins, terminal blocks) so the ERC engine
	// can run continuity/floating checks on designs that aren't SPICE-simulable.
	KindConnection ElementKind = "W"
)

// Waveform is the time/AC stimulus for a V or I source. Type "dc" is constant.
type Waveform struct {
	Type      string  `json:"type"`             // "dc" | "sine" | "pulse"
	DC        float64 `json:"dc,omitempty"`     // dc level (also AC analysis operating bias)
	ACMag     float64 `json:"acMag,omitempty"`  // small-signal AC magnitude (AC analysis)
	Offset    float64 `json:"offset,omitempty"` // sine offset
	Amplitude float64 `json:"amplitude,omitempty"`
	Freq      float64 `json:"freq,omitempty"`
	// pulse parameters (SPICE PULSE order)
	V1     float64 `json:"v1,omitempty"`
	V2     float64 `json:"v2,omitempty"`
	Delay  float64 `json:"delay,omitempty"`
	Rise   float64 `json:"rise,omitempty"`
	Fall   float64 `json:"fall,omitempty"`
	Width  float64 `json:"width,omitempty"`
	Period float64 `json:"period,omitempty"`
}

// At returns the source value at time t (used by transient analysis).
func (w *Waveform) At(t float64) float64 {
	if w == nil {
		return 0
	}
	switch w.Type {
	case "sine":
		return w.Offset + w.Amplitude*math.Sin(2*math.Pi*w.Freq*t)
	case "pulse":
		if w.Period <= 0 {
			// single pulse
			return pulseVal(w, t)
		}
		tp := t - w.Delay
		if tp < 0 {
			return w.V1
		}
		tp = math.Mod(tp, w.Period)
		return pulseVal(w, w.Delay+tp)
	default:
		return w.DC
	}
}

func pulseVal(w *Waveform, t float64) float64 {
	td := w.Delay
	if t < td {
		return w.V1
	}
	x := t - td
	switch {
	case w.Rise > 0 && x < w.Rise:
		return w.V1 + (w.V2-w.V1)*(x/w.Rise)
	case x < w.Rise+w.Width:
		return w.V2
	case w.Fall > 0 && x < w.Rise+w.Width+w.Fall:
		xf := x - (w.Rise + w.Width)
		return w.V2 + (w.V1-w.V2)*(xf/w.Fall)
	default:
		return w.V1
	}
}

// Element is one parsed circuit element. Nodes are listed in card order.
type Element struct {
	Name      string      `json:"name"` // "R1", "C2", "V1"
	Kind      ElementKind `json:"kind"`
	Nodes     []string    `json:"nodes"`               // connection nodes
	Value     float64     `json:"value,omitempty"`     // ohms/farads/henries/volts/amps/gain
	Wave      *Waveform   `json:"wave,omitempty"`      // V/I time stimulus
	CtrlNodes []string    `json:"ctrlNodes,omitempty"` // E/G controlling node pair
	Model     string      `json:"model,omitempty"`     // diode model token
	Raw       string      `json:"raw,omitempty"`       // original source card
}

// Netlist is the full parametric circuit. Directives carry .model/.tran/etc
// lines verbatim for the ngspice pass-through.
type Netlist struct {
	Title       string             `json:"title,omitempty"`
	Elements    []Element          `json:"elements"`
	Directives  []string           `json:"directives,omitempty"`
	NodeDomains map[string]float64 `json:"nodeDomains,omitempty"` // net → nominal volts (ERC domain check)
	Source      string             `json:"source,omitempty"`      // spice|kicad|eplan|json|builtin
}

// GroundNames are the canonical reference-node aliases (all map to MNA index -1).
var GroundNames = map[string]bool{"0": true, "gnd": true, "GND": true, "ground": true, "vss": true, "VSS": true}

// IsGround reports whether a node name is the circuit reference (0 V).
func IsGround(n string) bool { return GroundNames[strings.TrimSpace(n)] }

// Nets returns the sorted unique non-ground node names plus a flag for whether a
// ground reference exists anywhere.
func (nl Netlist) Nets() ([]string, bool) {
	set := map[string]bool{}
	hasGnd := false
	for _, e := range nl.Elements {
		for _, n := range append(append([]string{}, e.Nodes...), e.CtrlNodes...) {
			if IsGround(n) {
				hasGnd = true
				continue
			}
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, hasGnd
}

// Net is a UI/describe summary of one node.
type Net struct {
	Name      string  `json:"name"`
	ConnCount int     `json:"connCount"`
	DomainV   float64 `json:"domainV,omitempty"`
	IsGround  bool    `json:"isGround,omitempty"`
}

// ElementInfo is a UI/describe summary of one element.
type ElementInfo struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Nodes   []string `json:"nodes"`
	Value   float64  `json:"value,omitempty"`
	Display string   `json:"display,omitempty"`
}

// CircuitInfo is the parametric snapshot returned by Describe — the electrical
// analogue of arm.ArmInfo.
type CircuitInfo struct {
	Title        string        `json:"title"`
	Nets         []Net         `json:"nets"`
	Elements     []ElementInfo `json:"elements"`
	NodeCount    int           `json:"nodeCount"`
	ElementCount int           `json:"elementCount"`
	Sources      []string      `json:"sources"`
	HasGround    bool          `json:"hasGround"`
	Simulatable  bool          `json:"simulatable"` // false for pure connection-list (harness) imports
	Source       string        `json:"source"`
}

// Describe summarizes a netlist into CircuitInfo.
func (nl Netlist) Describe() CircuitInfo {
	nets, hasGnd := nl.Nets()
	conn := map[string]int{}
	simulatable := false
	for _, e := range nl.Elements {
		if e.Kind != KindConnection {
			simulatable = true
		}
		for _, n := range e.Nodes {
			conn[n]++
		}
	}
	info := CircuitInfo{Title: nl.Title, HasGround: hasGnd, Source: nl.Source, Simulatable: simulatable}
	for _, n := range nets {
		net := Net{Name: n, ConnCount: conn[n]}
		if nl.NodeDomains != nil {
			net.DomainV = nl.NodeDomains[n]
		}
		info.Nets = append(info.Nets, net)
	}
	if hasGnd {
		info.Nets = append(info.Nets, Net{Name: "0", ConnCount: conn["0"] + conn["gnd"] + conn["GND"], IsGround: true})
	}
	for _, e := range nl.Elements {
		info.Elements = append(info.Elements, ElementInfo{
			Name: e.Name, Kind: string(e.Kind), Nodes: e.Nodes, Value: e.Value,
			Display: displayValue(e),
		})
		if e.Kind == KindVSource || e.Kind == KindISource {
			info.Sources = append(info.Sources, e.Name)
		}
	}
	info.NodeCount = len(info.Nets)
	info.ElementCount = len(nl.Elements)
	return info
}

func displayValue(e Element) string {
	switch e.Kind {
	case KindResistor:
		return eng(e.Value) + "Ω"
	case KindCapacitor:
		return eng(e.Value) + "F"
	case KindInductor:
		return eng(e.Value) + "H"
	case KindVSource:
		return eng(e.Value) + "V"
	case KindISource:
		return eng(e.Value) + "A"
	case KindConnection:
		return "wire"
	}
	return ""
}

// eng renders a value with an engineering SI prefix.
func eng(v float64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	v = math.Abs(v)
	prefixes := []struct {
		exp int
		sym string
	}{{12, "T"}, {9, "G"}, {6, "M"}, {3, "k"}, {0, ""}, {-3, "m"}, {-6, "µ"}, {-9, "n"}, {-12, "p"}, {-15, "f"}}
	for _, p := range prefixes {
		scale := math.Pow(10, float64(p.exp))
		if v >= scale {
			out := fmt.Sprintf("%.4g%s", v/scale, p.sym)
			if neg {
				return "-" + out
			}
			return out
		}
	}
	out := fmt.Sprintf("%.4g", v)
	if neg {
		return "-" + out
	}
	return out
}

// Analysis selects what kind of simulation to run.
type Analysis struct {
	Type string `json:"type"` // "op" | "tran" | "ac" | "dc"

	// transient
	TStop float64 `json:"tstop,omitempty"`
	TStep float64 `json:"tstep,omitempty"`

	// ac (logarithmic decade sweep)
	FStart float64 `json:"fstart,omitempty"`
	FStop  float64 `json:"fstop,omitempty"`
	Points int     `json:"points,omitempty"` // points per decade

	// dc sweep
	SweepSrc   string  `json:"sweepSrc,omitempty"`
	SweepStart float64 `json:"sweepStart,omitempty"`
	SweepStop  float64 `json:"sweepStop,omitempty"`
	SweepStep  float64 `json:"sweepStep,omitempty"`
}

// Normalize fills sane defaults so a bare {type:"tran"} request still runs.
func (a *Analysis) Normalize() {
	a.Type = strings.ToLower(strings.TrimSpace(a.Type))
	if a.Type == "" {
		a.Type = "op"
	}
	switch a.Type {
	case "tran":
		if a.TStop <= 0 {
			a.TStop = 1e-3
		}
		if a.TStep <= 0 {
			a.TStep = a.TStop / 500
		}
	case "ac":
		if a.FStart <= 0 {
			a.FStart = 1
		}
		if a.FStop <= 0 {
			a.FStop = 1e6
		}
		if a.Points <= 0 {
			a.Points = 20
		}
	case "dc":
		if a.SweepStep == 0 {
			a.SweepStep = (a.SweepStop - a.SweepStart) / 100
		}
	}
}

// SimResult is a column-oriented result set: Samples[i] aligns with Signals.
type SimResult struct {
	Analysis       string             `json:"analysis"`
	Signals        []string           `json:"signals"` // e.g. ["time","V(out)","V(in)","I(V1)"]
	Samples        [][]float64        `json:"samples"` // for AC: real magnitude (dB) columns
	NodeVoltages   map[string]float64 `json:"nodeVoltages,omitempty"`
	BranchCurrents map[string]float64 `json:"branchCurrents,omitempty"`
	Engine         string             `json:"engine"`
	Note           string             `json:"note,omitempty"`
}

// errConst mirrors arm's tiny constant-error type.
type errConst string

func (e errConst) Error() string { return string(e) }
