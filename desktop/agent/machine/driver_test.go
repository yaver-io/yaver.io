package machine

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeModbusTCP is a tiny in-process Modbus-TCP slave for tests: it serves
// fc 3 (read holding) and fc 6 (write single) over a register store. No mocks,
// real sockets — matches the repo's "real servers on random ports" test rule.
type fakeModbusTCP struct {
	ln   net.Listener
	mu   sync.Mutex
	regs map[int]uint16
}

func newFakeModbusTCP(t *testing.T, initial map[int]uint16) *fakeModbusTCP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeModbusTCP{ln: ln, regs: map[int]uint16{}}
	for k, v := range initial {
		f.regs[k] = v
	}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeModbusTCP) addr() string { return f.ln.Addr().String() }

func (f *fakeModbusTCP) get(addr int) uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.regs[addr]
}

func (f *fakeModbusTCP) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeModbusTCP) handle(c net.Conn) {
	defer c.Close()
	for {
		hdr := make([]byte, 7)
		if _, err := readFull(c, hdr); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[4:6])) - 1
		if n < 1 || n > 260 {
			return
		}
		pdu := make([]byte, n)
		if _, err := readFull(c, pdu); err != nil {
			return
		}
		resp := f.respond(pdu)
		out := make([]byte, 6, 6+1+len(resp))
		copy(out, hdr[:6])
		binary.BigEndian.PutUint16(out[4:6], uint16(len(resp)+1))
		out = append(out, hdr[6]) // unit
		out = append(out, resp...)
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

func (f *fakeModbusTCP) respond(pdu []byte) []byte {
	if len(pdu) < 1 {
		return []byte{0x80, 0x01}
	}
	switch pdu[0] {
	case 0x03, 0x04: // read holding/input
		if len(pdu) < 5 {
			return []byte{pdu[0] | 0x80, 0x03}
		}
		start := int(binary.BigEndian.Uint16(pdu[1:3]))
		count := int(binary.BigEndian.Uint16(pdu[3:5]))
		out := []byte{pdu[0], byte(count * 2)}
		f.mu.Lock()
		for i := 0; i < count; i++ {
			v := f.regs[start+i]
			out = append(out, byte(v>>8), byte(v))
		}
		f.mu.Unlock()
		return out
	case 0x06: // write single
		if len(pdu) < 5 {
			return []byte{0x86, 0x03}
		}
		addr := int(binary.BigEndian.Uint16(pdu[1:3]))
		val := binary.BigEndian.Uint16(pdu[3:5])
		f.mu.Lock()
		f.regs[addr] = val
		f.mu.Unlock()
		return pdu // echo
	default:
		return []byte{pdu[0] | 0x80, 0x01}
	}
}

func f64(v float64) *float64 { return &v }

func newTestModbusDriver(addr string) *ModbusDriver {
	return NewModbusDriver(ModbusConfig{
		Name: "yh8030h-test", Kind: "cut_strip", Addr: addr, Unit: 1,
		Timeout:    2 * time.Second,
		StatusTag:  "running",
		ProgramTag: "program",
		Tags: []Tag{
			{Name: "cut_length", Addr: 12, Func: 3, Unit2: "mm", Scale: 0.25, Min: f64(0), Max: f64(5000), Writable: true},
			{Name: "quantity", Addr: 13, Func: 3, Unit2: "pcs", Scale: 1, Min: f64(0), Max: f64(100000), Writable: true},
			{Name: "program", Addr: 20, Func: 3, Scale: 1, Writable: true},
			{Name: "running", Addr: 100, Func: 3, Kind: "alarm"},
		},
	})
}

func TestModbusDriver_ReadScaled(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{12: 5000, 13: 500})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	ctx := context.Background()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	samples, err := d.Read(ctx, []TagRef{{Name: "cut_length"}, {Name: "quantity"}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("want 2 samples, got %d", len(samples))
	}
	// 5000 raw * 0.25 scale = 1250 mm
	if samples[0].Value != 1250 {
		t.Errorf("cut_length: want 1250mm, got %v (raw %d)", samples[0].Value, samples[0].Raw)
	}
	if samples[0].Unit2 != "mm" {
		t.Errorf("cut_length unit: want mm, got %q", samples[0].Unit2)
	}
	if samples[1].Value != 500 {
		t.Errorf("quantity: want 500, got %v", samples[1].Value)
	}
}

func TestModbusDriver_ReadAllWhenNoRefs(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{12: 4000, 13: 250, 20: 7, 100: 0})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	samples, err := d.Read(context.Background(), nil)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("want 4 tags, got %d", len(samples))
	}
}

func TestModbusDriver_WriteInverseScaleAndVerify(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{12: 0})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	ctx := context.Background()
	// write 1000 mm → raw 1000/0.25 = 4000
	if err := d.Write(ctx, []TagWrite{{Ref: TagRef{Name: "cut_length"}, Value: 1000}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := f.get(12); got != 4000 {
		t.Errorf("register 12: want raw 4000, got %d", got)
	}
}

func TestModbusDriver_WriteRejectsOutOfRange(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{12: 0})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	// cut_length max is 5000mm → 6000 must be refused, register untouched
	err := d.Write(context.Background(), []TagWrite{{Ref: TagRef{Name: "cut_length"}, Value: 6000}})
	if err == nil {
		t.Fatal("want error for out-of-range write")
	}
	if got := f.get(12); got != 0 {
		t.Errorf("register must be untouched on rejected write, got %d", got)
	}
}

func TestModbusDriver_StatusRunning(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{100: 1})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	st, err := d.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Connected {
		t.Error("want connected")
	}
	if st.State != "running" {
		t.Errorf("want running (status reg=1), got %q", st.State)
	}
}

func TestModbusDriver_SubmitJobAndRecall(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	ctx := context.Background()
	job := Job{Program: "7", Params: map[string]float64{"cut_length": 500, "quantity": 1000}}
	if err := d.SubmitJob(ctx, job); err != nil {
		t.Fatalf("submit job: %v", err)
	}
	if got := f.get(20); got != 7 {
		t.Errorf("program slot: want 7, got %d", got)
	}
	if got := f.get(12); got != 2000 { // 500/0.25
		t.Errorf("cut_length raw: want 2000, got %d", got)
	}
	if got := f.get(13); got != 1000 {
		t.Errorf("quantity raw: want 1000, got %d", got)
	}
}

func TestModbusDriver_Subscribe(t *testing.T) {
	f := newFakeModbusTCP(t, map[int]uint16{12: 4000})
	d := newTestModbusDriver(f.addr())
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := d.Subscribe(ctx, []TagRef{{Name: "cut_length"}}, SubOpts{IntervalMs: 50})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	select {
	case s := <-ch:
		if s.Value != 1000 { // 4000 * 0.25
			t.Errorf("subscribe sample: want 1000, got %v", s.Value)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no sample from subscription")
	}
}

func TestCapSet(t *testing.T) {
	c := Caps(CapRead, CapStatus, CapWrite)
	if !c.Has(CapRead) || c.Has(CapVision) {
		t.Error("CapSet.Has wrong")
	}
	list := c.List()
	if len(list) != 3 {
		t.Fatalf("want 3 caps, got %d", len(list))
	}
	// sorted: read, status, write
	if list[0] != CapRead || list[1] != CapStatus || list[2] != CapWrite {
		t.Errorf("caps not sorted: %v", list)
	}
}
