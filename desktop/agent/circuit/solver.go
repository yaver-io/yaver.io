package circuit

import (
	"context"
	"fmt"
	"math"
	"math/cmplx"
	"sort"
)

// BuiltinBackend is the pure-Go modified-nodal-analysis solver. Zero external
// dependencies — it runs on any device Yaver's agent runs on. It supports
// linear R/L/C, independent V/I sources (DC, sine, pulse), linear controlled
// sources (E/G) and nonlinear diodes (Newton-Raphson), across op/dc/tran/ac
// analyses. For transistor/IC SPICE models, the ngspice pass-through takes over.
type BuiltinBackend struct{}

func NewBuiltinBackend() *BuiltinBackend { return &BuiltinBackend{} }

func (b *BuiltinBackend) Name() string    { return "builtin" }
func (b *BuiltinBackend) Available() bool { return true }

func (b *BuiltinBackend) Capabilities() Capabilities {
	return Capabilities{
		Engine:    "builtin",
		Available: true,
		Analyses:  []string{"op", "dc", "tran", "ac"},
		Elements:  []string{"R", "C", "L", "V", "I", "D", "E", "G"},
		Nonlinear: true,
		Note:      "dependency-free pure-Go MNA solver; runs anywhere, no install",
	}
}

func (b *BuiltinBackend) Simulate(ctx context.Context, nl Netlist, a Analysis) (SimResult, error) {
	a.Normalize()
	s, err := newSolver(nl)
	if err != nil {
		return SimResult{}, err
	}
	switch a.Type {
	case "op":
		return s.op()
	case "dc":
		return s.dcSweep(a)
	case "tran":
		return s.tran(ctx, a)
	case "ac":
		return s.ac(a)
	default:
		return SimResult{}, fmt.Errorf("unknown analysis %q", a.Type)
	}
}

// diode defaults (silicon, 300 K)
const (
	diodeIs   = 1e-14
	diodeVt   = 0.025852
	diodeN    = 1.0
	gmin      = 1e-12
	maxNRIter = 200
	nrTol     = 1e-7
)

type solver struct {
	nl        Netlist
	nodeIdx   map[string]int // non-ground node → 0..n-1
	nodeNames []string       // index → name
	n         int            // node count
	branches  []string       // element names with a current unknown (V, E, L-in-DC)
	branchIdx map[string]int
	size      int // n + len(branches)
}

func newSolver(nl Netlist) (*solver, error) {
	s := &solver{nl: nl, nodeIdx: map[string]int{}, branchIdx: map[string]int{}}
	nets, _ := nl.Nets()
	for _, name := range nets {
		s.nodeIdx[name] = len(s.nodeNames)
		s.nodeNames = append(s.nodeNames, name)
	}
	s.n = len(s.nodeNames)
	if s.n == 0 {
		return nil, fmt.Errorf("circuit has no non-ground nodes to solve")
	}
	return s, nil
}

func (s *solver) idx(node string) int {
	if IsGround(node) {
		return -1
	}
	return s.nodeIdx[node]
}

// buildBranches assigns current unknowns. inductorsAsShort=true for DC/op (an
// inductor is a 0 V source); false for transient (companion Norton, no unknown).
func (s *solver) buildBranches(inductorsAsShort bool) {
	s.branches = nil
	s.branchIdx = map[string]int{}
	add := func(name string) {
		s.branchIdx[name] = s.n + len(s.branches)
		s.branches = append(s.branches, name)
	}
	for _, e := range s.nl.Elements {
		switch e.Kind {
		case KindVSource, KindVCVS:
			add(e.Name)
		case KindInductor:
			if inductorsAsShort {
				add(e.Name)
			}
		}
	}
	s.size = s.n + len(s.branches)
}

// ---- linear algebra (dense, partial-pivot Gaussian elimination) ----

