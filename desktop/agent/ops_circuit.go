package main

// ops_circuit.go — Yaver's electrical-circuit cell as native ops verbs, the
// electrical sibling of ops_armcell.go / ops_robot.go. One parametric layer
// imports a circuit (SPICE netlist, KiCad export, or an EPLAN/harness
// connection list), simulates it with a dependency-free pure-Go MNA engine
// (op/dc/tran/ac) — or an installed ngspice for full SPICE device models —
// runs a generic ERC, and renders waveforms the host model can SEE via the
// circuit_plot first-class MCP tool.
//
// Like arm programs, netlists are user work-derived: they live in the local
// vault ("circuit"/"circuit-config") + a ~/.yaver/circuit-config.json fallback
// and NEVER touch Convex.
//
// Service shape (per docs/yaver-circuit-simulation-cross-repo.md): this cell is
// the engine behind a hosted "black box" simulator that other products (Talos,
// OCPP/Kalkan) drive remotely on a per-product Hetzner box. That adds three
// service primitives on top of the single-circuit cell:
//   - design slots — an optional `design` id on every verb selects a named
//     netlist slot (vault "circuit"/"circuit-design-<id>"), so many designs
//     coexist on one node. Empty/"default" = the legacy single slot.
//   - circuit_health — engine availability + active-design summary for the
//     consumer's status widget.
//   - run audit — every import/sim/erc/plot is recorded (who + design + analysis
//     + outcome) to the local audit black box, which syncs to Convex as a
//     contents-free activity summary. NEVER the netlist itself.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/circuit"
)

const circuitVaultProject = "circuit"
const circuitVaultConfigName = "circuit-config"
const circuitDesignSlotPrefix = "circuit-design-"

// sanitizeDesignID normalizes an optional design-slot id. "" and "default" both
// map to the legacy single slot. The id keys both a vault entry name and a
// filename, so we keep it to a safe charset and a sane length.
func sanitizeDesignID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "default" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// designLabelOut renders a slot id for output: the empty slot reads "default".
func designLabelOut(design string) string {
	if d := sanitizeDesignID(design); d != "" {
		return d
	}
	return "default"
}

// circuitSlotName maps a (sanitized) design id to its vault entry name.
func circuitSlotName(design string) string {
	if design == "" {
		return circuitVaultConfigName
	}
	return circuitDesignSlotPrefix + design
}

func circuitConfigFilePathFor(design string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", circuitSlotName(sanitizeDesignID(design))+".json")
}

// circuitConfigFilePath is the default-slot file path (back-compat).
func circuitConfigFilePath() string { return circuitConfigFilePathFor("") }

var (
	circuitMu    sync.Mutex
	circuitCtrls = map[string]*circuit.Controller{}
)

func circuitConfigDefault() circuit.Config {
	c := circuit.Config{Engine: strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_CIRCUIT_ENGINE")))}
	c.Normalize()
	return c
}

// circuitConfigGetFor loads the config for a design slot: vault first, then the
// ~/.yaver file fallback, then defaults.
func circuitConfigGetFor(design string) circuit.Config {
	design = sanitizeDesignID(design)
	name := circuitSlotName(design)
	cfg := circuitConfigDefault()
	found := false
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(circuitVaultProject, name); gerr == nil && e != nil && e.Value != "" {
			var c circuit.Config
			if json.Unmarshal([]byte(e.Value), &c) == nil {
				cfg, found = c, true
			}
		}
	}
	if !found {
		if b, err := os.ReadFile(circuitConfigFilePathFor(design)); err == nil {
			var c circuit.Config
			if json.Unmarshal(b, &c) == nil {
				cfg = c
			}
		}
	}
	cfg.Normalize()
	return cfg
}

func circuitConfigSaveFor(design string, c circuit.Config) error {
	design = sanitizeDesignID(design)
	c.Normalize()
	c.UpdatedAt = time.Now().UnixMilli()
	b, _ := json.Marshal(c)
	name := circuitSlotName(design)
	var vaultErr error
	if vs, err := openVaultOptional(); err == nil {
		vaultErr = vs.Set(VaultEntry{Project: circuitVaultProject, Name: name, Category: "custom", Value: string(b), Notes: "Yaver circuit cell — design '" + designLabelOut(design) + "'"})
	} else {
		vaultErr = err
	}
	if vaultErr != nil {
		if ferr := os.WriteFile(circuitConfigFilePathFor(design), b, 0o600); ferr != nil {
			return ferr
		}
	}
	return nil
}

