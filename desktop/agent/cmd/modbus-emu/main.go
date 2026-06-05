// Command modbus-emu is a standalone Modbus-TCP slave that emulates a
// wire-processing machine's PLC for the Talos-IoT / Yaver machine-hijack test
// suite. It serves a realistic register map and (in --dynamic mode) animates it
// so the edge agent sees live behaviour: a constant setpoint, a jittering live
// measurement, a monotonic piece counter, and an alarm word. fc3/fc4 read,
// fc6 writes setpoints (read-back). No deps; runs in any Linux container.
//
//	modbus-emu [--port 502] [--dynamic]
//
// Register map (holding + input):
//
//	0: cut-length setpoint   (e.g. 1250 = 125.0 mm @0.1)   — written by master
//	1: live measured length  (jitters around the setpoint) — live measurement
//	2: piece counter         (monotonic ++)                — counter
//	3: alarm word            (bitfield, mostly 0)          — alarm
//	4: speed setpoint
package main

import (
	"encoding/binary"
	"flag"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

func main() {
	port := flag.String("port", "502", "TCP port to listen on")
	dynamic := flag.Bool("dynamic", false, "animate counter/live registers over time")
	flag.Parse()

	regs := []uint16{1250, 1250, 0, 0, 800}
	var mu sync.Mutex

	if *dynamic {
		go func() {
			rng := rand.New(rand.NewSource(42))
			for range time.Tick(150 * time.Millisecond) {
				mu.Lock()
				regs[2]++                                   // piece counter, monotonic
				regs[1] = regs[0] + uint16(rng.Intn(7)) - 3 // live jitters ±3 around setpoint
				if regs[2]%50 == 0 {
					regs[3] = 0x0001 // occasional alarm bit
				} else {
					regs[3] = 0x0000
				}
				mu.Unlock()
			}
		}()
	}

	ln, err := net.Listen("tcp", "0.0.0.0:"+*port)
	if err != nil {
		log.Fatalf("modbus-emu: listen :%s: %v", *port, err)
	}
	log.Printf("modbus-emu: serving Modbus-TCP on :%s (dynamic=%v)", *port, *dynamic)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go serve(conn, regs, &mu)
	}
}

func serve(conn net.Conn, regs []uint16, mu *sync.Mutex) {
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
		case 3, 4:
			start := int(binary.BigEndian.Uint16(pdu[1:3]))
			count := int(binary.BigEndian.Uint16(pdu[3:5]))
			resp = make([]byte, 2+count*2)
			resp[0] = pdu[0]
			resp[1] = byte(count * 2)
			mu.Lock()
			for i := 0; i < count; i++ {
				var v uint16
				if start+i < len(regs) {
					v = regs[start+i]
				}
				binary.BigEndian.PutUint16(resp[2+i*2:], v)
			}
			mu.Unlock()
		case 6:
			addr := int(binary.BigEndian.Uint16(pdu[1:3]))
			val := binary.BigEndian.Uint16(pdu[3:5])
			mu.Lock()
			if addr < len(regs) {
				regs[addr] = val
			}
			mu.Unlock()
			resp = pdu
		default:
			resp = []byte{pdu[0] | 0x80, 0x01}
		}
		out := make([]byte, 7+len(resp))
		copy(out[0:2], hdr[0:2])
		binary.BigEndian.PutUint16(out[4:6], uint16(len(resp)+1))
		out[6] = hdr[6]
		copy(out[7:], resp)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}
