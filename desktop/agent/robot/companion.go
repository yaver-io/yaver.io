package robot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Companion is a small microcontroller (Arduino/RP2040/ESP32) on a second
// USB-serial link (or BLE) that gives the cell the I/O the printer board lacks:
// extra GPIO and — crucially — FORCE/TORQUE sensing (INA219 current or HX711
// load cell) so a screw can be driven to a real torque target instead of blind
// dwell. See docs/yaver-companion-mcu.md. The line protocol mirrors Marlin's
// send-line/get-line simplicity so the same transports work.
//
// Wire protocol (ASCII, newline-terminated, 115200):
//
//	PING            -> PONG
//	ZERO            -> OK            (tare the load cell)
//	SENSE           -> S cur=<mA> force=<g> tq=<Nmm>
//	GPIO <pin> <0|1>-> OK
//	STREAM <hz>     -> repeated "S ..." lines (0 = stop)
type SenseReading struct {
	CurrentmA float64 `json:"currentmA"`
	ForceG    float64 `json:"forceG"`
	TorqueNmm float64 `json:"torqueNmm"`
	Raw       string  `json:"raw,omitempty"`
}

type Companion interface {
	Ping(ctx context.Context) error
	Zero(ctx context.Context) error
	Sense(ctx context.Context) (SenseReading, error)
	GPIO(ctx context.Context, pin int, on bool) error
	Close() error
}

// LineCompanion speaks the protocol over any byte stream (a serial port opened
// elsewhere, or a BLE characteristic adapter exposing io.ReadWriteCloser).
type LineCompanion struct {
	mu sync.Mutex
	rw io.ReadWriteCloser
	br *bufio.Reader
}

func NewLineCompanion(rw io.ReadWriteCloser) *LineCompanion {
	return &LineCompanion{rw: rw, br: bufio.NewReader(rw)}
}

func (c *LineCompanion) cmd(ctx context.Context, line string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := io.WriteString(c.rw, line+"\n"); err != nil {
		return "", err
	}
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := c.br.ReadString('\n')
		ch <- res{strings.TrimSpace(s), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.s, r.err
	}
}

func (c *LineCompanion) Ping(ctx context.Context) error {
	r, err := c.cmd(ctx, "PING")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(r, "PONG") {
		return fmt.Errorf("companion: unexpected ping reply %q", r)
	}
	return nil
}

func (c *LineCompanion) Zero(ctx context.Context) error {
	_, err := c.cmd(ctx, "ZERO")
	return err
}

func (c *LineCompanion) GPIO(ctx context.Context, pin int, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := c.cmd(ctx, fmt.Sprintf("GPIO %d %d", pin, v))
	return err
}

func (c *LineCompanion) Sense(ctx context.Context) (SenseReading, error) {
	r, err := c.cmd(ctx, "SENSE")
	if err != nil {
		return SenseReading{}, err
	}
	return parseSense(r)
}

// parseSense reads "S cur=123.4 force=56.7 tq=0.40".
func parseSense(line string) (SenseReading, error) {
	sr := SenseReading{Raw: line}
	if !strings.HasPrefix(line, "S ") && !strings.HasPrefix(line, "S\t") {
		return sr, fmt.Errorf("companion: bad sense line %q", line)
	}
	for _, tok := range strings.Fields(line[1:]) {
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) != 2 {
			continue
		}
		f, err := strconv.ParseFloat(kv[1], 64)
		if err != nil {
			continue
		}
		switch kv[0] {
		case "cur":
			sr.CurrentmA = f
		case "force":
			sr.ForceG = f
		case "tq":
			sr.TorqueNmm = f
		}
	}
	return sr, nil
}

func (c *LineCompanion) Close() error { return c.rw.Close() }

// pollTorque samples the companion until torque ≥ target, ctx is done, or the
// deadline passes. Returns the last reading and whether the target was reached.
func pollTorque(ctx context.Context, comp Companion, target float64, every time.Duration, deadline time.Time) (SenseReading, bool) {
	var last SenseReading
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return last, false
		default:
		}
		if r, err := comp.Sense(ctx); err == nil {
			last = r
			if r.TorqueNmm >= target {
				return r, true
			}
		}
		time.Sleep(every)
	}
	return last, false
}
