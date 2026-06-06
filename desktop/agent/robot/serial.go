package robot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SerialBackend drives Marlin DIRECTLY over a byte stream (a USB-serial fd) —
// no Python bridge. It is the same code whether the stream is /dev/ttyUSB0 on a
// laptop or the file descriptor termux-usb hands a process on an Android phone
// (docs/yaver-robotics-edge-vibing.md). This is what makes the phone a true
// Pi-replacement host: robotd opens the fd and speaks G-code itself.
//
// Protocol = Marlin's send-line / wait-`ok` (identical semantics to the ender_ui
// bridge we proved live): the board resets on open (Settle waits ~2s), busy
// lines extend the wait, Error/!!/Halt abort.
type SerialBackend struct {
	ToolMode string // "fan" (M106/M107) | "screw" (M42) | default fan
	ToolPin  int    // M42 pin when ToolMode=="screw"
	// Reopen, if set, lets the backend recover from a serial disconnect (USB
	// glitch / board reset) by re-opening the port. After a reconnect the board
	// has reset, so the in-flight command fails VISIBLY and a re-home is forced
	// (we never silently continue with an unknown position).
	Reopen func() (io.ReadWriteCloser, error)

	mu  sync.Mutex
	mc  *marlinConn
	pos Position
}

func NewSerialBackend(rw io.ReadWriteCloser, toolMode string, toolPin int) *SerialBackend {
	if toolMode == "" {
		toolMode = "fan"
	}
	return &SerialBackend{mc: newMarlinConn(rw), ToolMode: toolMode, ToolPin: toolPin}
}

func (b *SerialBackend) conn() *marlinConn {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mc
}

// cmd routes every command so a dropped serial link is recovered: it reconnects
// the transport for next time, but returns a visible error now (forcing a
// re-home) rather than silently retrying motion against a reset board.
func (b *SerialBackend) cmd(ctx context.Context, line string) ([]string, error) {
	lines, err := b.conn().sendOK(ctx, line)
	if err != nil && b.Reopen != nil && strings.Contains(err.Error(), "serial link") {
		if rerr := b.reconnect(ctx); rerr != nil {
			return lines, fmt.Errorf("serial link lost and reconnect failed: %w", rerr)
		}
		return lines, fmt.Errorf("serial link reset — re-home required: %w", err)
	}
	return lines, err
}

func (b *SerialBackend) reconnect(ctx context.Context) error {
	rw, err := b.Reopen()
	if err != nil {
		return err
	}
	mc := newMarlinConn(rw)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second): // board resets on open
	}
	mc.drain()
	_, _ = mc.sendOK(ctx, "M115")
	b.mu.Lock()
	old := b.mc
	b.mc = mc
	b.pos = Position{} // position is unknown until re-home
	b.mu.Unlock()
	if old != nil {
		_ = old.rw.Close()
	}
	return nil
}

func (b *SerialBackend) Name() string { return "marlin-serial" }

// Settle waits out the board's reset-on-open, drains the boot banner, and pings
// firmware. Call once after opening the port before any motion.
func (b *SerialBackend) Settle(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}
	b.mc.drain()
	_, err := b.cmd(ctx, "M115")
	return err
}

func (b *SerialBackend) Home(ctx context.Context, axes string) error {
	_, err := b.cmd(ctx, strings.TrimSpace("G28 "+strings.ToUpper(axes)))
	return err
}

func (b *SerialBackend) Jog(ctx context.Context, axis string, dist float64, feed int) error {
	if _, err := b.cmd(ctx, "G91"); err != nil {
		return err
	}
	cmd := fmt.Sprintf("G1 %s%s F%d", strings.ToUpper(axis), trimNum(dist), feedOr(feed, 600))
	if _, err := b.cmd(ctx, cmd); err != nil {
		_, _ = b.cmd(ctx, "G90")
		return err
	}
	_, err := b.cmd(ctx, "G90")
	return err
}

func (b *SerialBackend) Move(ctx context.Context, x, y, z *float64, feed int) error {
	if _, err := b.cmd(ctx, "G90"); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("G1")
	if x != nil {
		sb.WriteString(" X" + trimNum(*x))
	}
	if y != nil {
		sb.WriteString(" Y" + trimNum(*y))
	}
	if z != nil {
		sb.WriteString(" Z" + trimNum(*z))
	}
	sb.WriteString(fmt.Sprintf(" F%d", feedOr(feed, 1500)))
	_, err := b.cmd(ctx, sb.String())
	return err
}

func (b *SerialBackend) Tool(ctx context.Context, on bool) error {
	switch b.ToolMode {
	case "screw":
		v := 0
		if on {
			v = 255
		}
		_, err := b.cmd(ctx, fmt.Sprintf("M42 P%d S%d", b.ToolPin, v))
		return err
	default: // "fan"
		if on {
			_, err := b.cmd(ctx, "M106 S255")
			return err
		}
		_, err := b.cmd(ctx, "M107")
		return err
	}
}

