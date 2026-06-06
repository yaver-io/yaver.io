package machine

// gcode.go — the G-code / CNC machine class. G-code is a different protocol from
// Modbus: line-oriented ASCII over RS-232/USB with an `ok`/`error` flow-control
// handshake (GRBL, Marlin, LinuxCNC-ish). It is NOT register read/write, so it
// gets its own client rather than being bolted onto the Modbus engine — they
// share only the serial-port primitives + the half-duplex bus arbitration.
//
// Safety (the CNC-specific reason this is careful): unlike a clamped register
// write, a bad move drives a real axis into a fixture. So:
//   - EStop is realtime and UN-gated: it bypasses the send mutex and writes the
//     feed-hold + soft-reset bytes immediately, even mid-stream. Stopping must
//     never need approval.
//   - Motion (Send/Stream of G0/G1...) is validated against a soft-limit
//     envelope and can be dry-run (validate, don't send). Out-of-envelope moves
//     are refused before a byte hits the wire.

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GCodeDialect selects the realtime/abort conventions of the controller.
type GCodeDialect string

const (
	DialectGRBL    GCodeDialect = "grbl"    // ?/~/!/0x18 realtime, ALARM/error:
	DialectMarlin  GCodeDialect = "marlin"  // M114 status, M112 emergency stop
	DialectGeneric GCodeDialect = "generic" // best-effort: try both
)

// SoftLimits is the allowed motion envelope in machine units (mm). When Enabled,
// any move whose target leaves the box is refused. Mirrors a controller's own
// soft limits but enforced before transmission so it works even on controllers
// that have them disabled.
type SoftLimits struct {
	Enabled bool    `json:"enabled"`
	XMin    float64 `json:"xMin"`
	XMax    float64 `json:"xMax"`
	YMin    float64 `json:"yMin"`
	YMax    float64 `json:"yMax"`
	ZMin    float64 `json:"zMin"`
	ZMax    float64 `json:"zMax"`
}