func solveLinear(A [][]float64, z []float64) ([]float64, error) {
	n := len(z)
	for col := 0; col < n; col++ {
		piv := col
		best := math.Abs(A[col][col])
		for r := col + 1; r < n; r++ {
			if v := math.Abs(A[r][col]); v > best {
				best, piv = v, r
			}
		}
		if best < 1e-18 {
			return nil, fmt.Errorf("singular matrix (node with no DC path to ground?)")
		}
		if piv != col {
			A[col], A[piv] = A[piv], A[col]
			z[col], z[piv] = z[piv], z[col]
		}
		inv := 1 / A[col][col]
		for r := col + 1; r < n; r++ {
			f := A[r][col] * inv
			if f == 0 {
				continue
			}
			for c := col; c < n; c++ {
				A[r][c] -= f * A[col][c]
			}
			z[r] -= f * z[col]
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		sum := z[r]
		for c := r + 1; c < n; c++ {
			sum -= A[r][c] * x[c]
		}
		x[r] = sum / A[r][r]
	}
	return x, nil
}

func solveComplex(A [][]complex128, z []complex128) ([]complex128, error) {
	n := len(z)
	for col := 0; col < n; col++ {
		piv := col
		best := cmplx.Abs(A[col][col])
		for r := col + 1; r < n; r++ {
			if v := cmplx.Abs(A[r][col]); v > best {
				best, piv = v, r
			}
		}
		if best < 1e-18 {
			return nil, fmt.Errorf("singular complex matrix")
		}
		if piv != col {
			A[col], A[piv] = A[piv], A[col]
			z[col], z[piv] = z[piv], z[col]
		}
		inv := 1 / A[col][col]
		for r := col + 1; r < n; r++ {
			f := A[r][col] * inv
			if f == 0 {
				continue
			}
			for c := col; c < n; c++ {
				A[r][c] -= f * A[col][c]
			}
			z[r] -= f * z[col]
		}
	}
	x := make([]complex128, n)
	for r := n - 1; r >= 0; r-- {
		sum := z[r]
		for c := r + 1; c < n; c++ {
			sum -= A[r][c] * x[c]
		}
		x[r] = sum / A[r][r]
	}
	return x, nil
}

func newMat(n int) [][]float64 {
	A := make([][]float64, n)
	for i := range A {
		A[i] = make([]float64, n)
	}
	return A
}

// stamp helpers (real)
func stampG(A [][]float64, a, b int, g float64) {
	if a >= 0 {
		A[a][a] += g
	}
	if b >= 0 {
		A[b][b] += g
	}
	if a >= 0 && b >= 0 {
		A[a][b] -= g
		A[b][a] -= g
	}
}

// inject adds an independent current of value cur injected INTO node a and
// removed from node b (i.e. a current source pushing current a→b externally).
func inject(z []float64, a, b int, cur float64) {
	if a >= 0 {
		z[a] += cur
	}
	if b >= 0 {
		z[b] -= cur
	}
}

// ---- DC operating point (Newton-Raphson for diodes) ----

func (s *solver) op() (SimResult, error) {
	v, err := s.solveDC(nil, 0)
	if err != nil {
		return SimResult{}, err
	}
	res := SimResult{Analysis: "op", Engine: "builtin", NodeVoltages: map[string]float64{}, BranchCurrents: map[string]float64{}}
	for name, i := range s.nodeIdx {
		res.NodeVoltages[name] = v[i]
	}
	for _, name := range s.branches {
		res.BranchCurrents[name] = v[s.branchIdx[name]]
	}
	// signals: a single row table
	sigs := []string{}
	row := []float64{}
	for _, name := range sortedKeys(res.NodeVoltages) {
		sigs = append(sigs, "V("+name+")")
		row = append(row, res.NodeVoltages[name])
	}
	for _, name := range s.branches {
		sigs = append(sigs, "I("+name+")")
		row = append(row, res.BranchCurrents[name])
	}
	res.Signals = sigs
	res.Samples = [][]float64{row}
	return res, nil
}

// solveDC solves the operating point. override/overrideVal optionally pin one
// voltage source to a swept value (for dc sweeps). prevState is nil for pure DC.
func (s *solver) solveDC(override map[string]float64, t float64) ([]float64, error) {
	return s.solveStep(true, nil, nil, 0, t, override)
}

// solveStep is the shared NR-on-diodes assemble+solve, used by op/dc/tran.
//
//	inductorsAsShort: DC/op semantics (L is a wire). false → transient companion.
//	capPrev/indPrev: previous-step companion state keyed by element name.
//	h: timestep (0 for DC). t: time for source waveforms.
//	override: pinned source DC values (dc sweep).
func (s *solver) solveStep(inductorsAsShort bool, capPrev, indPrev map[string]float64, h, t float64, override map[string]float64) ([]float64, error) {
	s.buildBranches(inductorsAsShort)
	// Newton-Raphson outer loop for nonlinear diodes.
	vd := map[string]float64{} // per-diode junction guess
	var x []float64
	for iter := 0; iter < maxNRIter; iter++ {
		A := newMat(s.size)
		z := make([]float64, s.size)
		for _, e := range s.nl.Elements {
			s.stampElement(A, z, e, inductorsAsShort, capPrev, indPrev, h, t, override, vd)
		}
		sol, err := solveLinear(A, z)
		if err != nil {
			return nil, err
		}
		// update diode guesses, check convergence
		converged := true
		for _, e := range s.nl.Elements {
			if e.Kind != KindDiode {
				continue
			}
			va := nodeV(sol, s.idx(e.Nodes[0]))
			vc := nodeV(sol, s.idx(e.Nodes[1]))
			nv := va - vc
			if old, ok := vd[e.Name]; ok {
				if math.Abs(nv-old) > nrTol+1e-3*math.Abs(nv) {
					converged = false
				}
			} else {
				converged = false
			}
			vd[e.Name] = limitJunction(vd[e.Name], nv)
		}
		x = sol
		if converged || !s.hasDiode() {
			break
		}
	}
	return x, nil
}

func (s *solver) hasDiode() bool {
	for _, e := range s.nl.Elements {
		if e.Kind == KindDiode {
			return true
		}
	}
	return false
}

func nodeV(x []float64, i int) float64 {
	if i < 0 {
		return 0
	}
	return x[i]
}

// limitJunction damps NR steps across a diode junction to keep exp() sane.
func limitJunction(vold, vnew float64) float64 {
	const vcrit = 0.6
	if vnew > vcrit && math.Abs(vnew-vold) > 2*diodeVt {
		if vold > 0 {
			arg := 1 + (vnew-vold)/diodeVt
			if arg > 0 {
				return vold + diodeVt*math.Log(arg)
			}
			return vcrit
		}
		return diodeVt * math.Log(vnew/diodeVt)
	}
	return vnew
}

func (s *solver) stampElement(A [][]float64, z []float64, e Element, inductorsAsShort bool, capPrev, indPrev map[string]float64, h, t float64, override, vd map[string]float64) {
	switch e.Kind {
	case KindResistor:
		if e.Value != 0 {
			stampG(A, s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), 1/e.Value)
		}
	case KindCapacitor:
		if h > 0 {
			geq := e.Value / h
			stampG(A, s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), geq)
			ieq := geq * capPrev[e.Name]
			inject(z, s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), ieq)
		}
		// DC/op: open — no stamp.
	case KindInductor:
		if inductorsAsShort {
			bi := s.branchIdx[e.Name]
			s.stampVoltageBranch(A, z, s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), bi, 0)
		} else {
			geq := h / e.Value
			stampG(A, s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), geq)
			// previous inductor current flows n+ -> n-
			inject(z, s.idx(e.Nodes[1]), s.idx(e.Nodes[0]), indPrev[e.Name])
		}
	case KindVSource:
		val := e.Wave.At(t)
		if override != nil {
			if ov, ok := override[e.Name]; ok {
				val = ov
			}
		}
		bi := s.branchIdx[e.Name]
		s.stampVoltageBranch(A, z, s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), bi, val)
	case KindISource:
		val := e.Wave.At(t)
		if override != nil {
			if ov, ok := override[e.Name]; ok {
				val = ov
			}
		}
		inject(z, s.idx(e.Nodes[1]), s.idx(e.Nodes[0]), val) // into n-, out of n+
	case KindDiode:
		a, c := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
		v := vd[e.Name]
		ex := math.Exp(math.Min(v/(diodeN*diodeVt), 80))
		id := diodeIs * (ex - 1)
		geq := diodeIs/(diodeN*diodeVt)*ex + gmin
		ieq := id - geq*v
		stampG(A, a, c, geq)
		inject(z, c, a, ieq) // diode current a->c contributes companion source
	case KindVCCS: // current gm*(vc+ - vc-) from n+ to n-
		gm := e.Value
		np, nm := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
		cp, cm := s.idx(e.CtrlNodes[0]), s.idx(e.CtrlNodes[1])
		if np >= 0 {
			if cp >= 0 {
				A[np][cp] += gm
			}
			if cm >= 0 {
				A[np][cm] -= gm
			}
		}
		if nm >= 0 {
			if cp >= 0 {
				A[nm][cp] -= gm
			}
			if cm >= 0 {
				A[nm][cm] += gm
			}
		}
	case KindVCVS: // v(n+)-v(n-) = gain*(vc+ - vc-)
		bi := s.branchIdx[e.Name]
		np, nm := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
		cp, cm := s.idx(e.CtrlNodes[0]), s.idx(e.CtrlNodes[1])
		if np >= 0 {
			A[np][bi] += 1
			A[bi][np] += 1
		}
		if nm >= 0 {
			A[nm][bi] -= 1
			A[bi][nm] -= 1
		}
		if cp >= 0 {
			A[bi][cp] -= e.Value
		}
		if cm >= 0 {
			A[bi][cm] += e.Value
		}
	}
}

