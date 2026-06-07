// Package netcapture is Yaver's wire-observe & deep-analysis layer: a persistent,
// multi-source capture engine for troubleshooting industrial / robotics / ERP
// links. It taps two kinds of source:
//
//   - net    — IP/TCP/UDP traffic on a NIC (captured with tcpdump, decoded in
//     pure Go) for Modbus-TCP, S7/LOGO!, OPC-UA, MS-SQL/TDS, HTTP, DNS, and
//     raw TCP/IP health (resets, retransmits, refused connects, RTT).
//   - serial — an RS232/RS485 tty byte stream (Modbus-RTU, Marlin G-code, or
//     generic ASCII). On a Linux box this can open /dev/tty* directly; on a
//     phone-as-host (Android USB-OTG / the IoT connector box) the USB-serial
//     bytes are mirrored in via Feed — so a "fed stream" is a first-class
//     capture source, not a fallback.
//
// The package is pure Go and dependency-free: it parses pcap and every protocol
// itself so it works on a minimal Pi / edge image with only tcpdump installed
// (and not even that for the serial / replay paths). The LLM narrative-diagnosis
// step lives in the main package (ops_netcapture.go), mirroring how machine/
// stays LLM-free.
package netcapture

// Event is one decoded protocol event surfaced on the live stream and kept in
// the session ring buffer for netcapture_tail. Timestamps are unix millis so the
// camera/vision loop can line capture events up against frames.
type Event struct {
	TS       int64                  `json:"ts"`
	Proto    string                 `json:"proto"`            // tcp|udp|icmp|modbus|http|dns|s7|tds|opcua|modbus_rtu|marlin|serial
	Src      string                 `json:"src,omitempty"`    // ip:port (net) or "host"/"device" (serial)
	Dst      string                 `json:"dst,omitempty"`
	Summary  string                 `json:"summary"`          // human one-liner
	Severity string                 `json:"severity,omitempty"` // info|warn|error
	Detail   map[string]interface{} `json:"detail,omitempty"`
}

// Flow is a per-5-tuple (or per-serial-link) aggregate.
type Flow struct {
	Key         string  `json:"key"`   // "a:p>b:q/tcp"
	Proto       string  `json:"proto"` // tcp|udp
	AppProto    string  `json:"appProto,omitempty"`
	Src         string  `json:"src"`
	Dst         string  `json:"dst"`
	Packets     int     `json:"packets"`
	Bytes       int     `json:"bytes"`
	FirstTS     int64   `json:"firstTs"`
	LastTS      int64   `json:"lastTs"`
	Retransmits int     `json:"retransmits"`
	Resets      int     `json:"resets"`
	State       string  `json:"state"` // syn_sent|established|reset|refused|closed|active
	RTTms       float64 `json:"rttMs,omitempty"`

	// internal TCP-state bookkeeping (not serialized)
	synTS    int64
	sawSYN   bool
	sawSYNAK bool
	lastSeq  uint32
	hadSeq   bool
	app      interface{} // per-flow app-decoder state
}

// DisconnectEvent is one entry in the connectivity timeline — the thing you
// actually want when "the PLC keeps dropping".
type DisconnectEvent struct {
	TS    int64  `json:"ts"`
	Flow  string `json:"flow"`
	Cause string `json:"cause"` // tcp_reset|fin|conn_refused|idle_timeout|serial_idle|serial_gap
	Note  string `json:"note,omitempty"`
}

// Finding is one deterministic diagnosis result from Diagnose().
type Finding struct {
	Severity string `json:"severity"` // info|warn|error
	Code     string `json:"code"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
}

// ── per-protocol rollups (typed so the AI can branch cleanly) ──────────────

type ModbusStats struct {
	Transactions int            `json:"transactions"`
	Exceptions   int            `json:"exceptions"`
	ByFunc       map[string]int `json:"byFunc"`       // "read_holding" -> n
	ByException  map[string]int `json:"byException"`  // "0x06 slave_busy" -> n
	Units        map[string]int `json:"units"`        // unit id -> n
	AvgLatencyMs float64        `json:"avgLatencyMs"`
	MaxLatencyMs float64        `json:"maxLatencyMs"`
}

type HTTPStats struct {
	Requests  int            `json:"requests"`
	Responses int            `json:"responses"`
	ByStatus  map[string]int `json:"byStatus"` // "200","404","500"
	ByMethod  map[string]int `json:"byMethod"`
	Errors    int            `json:"errors"` // 4xx+5xx
}

type DNSStats struct {
	Queries   int            `json:"queries"`
	Responses int            `json:"responses"`
	NXDomain  int            `json:"nxdomain"`
	ServFail  int            `json:"servfail"`
	ByName    map[string]int `json:"byName"`
}

type S7Stats struct {
	Jobs     int            `json:"jobs"`
	AckData  int            `json:"ackData"`
	Errors   int            `json:"errors"`
	ByFunc   map[string]int `json:"byFunc"`  // setup_comm|read_var|write_var
	ByError  map[string]int `json:"byError"` // "0x05 addr" -> n
}

type TDSStats struct {
	// Payload-redacted by default: we count packet/token types only — never SQL
	// text or credentials (privacy contract).
	Logins        int            `json:"logins"`
	LoginFailures int            `json:"loginFailures"`
	Batches       int            `json:"batches"`
	RPCs          int            `json:"rpcs"`
	Errors        int            `json:"errors"`
	ByErrorNo     map[string]int `json:"byErrorNo"`
}

type OPCUAStats struct {
	Hello    int            `json:"hello"`
	Messages int            `json:"messages"`
	Errors   int            `json:"errors"`
	ByMsg    map[string]int `json:"byMsg"` // HEL|ACK|OPN|MSG|CLO|ERR
	ByStatus map[string]int `json:"byStatus"`
}

type SerialStats struct {
	Bytes        int            `json:"bytes"`
	Frames       int            `json:"frames"`
	Decoder      string         `json:"decoder"` // modbus_rtu|marlin|ascii|auto
	CRCErrors    int            `json:"crcErrors"`
	Gaps         int            `json:"gaps"`
	ByFunc       map[string]int `json:"byFunc,omitempty"`
	ByException  map[string]int `json:"byException,omitempty"`
}

// Analysis is the full structured report returned by /netcapture/analysis and
// the netcapture_analyze verb (which also adds an LLM narrative on top).
type Analysis struct {
	Session     string            `json:"session"`
	Kind        string            `json:"kind"`   // net|serial
	Source      string            `json:"source"` // iface or tty/stream label
	Status      string            `json:"status"` // running|stopped|error
	StartedAt   int64             `json:"startedAt"`
	UpdatedAt   int64             `json:"updatedAt"`
	Packets     int               `json:"packets"`
	Bytes       int               `json:"bytes"`
	Protocols   map[string]int    `json:"protocols"`
	Flows       []Flow            `json:"flows"`
	Disconnects []DisconnectEvent `json:"disconnects"`
	Findings    []Finding         `json:"findings"`
	Modbus      *ModbusStats      `json:"modbus,omitempty"`
	HTTP        *HTTPStats        `json:"http,omitempty"`
	DNS         *DNSStats         `json:"dns,omitempty"`
	S7          *S7Stats          `json:"s7,omitempty"`
	TDS         *TDSStats         `json:"tds,omitempty"`
	OPCUA       *OPCUAStats       `json:"opcua,omitempty"`
	Serial      *SerialStats      `json:"serial,omitempty"`
	Error       string            `json:"error,omitempty"`
}
