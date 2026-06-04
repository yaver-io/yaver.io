// Package machine is Yaver's PLC/machine hijack capability: it talks to a
// wire-processing (or any) machine's controller over Modbus (RTU over a serial
// bus, or TCP over Ethernet), passively SNIFFS the bus read-only, fetches
// registers, and aggregates observations into a candidate register Schematic
// that the AI layer can label. Yaver is the heavy worker (the "AI adapter /
// attacker"); Talos is the thin record/schema/UI plane.
//
// This package is LLM-free and dependency-free (pure Go + golang.org/x/sys for
// the Linux serial port). The AI labelling step lives in the main package
// (ops_machine.go), mirroring how ghost/ stays free of any LLM and ghost_vision.go
// supplies the model. See MACHINE_HIJACK_YAVER_DESIGN.md.
package machine

import (
	"errors"
	"sync"
)

// ErrUnsupported is returned when serial sniffing is requested on a platform
// without an implementation (everything non-Linux for now). Modbus-TCP works
// on every platform regardless.
var ErrUnsupported = errors.New("machine: serial sniffing not supported on this platform")

// RegisterKind is the inferred role of a register address, derived from how its
// value behaves on the bus over time. The AI step refines these into human
// meanings (cut length, strip length, count, …).
type RegisterKind string

const (
	KindUnknown  RegisterKind = "unknown"
	KindSetpoint RegisterKind = "setpoint" // written by the master / changes in steps → a parameter
	KindLive     RegisterKind = "live"     // jitters / many distinct values → a live measurement
	KindCounter  RegisterKind = "counter"  // monotonically increasing → a piece/stroke counter
	KindAlarm    RegisterKind = "alarm"    // sparse bitfield → status/alarm word
)

// Frame is one decoded Modbus frame observed on (or sent to) the bus.
type Frame struct {
	TS      int64    `json:"ts"`   // unix millis
	Unit    byte     `json:"unit"` // slave/unit address
	Func    byte     `json:"func"` // function code (0x01..0x10), or |0x80 for exception
	Addr    int      `json:"addr"` // start address for reads/writes (−1 if N/A)
	Count   int      `json:"count"`
	Values  []uint16 `json:"values,omitempty"`
	IsWrite bool     `json:"isWrite"`
	IsResp  bool     `json:"isResp"` // best-effort: response (carries values) vs request
	Excpt   byte     `json:"excpt,omitempty"`
	Raw     string   `json:"raw"` // hex of the whole frame
}

// RegisterObs aggregates everything seen for one (unit, func, addr) tuple.
type RegisterObs struct {
	Unit            byte         `json:"unit"`
	Func            byte         `json:"func"`
	Addr            int          `json:"addr"`
	Samples         int          `json:"samples"`
	Distinct        int          `json:"distinct"`
	Changes         int          `json:"changes"`
	Min             uint16       `json:"min"`
	Max             uint16       `json:"max"`
	Last            uint16       `json:"last"`
	Monotonic       bool         `json:"monotonic"`
	WrittenByMaster bool         `json:"writtenByMaster"`
	Kind            RegisterKind `json:"kind"`
	Confidence      float64      `json:"confidence"`
	// Name/unit/scale are filled by the AI labelling step (Talos machineManuals).
	Name  string  `json:"name,omitempty"`
	Unit2 string  `json:"unit2,omitempty"` // engineering unit, e.g. "mm"
	Scale float64 `json:"scale,omitempty"`

	seen map[uint16]struct{}
}

// Schematic is the candidate/learned machine register map. It maps 1:1 onto a
// Talos machineManuals row (the "registry schematic").
type Schematic struct {
	MachineKey string        `json:"machineKey,omitempty"`
	Driver     string        `json:"driver"` // "modbus_rtu" | "modbus_tcp"
	Source     string        `json:"source"` // "sniff" | "supervised_sniff" | "scan"
	Registers  []RegisterObs `json:"registers"`
	Frames     int           `json:"frames"`
	Confidence float64       `json:"confidence"`
	Notes      string        `json:"notes,omitempty"`
}

// Supported reports whether this platform can do serial (RTU) sniffing. TCP is
// always available, so an Engine is always usable for TCP scan/read.
func Supported() bool { return machineSerialSupported }

// Engine owns active sniff sessions (one per serial port / bus tap). It is
// constructed lazily by the ops layer and is safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	sniffers map[string]*Sniffer // sessionID -> sniffer
	seq      int
}

// New constructs an Engine. It never fails today (TCP always works); the error
// return mirrors ghost.New() for symmetry and future platform checks.
func New() (*Engine, error) {
	return &Engine{sniffers: map[string]*Sniffer{}}, nil
}

// nextID returns a stable session id like "sniff-1".
func (e *Engine) nextID(prefix string) string {
	e.seq++
	return prefix + "-" + itoa(e.seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
