package machine

// ports.go — serial port discovery + half-duplex bus arbitration, the two
// things a Raspberry Pi wired to a real RS-485/RS-232 bus needs that a pure
// TCP-over-Ethernet PLC never did.
//
//   - SerialPortInfo / ListSerialPorts: enumerate USB-serial adapters so the
//     operator (often on a phone) doesn't have to guess /dev/ttyUSB0.
//   - AutoBaud: a passive bus tap at each candidate baud, keeping the one that
//     yields the most CRC-valid Modbus frames — turns "I think it's 9600 8N1"
//     into an answer.
//   - bus arbitration: an RS-485 pair is one shared half-duplex wire. A passive
//     sniff and an active master cannot own it at once, and two masters must
//     serialize. The Engine tracks an exclusive holder per *resolved* device
//     (so /dev/ttyUSB0 and its /dev/serial/by-id/... symlink are the same bus).

import (
	"fmt"
	"sync"
)

// SerialPortInfo describes a discovered serial device. ByID is the stable
// /dev/serial/by-id/... symlink (survives replug + renumbering) when one exists;
// prefer it for durable workers. Path is the canonical /dev/tty* node.
type SerialPortInfo struct {
	Path        string `json:"path"`
	ByID        string `json:"byId,omitempty"`
	Driver      string `json:"driver,omitempty"`      // e.g. ftdi_sio, ch341, cp210x
	Description string `json:"description,omitempty"` // vendor/product when known
}

// AutoBaudResult reports the chosen baud and the CRC-valid frame count seen at
// each candidate, so a low-confidence guess is visible rather than silent.
type AutoBaudResult struct {
	Best   int         `json:"best"`
	Counts map[int]int `json:"counts"` // baud -> CRC-valid frames observed
}

// commonBauds are the baud rates worth probing for Modbus-RTU field devices,
// most-likely first so AutoBaud can stop early on a clear winner.
var commonBauds = []int{9600, 19200, 38400, 115200, 57600, 4800, 2400, 1200}

// ListSerialPorts enumerates candidate serial devices on this host. Linux-only;
// returns ErrUnsupported elsewhere (TCP needs no enumeration).
func (e *Engine) ListSerialPorts() ([]SerialPortInfo, error) {
	return listSerialPorts()
}

// AutoBaud passively taps `dev` at each candidate baud and returns the one that
// decodes the most CRC-valid Modbus frames. Requires live bus traffic. Linux-only.
func (e *Engine) AutoBaud(dev string, perBaud ...int) (AutoBaudResult, error) {
	return autoBaud(dev, perBaud...)
}

// ── half-duplex bus arbitration ────────────────────────────────────────────

type busState struct {
	mu     sync.Mutex // serializes transient RTU/GCode txns on this bus
	holder string     // exclusive long-lived owner (sniff/gcode session id), or ""
}

// busFor returns (creating if needed) the arbitration state for a device,
// keyed by its resolved path so symlink and real node collapse to one bus.
func (e *Engine) busFor(dev string) *busState {
	key := resolveSerialDevice(dev)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.buses == nil {
		e.buses = map[string]*busState{}
	}
	bs := e.buses[key]
	if bs == nil {
		bs = &busState{}
		e.buses[key] = bs
	}
	return bs
}

// claimExclusive grants a long-lived holder (a sniff or gcode session) sole
// ownership of a bus. Fails if anyone already holds it.
func (e *Engine) claimExclusive(dev, holder string) error {
	bs := e.busFor(dev)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.holder != "" && bs.holder != holder {
		return fmt.Errorf("bus %s is busy: held by session %s — stop it before opening another owner", resolveSerialDevice(dev), bs.holder)
	}
	bs.holder = holder
	return nil
}

// releaseExclusive drops a long-lived holder's claim (no-op if not the holder).
func (e *Engine) releaseExclusive(dev, holder string) {
	bs := e.busFor(dev)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.holder == holder {
		bs.holder = ""
	}
}

// withBusLock runs a transient txn (an RTU read/write) under the bus mutex,
// refusing if a sniff/gcode session exclusively holds the bus. This is what
// keeps a remote machine_write from colliding with a running sniff.
func (e *Engine) withBusLock(dev string, fn func() error) error {
	bs := e.busFor(dev)
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.holder != "" {
		return fmt.Errorf("bus %s is busy: held by session %s — active read/write conflicts with it", resolveSerialDevice(dev), bs.holder)
	}
	return fn()
}