// back-compat wrappers for the default slot.
func circuitConfigGet() circuit.Config         { return circuitConfigGetFor("") }
func circuitConfigSave(c circuit.Config) error { return circuitConfigSaveFor("", c) }

// ensureCircuitFor returns the process-wide controller for a design slot,
// hydrated from the persisted config on first use. Each slot gets its own
// controller so concurrent designs on a shared node never stomp each other.
func ensureCircuitFor(design string) *circuit.Controller {
	design = sanitizeDesignID(design)
	circuitMu.Lock()
	defer circuitMu.Unlock()
	if c := circuitCtrls[design]; c != nil {
		return c
	}
	c := circuit.NewController(circuitConfigGetFor(design))
	circuitCtrls[design] = c
	return c
}

// persistCircuitFor writes a slot's controller config (incl. loaded netlist).
func persistCircuitFor(design string, ctrl *circuit.Controller) {
	_ = circuitConfigSaveFor(sanitizeDesignID(design), ctrl.Config())
}

// back-compat wrappers for the default slot.
func ensureCircuit() *circuit.Controller      { return ensureCircuitFor("") }
func persistCircuit(ctrl *circuit.Controller) { persistCircuitFor("", ctrl) }

// circuitListDesigns enumerates the design slots stored on this node (vault
// entries by name-prefix, plus the ~/.yaver file fallback for slots written
// while the vault was locked). The default slot is always present.
func circuitListDesigns() []map[string]any {
	out := []map[string]any{}
	seen := map[string]bool{}
	add := func(design string) {
		design = sanitizeDesignID(design)
		if seen[design] {
			return
		}
		seen[design] = true
		cfg := circuitConfigGetFor(design)
		info := cfg.Netlist.Describe()
		out = append(out, map[string]any{
			"design":      designLabelOut(design),
			"title":       info.Title,
			"elements":    len(cfg.Netlist.Elements),
			"simulatable": info.Simulatable,
			"engine":      cfg.Engine,
			"updatedAt":   cfg.UpdatedAt,
		})
	}
	add("") // default slot always listed
	if vs, err := openVaultOptional(); err == nil {
		for _, s := range vs.List(circuitVaultProject) {
			if strings.HasPrefix(s.Name, circuitDesignSlotPrefix) {
				add(strings.TrimPrefix(s.Name, circuitDesignSlotPrefix))
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(home, ".yaver", circuitDesignSlotPrefix+"*.json"))
		for _, m := range matches {
			id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), circuitDesignSlotPrefix), ".json")
			add(id)
		}
	}
	return out
}

// circuitAudit records a circuit run to the local audit black box. Privacy: the
// target + detail carry only the design slot id and the analysis type — NEVER
// netlist contents, element values, or filesystem paths. Of these, only
// action/target/outcome/error sync to Convex (detail stays in the local
// audit.db payload column).
func circuitAudit(c OpsContext, action, design, detail string, res OpsResult) {
	user := strings.TrimSpace(c.ActorUserID)
	if user == "" {
		user = "owner"
	}
	outcome, errMsg := "ok", ""
	if !res.OK {
		outcome, errMsg = "error", res.Error
	}
	AuditLog(user, "circuit_"+action, "circuit/"+designLabelOut(design), detail, outcome, errMsg, "")
}

type circuitPayload struct {
	// Design selects a named netlist slot on this node; "" / "default" = the
	// legacy single slot. Per-product boxes use this to hold many designs.
	Design string `json:"design"`

	Format string `json:"format"`
	Text   string `json:"text"`
	Spice  string `json:"spice"` // alias for Text

	Analysis *circuit.Analysis `json:"analysis"`
	// flat analysis fields (alternative to nested Analysis)
	Type       string  `json:"type"`
	TStop      float64 `json:"tstop"`
	TStep      float64 `json:"tstep"`
	FStart     float64 `json:"fstart"`
	FStop      float64 `json:"fstop"`
	Points     int     `json:"points"`
	SweepSrc   string  `json:"sweepSrc"`
	SweepStart float64 `json:"sweepStart"`
	SweepStop  float64 `json:"sweepStop"`
	SweepStep  float64 `json:"sweepStep"`

	Signals []string `json:"signals"`
	Net     string   `json:"net"`
	Volts   float64  `json:"volts"`

	Engine          string            `json:"engine"`
	NgspicePath     string            `json:"ngspicePath"`
	DefaultAnalysis *circuit.Analysis `json:"defaultAnalysis"`
}

