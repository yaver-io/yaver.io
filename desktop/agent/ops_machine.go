package main

// ops_machine.go — machine/PLC hijack verbs. These expose Yaver's machine
// package (desktop/agent/machine) as ops verbs so a remote Commander (Talos
// over the mesh) can passively SNIFF a machine's Modbus bus, fetch registers,
// have the AI UNDERSTAND them into a register Schematic, and SYNC that schematic
// + telemetry to Talos. Yaver is the heavy worker; Talos is the thin record/UI.
//
// Security posture (mirrors ops_ghost.go):
//   - Opt-in only: verbs refuse unless started with --machine (config.MachineEnabled).
//     Capability advertised via MachineCapabilities.SupportsMachineSniff.
//   - Owner-only: AllowGuest is false on every verb.
//   - Reads/sniffs are safe; machine_write is range-clamped + read-back verified
//     and refuses high-risk values unless explicitly allowed. Safety stays
//     hardwired on the machine — Yaver never touches safety functions.
//   - Privacy: only structured register observations + the learned schematic are
//     synced; no raw proprietary dumps (migration "structure-not-values" rule).

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yaver-io/agent/machine"
)

// ensureMachine lazily constructs the machine engine. Non-machine agents never
// pay for it.
func (s *HTTPServer) ensureMachine() (*machine.Engine, error) {
	s.machineOnce.Do(func() {
		s.machineEngine, s.machineErr = machine.New()
	})
	return s.machineEngine, s.machineErr
}

// machineEngineForOps centralizes the opt-in gate so every verb fails identically.
func machineEngineForOps(c OpsContext) (*machine.Engine, *OpsResult) {
	if c.Server == nil {
		return nil, &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.machineEnabled {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "machine/PLC hijack is disabled on this agent; start it with `yaver serve --machine`"}
	}
	eng, err := c.Server.ensureMachine()
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: "machine engine unavailable: " + err.Error()}
	}
	return eng, nil
}

