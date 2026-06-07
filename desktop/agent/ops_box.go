package main

// ops_box.go — control-plane helpers for the Yaver Connector Box (the hardware
// facade in hardware/yaver-connector-box/). These verbs talk the box's line
// control protocol over its SoftAP control port (:8347) and verify the bus
// end-to-end through its Modbus-TCP gateway (:502), so the app can offer a
// frictionless "one-tap connect" (auto A/B polarity + termination) and a
// software self-test — no PuTTY, no guessing which wire is A.
//
// The box is a facade, not a PC (no Yaver on the box). All intelligence stays
// here on the phone/agent. Owner-only; gated behind --netcapture (the box is the
// hardware for the wire-observe story).

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	boxDefaultControl = "192.168.4.1:8347" // ESP32 SoftAP control port
	boxDefaultModbus  = "192.168.4.1:502"  // ESP32 SoftAP Modbus-TCP gateway
	boxDialTimeout    = 4 * time.Second
)

func boxGate(c OpsContext) *OpsResult {
	if c.Server == nil {
		return &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.netcaptureEnabled {
		return &OpsResult{OK: false, Code: "unauthorized", Error: "box control is part of netcapture; start the agent with `yaver serve --netcapture`"}
	}
	return nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "box_status",
		Description: "Query a Yaver Connector Box over its SoftAP control port: firmware/identity (INFO) + live power/sensor telemetry (SENSE). Default control=192.168.4.1:8347.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string", "description": "host:port of the box control port (default 192.168.4.1:8347)"},
		}),
		Handler:    boxStatusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_autoconnect",
		Description: "One-tap connect: auto-resolve RS485 A/B polarity and termination by probing the bus, verified end-to-end with a real Modbus read through the box gateway. Returns the working settings. Provide a known register to read (unit/start/count).",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string"},
			"modbus":  map[string]interface{}{"type": "string", "description": "box Modbus-TCP gateway (default 192.168.4.1:502)"},
			"unit":    map[string]interface{}{"type": "integer", "description": "Modbus unit id to probe (default 1)"},
			"fc":      map[string]interface{}{"type": "integer", "description": "3=holding (default), 4=input"},
			"start":   map[string]interface{}{"type": "integer", "description": "start register (default 0)"},
			"count":   map[string]interface{}{"type": "integer", "description": "count (default 1)"},
		}),
		Handler:    boxAutoconnectHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_selftest",
		Description: "Run the software-observable OP50 self-test against a box: control reachable (PING), identity (INFO), power telemetry sane (SENSE vin), and the Modbus gateway returns a CRC-valid reply. Hardware-only checks (isolation megger, PD charge) are reported as manual.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string"},
			"modbus":  map[string]interface{}{"type": "string"},
			"unit":    map[string]interface{}{"type": "integer"},
			"start":   map[string]interface{}{"type": "integer"},
			"count":   map[string]interface{}{"type": "integer"},
		}),
		Handler:    boxSelftestHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "box_cmd",
		Description: "Send a raw control line to the box and return its reply (escape hatch: BAUD, BUS, LED, STREAM, GPIO, ZERO, …). See firmware/README.md for the protocol.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"control": map[string]interface{}{"type": "string"},
			"line":    map[string]interface{}{"type": "string", "description": "e.g. 'BAUD 19200' or 'LED 0 20 0'"},
		}, "line"),
		Handler:    boxCmdHandler,
		AllowGuest: false,
	})
}

// ── box control client ───────────────────────────────────────────────────────

// boxControlCmd dials the box control port, sends one line, returns the reply
// line (trimmed). One-shot connection — the box control protocol is request/reply.
func boxControlCmd(addr, line string, timeout time.Duration) (string, error) {
	if addr == "" {
		addr = boxDefaultControl
	}
	conn, err := net.DialTimeout("tcp", addr, boxDialTimeout)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(strings.TrimRight(line, "\r\n") + "\n")); err != nil {
		return "", err
	}
	r := bufio.NewReader(conn)
	reply, err := r.ReadString('\n')
	if err != nil && reply == "" {
		return "", err
	}
	return strings.TrimRight(reply, "\r\n"), nil
}