func (p circuitPayload) source() string {
	if strings.TrimSpace(p.Text) != "" {
		return p.Text
	}
	return p.Spice
}

func (p circuitPayload) analysis() circuit.Analysis {
	if p.Analysis != nil {
		return *p.Analysis
	}
	return circuit.Analysis{
		Type: p.Type, TStop: p.TStop, TStep: p.TStep,
		FStart: p.FStart, FStop: p.FStop, Points: p.Points,
		SweepSrc: p.SweepSrc, SweepStart: p.SweepStart, SweepStop: p.SweepStop, SweepStep: p.SweepStep,
	}
}

func parseCircuitPayload(payload json.RawMessage) circuitPayload {
	var p circuitPayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	return p
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("circuit_engines", "List circuit simulation engines + capabilities (builtin always; ngspice if installed) {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		return OpsResult{OK: true, Initial: map[string]any{"engines": ctrl.Engines(), "active": ctrl.Config().Engine, "design": designLabelOut(p.Design)}}
	})

	reg("circuit_config_get", "Get the circuit cell config (engine + loaded-circuit summary) {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		cfg := ctrl.Config()
		return OpsResult{OK: true, Initial: map[string]any{
			"design":          designLabelOut(p.Design),
			"engine":          cfg.Engine,
			"ngspicePath":     cfg.NgspicePath,
			"enabled":         cfg.Enabled(),
			"info":            ctrl.Describe(),
			"defaultAnalysis": cfg.DefaultAnalysis,
		}}
	})

	reg("circuit_config_set", "Set engine ('auto'|'builtin'|'ngspice'), ngspicePath, or defaultAnalysis {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		cfg := ctrl.Config()
		if p.Engine != "" {
			cfg.Engine = p.Engine
		}
		if p.NgspicePath != "" {
			cfg.NgspicePath = p.NgspicePath
		}
		if p.DefaultAnalysis != nil {
			cfg.DefaultAnalysis = *p.DefaultAnalysis
		}
		ctrl.SetConfig(cfg)
		persistCircuitFor(p.Design, ctrl)
		return OpsResult{OK: true, Initial: map[string]any{"engine": ctrl.Config().Engine, "design": designLabelOut(p.Design)}}
	})

	reg("circuit_import", "Import a circuit from SPICE/KiCad/EPLAN text {format?:auto, text|spice, design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		src := p.source()
		if strings.TrimSpace(src) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "text (or spice) required"}
		}
		// allow base64-wrapped uploads (mobile/web file pickers)
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(src)); err == nil && looksBinaryText(dec) {
			src = string(dec)
		}
		ctrl := ensureCircuitFor(p.Design)
		info, err := ctrl.Import(p.Format, src)
		if err != nil {
			res := OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			circuitAudit(c, "import", p.Design, p.Format, res)
			return res
		}
		persistCircuitFor(p.Design, ctrl)
		res := OpsResult{OK: true, Initial: map[string]any{"info": info, "design": designLabelOut(p.Design)}}
		circuitAudit(c, "import", p.Design, info.Source, res)
		return res
	})

	reg("circuit_export", "Export the loaded circuit {format?:spice|json, design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		if strings.ToLower(p.Format) == "json" {
			return OpsResult{OK: true, Initial: map[string]any{"format": "json", "netlist": ctrl.Netlist()}}
		}
		return OpsResult{OK: true, Initial: map[string]any{"format": "spice", "spice": ctrl.ExportSPICE()}}
	})

	reg("circuit_describe", "Parametric snapshot of the loaded circuit (nets, elements, sources) {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		return OpsResult{OK: true, Initial: map[string]any{"info": ensureCircuitFor(p.Design).Describe(), "design": designLabelOut(p.Design)}}
	})

	reg("circuit_simulate", "Run an analysis {type:op|dc|tran|ac, ..., design?} on the loaded circuit", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		a := p.analysis()
		res, err := ctrl.Simulate(c.Ctx, a)
		if err != nil {
			out := OpsResult{OK: false, Code: "backend", Error: err.Error()}
			circuitAudit(c, "simulate", p.Design, a.Type, out)
			return out
		}
		out := OpsResult{OK: true, Initial: map[string]any{"result": res, "design": designLabelOut(p.Design)}}
		circuitAudit(c, "simulate", p.Design, a.Type, out)
		return out
	})

	reg("circuit_measure", "Convenience DC operating point — node voltages + branch currents {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		res, err := ctrl.Simulate(c.Ctx, circuit.Analysis{Type: "op"})
		if err != nil {
			out := OpsResult{OK: false, Code: "backend", Error: err.Error()}
			circuitAudit(c, "measure", p.Design, "op", out)
			return out
		}
		out := OpsResult{OK: true, Initial: map[string]any{
			"nodeVoltages": res.NodeVoltages, "branchCurrents": res.BranchCurrents, "engine": res.Engine, "design": designLabelOut(p.Design),
		}}
		circuitAudit(c, "measure", p.Design, "op", out)
		return out
	})

	reg("circuit_erc", "Run the generic electrical-rule-check (floating nets, no ground, voltage-domain mismatch, islands) {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		out := OpsResult{OK: true, Initial: map[string]any{"report": ensureCircuitFor(p.Design).ERC(), "design": designLabelOut(p.Design)}}
		circuitAudit(c, "erc", p.Design, "", out)
		return out
	})

	reg("circuit_set_domain", "Tag a net with its nominal voltage {net, volts, design?} to arm the ERC isolation check", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		if strings.TrimSpace(p.Net) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "net required"}
		}
		ctrl := ensureCircuitFor(p.Design)
		ctrl.SetDomain(p.Net, p.Volts)
		persistCircuitFor(p.Design, ctrl)
		return OpsResult{OK: true, Initial: map[string]any{"net": p.Net, "volts": p.Volts, "design": designLabelOut(p.Design)}}
	})

	reg("circuit_plot", "Render a waveform PNG (data URL) of an analysis {type, signals?, design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		a := p.analysis()
		png, res, err := ctrl.Plot(c.Ctx, a, p.Signals)
		if err != nil {
			out := OpsResult{OK: false, Code: "backend", Error: err.Error()}
			circuitAudit(c, "plot", p.Design, a.Type, out)
			return out
		}
		dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		out := OpsResult{OK: true, Initial: map[string]any{
			"image": dataURL, "analysis": res.Analysis, "signals": res.Signals, "engine": res.Engine, "design": designLabelOut(p.Design),
		}}
		circuitAudit(c, "plot", p.Design, a.Type, out)
		return out
	})

	// --- service primitives (hosted "black box" simulator) ---

	reg("circuit_designs", "List the netlist design slots stored on this sim node (per-product box can hold many)", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"designs": circuitListDesigns()}}
	})

	reg("circuit_design_delete", "Delete a named design slot {design} (the default slot cannot be deleted — circuit_import to replace it)", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		d := sanitizeDesignID(p.Design)
		if d == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "cannot delete the default design; circuit_import to replace it"}
		}
		if vs, err := openVaultOptional(); err == nil {
			_ = vs.Delete(circuitVaultProject, circuitSlotName(d))
		}
		_ = os.Remove(circuitConfigFilePathFor(d))
		circuitMu.Lock()
		delete(circuitCtrls, d)
		circuitMu.Unlock()
		out := OpsResult{OK: true, Initial: map[string]any{"design": d, "deleted": true}}
		circuitAudit(c, "design_delete", p.Design, "", out)
		return out
	})

	reg("circuit_health", "Sim-node service health: engine availability + active-design summary + design count (for a hosted node) {design?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuitFor(p.Design)
		cfg := ctrl.Config()
		info := ctrl.Describe()
		return OpsResult{OK: true, Initial: map[string]any{
			"ok":          true,
			"design":      designLabelOut(p.Design),
			"enabled":     cfg.Enabled(),
			"elements":    len(cfg.Netlist.Elements),
			"simulatable": info.Simulatable,
			"engine":      cfg.Engine,
			"engines":     ctrl.Engines(),
			"designCount": len(circuitListDesigns()),
		}}
	})
}

// looksBinaryText is a cheap heuristic: base64 of a netlist decodes to mostly
// printable text, so we only treat a successful decode as a real upload when the
// result looks like text (avoids mis-decoding a SPICE deck that happens to be
// valid base64).
func looksBinaryText(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	printable := 0
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' || (c >= 32 && c < 127) {
			printable++
		}
	}
	return float64(printable)/float64(len(b)) > 0.95 && (strings.Contains(string(b), "\n") || strings.Contains(string(b), " "))
}
