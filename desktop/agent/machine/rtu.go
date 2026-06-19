package machine

// rtu.go — the active Modbus-RTU master over a serial line. Until now the serial
// path was passive-sniff only; the active read/write plane was TCP-only, so a Pi
// wired to an RS-485 PLC could observe the bus but not actuate it. RTUClient
// closes that gap, reusing the same CRC-16 + PDU framing as the TCP client and
// the same verified-write discipline at the ops layer.
//
// Serial gives no length framing, so the response reader accumulates bytes and
// locates the first CRC-valid frame of the expected shape (resync-tolerant
// against a noisy bus / leading stale bytes). Reads run in a goroutine guarded
// by a wall-clock deadline so a silent slave times out instead of hanging.

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// RTUClient is a minimal Modbus-RTU master bound to one already-open serial port.
type RTUClient struct {
	port io.ReadWriteCloser
	unit byte
}

type rtuDeadlinePort interface {
	SetDeadline(time.Time) error
}

type rtuWriteDeadlinePort interface {
	SetWriteDeadline(time.Time) error
}

// NewRTUClient wraps an open serial port as an RTU master for `unit`.
func NewRTUClient(port io.ReadWriteCloser, unit byte) *RTUClient {
	if unit == 0 {
		unit = 1
	}
	return &RTUClient{port: port, unit: unit}
}

func (c *RTUClient) Close() error { return c.port.Close() }

// rtuFrame builds unit + pdu + CRC16 (little-endian on the wire).
func rtuFrame(unit byte, pdu []byte) []byte {
	f := append([]byte{unit}, pdu...)
	crc := crc16(f)
	return append(f, byte(crc), byte(crc>>8))
}

// txn writes a request frame and reads one response, framed by CRC.
func (c *RTUClient) txn(pdu []byte, expectedLen int, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	if p, ok := c.port.(rtuDeadlinePort); ok {
		_ = p.SetDeadline(deadline)
	} else if p, ok := c.port.(rtuWriteDeadlinePort); ok {
		_ = p.SetWriteDeadline(deadline)
	}
	if _, err := c.port.Write(rtuFrame(c.unit, pdu)); err != nil {
		return nil, err
	}
	fn := pdu[0]
	type result struct {
		body []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 0, expectedLen+8)
		tmp := make([]byte, 256)
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			n, rerr := c.port.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if body, ok, perr := parseRTUResponse(buf, c.unit, fn, expectedLen); ok {
					ch <- result{body, perr}
					return
				}
			}
			if rerr != nil {
				// EOF/closed: surface whatever we have for a final parse.
				if body, ok, perr := parseRTUResponse(buf, c.unit, fn, expectedLen); ok {
					ch <- result{body, perr}
					return
				}
				ch <- result{nil, rerr}
				return
			}
		}
		ch <- result{nil, fmt.Errorf("modbus-rtu: timeout waiting for response (unit %d fc %d)", c.unit, fn&0x0f)}
	}()
	select {
	case r := <-ch:
		return r.body, r.err
	case <-time.After(timeout + 500*time.Millisecond):
		return nil, fmt.Errorf("modbus-rtu: hard timeout (unit %d)", c.unit)
	}
}

// parseRTUResponse scans buf for the first valid response to (unit, fn). Returns
// (body-without-crc, true, nil) on a normal match, (nil, true, excErr) on a
// Modbus exception, or (nil, false, nil) meaning "need more bytes".
func parseRTUResponse(buf []byte, unit, fn byte, expectedLen int) ([]byte, bool, error) {
	for off := 0; off+5 <= len(buf); off++ {
		// Exception response: unit, fn|0x80, code, crc(2) = 5 bytes.
		if buf[off] == unit && buf[off+1] == (fn|0x80) {
			if off+5 <= len(buf) && crcOK(buf[off:off+5]) {
				return nil, true, fmt.Errorf("modbus exception 0x%02x", buf[off+2])
			}
		}
		// Normal response of the expected length.
		if buf[off] == unit && buf[off+1] == fn {
			if expectedLen >= 4 && off+expectedLen <= len(buf) && crcOK(buf[off:off+expectedLen]) {
				return buf[off : off+expectedLen-2], true, nil
			}
		}
	}
	return nil, false, nil
}

