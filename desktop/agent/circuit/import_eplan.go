package circuit

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseConnectionList imports an EPLAN-style / harness connection list (the
// reliable, structured interchange both control-panel and wire-harness
// workflows rely on, in place of brittle PDF OCR). It is delimiter-agnostic
// (comma/semicolon/tab) and shape-agnostic:
//
//   - wirelist shape  (fromConnector, fromPin, …, toConnector, toPin): nodes are
//     terminals ("CONN:PIN"), edges are wires. Continuity / island ERC applies.
//   - net-graph shape (net, component, pin): nodes are nets, edges are the
//     components that bridge them (a 2-pin part links its two nets). Dangling-net
//     and voltage-domain ERC apply; tag a net's nominal volts via a voltage
//     column to arm the isolation check.
//
// Everything is emitted as Connection elements, so the design is ERC-checkable
// but not SPICE-simulable (circuit_simulate refuses; circuit_erc runs).
func ParseConnectionList(text string) (Netlist, error) {
	rows := splitRows(text)
	if len(rows) == 0 {
		return Netlist{}, fmt.Errorf("empty connection list")
	}
	delim := sniffDelimiter(rows[0])
	header := splitFields(rows[0], delim)
	roles := mapRoles(header)

	dataRows := rows
	hasHeader := len(roles) > 0
	if hasHeader {
		dataRows = rows[1:]
	}

	nl := Netlist{Source: "eplan", NodeDomains: map[string]float64{}}

	wirelist := has(roles, "fromconn") && has(roles, "toconn")
	netgraph := has(roles, "net") && has(roles, "component")

	if !wirelist && !netgraph {
		// positional fallback
		ncols := len(splitFields(firstNonEmpty(dataRows), delim))
		if ncols >= 4 {
			wirelist = true
			roles = map[string]int{"fromconn": 0, "frompin": 1, "toconn": 3, "topin": 4}
		} else if ncols >= 3 {
			netgraph = true
			roles = map[string]int{"net": 0, "component": 1, "pin": 2}
		} else {
			return Netlist{}, fmt.Errorf("unrecognized connection list: need a header or ≥3 columns")
		}
		hasHeader = false
		dataRows = rows
	}

	get := func(f []string, role string) string {
		i, ok := roles[role]
		if !ok || i < 0 || i >= len(f) {
			return ""
		}
		return strings.TrimSpace(f[i])
	}

	if wirelist {
		idx := 0
		for _, r := range dataRows {
			f := splitFields(r, delim)
			if len(f) == 0 || strings.TrimSpace(r) == "" {
				continue
			}
			fc, fp := get(f, "fromconn"), get(f, "frompin")
			tc, tp := get(f, "toconn"), get(f, "topin")
			if fc == "" || tc == "" {
				continue
			}
			from := terminal(fc, fp)
			to := terminal(tc, tp)
			idx++
			name := get(f, "wire")
			if name == "" {
				name = fmt.Sprintf("W%d", idx)
			}
			nl.Elements = append(nl.Elements, Element{Name: name, Kind: KindConnection, Nodes: []string{from, to}})
		}
	} else { // netgraph: group by component, link its nets
		type pinNet struct{ pin, net string }
		comps := map[string][]pinNet{}
		var order []string
		for _, r := range dataRows {
			f := splitFields(r, delim)
			if strings.TrimSpace(r) == "" {
				continue
			}
			net := normalizeNet(get(f, "net"))
			comp := get(f, "component")
			pin := get(f, "pin")
			if comp == "" || net == "" {
				continue
			}
			if _, ok := comps[comp]; !ok {
				order = append(order, comp)
			}
			comps[comp] = append(comps[comp], pinNet{pin, net})
			if v := get(f, "voltage"); v != "" {
				if fv, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					nl.NodeDomains[net] = fv
				}
			}
		}
		for _, comp := range order {
			pins := comps[comp]
			if len(pins) < 2 {
				// single-pin device on a net → a stub edge to itself records presence
				if len(pins) == 1 {
					nl.Elements = append(nl.Elements, Element{Name: comp, Kind: KindConnection, Nodes: []string{pins[0].net, pins[0].net}})
				}
				continue
			}
			for i := 1; i < len(pins); i++ {
				name := comp
				if len(pins) > 2 {
					name = fmt.Sprintf("%s_%d", comp, i)
				}
				nl.Elements = append(nl.Elements, Element{Name: name, Kind: KindConnection, Nodes: []string{pins[0].net, pins[i].net}})
			}
		}
	}

	if len(nl.Elements) == 0 {
		return nl, fmt.Errorf("no connections parsed")
	}
	if len(nl.NodeDomains) == 0 {
		nl.NodeDomains = nil
	}
	return nl, nil
}