func (b *SerialBackend) WaitMoves(ctx context.Context) error {
	_, err := b.cmd(ctx, "M400")
	return err
}

func (b *SerialBackend) Raw(ctx context.Context, line string) error {
	_, err := b.cmd(ctx, strings.TrimSpace(line))
	return err
}

func (b *SerialBackend) Position(ctx context.Context) (Position, error) {
	lines, err := b.cmd(ctx, "M114")
	if err != nil {
		return Position{}, err
	}
	for _, ln := range lines {
		if m := m114re.FindStringSubmatch(ln); m != nil {
			x, _ := strconv.ParseFloat(m[1], 64)
			y, _ := strconv.ParseFloat(m[2], 64)
			z, _ := strconv.ParseFloat(m[3], 64)
			p := Position{X: x, Y: y, Z: z, Homed: true}
			b.mu.Lock()
			b.pos = p
			b.mu.Unlock()
			return p, nil
		}
	}
	return Position{}, fmt.Errorf("no M114 position in reply")
}

// EStop sends M112 (emergency kill) — like the bridge. Recovery needs a board
// reset (the Controller's reset latch re-requires homing).
func (b *SerialBackend) EStop(ctx context.Context) error {
	_, _ = b.cmd(ctx, "M107") // tool off first
	_, err := b.cmd(ctx, "M112")
	return err
}

func (b *SerialBackend) Status(ctx context.Context) (Status, error) {
	st := Status{OK: true, Backend: b.Name(), Connected: true}
	b.mu.Lock()
	p := b.pos
	b.mu.Unlock()
	if p.X != 0 || p.Y != 0 || p.Z != 0 || p.Homed {
		st.Position = &p
	}
	return st, nil
}

// --- Marlin line transport -------------------------------------------------

// marlinConn owns ONE background reader goroutine that pushes lines onto a
// channel; commands are serialized by mu and consume from that channel with
// context cancellation. This avoids the per-call read-goroutine leak a naive
// implementation has on a long-running edge daemon, and gives a single place to
// observe a serial disconnect (readErr).
type marlinConn struct {
	rw      io.ReadWriteCloser
	mu      sync.Mutex
	lines   chan string
	readErr atomic.Value // error, set once when the reader stops
	Log     func(string)
}

func newMarlinConn(rw io.ReadWriteCloser) *marlinConn {
	m := &marlinConn{rw: rw, lines: make(chan string, 128)}
	go m.readLoop()
	return m
}

func (m *marlinConn) readLoop() {
	br := bufio.NewReader(m.rw)
	for {
		line, err := br.ReadString('\n')
		if s := strings.TrimSpace(line); s != "" {
			select {
			case m.lines <- s:
			default: // channel full (shouldn't happen) — drop to avoid blocking the reader
			}
		}
		if err != nil {
			m.readErr.Store(err)
			close(m.lines)
			return
		}
	}
}

// Err returns the serial read error once the link has dropped (else nil) — the
// signal the Controller uses to e-stop on disconnect.
func (m *marlinConn) Err() error {
	if v := m.readErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// sendOK writes a line and reads until "ok", returning any non-ok payload lines
// (e.g. the M114 position line). Busy lines extend the wait; Error/!!/Halt abort;
// a closed channel means the serial link dropped.
func (m *marlinConn) sendOK(ctx context.Context, line string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := io.WriteString(m.rw, line+"\n"); err != nil {
		return nil, fmt.Errorf("serial link write failed: %w", err) // tag so cmd() reconnects
	}
	if m.Log != nil {
		m.Log(">> " + line)
	}
	var collected []string
	for {
		select {
		case <-ctx.Done():
			return collected, ctx.Err()
		case raw, ok := <-m.lines:
			if !ok {
				if e := m.Err(); e != nil {
					return collected, fmt.Errorf("serial link lost: %w", e)
				}
				return collected, fmt.Errorf("serial link closed")
			}
			if m.Log != nil {
				m.Log(raw)
			}
			low := strings.ToLower(raw)
			switch {
			case strings.HasPrefix(low, "echo:busy"):
				continue // printer working — keep waiting
			case strings.HasPrefix(low, "ok"):
				return collected, nil
			case strings.HasPrefix(raw, "Error") || strings.HasPrefix(raw, "!!") || strings.HasPrefix(low, "halt"):
				return collected, fmt.Errorf("marlin: %s", raw)
			default:
				collected = append(collected, raw)
			}
		}
	}
}

// drain consumes any currently-buffered lines (the boot banner) without blocking.
func (m *marlinConn) drain() {
	for {
		select {
		case <-m.lines:
		default:
			return
		}
	}
}

func feedOr(f, def int) int {
	if f > 0 {
		return f
	}
	return def
}

// trimNum formats a float without a trailing ".0" (G-code is happy either way,
// but tidy lines are easier to read in the motion log).
func trimNum(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}
