package main

// sniff.go — machinetest "sniff" mode: exercise the RTU serial-sniffing path
// (Linux-only) over a virtual serial pair (socat PTYs). The machine engine opens
// one PTY end and passively sniffs; we write CRC-valid Modbus-RTU write frames
// (fc6 — master sets a setpoint) to the other end. StopSniff must then return a
// schematic that has absorbed the register and classified it as a master-written
// setpoint. Proves openSerial (termios) + Feed → scanFrames → ingest → classify
// end-to-end on a real serial fd.
//
//   machinetest sniff <readDev> <writeDev>

import (
	"fmt"
	"os"
	"time"

	"github.com/yaver-io/agent/machine"
)

// crc16 is the Modbus RTU CRC-16 (poly 0xA001), little-endian on the wire.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// fc6Frame builds a Modbus-RTU "write single register" frame (master → slave).
func fc6Frame(unit byte, addr int, val uint16) []byte {
	f := []byte{unit, 0x06, byte(addr >> 8), byte(addr), byte(val >> 8), byte(val)}
	c := crc16(f)
	return append(f, byte(c), byte(c>>8))
}

func runSniffMode(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: machinetest sniff <readDev> <writeDev>")
		os.Exit(2)
	}
	readDev, writeDev := args[0], args[1]

	eng, _ := machine.New()
	if !machine.Supported() {
		fail("sniff", fmt.Errorf("serial sniffing unsupported on this platform"))
	}
	sid, err := eng.StartSniff(readDev, 9600)
	if err != nil {
		fail("StartSniff", err)
	}
	fmt.Printf("[edge] sniffing RTU bus on %s (session %s)\n", readDev, sid)

	// The "master" writes setpoints onto the bus (the other PTY end).
	w, err := os.OpenFile(writeDev, os.O_RDWR, 0)
	if err != nil {
		fail("open writeDev", err)
	}
	defer w.Close()
	// addr 0 = cut-length setpoint, written several times with a few values.
	for _, v := range []uint16{1250, 1300, 1250, 1280} {
		if _, err := w.Write(fc6Frame(1, 0, v)); err != nil {
			fail("write frame", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	// addr 4 = speed setpoint.
	_, _ = w.Write(fc6Frame(1, 4, 800))
	time.Sleep(600 * time.Millisecond)

	sch, ok := eng.StopSniff(sid, "sniff")
	if !ok {
		fail("StopSniff", fmt.Errorf("no session"))
	}
	fmt.Printf("[edge] sniff schematic: %d registers, %d frames\n", len(sch.Registers), sch.Frames)

	var setpoint0 *machine.RegisterObs
	for i := range sch.Registers {
		r := &sch.Registers[i]
		fmt.Printf("  reg addr=%d func=%d kind=%s writtenByMaster=%v samples=%d conf=%.2f\n",
			r.Addr, r.Func, r.Kind, r.WrittenByMaster, r.Samples, r.Confidence)
		if r.Addr == 0 {
			setpoint0 = r
		}
	}
	if setpoint0 == nil {
		fail("classify", fmt.Errorf("register addr 0 not absorbed from the RTU bus"))
	}
	if !setpoint0.WrittenByMaster || setpoint0.Kind != machine.KindSetpoint {
		fail("classify", fmt.Errorf("addr 0 should be a master-written setpoint, got kind=%s written=%v",
			setpoint0.Kind, setpoint0.WrittenByMaster))
	}
	fmt.Println()
	fmt.Println("RESULT: PASS — RTU serial sniff absorbed + classified the setpoint ✓")
}
