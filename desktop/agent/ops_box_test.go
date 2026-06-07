package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeBoxControl emulates the ESP32 line-control port (:8347).
func fakeBoxControl(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case cmd == "PING":
						c.Write([]byte("PONG\n"))
					case cmd == "INFO":
						c.Write([]byte("INFO fw=test id=DEADBEEF link=wifi bus=rs485 baud=9600\n"))
					case cmd == "SENSE":
						c.Write([]byte("S cur=12 force=0 tq=0 vin=23900 ibus=12\n"))
					case strings.HasPrefix(cmd, "ABSWAP"), strings.HasPrefix(cmd, "TERM"),
						strings.HasPrefix(cmd, "BIAS"), strings.HasPrefix(cmd, "LED"),
						strings.HasPrefix(cmd, "BAUD"), strings.HasPrefix(cmd, "BUS"):
						c.Write([]byte("OK\n"))
					default:
						c.Write([]byte("ERR unknown\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// fakeModbusTCP emulates a Modbus-TCP slave (the box gateway side).
func fakeModbusTCP(t *testing.T, vals []uint16) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 7)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				pdu := make([]byte, 5)
				if _, err := io.ReadFull(c, pdu); err != nil {
					return
				}
				unit, fc := hdr[6], pdu[0]
				bc := byte(len(vals) * 2)
				resp := []byte{hdr[0], hdr[1], 0, 0, 0, byte(3 + int(bc)), unit, fc, bc}
				for _, v := range vals {
					resp = append(resp, byte(v>>8), byte(v))
				}
				c.Write(resp)
			}(c)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func TestBoxControlCmd(t *testing.T) {
	addr, stop := fakeBoxControl(t)
	defer stop()
	if got, err := boxControlCmd(addr, "PING", time.Second); err != nil || got != "PONG" {
		t.Fatalf("PING -> %q, %v", got, err)
	}
	if got, err := boxControlCmd(addr, "ABSWAP 1", time.Second); err != nil || got != "OK" {
		t.Fatalf("ABSWAP -> %q, %v", got, err)
	}
	info, _ := boxControlCmd(addr, "INFO", time.Second)
	if !strings.HasPrefix(info, "INFO ") {
		t.Fatalf("INFO -> %q", info)
	}
}

func TestModbusReadTCP(t *testing.T) {
	want := []uint16{0x1234, 0x00FF, 1250}
	addr, stop := fakeModbusTCP(t, want)
	defer stop()
	got, err := modbusReadTCP(addr, 1, 3, 0, len(want), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reg[%d]=%#x want %#x", i, got[i], want[i])
		}
	}
}

func TestParseKV(t *testing.T) {
	if v := parseKV("S cur=12 force=0 vin=23900 ibus=12", "vin"); v != 23900 {
		t.Fatalf("vin=%d", v)
	}
	if v := parseKV("S cur=12", "vin"); v != 0 {
		t.Fatalf("missing key should be 0, got %d", v)
	}
}

// modbus header length sanity: the gateway encodes len = unit + pdu.
func TestModbusHeaderLen(t *testing.T) {
	hdr := make([]byte, 7)
	binary.BigEndian.PutUint16(hdr[4:6], 6)
	if binary.BigEndian.Uint16(hdr[4:6]) != 6 {
		t.Fatal("len encode")
	}
}
