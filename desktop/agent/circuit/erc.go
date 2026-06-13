package circuit

import (
	"fmt"
	"sort"
)

// ERCSeverity grades a finding.
type ERCSeverity string

const (
	SevError ERCSeverity = "error"
	SevWarn  ERCSeverity = "warning"
	SevInfo  ERCSeverity = "info"
)

// ERCFinding is one electrical-rule-check result.
type ERCFinding struct {
	Rule     string      `json:"rule"`
	Severity ERCSeverity `json:"severity"`
	Net      string      `json:"net,omitempty"`
	Element  string      `json:"element,omitempty"`
	Message  string      `json:"message"`
}

// ERCReport is the full result.
type ERCReport struct {
	Findings []ERCFinding `json:"findings"`
	Errors   int          `json:"errors"`
	Warnings int          `json:"warnings"`
	OK       bool         `json:"ok"` // true when no error-severity findings
}

// IsolationElements are element kinds that legitimately bridge two voltage
// domains (a transformer/optocoupler is modeled as such; here a Connection is
// NOT isolating but a capacitor/diode/controlled-source link is treated as an
// intentional, non-DC bridge so we don't false-positive on coupling caps).
func isolatingKind(k ElementKind) bool {
	switch k {
	case KindCapacitor, KindVCVS, KindVCCS:
		return true
	}
	return false
}

// RunERC runs the generic electrical-rule-check engine over a netlist. The rule
// set is domain-agnostic; power/safety/harness callers add per-net DomainV tags
// (nominal volts) to enable the voltage-domain rule without any code change.
func RunERC(nl Netlist) ERCReport {
	var rep ERCReport
	add := func(f ERCFinding) {
		rep.Findings = append(rep.Findings, f)
		switch f.Severity {
		case SevError:
			rep.Errors++
		case SevWarn:
			rep.Warnings++
		}
	}

	nets, hasGnd := nl.Nets()
	conn := map[string]int{}
	kindsOnNet := map[string]map[ElementKind]bool{}
	for _, e := range nl.Elements {
		for _, n := range e.Nodes {
			if IsGround(n) {
				continue
			}
			conn[n]++
			if kindsOnNet[n] == nil {
				kindsOnNet[n] = map[ElementKind]bool{}
			}
			kindsOnNet[n][e.Kind] = true
		}
	}

	// Rule 1: missing ground reference.
	if !hasGnd {
		add(ERCFinding{Rule: "no-ground", Severity: SevError,
			Message: "no ground/reference node (0) found — the circuit has no DC reference"})
	}

	// Rule 2: floating / dangling nets (fewer than two connections).
	for _, n := range nets {
		switch conn[n] {
		case 0:
			// shouldn't happen (net came from an element) but guard anyway
		case 1:
			add(ERCFinding{Rule: "dangling-net", Severity: SevWarn, Net: n,
				Message: fmt.Sprintf("net %q connects to only one pin — dangling/floating", n)})
		}
	}

	// Rule 3: source with both terminals on the same node (shorted source).
	for _, e := range nl.Elements {
		if (e.Kind == KindVSource) && len(e.Nodes) == 2 && e.Nodes[0] == e.Nodes[1] {
			add(ERCFinding{Rule: "shorted-source", Severity: SevError, Element: e.Name,
				Message: fmt.Sprintf("voltage source %s is shorted (both terminals on %q)", e.Name, e.Nodes[0])})
		}
	}

	// Rule 4: voltage-domain mismatch. Two nets with differing DomainV tags that
	// are tied together by a non-isolating element (R/L/wire) → dangerous direct
	// coupling between e.g. a mains net and a SELV net.
	if len(nl.NodeDomains) > 0 {
		for _, e := range nl.Elements {
			if len(e.Nodes) != 2 || isolatingKind(e.Kind) {
				continue
			}
			a, b := e.Nodes[0], e.Nodes[1]
			da, oka := nl.NodeDomains[a]
			db, okb := nl.NodeDomains[b]
			if oka && okb && da != db {
				add(ERCFinding{Rule: "voltage-domain-mismatch", Severity: SevError, Element: e.Name,
					Message: fmt.Sprintf("%s directly couples %gV domain (%s) to %gV domain (%s) with no isolation",
						e.Name, da, a, db, b)})
			}
		}
	}

	// Rule 5: no path to ground from a node (informational connectivity check
	// via union-find — flags nets in a component with no ground).
	if hasGnd {
		for _, n := range islandsWithoutGround(nl) {
			add(ERCFinding{Rule: "no-dc-path-to-ground", Severity: SevWarn, Net: n,
				Message: fmt.Sprintf("net %q has no connectivity path to ground", n)})
		}
	}

	sort.SliceStable(rep.Findings, func(i, j int) bool {
		order := map[ERCSeverity]int{SevError: 0, SevWarn: 1, SevInfo: 2}
		return order[rep.Findings[i].Severity] < order[rep.Findings[j].Severity]
	})
	rep.OK = rep.Errors == 0
	return rep
}

// islandsWithoutGround returns nets in a connected component that does not
// contain the ground node, using union-find over element edges.
func islandsWithoutGround(nl Netlist) []string {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" {
			parent[x] = x
		}
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) { parent[find(a)] = find(b) }

	gnd := "0"
	for _, e := range nl.Elements {
		if len(e.Nodes) < 2 {
			continue
		}
		a := e.Nodes[0]
		b := e.Nodes[1]
		if IsGround(a) {
			a = gnd
		}
		if IsGround(b) {
			b = gnd
		}
		union(a, b)
	}
	var out []string
	seen := map[string]bool{}
	nets, _ := nl.Nets()
	for _, n := range nets {
		if find(n) != find(gnd) && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