func (s *solver) stampVoltageBranch(A [][]float64, z []float64, np, nm, bi int, val float64) {
	if np >= 0 {
		A[np][bi] += 1
		A[bi][np] += 1
	}
	if nm >= 0 {
		A[nm][bi] -= 1
		A[bi][nm] -= 1
	}
	z[bi] = val
}

// ---- DC sweep ----

func (s *solver) dcSweep(a Analysis) (SimResult, error) {
	if a.SweepSrc == "" {
		return SimResult{}, fmt.Errorf("dc sweep needs sweepSrc")
	}
	res := SimResult{Analysis: "dc", Engine: "builtin"}
	res.Signals = append(res.Signals, a.SweepSrc)
	nodeNames := sortedKeysInt(s.nodeIdx)
	for _, n := range nodeNames {
		res.Signals = append(res.Signals, "V("+n+")")
	}
	step := a.SweepStep
	if step == 0 {
		step = 1
	}
	for val := a.SweepStart; (step > 0 && val <= a.SweepStop+1e-12) || (step < 0 && val >= a.SweepStop-1e-12); val += step {
		x, err := s.solveDC(map[string]float64{a.SweepSrc: val}, 0)
		if err != nil {
			return SimResult{}, err
		}
		row := []float64{val}
		for _, n := range nodeNames {
			row = append(row, x[s.nodeIdx[n]])
		}
		res.Samples = append(res.Samples, row)
	}
	return res, nil
}