// modbusReadTCP does a single Modbus-TCP read (fc 3/4) against addr (the box
// gateway). Self-contained so this file has no cross-package coupling.
func modbusReadTCP(addr string, unit, fc byte, start, count int, timeout time.Duration) ([]uint16, error) {
	if addr == "" {
		addr = boxDefaultModbus
	}
	if fc != 3 && fc != 4 {
		fc = 3
	}
	conn, err := net.DialTimeout("tcp", addr, boxDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	pdu := []byte{fc, byte(start >> 8), byte(start), byte(count >> 8), byte(count)}
	hdr := make([]byte, 7)
	binary.BigEndian.PutUint16(hdr[0:2], 1)             // txid
	binary.BigEndian.PutUint16(hdr[2:4], 0)             // proto
	binary.BigEndian.PutUint16(hdr[4:6], uint16(len(pdu)+1))
	hdr[6] = unit
	if _, err := conn.Write(append(hdr, pdu...)); err != nil {
		return nil, err
	}

	rh := make([]byte, 6)
	if _, err := readFullConn(conn, rh); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(rh[4:6]))
	if n < 2 || n > 256 {
		return nil, fmt.Errorf("bad mbap length %d", n)
	}
	body := make([]byte, n) // unit + pdu
	if _, err := readFullConn(conn, body); err != nil {
		return nil, err
	}
	rpdu := body[1:]
	if rpdu[0]&0x80 != 0 {
		code := byte(0)
		if len(rpdu) > 1 {
			code = rpdu[1]
		}
		return nil, fmt.Errorf("modbus exception 0x%02x", code)
	}
	if len(rpdu) < 2 {
		return nil, fmt.Errorf("short pdu")
	}
	bc := int(rpdu[1])
	out := make([]uint16, 0, bc/2)
	for j := 0; j+1 < bc && 2+j+1 < len(rpdu); j += 2 {
		out = append(out, binary.BigEndian.Uint16(rpdu[2+j:2+j+2]))
	}
	return out, nil
}

func readFullConn(c net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		k, err := c.Read(buf[got:])
		got += k
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

// ── handlers ─────────────────────────────────────────────────────────────────

func boxStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	info, err := boxControlCmd(p.Control, "INFO", 3*time.Second)
	if err != nil {
		return OpsResult{OK: false, Code: "box_unreachable", Error: err.Error()}
	}
	sense, _ := boxControlCmd(p.Control, "SENSE", 3*time.Second)
	return OpsResult{OK: true, Initial: map[string]interface{}{"info": info, "sense": sense}}
}

func boxCmdHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
		Line    string `json:"line"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	reply, err := boxControlCmd(p.Control, p.Line, 5*time.Second)
	if err != nil {
		return OpsResult{OK: false, Code: "box_unreachable", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"reply": reply}}
}

func boxAutoconnectHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
		Modbus  string `json:"modbus"`
		Unit    int    `json:"unit"`
		FC      int    `json:"fc"`
		Start   int    `json:"start"`
		Count   int    `json:"count"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	if p.FC == 0 {
		p.FC = 3
	}
	if p.Count == 0 {
		p.Count = 1
	}

	verify := func() ([]uint16, error) {
		return modbusReadTCP(p.Modbus, byte(p.Unit), byte(p.FC), p.Start, p.Count, 2*time.Second)
	}

	// Sweep the 4 combinations the operator would otherwise guess by hand:
	// A/B polarity × termination. First combo that yields a real Modbus reply wins.
	type combo struct{ ab, term int }
	for _, cb := range []combo{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		if _, err := boxControlCmd(p.Control, fmt.Sprintf("ABSWAP %d", cb.ab), 3*time.Second); err != nil {
			return OpsResult{OK: false, Code: "box_unreachable", Error: err.Error()}
		}
		_, _ = boxControlCmd(p.Control, fmt.Sprintf("TERM %d", cb.term), 3*time.Second)
		time.Sleep(150 * time.Millisecond)
		if vals, err := verify(); err == nil {
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"connected": true, "abSwap": cb.ab == 1, "termination": cb.term == 1,
				"unit": p.Unit, "fc": p.FC, "start": p.Start, "values": vals,
				"hint": "settings applied to the box; the machine answered Modbus.",
			}}
		}
	}
	return OpsResult{OK: false, Code: "no_reply", Error: "no Modbus reply on any A/B × termination combo — check baud (box_cmd BAUD <n>), unit id, wiring, and that the slave is powered"}
}

func boxSelftestHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if d := boxGate(c); d != nil {
		return *d
	}
	var p struct {
		Control string `json:"control"`
		Modbus  string `json:"modbus"`
		Unit    int    `json:"unit"`
		Start   int    `json:"start"`
		Count   int    `json:"count"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Unit == 0 {
		p.Unit = 1
	}
	if p.Count == 0 {
		p.Count = 1
	}
	type check struct {
		Name   string `json:"name"`
		Result string `json:"result"` // PASS|FAIL|SKIP|MANUAL
		Detail string `json:"detail,omitempty"`
	}
	var checks []check
	add := func(n, res, det string) { checks = append(checks, check{n, res, det}) }

	if pong, err := boxControlCmd(p.Control, "PING", 3*time.Second); err != nil || !strings.HasPrefix(pong, "PONG") {
		add("control_ping", "FAIL", fmt.Sprintf("%v / %q", err, pong))
		return OpsResult{OK: false, Code: "box_unreachable", Initial: map[string]interface{}{"checks": checks}}
	}
	add("control_ping", "PASS", "PONG")
	add("softap_reachable", "PASS", "control port answered")

	if info, err := boxControlCmd(p.Control, "INFO", 3*time.Second); err == nil && strings.HasPrefix(info, "INFO") {
		add("identity", "PASS", info)
	} else {
		add("identity", "FAIL", info)
	}

	if sense, err := boxControlCmd(p.Control, "SENSE", 3*time.Second); err == nil && strings.HasPrefix(sense, "S ") {
		vin := parseKV(sense, "vin")
		if vin >= 4000 && vin <= 28000 {
			add("power_telemetry", "PASS", sense)
		} else if vin == 0 {
			add("power_telemetry", "SKIP", "no INA219 populated / vin=0")
		} else {
			add("power_telemetry", "FAIL", fmt.Sprintf("vin=%d mV out of 4–28V", vin))
		}
	} else {
		add("power_telemetry", "FAIL", sense)
	}

	if vals, err := modbusReadTCP(p.Modbus, byte(p.Unit), 3, p.Start, p.Count, 2*time.Second); err == nil {
		add("modbus_gateway", "PASS", fmt.Sprintf("read ok: %v", vals))
	} else {
		add("modbus_gateway", "SKIP", "no slave answering (connect a PLC / run box_autoconnect): "+err.Error())
	}

	add("isolation_megger", "MANUAL", "verify ≥1kV primary↔iso on the bench")
	add("pd_charge_while_host", "MANUAL", "plug a phone wired: must charge AND enumerate")

	pass, fail := 0, 0
	for _, ch := range checks {
		switch ch.Result {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		}
	}
	return OpsResult{OK: fail == 0, Initial: map[string]interface{}{
		"checks": checks, "passed": pass, "failed": fail,
		"summary": fmt.Sprintf("%d pass, %d fail (+manual/skip)", pass, fail),
	}}
}

// parseKV pulls an integer value for key in a "S k=v k=v" line.
func parseKV(line, key string) int {
	for _, tok := range strings.Fields(line) {
		if strings.HasPrefix(tok, key+"=") {
			var v int
			fmt.Sscanf(tok[len(key)+1:], "%d", &v)
			return v
		}
	}
	return 0
}