const machineHTTPTimeout = 8 * time.Second

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_status",
		Description: "Report machine-hijack engine status: enabled, serial-sniff supported on this OS, active sniff sessions.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     machineStatusHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_sniff_start",
		Description: "Start a passive Modbus-RTU bus sniff. With `device` (e.g. /dev/ttyUSB0) it taps a serial bus read-only (Linux). Without a device it opens a manual session you feed via machine_feed (replay a capture). Returns a session id.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "serial device, e.g. /dev/ttyUSB0 (omit for manual/replay session)"},
			"baud":   map[string]interface{}{"type": "integer", "description": "baud rate (default 9600)"},
			"driver": map[string]interface{}{"type": "string", "description": "driver label for manual sessions (default modbus_rtu)"},
		}),
		Handler: machineSniffStartHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_feed",
		Description: "Inject raw bus bytes (hex) into a manual sniff session — replay/pipe a capture without hardware.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
			"hex":     map[string]interface{}{"type": "string", "description": "hex-encoded bytes"},
		}, "session", "hex"),
		Handler: machineFeedHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_sniff_status",
		Description: "Snapshot the current candidate Schematic of a running sniff session without stopping it.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
		}, "session"),
		Handler: machineSniffStatusHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_sniff_stop",
		Description: "Stop a sniff session and return its final candidate register Schematic.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
			"source":  map[string]interface{}{"type": "string", "description": "sniff|supervised_sniff (default sniff)"},
		}, "session"),
		Handler: machineSniffStopHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_scan_registers",
		Description: "Active read-scan a contiguous register range. Modbus-TCP via `addr` (host:port) OR Modbus-RTU via `device` (e.g. /dev/ttyUSB0 or a /dev/serial/by-id link) + `baud`. fc 3=holding, 4=input. Read-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"addr":   map[string]interface{}{"type": "string", "description": "host:port of the Modbus-TCP slave (e.g. 10.0.0.50:502)"},
			"device": map[string]interface{}{"type": "string", "description": "serial device for Modbus-RTU (e.g. /dev/ttyUSB0); use instead of addr"},
			"baud":   map[string]interface{}{"type": "integer", "description": "RTU baud (default 9600)"},
			"unit":   map[string]interface{}{"type": "integer", "description": "unit/slave id (default 1)"},
			"fc":     map[string]interface{}{"type": "integer", "description": "3=holding, 4=input (default 3)"},
			"start":  map[string]interface{}{"type": "integer"},
			"count":  map[string]interface{}{"type": "integer"},
		}, "start", "count"),
		Handler: machineScanHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_read",
		Description: "Read specific registers for verify / current value. Modbus-TCP via `addr` OR Modbus-RTU via `device`+`baud`. Returns raw uint16 values.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"addr":   map[string]interface{}{"type": "string"},
			"device": map[string]interface{}{"type": "string", "description": "serial device for Modbus-RTU"},
			"baud":   map[string]interface{}{"type": "integer"},
			"unit":   map[string]interface{}{"type": "integer"},
			"fc":     map[string]interface{}{"type": "integer"},
			"start":  map[string]interface{}{"type": "integer"},
			"count":  map[string]interface{}{"type": "integer"},
		}, "start", "count"),
		Handler: machineReadHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_ports",
		Description: "List serial devices on this machine (ttyUSB/ttyACM + stable /dev/serial/by-id links + kernel driver). With `autobaud` + `device`, passively probe the bus at each candidate baud and report the one with the most CRC-valid Modbus frames. Linux-only.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"autobaud": map[string]interface{}{"type": "boolean", "description": "probe baud rates on `device` (needs live bus traffic)"},
			"device":   map[string]interface{}{"type": "string", "description": "device to auto-baud (required when autobaud=true)"},
			"windowMs": map[string]interface{}{"type": "integer", "description": "per-baud probe window in ms (default 1500)"},
		}),
		Handler: machinePortsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_understand",
		Description: "Pipe a candidate Schematic (from a session or inline) + optional ground-truth labels to the AI, returning a labelled register map (name/unit/scale/kind/confidence). Cloud-brain primary; falls back to env/local.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session":    map[string]interface{}{"type": "string", "description": "sniff session to read the schematic from"},
			"schematic":  map[string]interface{}{"type": "object", "description": "inline schematic (alternative to session)"},
			"labels":     map[string]interface{}{"type": "object", "description": "ground-truth expected values, e.g. {lengthMm:1250,qty:500,stripL:6}"},
			"machineKey": map[string]interface{}{"type": "string"},
			"baseUrl":    map[string]interface{}{"type": "string"},
			"apiKey":     map[string]interface{}{"type": "string"},
			"model":      map[string]interface{}{"type": "string"},
		}),
		Handler: machineUnderstandHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_write",
		Description: "Write one holding register then read it back to verify. Modbus-TCP via `addr` OR Modbus-RTU via `device`+`baud`. Range-clamped: provide min/max; refuses if value is outside. High-risk (set allowHighRisk=true only after upstream approval). Safety functions are never network-writable.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"addr":          map[string]interface{}{"type": "string"},
			"device":        map[string]interface{}{"type": "string", "description": "serial device for Modbus-RTU"},
			"baud":          map[string]interface{}{"type": "integer"},
			"unit":          map[string]interface{}{"type": "integer"},
			"reg":           map[string]interface{}{"type": "integer"},
			"value":         map[string]interface{}{"type": "integer"},
			"min":           map[string]interface{}{"type": "integer"},
			"max":           map[string]interface{}{"type": "integer"},
			"allowHighRisk": map[string]interface{}{"type": "boolean"},
		}, "reg", "value"),
		Handler: machineWriteHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_sync",
		Description: "Push a device heartbeat + learned Schematic (and optional telemetry samples) to Talos over the org-secret machine-edge routes. Talos stores it in machineEdgeDevices/machineManuals/machineTelemetry.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"talosUrl":   map[string]interface{}{"type": "string", "description": "Talos Convex site URL (default env TALOS_MACHINE_URL/TALOS_CONVEX_SITE_URL)"},
			"orgId":      map[string]interface{}{"type": "string"},
			"orgSecret":  map[string]interface{}{"type": "string", "description": "org sync secret (default env TALOS_ORG_SECRET)"},
			"deviceId":   map[string]interface{}{"type": "string"},
			"name":       map[string]interface{}{"type": "string"},
			"machineId":  map[string]interface{}{"type": "string"},
			"machineKey": map[string]interface{}{"type": "string"},
			"snapshot":   map[string]interface{}{"type": "object"},
			"schematic":  map[string]interface{}{"type": "object"},
			"samples":    map[string]interface{}{"type": "array"},
		}, "deviceId"),
		Handler: machineSyncHandler,
	})

	// ── G-code / CNC class (separate protocol from Modbus; shares only the
	// serial port + bus arbitration). Line-oriented ok/error flow control. ──
	registerOpsVerb(opsVerbSpec{
		Name:        "gcode_open",
		Description: "Open a CNC / 3D-printer controller on a serial device (GRBL/Marlin/generic). Takes exclusive ownership of the bus. Returns a session id used by gcode_send/stream/status/estop/close.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"device":  map[string]interface{}{"type": "string", "description": "serial device, e.g. /dev/ttyACM0 or a /dev/serial/by-id link"},
			"baud":    map[string]interface{}{"type": "integer", "description": "baud (default 115200)"},
			"dialect": map[string]interface{}{"type": "string", "enum": []string{"grbl", "marlin", "generic"}, "description": "controller dialect (default generic)"},
		}, "device"),
		Handler: gcodeOpenHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gcode_send",
		Description: "Send one G-code line and wait for the controller's ok/error. Single motion lines are soft-limit checked when limits are supplied.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session":       map[string]interface{}{"type": "string"},
			"line":          map[string]interface{}{"type": "string"},
			"limits":        gcodeLimitsSchema(),
			"allowHighRisk": map[string]interface{}{"type": "boolean", "description": "required to send a motion line with no soft-limit envelope"},
		}, "session", "line"),
		Handler: gcodeSendHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gcode_stream",
		Description: "Stream a G-code program with ok-gated flow control. ALWAYS validated against the soft-limit envelope first; dryRun validates without sending. An out-of-envelope move aborts before any transmission.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
			"lines":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
			"program": map[string]interface{}{"type": "string", "description": "whole program as one newline-separated string (alternative to lines)"},
			"limits":  gcodeLimitsSchema(),
			"dryRun":  map[string]interface{}{"type": "boolean"},
		}, "session"),
		Handler: gcodeStreamHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gcode_status",
		Description: "Query live controller state (GRBL realtime `?`, Marlin M114).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
		}, "session"),
		Handler: gcodeStatusHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gcode_estop",
		Description: "Emergency stop: realtime feed-hold + soft-reset (GRBL) / M112 (Marlin). UN-gated and bypasses any in-flight stream — stopping never needs approval.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
		}, "session"),
		Handler: gcodeEStopHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gcode_close",
		Description: "Close a G-code session and release the serial bus.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
		}, "session"),
		Handler: gcodeCloseHandler,
	})
}