// ReadRegisters reads `count` 16-bit registers from `start` (fc 3=holding,
// 4=input) over the serial bus.
func (c *RTUClient) ReadRegisters(fc byte, start, count int, timeout time.Duration) ([]uint16, error) {
	if fc != 3 && fc != 4 {
		return nil, fmt.Errorf("modbus-rtu: ReadRegisters fc must be 3 or 4")
	}
	if count < 1 || count > 125 {
		return nil, fmt.Errorf("modbus-rtu: count out of range (1..125)")
	}
	pdu := []byte{fc, byte(start >> 8), byte(start), byte(count >> 8), byte(count)}
	expected := 5 + count*2 // unit+fc+bc + data + crc(2)
	body, err := c.txn(pdu, expected, timeout)
	if err != nil {
		return nil, err
	}
	// body = unit, fc, bc, data...
	if len(body) < 3 {
		return nil, fmt.Errorf("modbus-rtu: short response")
	}
	bc := int(body[2])
	out := make([]uint16, 0, bc/2)
	for j := 0; j+1 < bc && 3+j+1 < len(body); j += 2 {
		out = append(out, binary.BigEndian.Uint16(body[3+j:3+j+2]))
	}
	return out, nil
}

// WriteSingleRegister writes one holding register (fc 6). The slave echoes the
// request; caller (ops layer) clamps + approves.
func (c *RTUClient) WriteSingleRegister(addr int, val uint16, timeout time.Duration) error {
	pdu := []byte{0x06, byte(addr >> 8), byte(addr), byte(val >> 8), byte(val)}
	_, err := c.txn(pdu, 8, timeout) // echo: unit+fc+addr(2)+val(2)+crc(2)
	return err
}

// ── Engine RTU methods (mirror ScanTCP/ReadTCP/WriteTCP, + bus arbitration) ──

// dialRTU opens the port and constructs a master, honouring exclusive holders.
// The returned cleanup closes the port. Callers run inside withBusLock so two
// transient txns never interleave on the half-duplex wire.
func (e *Engine) openRTU(dev string, baud int, unit byte) (*RTUClient, error) {
	port, err := openSerial(dev, baud)
	if err != nil {
		return nil, err
	}
	return NewRTUClient(port, unit), nil
}

// ScanRTU read-scans a contiguous range over RTU and returns a one-shot schematic.
func (e *Engine) ScanRTU(dev string, baud int, unit, fc byte, start, count int, timeout time.Duration) (Schematic, error) {
	if fc == 0 {
		fc = 3
	}
	var sch Schematic
	err := e.withBusLock(dev, func() error {
		cl, err := e.openRTU(dev, baud, unit)
		if err != nil {
			return err
		}
		defer cl.Close()
		vals, err := cl.ReadRegisters(fc, start, count, timeout)
		if err != nil {
			return err
		}
		regs := make([]RegisterObs, 0, len(vals))
		for i, v := range vals {
			regs = append(regs, RegisterObs{
				Unit: unit, Func: fc, Addr: start + i,
				Samples: 1, Distinct: 1, Min: v, Max: v, Last: v,
				Kind: KindUnknown, Confidence: 0.2,
			})
		}
		sch = Schematic{Driver: "modbus_rtu", Source: "scan", Registers: regs, Confidence: 0.2}
		return nil
	})
	return sch, err
}

// ReadRTU reads registers over RTU (verify / current value).
func (e *Engine) ReadRTU(dev string, baud int, unit, fc byte, start, count int, timeout time.Duration) ([]uint16, error) {
	if fc == 0 {
		fc = 3
	}
	var vals []uint16
	err := e.withBusLock(dev, func() error {
		cl, err := e.openRTU(dev, baud, unit)
		if err != nil {
			return err
		}
		defer cl.Close()
		vals, err = cl.ReadRegisters(fc, start, count, timeout)
		return err
	})
	return vals, err
}

// WriteRTU writes one holding register over RTU then reads it back to verify.
// Range-clamp + approval stay at the ops layer, identical to WriteTCP.
func (e *Engine) WriteRTU(dev string, baud int, unit byte, reg int, val uint16, timeout time.Duration) (uint16, error) {
	var rb uint16
	err := e.withBusLock(dev, func() error {
		cl, err := e.openRTU(dev, baud, unit)
		if err != nil {
			return err
		}
		defer cl.Close()
		if err := cl.WriteSingleRegister(reg, val, timeout); err != nil {
			return err
		}
		got, err := cl.ReadRegisters(3, reg, 1, timeout)
		if err != nil {
			return err
		}
		if len(got) == 0 {
			return io.ErrUnexpectedEOF
		}
		rb = got[0]
		return nil
	})
	return rb, err
}
