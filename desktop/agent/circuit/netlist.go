package circuit

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSPICE parses a SPICE-style deck (the common subset: R/C/L/V/I/D/E/G cards,
// .model/.tran/.ac/.dc directives, '*' comments, ';' inline comments) into a
// Netlist. It is intentionally lenient: unknown cards are preserved as raw
// Connection-less directives so an ngspice pass-through still sees them, while
// the built-in solver simulates what it understands.
func ParseSPICE(text string) (Netlist, error) {
	nl := Netlist{Source: "spice"}
	lines := strings.Split(text, "\n")
	// Join continuation lines (next line starting with '+').
	joined := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimRight(ln, "\r")
		if strings.HasPrefix(strings.TrimSpace(t), "+") && len(joined) > 0 {
			joined[len(joined)-1] += " " + strings.TrimSpace(t)[1:]
			continue
		}
		joined = append(joined, t)
	}

	titleTaken := false
	for i, raw := range joined {
		line := raw
		if idx := strings.Index(line, ";"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "*") {
			if !titleTaken && i == 0 {
				nl.Title = strings.TrimSpace(strings.TrimPrefix(line, "*"))
				titleTaken = true
			}
			continue
		}
		if strings.HasPrefix(line, ".") {
			low := strings.ToLower(line)
			switch {
			case strings.HasPrefix(low, ".end"):
				continue
			case strings.HasPrefix(low, ".title"):
				nl.Title = strings.TrimSpace(line[len(".title"):])
			default:
				nl.Directives = append(nl.Directives, line)
			}
			continue
		}
		// First non-comment/non-directive line of a real deck is the title only
		// when it does NOT look like an element card.
		if !titleTaken && i == 0 && !looksLikeCard(line) {
			nl.Title = line
			titleTaken = true
			continue
		}
		el, err := parseCard(line)
		if err != nil {
			return nl, fmt.Errorf("line %d: %w", i+1, err)
		}
		if el != nil {
			nl.Elements = append(nl.Elements, *el)
		}
	}
	return nl, nil
}

func looksLikeCard(line string) bool {
	f := strings.Fields(line)
	if len(f) < 2 {
		return false
	}
	switch strings.ToUpper(line[:1]) {
	case "R", "C", "L", "V", "I", "D", "E", "G", "W":
		return true
	}
	return false
}

func parseCard(line string) (*Element, error) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return nil, fmt.Errorf("too few fields: %q", line)
	}
	name := f[0]
	kind := ElementKind(strings.ToUpper(name[:1]))
	el := &Element{Name: name, Kind: kind, Raw: line}

	switch kind {
	case KindResistor, KindCapacitor, KindInductor:
		el.Nodes = []string{f[1], f[2]}
		if len(f) >= 4 {
			v, err := parseValue(f[3])
			if err != nil {
				return nil, err
			}
			el.Value = v
		}
	case KindVSource, KindISource:
		el.Nodes = []string{f[1], f[2]}
		el.Wave = parseSourceSpec(f[3:])
		el.Value = el.Wave.DC
	case KindDiode:
		el.Nodes = []string{f[1], f[2]}
		if len(f) >= 4 {
			el.Model = f[3]
		}
	case KindVCVS, KindVCCS:
		if len(f) < 6 {
			return nil, fmt.Errorf("controlled source needs 4 nodes + gain: %q", line)
		}
		el.Nodes = []string{f[1], f[2]}
		el.CtrlNodes = []string{f[3], f[4]}
		v, err := parseValue(f[5])
		if err != nil {
			return nil, err
		}
		el.Value = v
	case KindConnection:
		el.Nodes = []string{f[1], f[2]}
	default:
		return nil, fmt.Errorf("unknown element kind %q", kind)
	}
	return el, nil
}

// parseSourceSpec parses the value spec of a V/I source: a bare number, "DC x",
// "AC mag", "SIN(off amp freq)" or "PULSE(v1 v2 td tr tf pw per)" in any combo.
func parseSourceSpec(fields []string) *Waveform {
	w := &Waveform{Type: "dc"}
	joined := strings.Join(fields, " ")
	upper := strings.ToUpper(joined)

	// DC value (explicit or bare leading number)
	if v, err := parseValue(fields[0]); err == nil {
		w.DC = v
	}
	if i := strings.Index(upper, "DC"); i >= 0 {
		rest := strings.Fields(joined[i+2:])
		if len(rest) > 0 {
			if v, err := parseValue(rest[0]); err == nil {
				w.DC = v
			}
		}
	}
	if i := strings.Index(upper, "AC"); i >= 0 {
		rest := strings.Fields(joined[i+2:])
		if len(rest) > 0 {
			if v, err := parseValue(rest[0]); err == nil {
				w.ACMag = v
			}
		}
	}
	if args := parenArgs(joined, "SIN"); args != nil {
		w.Type = "sine"
		set := func(i int, p *float64) {
			if i < len(args) {
				*p, _ = parseValueOr(args[i])
			}
		}
		set(0, &w.Offset)
		set(1, &w.Amplitude)
		set(2, &w.Freq)
	}
	if args := parenArgs(joined, "PULSE"); args != nil {
		w.Type = "pulse"
		ptrs := []*float64{&w.V1, &w.V2, &w.Delay, &w.Rise, &w.Fall, &w.Width, &w.Period}
		for i, p := range ptrs {
			if i < len(args) {
				*p, _ = parseValueOr(args[i])
			}
		}
	}
	if w.ACMag == 0 && (w.Type == "sine" || w.Type == "pulse") {
		w.ACMag = 1 // sensible AC stimulus for time-domain sources
	}
	return w
}