// gcodeLimitsSchema is the soft-limit envelope shared by gcode_send/stream.
func gcodeLimitsSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "soft-limit envelope in mm; when enabled, out-of-box moves are refused",
		"properties": map[string]interface{}{
			"enabled": map[string]interface{}{"type": "boolean"},
			"xMin":    map[string]interface{}{"type": "number"},
			"xMax":    map[string]interface{}{"type": "number"},
			"yMin":    map[string]interface{}{"type": "number"},
			"yMax":    map[string]interface{}{"type": "number"},
			"zMin":    map[string]interface{}{"type": "number"},
			"zMax":    map[string]interface{}{"type": "number"},
		},
	}
}

func machineStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"enabled":         true,
		"serialSupported": machine.Supported(),
		"sessions":        eng.Sessions(),
	}}
}

func machineSniffStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Device string `json:"device"`
		Baud   int    `json:"baud"`
		Driver string `json:"driver"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if strings.TrimSpace(p.Device) == "" {
		id := eng.StartManual(p.Driver)
		return OpsResult{OK: true, Initial: map[string]interface{}{"session": id, "mode": "manual"}}
	}
	id, err := eng.StartSniff(p.Device, p.Baud)
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"session": id, "mode": "serial", "device": p.Device}}
}

func machineFeedHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
		Hex     string `json:"hex"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(p.Hex), " ", ""))
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid hex: " + err.Error()}
	}
	if err := eng.FeedSession(p.Session, raw); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"fed": len(raw)}}
}

func machineSniffStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
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
	sch, ok := eng.SchematicOf(p.Session, "sniff")
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	return OpsResult{OK: true, Initial: sch}
}

func machineSniffStopHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
		Source  string `json:"source"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	sch, ok := eng.StopSniff(p.Session, p.Source)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	return OpsResult{OK: true, Initial: sch}
}

func machineScanHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Addr   string `json:"addr"`
		Device string `json:"device"`
		Baud   int    `json:"baud"`
		Unit   int    `json:"unit"`
		FC     int    `json:"fc"`
		Start  int    `json:"start"`
		Count  int    `json:"count"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	var (
		sch machine.Schematic
		err error
	)
	switch {
	case strings.TrimSpace(p.Device) != "":
		sch, err = eng.ScanRTU(p.Device, p.Baud, byte(p.Unit), byte(p.FC), p.Start, p.Count, machineHTTPTimeout)
	case strings.TrimSpace(p.Addr) != "":
		sch, err = eng.ScanTCP(p.Addr, byte(p.Unit), byte(p.FC), p.Start, p.Count, machineHTTPTimeout)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "provide addr (Modbus-TCP) or device (Modbus-RTU)"}
	}
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: sch}
}

func machineReadHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Addr   string `json:"addr"`
		Device string `json:"device"`
		Baud   int    `json:"baud"`
		Unit   int    `json:"unit"`
		FC     int    `json:"fc"`
		Start  int    `json:"start"`
		Count  int    `json:"count"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	var (
		vals []uint16
		err  error
	)
	switch {
	case strings.TrimSpace(p.Device) != "":
		vals, err = eng.ReadRTU(p.Device, p.Baud, byte(p.Unit), byte(p.FC), p.Start, p.Count, machineHTTPTimeout)
	case strings.TrimSpace(p.Addr) != "":
		vals, err = eng.ReadTCP(p.Addr, byte(p.Unit), byte(p.FC), p.Start, p.Count, machineHTTPTimeout)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "provide addr (Modbus-TCP) or device (Modbus-RTU)"}
	}
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"values": vals, "start": p.Start, "count": p.Count, "transport": machineTransport(p.Device)}}
}

func machineWriteHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Addr          string `json:"addr"`
		Device        string `json:"device"`
		Baud          int    `json:"baud"`
		Unit          int    `json:"unit"`
		Reg           int    `json:"reg"`
		Value         int    `json:"value"`
		Min           *int   `json:"min"`
		Max           *int   `json:"max"`
		AllowHighRisk bool   `json:"allowHighRisk"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	if p.Value < 0 || p.Value > 0xFFFF {
		return OpsResult{OK: false, Code: "bad_payload", Error: "value out of uint16 range"}
	}
	if p.Min != nil && p.Value < *p.Min {
		return OpsResult{OK: false, Code: "out_of_range", Error: fmt.Sprintf("value %d below min %d", p.Value, *p.Min)}
	}
	if p.Max != nil && p.Value > *p.Max {
		return OpsResult{OK: false, Code: "out_of_range", Error: fmt.Sprintf("value %d above max %d", p.Value, *p.Max)}
	}
	if (p.Min == nil || p.Max == nil) && !p.AllowHighRisk {
		return OpsResult{OK: false, Code: "needs_approval", Error: "write without explicit min/max range is high-risk; set allowHighRisk=true after upstream approval"}
	}
	var (
		rb  uint16
		err error
	)
	switch {
	case strings.TrimSpace(p.Device) != "":
		rb, err = eng.WriteRTU(p.Device, p.Baud, byte(p.Unit), p.Reg, uint16(p.Value), machineHTTPTimeout)
	case strings.TrimSpace(p.Addr) != "":
		rb, err = eng.WriteTCP(p.Addr, byte(p.Unit), p.Reg, uint16(p.Value), machineHTTPTimeout)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "provide addr (Modbus-TCP) or device (Modbus-RTU)"}
	}
	if err != nil {
		return OpsResult{OK: false, Code: "machine_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"reg": p.Reg, "wrote": p.Value, "readback": rb, "verified": int(rb) == p.Value, "transport": machineTransport(p.Device),
	}}
}

