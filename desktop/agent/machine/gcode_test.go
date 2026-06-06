package machine

// gcode_test.go — covers the G-code/CNC class with no hardware: a fake
// controller on one end of a net.Pipe answers `ok`/`error`, so the ok-gated
// send/stream handshake is exercised for real, plus the pure motion-safety
// validator (soft limits, modal G90/G91) and the un-gated realtime e-stop.

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeController answers each newline-terminated line. linesToFail (by 1-based
// index) get an "error:..." reply; everything else gets "ok". It records every
// raw byte received so realtime bytes (e-stop / status `?`) are observable.
type fakeController struct {
	conn     net.Conn
	received *strings.Builder
}

func startFakeController(t *testing.T, failAt map[int]bool, statusReply string) (*GCodeClient, *fakeController, func()) {
	t.Helper()
	master, slave := net.Pipe()
	fc := &fakeController{conn: slave, received: &strings.Builder{}}
	go func() {
		r := bufio.NewReader(slave)
		n := 0
		for {
			b, err := r.ReadByte()
			if err != nil {
				return
			}
			fc.received.WriteByte(b)
			switch b {
			case '?': // GRBL realtime status query
				_, _ = slave.Write([]byte(statusReply + "\nok\n"))
				continue
			case '!', 0x18: // feed-hold / soft-reset realtime bytes — no reply
				continue
			case '\n':
				n++
				if failAt[n] {
					_, _ = slave.Write([]byte("error:9\n"))
				} else {
					_, _ = slave.Write([]byte("ok\n"))
				}
			}
		}
	}()
	client := NewGCodeClient(master, DialectGRBL)
	return client, fc, func() { _ = master.Close(); _ = slave.Close() }
}

func TestGCodeSend_okGated(t *testing.T) {
	c, _, stop := startFakeController(t, nil, "")
	defer stop()
	reply, err := c.Send("G0 X10", 2*time.Second)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !reply.OK {
		t.Fatalf("expected ok, got %+v", reply)
	}
}

func TestGCodeSend_errorReply(t *testing.T) {
	c, _, stop := startFakeController(t, map[int]bool{1: true}, "")
	defer stop()
	reply, err := c.Send("G0 X10", 2*time.Second)
	if err != nil {
		t.Fatalf("send returned transport error: %v", err)
	}
	if reply.OK || reply.Error == "" {
		t.Fatalf("expected an error reply, got %+v", reply)
	}
}

func TestGCodeStream_okGatedCountsAndAborts(t *testing.T) {
	e, _ := New()
	c, _, stop := startFakeController(t, map[int]bool{2: true}, "")
	defer stop()
	// Register the client as a session so Engine.GCodeStream can drive it.
	id := "gcode-test"
	e.gcodes = map[string]*GCodeSession{id: {ID: id, client: c, Dialect: DialectGRBL}}

	res, err := e.GCodeStream(id, []string{"G0 X1", "G0 X2", "G0 X3"}, SoftLimits{}, false, 2*time.Second)
	if err == nil {
		t.Fatal("expected stream to abort on the failing line")
	}
	if res.FailedLine != 2 {
		t.Errorf("expected abort at line 2, got %d", res.FailedLine)
	}
	if res.Sent != 1 {
		t.Errorf("expected 1 line sent before abort, got %d", res.Sent)
	}
}

func TestGCodeStream_dryRunValidatesWithoutSending(t *testing.T) {
	e, _ := New()
	// No controller needed: dry-run must not transmit.
	id := "gcode-dry"
	e.gcodes = map[string]*GCodeSession{id: {ID: id, Dialect: DialectGRBL}}
	lim := SoftLimits{Enabled: true, XMin: 0, XMax: 100, YMin: 0, YMax: 100, ZMin: -50, ZMax: 0}

	res, err := e.GCodeStream(id, []string{"G90", "G0 X10 Y10", "G1 Z-10"}, lim, true, time.Second)
	if err != nil {
		t.Fatalf("dry-run of a safe program should pass: %v", err)
	}
	if !res.DryRun || !res.OK || res.Sent != 0 {
		t.Fatalf("dry-run should send nothing: %+v", res)
	}
}

func TestGCodeStream_softLimitAbortsBeforeSend(t *testing.T) {
	e, _ := New()
	id := "gcode-lim"
	e.gcodes = map[string]*GCodeSession{id: {ID: id, Dialect: DialectGRBL}}
	lim := SoftLimits{Enabled: true, XMin: 0, XMax: 100, YMin: 0, YMax: 100, ZMin: -50, ZMax: 0}

	res, err := e.GCodeStream(id, []string{"G90", "G0 X150"}, lim, false, time.Second)
	if err == nil {
		t.Fatal("a move outside the envelope must be refused")
	}
	if len(res.Violations) == 0 {
		t.Fatal("expected a recorded violation")
	}
	if res.Sent != 0 {
		t.Errorf("nothing should be sent when validation fails, sent=%d", res.Sent)
	}
}

func TestGCodeEStop_realtimeBytes(t *testing.T) {
	c, fc, stop := startFakeController(t, nil, "")
	defer stop()
	if err := c.EStop(); err != nil {
		t.Fatalf("estop: %v", err)
	}
	// Give the controller goroutine a moment to record the bytes.
	time.Sleep(50 * time.Millisecond)
	got := fc.received.String()
	if !strings.ContainsRune(got, '!') || !strings.ContainsRune(got, rune(0x18)) {
		t.Errorf("estop should send feed-hold ! and soft-reset 0x18; got %q", got)
	}
}

func TestValidateProgram_relativeMoves(t *testing.T) {
	lim := SoftLimits{Enabled: true, XMin: 0, XMax: 50, YMin: 0, YMax: 50, ZMin: -10, ZMax: 0}
	// Absolute start at X40, then a relative +20 → X60, outside [0,50].
	prog := []string{"G90", "G0 X40", "G91", "G0 X20"}
	v := ValidateProgram(prog, lim)
	if len(v) == 0 {
		t.Fatal("expected the relative move to breach the X envelope")
	}
	if v[0].Line != 4 {
		t.Errorf("expected violation at line 4, got %d", v[0].Line)
	}
}

func TestIsMotionLine(t *testing.T) {
	cases := map[string]bool{
		"G0 X10":     true,
		"G1 Y5 F100": true,
		"X12.5":      true,
		"M3 S1000":   false,
		"; comment":  false,
		"G28":        false,
		"G90":        false,
	}
	for line, want := range cases {
		if got := IsMotionLine(line); got != want {
			t.Errorf("IsMotionLine(%q)=%v want %v", line, got, want)
		}
	}
}