func parenArgs(s, fn string) []string {
	up := strings.ToUpper(s)
	i := strings.Index(up, fn+"(")
	if i < 0 {
		// also accept "fn (" with a space
		i = strings.Index(up, fn+" (")
		if i < 0 {
			return nil
		}
	}
	open := strings.Index(s[i:], "(")
	if open < 0 {
		return nil
	}
	open += i
	close := strings.Index(s[open:], ")")
	if close < 0 {
		return nil
	}
	inner := s[open+1 : open+close]
	inner = strings.ReplaceAll(inner, ",", " ")
	return strings.Fields(inner)
}

func parseValueOr(s string) (float64, error) { return parseValue(s) }

// parseValue parses a SPICE number with engineering suffixes (k, meg, m, u, n,
// p, f, g, t, mil) — case-insensitive, trailing unit letters ignored.
func parseValue(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	low := strings.ToLower(s)
	// Split numeric prefix from suffix.
	i := 0
	for i < len(low) {
		c := low[i]
		if (c >= '0' && c <= '9') || c == '.' || c == '+' || c == '-' || c == 'e' {
			// 'e' is ambiguous (exponent vs the 'e' in "1e3" already covered); allow
			// only when followed by a digit/sign.
			if c == 'e' {
				if i+1 < len(low) && (low[i+1] == '+' || low[i+1] == '-' || (low[i+1] >= '0' && low[i+1] <= '9')) {
					i++
					continue
				}
				break
			}
			i++
			continue
		}
		break
	}
	num := low[:i]
	suf := low[i:]
	base, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("bad number %q", s)
	}
	mult := 1.0
	switch {
	case strings.HasPrefix(suf, "meg"):
		mult = 1e6
	case strings.HasPrefix(suf, "mil"):
		mult = 25.4e-6
	case strings.HasPrefix(suf, "k"):
		mult = 1e3
	case strings.HasPrefix(suf, "g"):
		mult = 1e9
	case strings.HasPrefix(suf, "t"):
		mult = 1e12
	case strings.HasPrefix(suf, "m"):
		mult = 1e-3
	case strings.HasPrefix(suf, "u"), strings.HasPrefix(suf, "µ"):
		mult = 1e-6
	case strings.HasPrefix(suf, "n"):
		mult = 1e-9
	case strings.HasPrefix(suf, "p"):
		mult = 1e-12
	case strings.HasPrefix(suf, "f"):
		mult = 1e-15
	}
	return base * mult, nil
}

// EmitSPICE renders a Netlist back to a clean SPICE deck (fed verbatim to
// ngspice; also the canonical export format).
func (nl Netlist) EmitSPICE() string {
	var b strings.Builder
	title := nl.Title
	if title == "" {
		title = "yaver circuit"
	}
	fmt.Fprintf(&b, "* %s\n", title)
	for _, e := range nl.Elements {
		fmt.Fprintln(&b, emitCard(e))
	}
	for _, d := range nl.Directives {
		fmt.Fprintln(&b, d)
	}
	fmt.Fprintln(&b, ".end")
	return b.String()
}

func emitCard(e Element) string {
	switch e.Kind {
	case KindResistor, KindCapacitor, KindInductor:
		return fmt.Sprintf("%s %s %s %s", e.Name, e.Nodes[0], e.Nodes[1], num(e.Value))
	case KindVSource, KindISource:
		return fmt.Sprintf("%s %s %s %s", e.Name, e.Nodes[0], e.Nodes[1], emitSource(e.Wave))
	case KindDiode:
		m := e.Model
		if m == "" {
			m = "D"
		}
		return fmt.Sprintf("%s %s %s %s", e.Name, e.Nodes[0], e.Nodes[1], m)
	case KindVCVS, KindVCCS:
		return fmt.Sprintf("%s %s %s %s %s %s", e.Name, e.Nodes[0], e.Nodes[1], e.CtrlNodes[0], e.CtrlNodes[1], num(e.Value))
	case KindConnection:
		return fmt.Sprintf("* W %s %s %s (connection)", e.Name, e.Nodes[0], e.Nodes[1])
	}
	if e.Raw != "" {
		return e.Raw
	}
	return "* " + e.Name
}

func emitSource(w *Waveform) string {
	if w == nil {
		return "0"
	}
	switch w.Type {
	case "sine":
		return fmt.Sprintf("DC %s AC %s SIN(%s %s %s)", num(w.DC), num(w.ACMag), num(w.Offset), num(w.Amplitude), num(w.Freq))
	case "pulse":
		return fmt.Sprintf("DC %s AC %s PULSE(%s %s %s %s %s %s %s)", num(w.DC), num(w.ACMag),
			num(w.V1), num(w.V2), num(w.Delay), num(w.Rise), num(w.Fall), num(w.Width), num(w.Period))
	default:
		if w.ACMag != 0 {
			return fmt.Sprintf("DC %s AC %s", num(w.DC), num(w.ACMag))
		}
		return fmt.Sprintf("DC %s", num(w.DC))
	}
}

func num(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
