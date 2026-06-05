package machine

// modbus_test.go — fills the (previously zero) coverage of the machine engine's
// Modbus-TCP READ/WRITE plane, the core of the Talos-IoT machine-hijack flow:
// the agent absorbs a PLC's registers read-only, then writes back verified by
// read-back. An in-process Modbus-TCP slave stands in for the PLC (no hardware,
// no Docker) so DialTCP/ReadRegisters/WriteSingleRegister + Engine.ScanTCP/
// ReadTCP/WriteTCP are exercised end-to-end over a real TCP socket. Classify +
// frame parsing are covered too.

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// startModbusSim runs an in-process Modbus-TCP slave on a random port serving
// `regs` as holding (fc3) AND input (fc4) registers; fc6 writes mutate `regs`
// (read-back). Returns the address and a stop func.
func startModbusSim(t *testing.T, regs []uint16) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveModbusConn(conn, regs, &mu)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func serveModbusConn(conn net.Conn, regs []uint16, mu *sync.Mutex) {
	defer conn.Close()
	for {
		hdr := make([]byte, 7)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[4:6])) - 1
		if n < 1 || n > 260 {
			return
		}
		pdu := make([]byte, n)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		var resp []byte
		switch pdu[0] {
		case 3, 4: // read holding / input registers
			start := int(binary.BigEndian.Uint16(pdu[1:3]))
			count := int(binary.BigEndian.Uint16(pdu[3:5]))
			bc := count * 2
			resp = make([]byte, 2+bc)
			resp[0] = pdu[0]
			resp[1] = byte(bc)
			mu.Lock()
			for i := 0; i < count; i++ {
				var v uint16
				if start+i < len(regs) {
					v = regs[start+i]
				}
				binary.BigEndian.PutUint16(resp[2+i*2:], v)
			}
			mu.Unlock()
		case 6: // write single register — echo addr+val (Modbus spec)
			addr := int(binary.BigEndian.Uint16(pdu[1:3]))
			val := binary.BigEndian.Uint16(pdu[3:5])
			mu.Lock()
			if addr < len(regs) {
				regs[addr] = val
			}
			mu.Unlock()
			resp = pdu
		default:
			resp = []byte{pdu[0] | 0x80, 0x01} // illegal function
		}
		out := make([]byte, 7+len(resp))
		copy(out[0:2], hdr[0:2]) // echo txid
		binary.BigEndian.PutUint16(out[4:6], uint16(len(resp)+1))
		out[6] = hdr[6] // echo unit
		copy(out[7:], resp)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

func TestModbusTCP_ReadHolding(t *testing.T) {
	// A wire-machine register layout: setpoint, live, counter, alarm.
	regs := []uint16{1250, 4998, 42, 0x0001}
	addr, stop := startModbusSim(t, regs)
	defer stop()

	c, err := DialTCP(addr, 1, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	got, err := c.ReadRegisters(3, 0, 4, 2*time.Second)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []uint16{1250, 4998, 42, 1}
	if len(got) != 4 {
		t.Fatalf("expected 4 regs, got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reg %d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestModbusTCP_WriteReadback(t *testing.T) {
	regs := []uint16{1250, 0, 0, 0}
	addr, stop := startModbusSim(t, regs)
	defer stop()
	c, _ := DialTCP(addr, 1, 2*time.Second)
	defer c.Close()

	if err := c.WriteSingleRegister(0, 1300, 2*time.Second); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := c.ReadRegisters(3, 0, 1, 2*time.Second)
	if err != nil || len(got) != 1 || got[0] != 1300 {
		t.Fatalf("read-back after write: got %v err %v (want [1300])", got, err)
	}
}

func TestEngine_ScanTCP(t *testing.T) {
	regs := []uint16{1250, 4998, 42, 1}
	addr, stop := startModbusSim(t, regs)
	defer stop()
	e, _ := New()
	sch, err := e.ScanTCP(addr, 1, 3, 0, 4, 2*time.Second)
	if err != nil {
		t.Fatalf("ScanTCP: %v", err)
	}
	if sch.Driver != "modbus_tcp" || sch.Source != "scan" {
		t.Errorf("schematic driver/source: %+v", sch)
	}
	if len(sch.Registers) != 4 {
		t.Fatalf("expected 4 registers in schematic, got %d", len(sch.Registers))
	}
}

func TestEngine_WriteTCP_readback(t *testing.T) {
	regs := []uint16{100, 0, 0, 0}
	addr, stop := startModbusSim(t, regs)
	defer stop()
	e, _ := New()
	rb, err := e.WriteTCP(addr, 1, 0, 777, 2*time.Second)
	if err != nil {
		t.Fatalf("WriteTCP: %v", err)
	}
	if rb != 777 {
		t.Errorf("read-back after WriteTCP: got %d want 777", rb)
	}
}

// TestClassify locks the register-role heuristics that the AI "understand" step
// builds on (Talos machine manual). These are the inferences that make the
// hijack safe: setpoints vs live measurements vs counters.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		obs  RegisterObs
		want RegisterKind
	}{
		{"master-written setpoint", RegisterObs{WrittenByMaster: true}, KindSetpoint},
		{"monotonic counter", RegisterObs{Monotonic: true, Changes: 5, Distinct: 5, Samples: 10}, KindCounter},
		{"jittery live", RegisterObs{Distinct: 12, Changes: 9, Samples: 12}, KindLive},
		{"few-step setpoint", RegisterObs{Distinct: 3, Changes: 2, Samples: 20}, KindSetpoint},
		{"constant config", RegisterObs{Distinct: 1, Samples: 50}, KindSetpoint},
		{"unknown", RegisterObs{}, KindUnknown},
	}
	for _, tc := range cases {
		o := tc.obs
		classify(&o)
		if o.Kind != tc.want {
			t.Errorf("%s: classified %q, want %q (conf %.2f)", tc.name, o.Kind, tc.want, o.Confidence)
		}
		if o.Confidence <= 0 || o.Confidence > 1 {
			t.Errorf("%s: confidence out of range: %f", tc.name, o.Confidence)
		}
	}
}

// TestScanFrames_rtuRoundTrip builds a CRC-valid Modbus-RTU read-response frame
// and asserts the sniffer's frame extractor recovers it (CRC-16 + framing).
func TestScanFrames_rtuRoundTrip(t *testing.T) {
	// fc=3 response: unit=1, fc=3, byteCount=2, value=0x04D2 (1234), + CRC16.
	frame := []byte{0x01, 0x03, 0x02, 0x04, 0xD2}
	crc := crc16(frame)
	frame = append(frame, byte(crc), byte(crc>>8)) // RTU CRC is little-endian on the wire
	frames, leftover := scanFrames(frame)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d (leftover %d)", len(frames), len(leftover))
	}
	f := frames[0]
	if f.Unit != 1 || f.Func != 3 {
		t.Errorf("frame unit/func: %+v", f)
	}
	if len(f.Values) != 1 || f.Values[0] != 1234 {
		t.Errorf("frame values: got %v want [1234]", f.Values)
	}
}