// ---- transient ----

func (s *solver) tran(ctx context.Context, a Analysis) (SimResult, error) {
	capPrev := map[string]float64{}
	indPrev := map[string]float64{}
	// initial condition = DC op with caps open / inductors shorted
	x0, err := s.solveDC(nil, 0)
	if err != nil {
		return SimResult{}, err
	}
	for _, e := range s.nl.Elements {
		switch e.Kind {
		case KindCapacitor:
			capPrev[e.Name] = nodeV(x0, s.idx(e.Nodes[0])) - nodeV(x0, s.idx(e.Nodes[1]))
		case KindInductor:
			s.buildBranches(true)
			indPrev[e.Name] = x0[s.branchIdx[e.Name]]
		}
	}
	nodeNames := sortedKeysInt(s.nodeIdx)
	res := SimResult{Analysis: "tran", Engine: "builtin"}
	res.Signals = append(res.Signals, "time")
	for _, n := range nodeNames {
		res.Signals = append(res.Signals, "V("+n+")")
	}
	h := a.TStep
	steps := int(math.Ceil(a.TStop / h))
	for k := 0; k <= steps; k++ {
		if ctx != nil && ctx.Err() != nil {
			return res, ctx.Err()
		}
		t := float64(k) * h
		x, err := s.solveStep(false, capPrev, indPrev, h, t, nil)
		if err != nil {
			return SimResult{}, err
		}
		row := []float64{t}
		for _, n := range nodeNames {
			row = append(row, x[s.nodeIdx[n]])
		}
		res.Samples = append(res.Samples, row)
		// advance companion state
		for _, e := range s.nl.Elements {
			switch e.Kind {
			case KindCapacitor:
				capPrev[e.Name] = nodeV(x, s.idx(e.Nodes[0])) - nodeV(x, s.idx(e.Nodes[1]))
			case KindInductor:
				vp := nodeV(x, s.idx(e.Nodes[0])) - nodeV(x, s.idx(e.Nodes[1]))
				indPrev[e.Name] = h/e.Value*vp + indPrev[e.Name]
			}
		}
	}
	return res, nil
}

// ---- AC small-signal (linearized at the DC operating point) ----

