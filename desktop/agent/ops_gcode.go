package main

// ops_gcode.go — G-code / CNC verbs. Separate protocol from Modbus (line-based
// ok/error flow control), so it gets its own handlers; it shares the machine
// Engine only for the serial port + half-duplex bus arbitration. Owner-only
// (AllowGuest defaults false). Motion-safety posture:
//   - gcode_estop is realtime + un-gated (stopping never needs approval);
//   - gcode_send/stream of a MOTION line are refused without a soft-limit
//     envelope unless allowHighRisk is set, and any out-of-envelope move is
//     rejected before a byte hits the wire (dryRun validates without sending).

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yaver-io/agent/machine"
)

const gcodeSendTimeout = 12 * time.Second

func gcodeLimitsFrom(raw map[string]float64, enabled bool) machine.SoftLimits {
	return machine.SoftLimits{
		Enabled: enabled,
		XMin:    raw["xMin"], XMax: raw["xMax"],
		YMin: raw["yMin"], YMax: raw["yMax"],
		ZMin: raw["zMin"], ZMax: raw["zMax"],
	}
}

// parseLimits decodes the limits object (a map with an enabled bool + numbers).
func parseLimits(m map[string]json.RawMessage) machine.SoftLimits {
	lim := machine.SoftLimits{}
	if m == nil {
		return lim
	}
	if v, ok := m["enabled"]; ok {
		_ = json.Unmarshal(v, &lim.Enabled)
	}
	num := func(key string, dst *float64) {
		if v, ok := m[key]; ok {
			_ = json.Unmarshal(v, dst)
		}
	}
	num("xMin", &lim.XMin)
	num("xMax", &lim.XMax)
	num("yMin", &lim.YMin)
	num("yMax", &lim.YMax)
	num("zMin", &lim.ZMin)
	num("zMax", &lim.ZMax)
	return lim
}

func gcodeOpenHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Device  string `json:"device"`
		Baud    int    `json:"baud"`
		Dialect string `json:"dialect"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Baud == 0 {
		p.Baud = 115200
	}
	id, err := eng.OpenGCode(p.Device, p.Baud, machine.GCodeDialect(p.Dialect))
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"session": id, "device": p.Device, "dialect": machine.GCodeDialect(p.Dialect)}}
}

func gcodeSendHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session       string                     `json:"session"`
		Line          string                     `json:"line"`
		Limits        map[string]json.RawMessage `json:"limits"`
		AllowHighRisk bool                       `json:"allowHighRisk"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	lim := parseLimits(p.Limits)
	if machine.IsMotionLine(p.Line) {
		if !lim.Enabled && !p.AllowHighRisk {
			return OpsResult{OK: false, Code: "needs_approval", Error: "motion line without a soft-limit envelope is high-risk; pass limits{enabled:true,...} or allowHighRisk=true"}
		}
		if v := machine.ValidateProgram([]string{p.Line}, lim); len(v) > 0 {
			return OpsResult{OK: false, Code: "out_of_range", Error: v[0].Reason, Initial: map[string]interface{}{"violations": v}}
		}
	}
	reply, err := eng.GCodeSend(p.Session, p.Line, gcodeSendTimeout)
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error(), Initial: reply}
	}
	return OpsResult{OK: true, Initial: reply}
}

func gcodeStreamHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string                     `json:"session"`
		Lines   []string                   `json:"lines"`
		Program string                     `json:"program"`
		Limits  map[string]json.RawMessage `json:"limits"`
		DryRun  bool                       `json:"dryRun"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	lines := p.Lines
	if len(lines) == 0 && strings.TrimSpace(p.Program) != "" {
		lines = strings.Split(strings.ReplaceAll(p.Program, "\r\n", "\n"), "\n")
	}
	if len(lines) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "provide lines[] or program"}
	}
	lim := parseLimits(p.Limits)
	res, err := eng.GCodeStream(p.Session, lines, lim, p.DryRun, gcodeSendTimeout)
	if err != nil {
		code := "machine_failed"
		if len(res.Violations) > 0 {
			code = "out_of_range"
		}
		return OpsResult{OK: false, Code: code, Error: err.Error(), Initial: res}
	}
	return OpsResult{OK: true, Initial: res}
}

func gcodeStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	reply, err := eng.GCodeStatus(p.Session, gcodeSendTimeout)
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error(), Initial: reply}
	}
	return OpsResult{OK: true, Initial: reply}
}

func gcodeEStopHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if err := eng.GCodeEStop(p.Session); err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"estopped": true}}
}

func gcodeCloseHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if !eng.CloseGCode(p.Session) {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"closed": true}}
}
