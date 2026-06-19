package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/yaver-io/agent/machine"
)

// fakeModbus is a tiny in-process Modbus-TCP slave (fc 3 read, fc 6 write) so the
// machine_* driver verbs can be exercised end-to-end through dispatchOps against
// a real socket — no mocks, matching the repo's test conventions.
type fakeModbus struct {
	ln   net.Listener
	mu   sync.Mutex
	regs map[int]uint16
}

func startFakeModbus(t *testing.T, initial map[int]uint16) *fakeModbus {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeModbus{ln: ln, regs: map[int]uint16{}}
	for k, v := range initial {
		f.regs[k] = v
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeModbus) reg(a int) uint16 { f.mu.Lock(); defer f.mu.Unlock(); return f.regs[a] }

func (f *fakeModbus) handle(c net.Conn) {
	defer c.Close()
	rd := func(b []byte) bool {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		got := 0
		for got < len(b) {
			n, err := c.Read(b[got:])
			got += n
			if err != nil {
				return false
			}
		}
		return true
	}
	for {
		hdr := make([]byte, 7)
		if !rd(hdr) {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[4:6])) - 1
		if n < 1 || n > 260 {
			return
		}
		pdu := make([]byte, n)
		if !rd(pdu) {
			return
		}
		var resp []byte
		switch pdu[0] {
		case 0x03, 0x04:
			start := int(binary.BigEndian.Uint16(pdu[1:3]))
			count := int(binary.BigEndian.Uint16(pdu[3:5]))
			resp = []byte{pdu[0], byte(count * 2)}
			f.mu.Lock()
			for i := 0; i < count; i++ {
				v := f.regs[start+i]
				resp = append(resp, byte(v>>8), byte(v))
			}
			f.mu.Unlock()
		case 0x06:
			addr := int(binary.BigEndian.Uint16(pdu[1:3]))
			f.mu.Lock()
			f.regs[addr] = binary.BigEndian.Uint16(pdu[3:5])
			f.mu.Unlock()
			resp = pdu
		default:
			resp = []byte{pdu[0] | 0x80, 0x01}
		}
		out := make([]byte, 6, 7+len(resp))
		copy(out, hdr[:6])
		binary.BigEndian.PutUint16(out[4:6], uint16(len(resp)+1))
		out = append(out, hdr[6])
		out = append(out, resp...)
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

func machineTestCtx() OpsContext {
	return OpsContext{Ctx: context.Background(), Server: &HTTPServer{machineEnabled: true}, Caller: "owner"}
}

func dispatchVerb(t *testing.T, c OpsContext, verb string, payload map[string]any) OpsResult {
	t.Helper()
	raw, _ := json.Marshal(payload)
	spec, ok := opsRegistry[verb]
	if !ok {
		t.Fatalf("verb %q not registered", verb)
	}
	return spec.Handler(c, raw)
}

func TestMachineDriverVerbs_EndToEnd(t *testing.T) {
	f := startFakeModbus(t, map[int]uint16{12: 5000, 13: 500, 100: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := OpsContext{Ctx: ctx, Server: &HTTPServer{machineEnabled: true}, Caller: "owner"}

	tags := []map[string]any{
		{"name": "cut_length", "addr": 12, "func": 3, "unit2": "mm", "scale": 0.25, "min": 0, "max": 5000, "writable": true},
		{"name": "quantity", "addr": 13, "func": 3, "unit2": "pcs", "scale": 1, "min": 0, "max": 100000, "writable": true},
		{"name": "program", "addr": 20, "func": 3, "scale": 1, "writable": true},
		{"name": "running", "addr": 100, "func": 3, "kind": "alarm"},
	}

	// connect
	res := dispatchVerb(t, c, "machine_connect", map[string]any{
		"id": "yh-1", "kind": "cut_strip", "addr": f.ln.Addr().String(), "unit": 1,
		"statusTag": "running", "programTag": "program", "tags": tags,
	})
	if !res.OK {
		t.Fatalf("machine_connect failed: %s", res.Error)
	}

	// list shows it, running
	res = dispatchVerb(t, c, "machine_list", map[string]any{})
	if !res.OK {
		t.Fatalf("machine_list failed: %s", res.Error)
	}

	// read scaled
	res = dispatchVerb(t, c, "machine_read_tags", map[string]any{"id": "yh-1", "refs": []map[string]any{{"name": "cut_length"}}})
	if !res.OK {
		t.Fatalf("machine_read_tags failed: %s", res.Error)
	}
	init := res.Initial.(map[string]any)
	samples := init["samples"].([]machine.Sample)
	if samples[0].Value != 1250 {
		t.Errorf("cut_length: want 1250mm, got %v", samples[0].Value)
	}

	// write without confirm → refused
	res = dispatchVerb(t, c, "machine_write_tags", map[string]any{
		"id": "yh-1", "writes": []map[string]any{{"ref": map[string]any{"name": "cut_length"}, "value": 1000}},
	})
	if res.OK || res.Code != "needs_approval" {
		t.Fatalf("write without confirm should be refused, got ok=%v code=%s", res.OK, res.Code)
	}

	// write with confirm → applies inverse scale (1000mm → raw 4000)
	res = dispatchVerb(t, c, "machine_write_tags", map[string]any{
		"id": "yh-1", "confirm": true,
		"writes": []map[string]any{{"ref": map[string]any{"name": "cut_length"}, "value": 1000}},
	})
	if !res.OK {
		t.Fatalf("confirmed write failed: %s", res.Error)
	}
	if got := f.reg(12); got != 4000 {
		t.Errorf("register 12: want 4000, got %d", got)
	}

	// out-of-range write → driver rejects even with confirm
	res = dispatchVerb(t, c, "machine_write_tags", map[string]any{
		"id": "yh-1", "confirm": true,
		"writes": []map[string]any{{"ref": map[string]any{"name": "cut_length"}, "value": 9000}},
	})
	if res.OK {
		t.Error("out-of-range write should fail")
	}

	// submit job (gated) → program slot + params
	res = dispatchVerb(t, c, "machine_submit_job", map[string]any{
		"id": "yh-1", "confirm": true, "program": "7",
		"params": map[string]any{"quantity": 250},
	})
	if !res.OK {
		t.Fatalf("submit_job failed: %s", res.Error)
	}
	if got := f.reg(20); got != 7 {
		t.Errorf("program slot: want 7, got %d", got)
	}
	if got := f.reg(13); got != 250 {
		t.Errorf("quantity: want 250, got %d", got)
	}

	// disconnect
	res = dispatchVerb(t, c, "machine_disconnect", map[string]any{"id": "yh-1"})
	if !res.OK {
		t.Fatalf("disconnect failed: %s", res.Error)
	}
}

func TestMachineDriverVerbs_GatedWhenDisabled(t *testing.T) {
	c := OpsContext{Ctx: context.Background(), Server: &HTTPServer{machineEnabled: false}, Caller: "owner"}
	res := dispatchVerb(t, c, "machine_list", map[string]any{})
	if res.OK || res.Code != "unauthorized" {
		t.Fatalf("machine verbs must be gated when --machine is off, got ok=%v code=%s", res.OK, res.Code)
	}
}
