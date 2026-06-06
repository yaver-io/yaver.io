package machine

// rtu_test.go — covers the active Modbus-RTU master (the new actuate-over-serial
// plane) and the half-duplex bus arbitration, with no hardware: an in-process
// RTU slave speaks the protocol over a net.Pipe (an io.ReadWriteCloser, exactly
// what the RTUClient expects), so request framing + CRC + response decode +
// exception handling are exercised end-to-end.

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// startRTUSlavePipe runs an in-process Modbus-RTU slave on one end of a pipe and
// returns the master end. fc3/4 read `regs`; fc6 writes them (echo). An out-of-
// range read triggers an illegal-data-address exception (0x02).
func startRTUSlavePipe(t *testing.T, regs []uint16) (io.ReadWriteCloser, func()) {
	t.Helper()
	master, slave := net.Pipe()
	go func() {
		req := make([]byte, 8) // all read/write-single requests are 8 bytes on the wire
		for {
			if _, err := io.ReadFull(slave, req); err != nil {
				return
			}
			if !crcOK(req) {
				continue
			}
			unit, fc := req[0], req[1]
			var resp []byte
			switch fc {
			case 3, 4:
				start := int(binary.BigEndian.Uint16(req[2:4]))
				count := int(binary.BigEndian.Uint16(req[4:6]))
				if start < 0 || count < 1 || start+count > len(regs) {
					resp = []byte{unit, fc | 0x80, 0x02}
				} else {
					body := []byte{unit, fc, byte(count * 2)}
					for i := 0; i < count; i++ {
						body = append(body, byte(regs[start+i]>>8), byte(regs[start+i]))
					}
					resp = body
				}
			case 6:
				addr := int(binary.BigEndian.Uint16(req[2:4]))
				val := binary.BigEndian.Uint16(req[4:6])
				if addr >= 0 && addr < len(regs) {
					regs[addr] = val
				}
				resp = req[:6] // echo unit+fc+addr+val
			default:
				resp = []byte{unit, fc | 0x80, 0x01}
			}
			crc := crc16(resp)
			resp = append(resp, byte(crc), byte(crc>>8))
			if _, err := slave.Write(resp); err != nil {
				return
			}
		}
	}()
	return master, func() { _ = master.Close(); _ = slave.Close() }
}

func TestRTUClient_ReadRegisters(t *testing.T) {
	regs := []uint16{1250, 4998, 42, 0x0001}
	port, stop := startRTUSlavePipe(t, regs)
	defer stop()
	c := NewRTUClient(port, 1)

	got, err := c.ReadRegisters(3, 0, 4, 2*time.Second)
	if err != nil {
		t.Fatalf("ReadRegisters: %v", err)
	}
	want := []uint16{1250, 4998, 42, 1}
	if len(got) != 4 {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reg %d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestRTUClient_WriteSingleRegister(t *testing.T) {
	regs := []uint16{1250, 0, 0, 0}
	port, stop := startRTUSlavePipe(t, regs)
	defer stop()
	c := NewRTUClient(port, 1)

	if err := c.WriteSingleRegister(0, 1300, 2*time.Second); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := c.ReadRegisters(3, 0, 1, 2*time.Second)
	if err != nil || len(got) != 1 || got[0] != 1300 {
		t.Fatalf("read-back: got %v err %v (want [1300])", got, err)
	}
}

func TestRTUClient_Exception(t *testing.T) {
	regs := []uint16{1, 2}
	port, stop := startRTUSlavePipe(t, regs)
	defer stop()
	c := NewRTUClient(port, 1)

	_, err := c.ReadRegisters(3, 0, 10, 2*time.Second) // out of range → exception 0x02
	if err == nil {
		t.Fatal("expected a Modbus exception error, got nil")
	}
}

func TestRTUClient_Timeout(t *testing.T) {
	master, slave := net.Pipe()
	defer master.Close()
	defer slave.Close()
	// No slave goroutine reads/responds → the request times out.
	c := NewRTUClient(master, 1)
	start := time.Now()
	if _, err := c.ReadRegisters(3, 0, 1, 300*time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func TestParseRTUResponse_resync(t *testing.T) {
	// A valid fc3 1-register response (unit 1, value 0x04D2) preceded by garbage.
	body := []byte{0x01, 0x03, 0x02, 0x04, 0xD2}
	crc := crc16(body)
	frame := append(body, byte(crc), byte(crc>>8))
	buf := append([]byte{0xAA, 0xBB}, frame...) // leading noise
	got, ok, err := parseRTUResponse(buf, 1, 3, 7)
	if !ok || err != nil {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if len(got) < 3 || got[2] != 0x02 {
		t.Fatalf("decoded body wrong: %v", got)
	}
}

// ── bus arbitration ─────────────────────────────────────────────────────────

func TestBusArbitration(t *testing.T) {
	e, _ := New()
	const dev = "/dev/ttyUSB-test"

	if err := e.claimExclusive(dev, "sniff-1"); err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}
	if err := e.claimExclusive(dev, "gcode-2"); err == nil {
		t.Fatal("a second exclusive holder must be rejected")
	}
	// A transient RTU/GCode txn must refuse while a sniff holds the bus.
	if err := e.withBusLock(dev, func() error { return nil }); err == nil {
		t.Fatal("withBusLock must fail while the bus is exclusively held")
	}
	// Re-claiming with the same holder id is idempotent.
	if err := e.claimExclusive(dev, "sniff-1"); err != nil {
		t.Fatalf("same-holder re-claim should succeed: %v", err)
	}
	e.releaseExclusive(dev, "sniff-1")
	// After release, a transient txn runs.
	ran := false
	if err := e.withBusLock(dev, func() error { ran = true; return nil }); err != nil {
		t.Fatalf("withBusLock after release: %v", err)
	}
	if !ran {
		t.Fatal("withBusLock body did not run after release")
	}
}
