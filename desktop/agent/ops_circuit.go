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

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/circuit"
)

const circuitVaultProject = "circuit"
const circuitVaultConfigName = "circuit-config"

func circuitConfigFilePath() string {
	home, _ := os.UserHomeDir()
	return home + "/.yaver/circuit-config.json"
}

var (
	circuitMu   sync.Mutex
	circuitCtrl *circuit.Controller
)

func circuitConfigDefault() circuit.Config {
	c := circuit.Config{Engine: strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_CIRCUIT_ENGINE")))}
	c.Normalize()
	return c
}

func circuitConfigGet() circuit.Config {
	def := circuitConfigDefault()
	cfg := def
	found := false
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(circuitVaultProject, circuitVaultConfigName); gerr == nil && e != nil && e.Value != "" {
			var c circuit.Config
			if json.Unmarshal([]byte(e.Value), &c) == nil {
				cfg, found = c, true
			}
		}
	}
	if !found {
		if b, err := os.ReadFile(circuitConfigFilePath()); err == nil {
			var c circuit.Config
			if json.Unmarshal(b, &c) == nil {
				cfg = c
			}
		}
	}
	cfg.Normalize()
	return cfg
}

func circuitConfigSave(c circuit.Config) error {
	c.Normalize()
	c.UpdatedAt = time.Now().UnixMilli()
	b, _ := json.Marshal(c)
	var vaultErr error
	if vs, err := openVaultOptional(); err == nil {
		vaultErr = vs.Set(VaultEntry{Project: circuitVaultProject, Name: circuitVaultConfigName, Category: "custom", Value: string(b), Notes: "Yaver circuit cell config (engine + loaded netlist)"})
	} else {
		vaultErr = err
	}
	if vaultErr != nil {
		if ferr := os.WriteFile(circuitConfigFilePath(), b, 0o600); ferr != nil {
			return ferr
		}
	}
	return nil
}

// ensureCircuit returns the process-wide circuit controller, hydrated from the
// persisted config on first use.
func ensureCircuit() *circuit.Controller {
	circuitMu.Lock()
	defer circuitMu.Unlock()
	if circuitCtrl == nil {
		circuitCtrl = circuit.NewController(circuitConfigGet())
	}
	return circuitCtrl
}

// persistCircuit writes the controller's current config (incl. loaded netlist).
func persistCircuit(ctrl *circuit.Controller) {
	_ = circuitConfigSave(ctrl.Config())
}

type circuitPayload struct {
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

	reg("circuit_engines", "List circuit simulation engines + capabilities (builtin always; ngspice if installed)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl := ensureCircuit()
		return OpsResult{OK: true, Initial: map[string]any{"engines": ctrl.Engines(), "active": ctrl.Config().Engine}}
	})

	reg("circuit_config_get", "Get the circuit cell config (engine + loaded-circuit summary)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl := ensureCircuit()
		cfg := ctrl.Config()
		return OpsResult{OK: true, Initial: map[string]any{
			"engine":          cfg.Engine,
			"ngspicePath":     cfg.NgspicePath,
			"enabled":         cfg.Enabled(),
			"info":            ctrl.Describe(),
			"defaultAnalysis": cfg.DefaultAnalysis,
		}}
	})

	reg("circuit_config_set", "Set engine ('auto'|'builtin'|'ngspice'), ngspicePath, or defaultAnalysis", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuit()
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
		persistCircuit(ctrl)
		return OpsResult{OK: true, Initial: map[string]any{"engine": ctrl.Config().Engine}}
	})

	reg("circuit_import", "Import a circuit from SPICE/KiCad/EPLAN text {format?:auto, text|spice}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		src := p.source()
		if strings.TrimSpace(src) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "text (or spice) required"}
		}
		// allow base64-wrapped uploads (mobile/web file pickers)
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(src)); err == nil && looksBinaryText(dec) {
			src = string(dec)
		}
		ctrl := ensureCircuit()
		info, err := ctrl.Import(p.Format, src)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		persistCircuit(ctrl)
		return OpsResult{OK: true, Initial: map[string]any{"info": info}}
	})

	reg("circuit_export", "Export the loaded circuit {format?:spice|json}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuit()
		if strings.ToLower(p.Format) == "json" {
			return OpsResult{OK: true, Initial: map[string]any{"format": "json", "netlist": ctrl.Netlist()}}
		}
		return OpsResult{OK: true, Initial: map[string]any{"format": "spice", "spice": ctrl.ExportSPICE()}}
	})

	reg("circuit_describe", "Parametric snapshot of the loaded circuit (nets, elements, sources)", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"info": ensureCircuit().Describe()}}
	})

	reg("circuit_simulate", "Run an analysis {type:op|dc|tran|ac, ...} on the loaded circuit", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuit()
		res, err := ctrl.Simulate(c.Ctx, p.analysis())
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"result": res}}
	})

	reg("circuit_measure", "Convenience DC operating point — node voltages + branch currents", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl := ensureCircuit()
		res, err := ctrl.Simulate(c.Ctx, circuit.Analysis{Type: "op"})
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{
			"nodeVoltages": res.NodeVoltages, "branchCurrents": res.BranchCurrents, "engine": res.Engine,
		}}
	})

	reg("circuit_erc", "Run the generic electrical-rule-check (floating nets, no ground, voltage-domain mismatch, islands)", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"report": ensureCircuit().ERC()}}
	})

	reg("circuit_set_domain", "Tag a net with its nominal voltage {net, volts} to arm the ERC isolation check", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		if strings.TrimSpace(p.Net) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "net required"}
		}
		ctrl := ensureCircuit()
		ctrl.SetDomain(p.Net, p.Volts)
		persistCircuit(ctrl)
		return OpsResult{OK: true, Initial: map[string]any{"net": p.Net, "volts": p.Volts}}
	})

	reg("circuit_plot", "Render a waveform PNG (data URL) of an analysis {type, signals?}", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseCircuitPayload(payload)
		ctrl := ensureCircuit()
		png, res, err := ctrl.Plot(c.Ctx, p.analysis(), p.Signals)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		return OpsResult{OK: true, Initial: map[string]any{
			"image": dataURL, "analysis": res.Analysis, "signals": res.Signals, "engine": res.Engine,
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