func (s *solver) ac(a Analysis) (SimResult, error) {
	// operating point to freeze diode conductances
	xop, err := s.solveDC(nil, 0)
	if err != nil {
		return SimResult{}, err
	}
	diodeG := map[string]float64{}
	for _, e := range s.nl.Elements {
		if e.Kind == KindDiode {
			v := nodeV(xop, s.idx(e.Nodes[0])) - nodeV(xop, s.idx(e.Nodes[1]))
			ex := math.Exp(math.Min(v/(diodeN*diodeVt), 80))
			diodeG[e.Name] = diodeIs/(diodeN*diodeVt)*ex + gmin
		}
	}
	s.buildBranches(false) // inductors get their own AC admittance, no short branch
	// AC needs voltage-source + VCVS branches.
	s.branches = nil
	s.branchIdx = map[string]int{}
	for _, e := range s.nl.Elements {
		if e.Kind == KindVSource || e.Kind == KindVCVS {
			s.branchIdx[e.Name] = s.n + len(s.branches)
			s.branches = append(s.branches, e.Name)
		}
	}
	s.size = s.n + len(s.branches)

	nodeNames := sortedKeysInt(s.nodeIdx)
	res := SimResult{Analysis: "ac", Engine: "builtin"}
	res.Signals = append(res.Signals, "freq")
	for _, n := range nodeNames {
		res.Signals = append(res.Signals, "V("+n+")dB", "V("+n+")deg")
	}

	decades := math.Log10(a.FStop / a.FStart)
	total := int(decades*float64(a.Points)) + 1
	for k := 0; k <= total; k++ {
		f := a.FStart * math.Pow(10, float64(k)/float64(a.Points))
		if f > a.FStop*(1+1e-9) {
			break
		}
		w := 2 * math.Pi * f
		x, err := s.acSolveAt(w, diodeG)
		if err != nil {
			return SimResult{}, err
		}
		row := []float64{f}
		for _, n := range nodeNames {
			v := x[s.nodeIdx[n]]
			mag := cmplx.Abs(v)
			db := -300.0
			if mag > 0 {
				db = 20 * math.Log10(mag)
			}
			row = append(row, db, cmplx.Phase(v)*180/math.Pi)
		}
		res.Samples = append(res.Samples, row)
	}
	return res, nil
}

func (s *solver) acSolveAt(w float64, diodeG map[string]float64) ([]complex128, error) {
	A := make([][]complex128, s.size)
	for i := range A {
		A[i] = make([]complex128, s.size)
	}
	z := make([]complex128, s.size)
	cstampG := func(a, b int, g complex128) {
		if a >= 0 {
			A[a][a] += g
		}
		if b >= 0 {
			A[b][b] += g
		}
		if a >= 0 && b >= 0 {
			A[a][b] -= g
			A[b][a] -= g
		}
	}
	for _, e := range s.nl.Elements {
		switch e.Kind {
		case KindResistor:
			if e.Value != 0 {
				cstampG(s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), complex(1/e.Value, 0))
			}
		case KindCapacitor:
			cstampG(s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), complex(0, w*e.Value))
		case KindInductor:
			if w*e.Value != 0 {
				cstampG(s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), complex(0, -1/(w*e.Value)))
			}
		case KindDiode:
			cstampG(s.idx(e.Nodes[0]), s.idx(e.Nodes[1]), complex(diodeG[e.Name], 0))
		case KindVCCS:
			gm := complex(e.Value, 0)
			np, nm := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
			cp, cm := s.idx(e.CtrlNodes[0]), s.idx(e.CtrlNodes[1])
			if np >= 0 {
				if cp >= 0 {
					A[np][cp] += gm
				}
				if cm >= 0 {
					A[np][cm] -= gm
				}
			}
			if nm >= 0 {
				if cp >= 0 {
					A[nm][cp] -= gm
				}
				if cm >= 0 {
					A[nm][cm] += gm
				}
			}
		case KindVSource:
			bi := s.branchIdx[e.Name]
			np, nm := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
			if np >= 0 {
				A[np][bi] += 1
				A[bi][np] += 1
			}
			if nm >= 0 {
				A[nm][bi] -= 1
				A[bi][nm] -= 1
			}
			z[bi] = complex(acMag(e.Wave), 0)
		case KindISource:
			cur := complex(acMag(e.Wave), 0)
			np, nm := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
			if nm >= 0 {
				z[nm] += cur
			}
			if np >= 0 {
				z[np] -= cur
			}
		case KindVCVS:
			bi := s.branchIdx[e.Name]
			np, nm := s.idx(e.Nodes[0]), s.idx(e.Nodes[1])
			cp, cm := s.idx(e.CtrlNodes[0]), s.idx(e.CtrlNodes[1])
			if np >= 0 {
				A[np][bi] += 1
				A[bi][np] += 1
			}
			if nm >= 0 {
				A[nm][bi] -= 1
				A[bi][nm] -= 1
			}
			if cp >= 0 {
				A[bi][cp] -= complex(e.Value, 0)
			}
			if cm >= 0 {
				A[bi][cm] += complex(e.Value, 0)
			}
		}
	}
	return solveComplex(A, z)
}

func acMag(w *Waveform) float64 {
	if w == nil {
		return 0
	}
	if w.ACMag != 0 {
		return w.ACMag
	}
	if w.Type == "sine" {
		return w.Amplitude
	}
	return 0
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// also support sortedKeys for int-valued maps
func sortedKeysInt(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