// Violation is one motion-safety problem found by the validator.
type Violation struct {
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// GCodeReply is the controller's answer to one line.
type GCodeReply struct {
	Lines []string `json:"lines"`
	OK    bool     `json:"ok"`
	Error string   `json:"error,omitempty"`
}

// GCodeClient is a line-protocol master bound to one open serial port.
type GCodeClient struct {
	port    io.ReadWriteCloser
	r       *bufio.Reader
	dialect GCodeDialect
	mu      sync.Mutex // serializes line sends; EStop deliberately bypasses it
}

// NewGCodeClient wraps an open serial port as a G-code master.
func NewGCodeClient(port io.ReadWriteCloser, dialect GCodeDialect) *GCodeClient {
	if dialect == "" {
		dialect = DialectGeneric
	}
	return &GCodeClient{port: port, r: bufio.NewReader(port), dialect: dialect}
}

func (c *GCodeClient) Close() error { return c.port.Close() }

// Send writes one line and reads until a terminal token (ok / error / ALARM).
func (c *GCodeClient) Send(line string, timeout time.Duration) (GCodeReply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendLocked(line, timeout)
}

func (c *GCodeClient) sendLocked(line string, timeout time.Duration) (GCodeReply, error) {
	line = strings.TrimRight(line, "\r\n")
	if _, err := c.port.Write([]byte(line + "\n")); err != nil {
		return GCodeReply{}, err
	}
	return c.readReply(timeout)
}

// readReply collects response lines until a terminal token or timeout.
func (c *GCodeClient) readReply(timeout time.Duration) (GCodeReply, error) {
	type lineMsg struct {
		s   string
		err error
	}
	ch := make(chan lineMsg, 1)
	reply := GCodeReply{}
	deadline := time.Now().Add(timeout)
	for {
		go func() {
			s, err := c.r.ReadString('\n')
			ch <- lineMsg{s, err}
		}()
		select {
		case m := <-ch:
			s := strings.TrimSpace(m.s)
			if s != "" {
				reply.Lines = append(reply.Lines, s)
				low := strings.ToLower(s)
				switch {
				case low == "ok" || strings.HasPrefix(low, "ok "):
					reply.OK = true
					return reply, nil
				case strings.HasPrefix(low, "error") || strings.HasPrefix(low, "alarm") || strings.HasPrefix(s, "!!"):
					reply.Error = s
					return reply, nil
				}
			}
			if m.err != nil {
				if reply.Error == "" && !reply.OK {
					reply.Error = "stream closed before ok"
				}
				return reply, m.err
			}
		case <-time.After(time.Until(deadline)):
			reply.Error = "timeout waiting for ok"
			return reply, fmt.Errorf("gcode: timeout waiting for ok/error")
		}
		if time.Now().After(deadline) {
			reply.Error = "timeout waiting for ok"
			return reply, fmt.Errorf("gcode: timeout waiting for ok/error")
		}
	}
}

// Status queries the controller's live state (GRBL realtime `?`, Marlin M114).
func (c *GCodeClient) Status(timeout time.Duration) (GCodeReply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.dialect {
	case DialectMarlin:
		return c.sendLocked("M114", timeout)
	default:
		if _, err := c.port.Write([]byte{'?'}); err != nil {
			return GCodeReply{}, err
		}
		return c.readReply(timeout)
	}
}

// EStop is the emergency stop: realtime, un-gated, and it bypasses the send
// mutex on purpose so it lands even while a Stream holds it. GRBL: feed-hold
// `!` then soft-reset 0x18. Marlin: M112. Generic: all of them.
func (c *GCodeClient) EStop() error {
	var firstErr error
	w := func(b []byte) {
		if _, err := c.port.Write(b); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	switch c.dialect {
	case DialectMarlin:
		w([]byte("M112\n"))
	case DialectGRBL:
		w([]byte{'!'})
		w([]byte{0x18})
	default:
		w([]byte{'!'})
		w([]byte{0x18})
		w([]byte("M112\n"))
	}
	return firstErr
}

// ── motion-safety validator (used by dry-run + before every real stream) ─────

// ValidateProgram checks a G-code program against soft limits, tracking modal
// G90/G91 (absolute/relative) and position so relative moves are bounded too.
// Returns every violation; an empty slice means safe to run.
func ValidateProgram(lines []string, lim SoftLimits) []Violation {
	var v []Violation
	absolute := true
	var pos [3]float64 // X, Y, Z
	for i, raw := range lines {
		line := stripGCodeComment(raw)
		if line == "" {
			continue
		}
		up := strings.ToUpper(line)
		gcodes := lineGCodes(up)
		if gcodeSetHas(gcodes, 90) {
			absolute = true
		}
		if gcodeSetHas(gcodes, 91) {
			absolute = false
		}
		isMove := gcodeSetHas(gcodes, 0, 1, 2, 3)
		x, hasX := gcodeWord(up, 'X')
		y, hasY := gcodeWord(up, 'Y')
		z, hasZ := gcodeWord(up, 'Z')
		if !isMove && !hasX && !hasY && !hasZ {
			continue
		}
		target := pos
		setAxis := func(idx int, val float64, has bool) {
			if !has {
				return
			}
			if absolute {
				target[idx] = val
			} else {
				target[idx] = pos[idx] + val
			}
		}
		setAxis(0, x, hasX)
		setAxis(1, y, hasY)
		setAxis(2, z, hasZ)
		if lim.Enabled {
			if reason := limitViolation(target, lim); reason != "" {
				v = append(v, Violation{Line: i + 1, Text: strings.TrimSpace(raw), Reason: reason})
			}
		}
		pos = target
	}
	return v
}

func limitViolation(p [3]float64, lim SoftLimits) string {
	if p[0] < lim.XMin || p[0] > lim.XMax {
		return fmt.Sprintf("X=%.3f outside [%.3f,%.3f]", p[0], lim.XMin, lim.XMax)
	}
	if p[1] < lim.YMin || p[1] > lim.YMax {
		return fmt.Sprintf("Y=%.3f outside [%.3f,%.3f]", p[1], lim.YMin, lim.YMax)
	}
	if p[2] < lim.ZMin || p[2] > lim.ZMax {
		return fmt.Sprintf("Z=%.3f outside [%.3f,%.3f]", p[2], lim.ZMin, lim.ZMax)
	}
	return ""
}

// IsMotionLine reports whether a G-code line commands axis motion (G0/G1/G2/G3
// or a bare axis-word move under a modal motion mode). Used by the ops layer to
// gate writes. G-codes are matched as whole words (G28/G20/G92 are NOT motion).
func IsMotionLine(line string) bool {
	up := strings.ToUpper(stripGCodeComment(line))
	if up == "" {
		return false
	}
	if gcodeSetHas(lineGCodes(up), 0, 1, 2, 3) {
		return true
	}
	_, hasX := gcodeWord(up, 'X')
	_, hasY := gcodeWord(up, 'Y')
	_, hasZ := gcodeWord(up, 'Z')
	return hasX || hasY || hasZ
}

// lineGCodes returns the integer G-code words on an (already upper-cased) line,
// e.g. "G90 G0 X1" → [90, 0]. Decimal modifiers (G38.2) keep the integer part.
func lineGCodes(up string) []int {
	var out []int
	for i := 0; i < len(up); i++ {
		if up[i] != 'G' {
			continue
		}
		j := i + 1
		for j < len(up) && up[j] >= '0' && up[j] <= '9' {
			j++
		}
		if j > i+1 {
			n := 0
			for k := i + 1; k < j; k++ {
				n = n*10 + int(up[k]-'0')
			}
			out = append(out, n)
		}
	}
	return out
}

func gcodeSetHas(codes []int, want ...int) bool {
	for _, c := range codes {
		for _, w := range want {
			if c == w {
				return true
			}
		}
	}
	return false
}

// gcodeWord extracts the numeric value of an axis/parameter word (e.g. 'X').
func gcodeWord(upperLine string, word byte) (float64, bool) {
	i := strings.IndexByte(upperLine, word)
	if i < 0 || i+1 >= len(upperLine) {
		return 0, false
	}
	j := i + 1
	for j < len(upperLine) {
		ch := upperLine[j]
		if (ch >= '0' && ch <= '9') || ch == '.' || ch == '-' || ch == '+' {
			j++
			continue
		}
		break
	}
	f, err := strconv.ParseFloat(upperLine[i+1:j], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// stripGCodeComment removes ; line comments and (paren) comments.
func stripGCodeComment(line string) string {
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	for {
		open := strings.IndexByte(line, '(')
		if open < 0 {
			break
		}
		closeIdx := strings.IndexByte(line[open:], ')')
		if closeIdx < 0 {
			line = line[:open]
			break
		}
		line = line[:open] + line[open+closeIdx+1:]
	}
	return strings.TrimSpace(line)
}

// ── Engine G-code session management ─────────────────────────────────────────

// GCodeSession is an open controller connection tracked by the Engine.
type GCodeSession struct {
	ID      string       `json:"id"`
	Device  string       `json:"device"`
	Dialect GCodeDialect `json:"dialect"`
	client  *GCodeClient
}

// GCodeStreamResult reports the outcome of streaming a program.
type GCodeStreamResult struct {
	Sent        int         `json:"sent"`
	Total       int         `json:"total"`
	DryRun      bool        `json:"dryRun"`
	Violations  []Violation `json:"violations,omitempty"`
	FailedLine  int         `json:"failedLine,omitempty"`
	FailedError string      `json:"failedError,omitempty"`
	OK          bool        `json:"ok"`
}

// OpenGCode opens a CNC/3D-printer controller on a serial device and tracks the
// session. Takes exclusive ownership of the bus (no concurrent Modbus on it).
func (e *Engine) OpenGCode(dev string, baud int, dialect GCodeDialect) (string, error) {
	port, err := openSerial(dev, baud)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	if e.gcodes == nil {
		e.gcodes = map[string]*GCodeSession{}
	}
	id := e.nextID("gcode")
	e.mu.Unlock()
	if err := e.claimExclusive(dev, id); err != nil {
		_ = port.Close()
		return "", err
	}
	sess := &GCodeSession{ID: id, Device: dev, Dialect: dialect, client: NewGCodeClient(port, dialect)}
	e.mu.Lock()
	e.gcodes[id] = sess
	e.mu.Unlock()
	return id, nil
}

func (e *Engine) gcodeSession(id string) (*GCodeSession, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.gcodes[id]
	return s, ok
}

// GCodeSend sends one line on a session.
func (e *Engine) GCodeSend(id, line string, timeout time.Duration) (GCodeReply, error) {
	s, ok := e.gcodeSession(id)
	if !ok {
		return GCodeReply{}, fmt.Errorf("unknown gcode session %s", id)
	}
	return s.client.Send(line, timeout)
}

// GCodeStatus queries live controller state.
func (e *Engine) GCodeStatus(id string, timeout time.Duration) (GCodeReply, error) {
	s, ok := e.gcodeSession(id)
	if !ok {
		return GCodeReply{}, fmt.Errorf("unknown gcode session %s", id)
	}
	return s.client.Status(timeout)
}

// GCodeEStop fires the realtime emergency stop. Never gated.
func (e *Engine) GCodeEStop(id string) error {
	s, ok := e.gcodeSession(id)
	if !ok {
		return fmt.Errorf("unknown gcode session %s", id)
	}
	return s.client.EStop()
}

// GCodeStream runs a program with ok-gated flow control. It always validates
// against soft limits first; if dryRun, it returns the validation and sends
// nothing. A non-empty violation set aborts before any transmission.
func (e *Engine) GCodeStream(id string, lines []string, lim SoftLimits, dryRun bool, timeout time.Duration) (GCodeStreamResult, error) {
	res := GCodeStreamResult{Total: len(lines), DryRun: dryRun}
	res.Violations = ValidateProgram(lines, lim)
	if len(res.Violations) > 0 {
		return res, fmt.Errorf("gcode: %d soft-limit violation(s); refusing to stream", len(res.Violations))
	}
	if dryRun {
		res.OK = true
		return res, nil
	}
	s, ok := e.gcodeSession(id)
	if !ok {
		return res, fmt.Errorf("unknown gcode session %s", id)
	}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		reply, err := s.client.Send(line, timeout)
		if err != nil || !reply.OK {
			res.FailedLine = i + 1
			if err != nil {
				res.FailedError = err.Error()
			} else {
				res.FailedError = reply.Error
			}
			return res, fmt.Errorf("gcode: stream aborted at line %d: %s", res.FailedLine, res.FailedError)
		}
		res.Sent++
	}
	res.OK = true
	return res, nil
}

// CloseGCode ends a session and releases the bus.
func (e *Engine) CloseGCode(id string) bool {
	e.mu.Lock()
	s, ok := e.gcodes[id]
	delete(e.gcodes, id)
	e.mu.Unlock()
	if !ok {
		return false
	}
	_ = s.client.Close()
	e.releaseExclusive(s.Device, id)
	return true
}

// GCodeSessions lists open G-code session ids.
func (e *Engine) GCodeSessions() []GCodeSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]GCodeSession, 0, len(e.gcodes))
	for _, s := range e.gcodes {
		out = append(out, GCodeSession{ID: s.ID, Device: s.Device, Dialect: s.Dialect})
	}
	return out
}