func terminal(conn, pin string) string {
	conn = strings.TrimSpace(conn)
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return conn
	}
	return conn + ":" + pin
}

func splitRows(text string) []string {
	raw := strings.Split(text, "\n")
	var out []string
	for _, r := range raw {
		r = strings.TrimRight(r, "\r")
		if strings.TrimSpace(r) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(r), "#") {
			continue
		}
		out = append(out, r)
	}
	return out
}

func firstNonEmpty(rows []string) string {
	for _, r := range rows {
		if strings.TrimSpace(r) != "" {
			return r
		}
	}
	return ""
}

func sniffDelimiter(line string) rune {
	c := strings.Count(line, ",")
	s := strings.Count(line, ";")
	t := strings.Count(line, "\t")
	switch {
	case t >= c && t >= s && t > 0:
		return '\t'
	case s >= c && s > 0:
		return ';'
	default:
		return ','
	}
}

func splitFields(line string, delim rune) []string {
	parts := strings.Split(line, string(delim))
	for i := range parts {
		parts[i] = strings.TrimSpace(strings.Trim(parts[i], `"`))
	}
	return parts
}

// mapRoles fuzzy-matches header column names to logical roles. Returns an empty
// map when no column name is recognizable (→ caller uses positional fallback).
func mapRoles(header []string) map[string]int {
	roles := map[string]int{}
	for i, h := range header {
		assignRole(roles, strings.ToLower(strings.TrimSpace(h)), i)
	}
	return roles
}

func assignRole(roles map[string]int, l string, i int) {
	set := func(role string) {
		if _, ok := roles[role]; !ok {
			roles[role] = i
		}
	}
	switch {
	case matchAny(l, "from connector", "from device", "connector a", "conn a", "source connector") && !strings.Contains(l, "pin"):
		set("fromconn")
	case matchAny(l, "from pin", "from terminal", "pin a", "terminal a"):
		set("frompin")
	case matchAny(l, "to connector", "to device", "connector b", "conn b", "dest connector", "target connector") && !strings.Contains(l, "pin"):
		set("toconn")
	case matchAny(l, "to pin", "to terminal", "pin b", "terminal b"):
		set("topin")
	case (l == "from" || strings.HasPrefix(l, "from")) && !strings.Contains(l, "pin"):
		set("fromconn")
	case (l == "to" || strings.HasPrefix(l, "to")) && !strings.Contains(l, "pin"):
		set("toconn")
	case matchAny(l, "wire", "cable", "wire no", "wire number", "marking", "label"):
		set("wire")
	case matchAny(l, "net", "signal", "potential"):
		set("net")
	case matchAny(l, "component", "device", "ref", "reference", "part", "designation", "bmk"):
		set("component")
	case matchAny(l, "pin", "terminal", "klemme", "contact"):
		set("pin")
	case matchAny(l, "voltage", "volts", "nominal v", "domain"):
		set("voltage")
	}
}

func has(m map[string]int, k string) bool { _, ok := m[k]; return ok }

func matchAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
