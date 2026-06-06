package robot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeMarlin is a Marlin firmware emulator over a real byte stream (io.Pipe
// pair, repo convention: no mocks). It tracks position and answers the same
// send-line/wait-`ok` protocol the real board does, so SerialBackend is
// exercised exactly as it will be against /dev/ttyUSB0 or a termux-usb fd.
type fakeMarlinState struct {
	mu      sync.Mutex
	x, y, z float64
	rel     bool
	lastFan int
	lastM42 int
}

func (s *fakeMarlinState) handle(line string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	up := strings.ToUpper(strings.TrimSpace(line))
	switch {
	case up == "G91":
		s.rel = true
	case up == "G90":
		s.rel = false
	case strings.HasPrefix(up, "G28"):
		s.x, s.y, s.z = 0, 0, 0
	case strings.HasPrefix(up, "G1") || strings.HasPrefix(up, "G0"):
		for _, tok := range strings.Fields(up)[1:] {
			if len(tok) < 2 {
				continue
			}
			val, err := strconv.ParseFloat(tok[1:], 64)
			if err != nil {
				continue
			}
			switch tok[0] {
			case 'X':
				if s.rel {
					s.x += val
				} else {
					s.x = val
				}
			case 'Y':
				if s.rel {
					s.y += val
				} else {
					s.y = val
				}
			case 'Z':
				if s.rel {
					s.z += val
				} else {
					s.z = val
				}
			}
		}
	case strings.HasPrefix(up, "M114"):
		return fmt.Sprintf("X:%.2f Y:%.2f Z:%.2f E:0.00 Count X:0 Y:0 Z:0\nok\n", s.x, s.y, s.z)
	case strings.HasPrefix(up, "M106"):
		s.lastFan = 255
	case up == "M107":
		s.lastFan = 0
	case strings.HasPrefix(up, "M42"):
		s.lastM42 = 1
	}
	return "ok\n"
}

// newFakeSerial wires a SerialBackend to a running fakeMarlin over io.Pipe pairs.
func newFakeSerial(t *testing.T, toolMode string) (*SerialBackend, *fakeMarlinState) {
	t.Helper()
	hr, hw := io.Pipe() // host writes -> device reads
	dr, dw := io.Pipe() // device writes -> host reads
	st := &fakeMarlinState{}
	go func() {
		cmds := bufio.NewReader(hr)
		_, _ = io.WriteString(dw, "start\n")
		for {
			line, err := cmds.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = io.WriteString(dw, st.handle(line))
		}
	}()
	rw := &pipeRW{r: dr, w: hw}
	t.Cleanup(func() { _ = rw.Close() })
	return NewSerialBackend(rw, toolMode, 6), st
}

type pipeRW struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRW) Close() error                { _ = p.r.Close(); return p.w.Close() }

func TestSerialBackendHomeMoveJog(t *testing.T) {
	be, st := newFakeSerial(t, "fan")
	c := NewController(be, fakeCam{}, VisionConfig{})
	ctx := context.Background()

	if h := c.Home(ctx, "", "off", ""); !h.OK {
		t.Fatalf("home: %s", h.Error)
	}
	x, y, z := 110.0, 110.0, 25.0
	r := c.Move(ctx, &x, &y, &z, 3000, "off", "")
	if !r.OK || r.Position == nil || r.Position.Z != 25 {
		t.Fatalf("move readback wrong: ok=%v pos=%+v", r.OK, r.Position)
	}
	if r.Cross == nil || !r.Cross.Agree {
		t.Fatalf("cross-check should agree over native serial: %+v", r.Cross)
	}
	j := c.Jog(ctx, "Z", 10, 600, "off", "")
	if !j.OK || j.Position == nil || j.Position.Z != 35 {
		t.Fatalf("jog Z+10 from 25 should be 35: %+v", j.Position)
	}
	// Tool fan mode → M106/M107 reached the board.
	if err := be.Tool(ctx, true); err != nil {
		t.Fatalf("tool on: %v", err)
	}
	st.mu.Lock()
	fan := st.lastFan
	st.mu.Unlock()
	if fan != 255 {
		t.Fatalf("fan-mode tool on should send M106 S255, got fan=%d", fan)
	}
}

// mkFakeMarlin builds a fake Marlin over io.Pipe; dieAfter>0 makes it drop the
// link (close both ends, simulating a USB disconnect) after that many commands.
func mkFakeMarlin(dieAfter int) *pipeRW {
	hr, hw := io.Pipe()
	dr, dw := io.Pipe()
	st := &fakeMarlinState{}
	go func() {
		cmds := bufio.NewReader(hr)
		_, _ = io.WriteString(dw, "start\n")
		n := 0
		for {
			line, err := cmds.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = io.WriteString(dw, st.handle(line))
			n++
			if dieAfter > 0 && n >= dieAfter {
				_ = dw.Close()
				_ = hr.Close() // host writes now fail → "serial link" → reconnect
				return
			}
		}
	}()
	return &pipeRW{r: dr, w: hw}
}

func TestSerialBackendReconnect(t *testing.T) {
	be := NewSerialBackend(mkFakeMarlin(1), "fan", 6) // device #1 dies after 1 cmd
	reopened := false
	be.Reopen = func() (io.ReadWriteCloser, error) { reopened = true; return mkFakeMarlin(0), nil }
	ctx := context.Background()

	if err := be.Home(ctx, ""); err != nil { // G28 = 1 cmd, succeeds; device #1 then dies
		t.Fatalf("first home: %v", err)
	}
	// Next command hits the dropped link → visible error + transport reconnect.
	if _, err := be.Position(ctx); err == nil {
		t.Fatalf("expected a link-lost error after disconnect")
	}
	if !reopened {
		t.Fatalf("backend should have reopened the port")
	}
	// On the fresh link, motion works again.
	if err := be.Home(ctx, ""); err != nil {
		t.Fatalf("home after reconnect should work: %v", err)
	}
}

func TestSerialBackendBusyThenOk(t *testing.T) {
	// A device that emits echo:busy lines before ok must not hang or error.
	hr, hw := io.Pipe()
	dr, dw := io.Pipe()
	go func() {
		cmds := bufio.NewReader(hr)
		for {
			line, err := cmds.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "G28") {
				_, _ = io.WriteString(dw, "echo:busy: processing\necho:busy: processing\nok\n")
				continue
			}
			_, _ = io.WriteString(dw, "ok\n")
		}
	}()
	rw := &pipeRW{r: dr, w: hw}
	defer rw.Close()
	be := NewSerialBackend(rw, "fan", 6)
	if err := be.Home(context.Background(), ""); err != nil {
		t.Fatalf("home through busy lines failed: %v", err)
	}
}
