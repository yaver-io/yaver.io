package main

// rtu.go — machinetest "rtu" mode: exercise the ACTIVE Modbus-RTU master over a
// real serial fd (termios), the plane that lets a Pi actuate an RS-485 PLC and
// not just sniff it. Over a virtual serial pair (socat PTYs) we run a tiny RTU
// slave on one end while the machine Engine's ReadRTU/WriteRTU drive the other.
// PASS = registers read back correctly + a verified write round-trips.
//
//   machinetest rtu <masterDev> <slaveDev>

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/yaver-io/agent/machine"
)

func runRTUMode(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: machinetest rtu <masterDev> <slaveDev>")
		os.Exit(2)
	}
	masterDev, slaveDev := args[0], args[1]
	const baud = 9600

	regs := []uint16{1250, 1250, 0, 0, 800}
	slave, err := os.OpenFile(slaveDev, os.O_RDWR, 0)
	if err != nil {
		fail("open slaveDev", err)
	}
	defer slave.Close()
	stop := make(chan struct{})
	go serveRTUSlave(slave, regs, stop)
	defer close(stop)

	eng, _ := machine.New()
	const timeout = 2 * time.Second

	// READ plane over RTU.
	vals, err := eng.ReadRTU(masterDev, baud, 1, 3, 0, 5, timeout)
	if err != nil {
		fail("ReadRTU", err)
	}
	fmt.Printf("[edge] RTU read 5 registers: %v\n", vals)
	if len(vals) != 5 || vals[0] != 1250 {
		fail("rtu-read", fmt.Errorf("unexpected registers: %v", vals))
	}

	// WRITE plane over RTU, verified by read-back.
	const newSetpoint = 1300
	rb, err := eng.WriteRTU(masterDev, baud, 1, 0, newSetpoint, timeout)
	if err != nil {
		fail("WriteRTU", err)
	}
	if rb != newSetpoint {
		fail("rtu-write-readback", fmt.Errorf("read-back %d != written %d", rb, newSetpoint))
	}
	fmt.Printf("[edge] RTU wrote setpoint=%d, verified read-back=%d ✓ (active RTU master)\n", newSetpoint, rb)

	fmt.Println()
	fmt.Println("RESULT: PASS — active Modbus-RTU master over serial (read + verified write) ✓")
}

// serveRTUSlave answers 8-byte RTU requests on a serial fd: fc3/4 read regs,
// fc6 writes one (echo). CRC-checked; unknown function → exception 0x01.
func serveRTUSlave(rw io.ReadWriter, regs []uint16, stop <-chan struct{}) {
	req := make([]byte, 8)
	for {
		select {
		case <-stop:
			return
		default:
		}
		if _, err := io.ReadFull(rw, req); err != nil {
			return
		}
		if crc16(req[:6]) != binary.LittleEndian.Uint16(req[6:8]) {
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
				resp = []byte{unit, fc, byte(count * 2)}
				for i := 0; i < count; i++ {
					resp = append(resp, byte(regs[start+i]>>8), byte(regs[start+i]))
				}
			}
		case 6:
			addr := int(binary.BigEndian.Uint16(req[2:4]))
			val := binary.BigEndian.Uint16(req[4:6])
			if addr >= 0 && addr < len(regs) {
				regs[addr] = val
			}
			resp = req[:6]
		default:
			resp = []byte{unit, fc | 0x80, 0x01}
		}
		c := crc16(resp)
		resp = append(resp, byte(c), byte(c>>8))
		if _, err := rw.Write(resp); err != nil {
			return
		}
	}
}
