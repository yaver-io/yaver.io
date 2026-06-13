package circuit

import (
	"fmt"
	"strings"
)

// ParseKiCad parses a KiCad netlist export (the s-expression `(export …)` form
// produced by Eeschema "Export Netlist"). It reconstructs a simulatable Netlist:
// components become elements (kind inferred from the reference prefix), and the
// (nets …) section maps each pin to its net so two-terminal parts get their two
// node names. Multi-pin / unknown parts are emitted as Connection edges so ERC
// still runs over the full design.
func ParseKiCad(text string) (Netlist, error) {
	node, _, err := parseSexpr(text, 0)
	if err != nil {
		return Netlist{}, fmt.Errorf("kicad: %w", err)
	}
	root := node
	if root == nil || root.head() != "export" {
		// some exports wrap differently; try to locate the export node
		root = findChild(node, "export")
		if root == nil {
			return Netlist{}, fmt.Errorf("kicad: not a netlist export (no (export …))")
		}
	}

	nl := Netlist{Source: "kicad"}

	// component values
	values := map[string]string{}
	if comps := findChild(root, "components"); comps != nil {
		for _, c := range comps.children() {
			if c.head() != "comp" {
				continue
			}
			ref := atomOf(findChild(c, "ref"))
			val := atomOf(findChild(c, "value"))
			if ref != "" {
				values[ref] = val
			}
		}
	}

	// nets: build (ref,pin) -> netname and ref -> ordered pins
	pinNet := map[string]string{}    // "REF.PIN" -> net
	refPins := map[string][]string{} // ref -> pins (in net-declared order)
	if nets := findChild(root, "nets"); nets != nil {
		for _, net := range nets.children() {
			if net.head() != "net" {
				continue
			}
			name := atomOf(findChild(net, "name"))
			if name == "" {
				name = "code" + atomOf(findChild(net, "code"))
			}
			name = normalizeNet(name)
			for _, ch := range net.children() {
				if ch.head() != "node" {
					continue
				}
				ref := atomOf(findChild(ch, "ref"))
				pin := atomOf(findChild(ch, "pin"))
				if ref == "" || pin == "" {
					continue
				}
				pinNet[ref+"."+pin] = name
				refPins[ref] = append(refPins[ref], pin)
			}
		}
	}
	if len(refPins) == 0 {
		return nl, fmt.Errorf("kicad: no nets found")
	}

	for ref, pins := range refPins {
		kind := kindFromRef(ref)
		el := Element{Name: ref, Kind: kind}
		uniq := dedupePins(pins)
		if len(uniq) == 2 && kind != KindConnection {
			el.Nodes = []string{pinNet[ref+"."+uniq[0]], pinNet[ref+"."+uniq[1]]}
			el.Value, _ = parseValue(values[ref])
			if kind == KindVSource || kind == KindISource {
				el.Wave = &Waveform{Type: "dc", DC: el.Value}
			}
			nl.Elements = append(nl.Elements, el)
		} else {
			// multi-pin or unknown: emit pairwise connection edges (continuity)
			el.Kind = KindConnection
			for i := 1; i < len(uniq); i++ {
				nl.Elements = append(nl.Elements, Element{
					Name:  fmt.Sprintf("%s_%s_%s", ref, uniq[0], uniq[i]),
					Kind:  KindConnection,
					Nodes: []string{pinNet[ref+"."+uniq[0]], pinNet[ref+"."+uniq[i]]},
				})
			}
		}
	}
	if len(nl.Elements) == 0 {
		return nl, fmt.Errorf("kicad: no usable components")
	}
	return nl, nil
}

func normalizeNet(n string) string {
	n = strings.TrimSpace(n)
	n = strings.TrimPrefix(n, "/")
	if n == "" {
		return "0"
	}
	return n
}

func dedupePins(pins []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pins {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// kindFromRef maps a KiCad reference designator prefix to an element kind.
func kindFromRef(ref string) ElementKind {
	p := strings.ToUpper(strings.TrimLeft(ref, "#"))
	if p == "" {
		return KindConnection
	}
	switch p[0] {
	case 'R':
		return KindResistor
	case 'C':
		return KindCapacitor
	case 'L':
		return KindInductor
	case 'D':
		return KindDiode
	case 'V':
		return KindVSource
	case 'I':
		return KindISource
	default:
		return KindConnection // U/Q/J/P/SW/connectors → continuity only
	}
}

// ---- minimal s-expression parser ----

type sexpr struct {
	atom   string
	list   []*sexpr
	isList bool
}

func (s *sexpr) head() string {
	if s == nil || !s.isList || len(s.list) == 0 {
		return ""
	}
	return s.list[0].atom
}

func (s *sexpr) children() []*sexpr {
	if s == nil || !s.isList {
		return nil
	}
	return s.list
}

// atomOf returns the first atom argument of a list node, e.g. (ref R1) → "R1".
func atomOf(s *sexpr) string {
	if s == nil || !s.isList || len(s.list) < 2 {
		return ""
	}
	return s.list[1].atom
}

// findChild returns the first direct child list whose head == name.
func findChild(s *sexpr, name string) *sexpr {
	if s == nil || !s.isList {
		return nil
	}
	for _, c := range s.list {
		if c.isList && c.head() == name {
			return c
		}
	}
	return nil
}

func parseSexpr(s string, i int) (*sexpr, int, error) {
	i = skipWS(s, i)
	if i >= len(s) {
		return nil, i, fmt.Errorf("unexpected end")
	}
	if s[i] != '(' {
		// atom
		start := i
		for i < len(s) && !isDelim(s[i]) {
			i++
		}
		return &sexpr{atom: s[start:i]}, i, nil
	}
	i++ // consume '('
	node := &sexpr{isList: true}
	for {
		i = skipWS(s, i)
		if i >= len(s) {
			return nil, i, fmt.Errorf("unterminated list")
		}
		if s[i] == ')' {
			return node, i + 1, nil
		}
		if s[i] == '"' {
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
				j++
			}
			node.list = append(node.list, &sexpr{atom: b.String()})
			i = j + 1
			continue
		}
		child, ni, err := parseSexpr(s, i)
		if err != nil {
			return nil, ni, err
		}
		node.list = append(node.list, child)
		i = ni
	}
}

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

func isDelim(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' || c == ')' || c == '"'
}