// machineTransport labels a result by the path it took.
func machineTransport(device string) string {
	if strings.TrimSpace(device) != "" {
		return "modbus_rtu"
	}
	return "modbus_tcp"
}

func machinePortsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		AutoBaud bool   `json:"autobaud"`
		Device   string `json:"device"`
		WindowMs int    `json:"windowMs"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	ports, err := eng.ListSerialPorts()
	if err != nil && !p.AutoBaud {
		return OpsResult{OK: false, Code: "unsupported", Error: err.Error()}
	}
	out := map[string]interface{}{"ports": ports, "serialSupported": machine.Supported()}
	if p.AutoBaud {
		if strings.TrimSpace(p.Device) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "autobaud needs a device"}
		}
		ab, aerr := eng.AutoBaud(p.Device, p.WindowMs)
		if aerr != nil {
			return OpsResult{OK: false, Code: "machine_failed", Error: aerr.Error()}
		}
		out["autobaud"] = ab
	}
	return OpsResult{OK: true, Initial: out}
}

func machineUnderstandHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := machineEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session    string             `json:"session"`
		Schematic  *machine.Schematic `json:"schematic"`
		Labels     map[string]any     `json:"labels"`
		MachineKey string             `json:"machineKey"`
		BaseURL    string             `json:"baseUrl"`
		APIKey     string             `json:"apiKey"`
		Model      string             `json:"model"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	var sch machine.Schematic
	if p.Schematic != nil {
		sch = *p.Schematic
	} else if p.Session != "" {
		s, ok := eng.SchematicOf(p.Session, "supervised_sniff")
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
		}
		sch = s
	} else {
		return OpsResult{OK: false, Code: "bad_payload", Error: "provide a session or an inline schematic"}
	}
	if p.MachineKey != "" {
		sch.MachineKey = p.MachineKey
	}
	labelled, err := machineUnderstandLLM(c.Ctx, p.BaseURL, p.APIKey, p.Model, sch, p.Labels)
	if err != nil {
		return OpsResult{OK: false, Code: "ai_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: labelled}
}

func machineSyncHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if _, deny := machineEngineForOps(c); deny != nil {
		return *deny
	}
	var p struct {
		TalosURL   string             `json:"talosUrl"`
		OrgID      string             `json:"orgId"`
		OrgSecret  string             `json:"orgSecret"`
		DeviceID   string             `json:"deviceId"`
		Name       string             `json:"name"`
		MachineID  string             `json:"machineId"`
		MachineKey string             `json:"machineKey"`
		Snapshot   map[string]any     `json:"snapshot"`
		Schematic  *machine.Schematic `json:"schematic"`
		Samples    []any              `json:"samples"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	base := firstNonEmptyStr(p.TalosURL, os.Getenv("TALOS_MACHINE_URL"), os.Getenv("TALOS_CONVEX_SITE_URL"))
	orgID := firstNonEmptyStr(p.OrgID, os.Getenv("TALOS_ORG_ID"))
	secret := firstNonEmptyStr(p.OrgSecret, os.Getenv("TALOS_ORG_SECRET"))
	if base == "" || orgID == "" || secret == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "need talosUrl + orgId + orgSecret (or TALOS_MACHINE_URL/TALOS_ORG_ID/TALOS_ORG_SECRET)"}
	}
	base = strings.TrimRight(base, "/")
	out := map[string]interface{}{}

	hb := map[string]any{
		"orgId": orgID, "deviceId": p.DeviceID, "name": firstNonEmptyStr(p.Name, p.DeviceID),
		"machineKey": p.MachineKey, "protocol": "modbus", "transport": "yaver",
		"capabilities": []string{"read", "sniff", "understand"}, "snapshot": p.Snapshot,
	}
	if p.MachineID != "" {
		hb["machineId"] = p.MachineID
	}
	if code, err := machinePost(c.Ctx, base+"/machine-edge/heartbeat", secret, hb); err != nil {
		return OpsResult{OK: false, Code: "sync_failed", Error: "heartbeat: " + err.Error()}
	} else {
		out["heartbeat"] = code
	}

	if p.Schematic != nil {
		man := map[string]any{
			"orgId": orgID, "deviceId": p.DeviceID,
			"machineKey":  firstNonEmptyStr(p.MachineKey, p.Schematic.MachineKey),
			"driver":      p.Schematic.Driver,
			"registers":   p.Schematic.Registers,
			"confidence":  p.Schematic.Confidence,
			"learnedFrom": p.Schematic.Source,
		}
		if code, err := machinePost(c.Ctx, base+"/machine-edge/manual", secret, man); err != nil {
			return OpsResult{OK: false, Code: "sync_failed", Error: "manual: " + err.Error()}
		} else {
			out["manual"] = code
		}
	}

	if len(p.Samples) > 0 {
		tel := map[string]any{"orgId": orgID, "deviceId": p.DeviceID, "machineId": p.MachineID, "samples": p.Samples}
		if code, err := machinePost(c.Ctx, base+"/machine-edge/telemetry", secret, tel); err != nil {
			return OpsResult{OK: false, Code: "sync_failed", Error: "telemetry: " + err.Error()}
		} else {
			out["telemetry"] = code
		}
	}
	out["ok"] = true
	return OpsResult{OK: true, Initial: out}
}

// machinePost POSTs a JSON body to a Talos org-secret route, returning the HTTP code.
func machinePost(ctx context.Context, url, secret string, body any) (int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// ── AI understand (OpenAI-compatible chat; mirrors ghost_vision resolution) ──

const machineUnderstandSystemPrompt = `You are an industrial controls reverse-engineering assistant for wire-harness machines (cut/strip/crimp). You are given observed Modbus register statistics and (optionally) ground-truth expected values from the job that was running. Infer, for each register, a human meaning (e.g. cut_length, strip_left, strip_right, quantity, speed, crimp_height, blade_depth, alarm_word, piece_counter), the engineering unit (mm, pcs, etc.), a numeric scale (observed_value * scale = engineering_value), and a confidence 0..1. Use the ground-truth labels to anchor: if a label says lengthMm=1250 and a setpoint register reads 5000, infer scale 0.25 and name cut_length. Respond ONLY with a compact JSON object: {"registers":[{"addr":N,"unit":U,"func":F,"name":"...","unit2":"mm","scale":0.25,"kind":"setpoint|live|counter|alarm|unknown","confidence":0.0}], "notes":"..."}.`

func machineUnderstandLLM(ctx context.Context, baseURL, apiKey, model string, sch machine.Schematic, labels map[string]any) (machine.Schematic, error) {
	// Inference offload: a Raspberry Pi can't run a useful model, so resolution
	// prefers an explicitly pointed endpoint (a paired beefy peer, or a rented
	// GPU from the GPU-rental lane) before falling back to the on-box Ollama. Set
	// YAVER_MACHINE_UNDERSTAND_URL on the edge box to your Mac / GPU host.
	baseURL = firstNonEmptyStr(baseURL,
		os.Getenv("YAVER_MACHINE_UNDERSTAND_URL"),
		os.Getenv("GHOST_VISION_BASE_URL"), os.Getenv("OPENAI_BASE_URL"), localOllamaV1)
	apiKey = firstNonEmptyStr(apiKey,
		os.Getenv("YAVER_MACHINE_UNDERSTAND_API_KEY"),
		os.Getenv("GHOST_VISION_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	if model == "" {
		model = firstNonEmptyStr(os.Getenv("YAVER_MACHINE_UNDERSTAND_MODEL"), os.Getenv("GHOST_VISION_MODEL"), os.Getenv("OPENAI_MODEL"))
		if model == "" {
			if strings.Contains(baseURL, "11434") {
				model = "llama3.1"
			} else {
				model = "gpt-4o-mini"
			}
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	userPayload := map[string]any{"schematic": sch, "labels": labels}
	uj, _ := json.Marshal(userPayload)
	body := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []any{
			map[string]any{"role": "system", "content": machineUnderstandSystemPrompt},
			map[string]any{"role": "user", "content": "Observed registers + labels:\n" + string(uj)},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return sch, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	cl := &http.Client{Timeout: 90 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return sch, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return sch, err
	}
	if out.Error != nil {
		return sch, fmt.Errorf("understand: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return sch, fmt.Errorf("understand: empty response")
	}
	merged := mergeUnderstanding(sch, out.Choices[0].Message.Content)
	return merged, nil
}

// mergeUnderstanding parses the LLM JSON and merges names/units/scales onto the
// observed registers by (addr, func), keeping the deterministic observations.
func mergeUnderstanding(sch machine.Schematic, content string) machine.Schematic {
	content = strings.TrimSpace(content)
	// strip markdown fences if present
	if i := strings.Index(content, "{"); i > 0 {
		content = content[i:]
	}
	if j := strings.LastIndex(content, "}"); j >= 0 && j < len(content)-1 {
		content = content[:j+1]
	}
	var parsed struct {
		Registers []struct {
			Addr       int     `json:"addr"`
			Func       int     `json:"func"`
			Name       string  `json:"name"`
			Unit2      string  `json:"unit2"`
			Scale      float64 `json:"scale"`
			Kind       string  `json:"kind"`
			Confidence float64 `json:"confidence"`
		} `json:"registers"`
		Notes string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		sch.Notes = "AI parse failed: " + err.Error()
		return sch
	}
	idx := map[[2]int]int{}
	for i := range sch.Registers {
		idx[[2]int{sch.Registers[i].Addr, int(sch.Registers[i].Func)}] = i
	}
	for _, r := range parsed.Registers {
		key := [2]int{r.Addr, r.Func}
		if i, ok := idx[key]; ok {
			if r.Name != "" {
				sch.Registers[i].Name = r.Name
			}
			if r.Unit2 != "" {
				sch.Registers[i].Unit2 = r.Unit2
			}
			if r.Scale != 0 {
				sch.Registers[i].Scale = r.Scale
			}
			if r.Kind != "" {
				sch.Registers[i].Kind = machine.RegisterKind(r.Kind)
			}
			if r.Confidence > 0 {
				sch.Registers[i].Confidence = r.Confidence
			}
		}
	}
	if parsed.Notes != "" {
		sch.Notes = parsed.Notes
	}
	sch.Source = "supervised_sniff"
	return sch
}
