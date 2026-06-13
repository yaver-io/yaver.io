package circuit

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// NgspiceBackend shells out to an installed `ngspice -b` for full SPICE device
// models (BJT/MOSFET/.model libraries) that the built-in solver doesn't carry.
// It is optional: Available() is false when the binary isn't on PATH, and the
// controller's "auto" engine falls back to the built-in solver.
type NgspiceBackend struct {
	path string
}

func NewNgspiceBackend(path string) *NgspiceBackend { return &NgspiceBackend{path: path} }

func (n *NgspiceBackend) bin() string {
	if strings.TrimSpace(n.path) != "" {
		return n.path
	}
	return "ngspice"
}

func (n *NgspiceBackend) Name() string { return "ngspice" }

func (n *NgspiceBackend) Available() bool {
	_, err := exec.LookPath(n.bin())
	return err == nil
}

func (n *NgspiceBackend) Capabilities() Capabilities {
	avail := n.Available()
	note := "external SPICE engine — full .model device support (BJT/MOSFET/etc)"
	if !avail {
		note = "not installed; `apt install ngspice` / `brew install ngspice` to enable"
	}
	return Capabilities{
		Engine:    "ngspice",
		Available: avail,
		Analyses:  []string{"op", "dc", "tran", "ac"},
		Elements:  []string{"R", "C", "L", "V", "I", "D", "E", "G", "Q", "M", "X"},
		Nonlinear: true,
		Note:      note,
	}
}

func (n *NgspiceBackend) Simulate(ctx context.Context, nl Netlist, a Analysis) (SimResult, error) {
	a.Normalize()
	if !n.Available() {
		return SimResult{}, fmt.Errorf("ngspice not installed")
	}
	nets, _ := nl.Nets()
	if len(nets) == 0 {
		return SimResult{}, fmt.Errorf("circuit has no nodes")
	}

	dir, err := os.MkdirTemp("", "yaver-spice-")
	if err != nil {
		return SimResult{}, err
	}
	defer os.RemoveAll(dir)
	outFile := filepath.Join(dir, "out.data")

	deck := n.buildDeck(nl, a, nets, outFile)
	deckFile := filepath.Join(dir, "deck.cir")
	if err := os.WriteFile(deckFile, []byte(deck), 0o600); err != nil {
		return SimResult{}, err
	}

	cmd := exec.CommandContext(ctx, n.bin(), "-b", deckFile)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return SimResult{}, fmt.Errorf("ngspice: %v: %s", err, strings.TrimSpace(stderr.String()))
	}

	cols := n.outputVectors(a, nets)
	return n.parseWrdata(outFile, a, cols)
}

// buildDeck assembles a SPICE deck with a .control block that runs the analysis
// and dumps single-scale wrdata we can parse back.
func (n *NgspiceBackend) buildDeck(nl Netlist, a Analysis, nets []string, outFile string) string {
	var b strings.Builder
	title := nl.Title
	if title == "" {
		title = "yaver circuit"
	}
	fmt.Fprintf(&b, "* %s\n", title)
	for _, e := range nl.Elements {
		if e.Kind == KindConnection {
			continue
		}
		fmt.Fprintln(&b, emitCard(e))
	}
	for _, d := range nl.Directives {
		fmt.Fprintln(&b, d)
	}

	vecs := make([]string, 0, len(nets))
	for _, net := range nets {
		if a.Type == "ac" {
			vecs = append(vecs, "vdb("+net+")")
		} else {
			vecs = append(vecs, "v("+net+")")
		}
	}

	b.WriteString(".control\n")
	b.WriteString("set wr_singlescale\n")
	b.WriteString("set wr_vecnames\n")
	switch a.Type {
	case "op":
		b.WriteString("op\n")
	case "tran":
		fmt.Fprintf(&b, "tran %s %s\n", num(a.TStep), num(a.TStop))
	case "ac":
		fmt.Fprintf(&b, "ac dec %d %s %s\n", a.Points, num(a.FStart), num(a.FStop))
	case "dc":
		fmt.Fprintf(&b, "dc %s %s %s %s\n", a.SweepSrc, num(a.SweepStart), num(a.SweepStop), num(a.SweepStep))
	}
	fmt.Fprintf(&b, "wrdata %s %s\n", outFile, strings.Join(vecs, " "))
	b.WriteString(".endc\n")
	b.WriteString(".end\n")
	return b.String()
}

func (n *NgspiceBackend) outputVectors(a Analysis, nets []string) []string {
	xlabel := "x"
	switch a.Type {
	case "tran":
		xlabel = "time"
	case "ac":
		xlabel = "freq"
	case "dc":
		xlabel = a.SweepSrc
	}
	cols := []string{xlabel}
	for _, net := range nets {
		if a.Type == "ac" {
			cols = append(cols, "V("+net+")dB")
		} else {
			cols = append(cols, "V("+net+")")
		}
	}
	return cols
}

func (n *NgspiceBackend) parseWrdata(file string, a Analysis, cols []string) (SimResult, error) {
	f, err := os.Open(file)
	if err != nil {
		return SimResult{}, fmt.Errorf("ngspice produced no output: %w", err)
	}
	defer f.Close()

	res := SimResult{Analysis: a.Type, Engine: "ngspice", Signals: cols}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// skip header / vecname lines (non-numeric first token)
		if _, e := strconv.ParseFloat(fields[0], 64); e != nil {
			continue
		}
		row := make([]float64, 0, len(fields))
		for _, fld := range fields {
			v, e := strconv.ParseFloat(fld, 64)
			if e != nil {
				v = 0
			}
			row = append(row, v)
		}
		res.Samples = append(res.Samples, row)
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	if a.Type == "op" && len(res.Samples) > 0 {
		res.NodeVoltages = map[string]float64{}
		row := res.Samples[len(res.Samples)-1]
		for i, name := range cols {
			if i == 0 || i >= len(row) {
				continue
			}
			net := strings.TrimSuffix(strings.TrimPrefix(name, "V("), ")")
			res.NodeVoltages[net] = row[i]
		}
	}
	return res, nil
}
