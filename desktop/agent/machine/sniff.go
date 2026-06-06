package machine

import (
	"io"
	"sort"
	"sync"
	"time"
)

// Sniffer accumulates raw bus bytes, extracts CRC-valid Modbus frames, and
// aggregates per-register observations. It is fed either from a live serial
// port (StartSniff) or by injecting captured bytes (StartManual + Feed), so the
// same classifier works for live hardware and for replaying/piping a capture.
type Sniffer struct {
	mu     sync.Mutex
	driver string
	buf    []byte
	obs    map[obsKey]*RegisterObs
	frames int

	dev     string // serial device (for arbitration + hotplug reopen); "" for manual
	reopens int    // count of hotplug reconnects, for status visibility
	src     io.ReadCloser
	stop    chan struct{}
	done    chan struct{}
}

type obsKey struct {
	unit byte
	fn   byte
	addr int
}

func newSniffer(driver string) *Sniffer {
	return &Sniffer{driver: driver, obs: map[obsKey]*RegisterObs{}}
}

// Feed pushes raw bus bytes through the frame extractor and updates aggregates.
func (s *Sniffer) Feed(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, b...)
	frames, tail := scanFrames(s.buf)
	s.buf = tail
	for _, f := range frames {
		s.frames++
		s.ingest(f)
	}
}

func (s *Sniffer) ingest(f Frame) {
	if f.Addr < 0 {
		return
	}
	// Reads/writes carrying values record each value at addr+index.
	if len(f.Values) > 0 {
		for k, v := range f.Values {
			val := v
			s.update(f.Unit, normFunc(f.Func), f.Addr+k, &val, f.IsWrite)
		}
		return
	}
	// A request with no values still tells us these addresses are polled.
	for k := 0; k < f.Count; k++ {
		s.update(f.Unit, normFunc(f.Func), f.Addr+k, nil, f.IsWrite)
	}
}

// normFunc folds read-request/response function codes together so a polled
// holding register and its response land on the same observation key.
func normFunc(fn byte) byte {
	if fn&0x80 != 0 {
		return fn &^ 0x80
	}
	return fn
}

func (s *Sniffer) update(unit, fn byte, addr int, val *uint16, written bool) {
	k := obsKey{unit, fn, addr}
	o := s.obs[k]
	if o == nil {
		o = &RegisterObs{Unit: unit, Func: fn, Addr: addr, seen: map[uint16]struct{}{}}
		s.obs[k] = o
	}
	o.Samples++
	if written {
		o.WrittenByMaster = true
	}
	if val != nil {
		v := *val
		if len(o.seen) == 0 {
			o.Min, o.Max, o.Last, o.Monotonic = v, v, v, true
		} else {
			if v < o.Min {
				o.Min = v
			}
			if v > o.Max {
				o.Max = v
			}
			if v != o.Last {
				o.Changes++
				if v < o.Last {
					o.Monotonic = false
				}
				o.Last = v
			}
		}
		o.seen[v] = struct{}{}
		o.Distinct = len(o.seen)
	}
}

// classify infers a RegisterKind + confidence from the value behaviour.
func classify(o *RegisterObs) {
	switch {
	case o.WrittenByMaster:
		o.Kind, o.Confidence = KindSetpoint, 0.9
	case o.Monotonic && o.Changes >= 3 && o.Distinct >= 3:
		o.Kind, o.Confidence = KindCounter, 0.8
	case o.Distinct >= 8 && o.Changes*3 >= o.Samples:
		o.Kind, o.Confidence = KindLive, 0.6
	case o.Distinct >= 2 && o.Distinct <= 4 && o.Changes >= 1:
		o.Kind, o.Confidence = KindSetpoint, 0.5
	case o.Distinct <= 1 && o.Samples > 0:
		// constant value that is polled but never changes → config/setpoint
		o.Kind, o.Confidence = KindSetpoint, 0.35
	default:
		o.Kind, o.Confidence = KindUnknown, 0.3
	}
}

// Schematic snapshots the current aggregate into a candidate register map.
func (s *Sniffer) Schematic(source string) Schematic {
	s.mu.Lock()
	defer s.mu.Unlock()
	regs := make([]RegisterObs, 0, len(s.obs))
	var confSum float64
	for _, o := range s.obs {
		classify(o)
		confSum += o.Confidence
		cp := *o
		cp.seen = nil
		regs = append(regs, cp)
	}
	sort.Slice(regs, func(i, j int) bool {
		if regs[i].Unit != regs[j].Unit {
			return regs[i].Unit < regs[j].Unit
		}
		if regs[i].Func != regs[j].Func {
			return regs[i].Func < regs[j].Func
		}
		return regs[i].Addr < regs[j].Addr
	})
	conf := 0.0
	if len(regs) > 0 {
		conf = confSum / float64(len(regs))
	}
	if source == "" {
		source = "sniff"
	}
	return Schematic{
		Driver:     s.driver,
		Source:     source,
		Registers:  regs,
		Frames:     s.frames,
		Confidence: conf,
	}
}

// ── Engine session management ──────────────────────────────────────────────

// StartSniff opens a serial port and passively reads it into a new session.
// Serial is Linux-only for now; returns ErrUnsupported elsewhere.
func (e *Engine) StartSniff(dev string, baud int) (string, error) {
	return e.StartSniffOpts(dev, baud, false)
}

// StartSniffOpts is StartSniff with a reconnect option. When reconnect is true
// the read loop reopens the device (by its by-id path if that's what was given)
// after a read error — surviving USB-serial replug / ttyUSB renumbering, which
// is the difference between a durable edge worker and one that dies on the first
// cable wiggle. The session takes exclusive ownership of the bus.
func (e *Engine) StartSniffOpts(dev string, baud int, reconnect bool) (string, error) {
	rc, err := openSerial(dev, baud)
	if err != nil {
		return "", err
	}
	sn := newSniffer("modbus_rtu")
	sn.dev = dev
	sn.src = rc
	sn.stop = make(chan struct{})
	sn.done = make(chan struct{})

	e.mu.Lock()
	id := e.nextID("sniff")
	e.mu.Unlock()
	if err := e.claimExclusive(dev, id); err != nil {
		_ = rc.Close()
		return "", err
	}
	e.mu.Lock()
	e.sniffers[id] = sn
	e.mu.Unlock()

	go func() {
		defer close(sn.done)
		buf := make([]byte, 1024)
		cur := rc
		for {
			select {
			case <-sn.stop:
				return
			default:
			}
			n, rerr := cur.Read(buf)
			if n > 0 {
				sn.Feed(buf[:n])
			}
			if rerr != nil {
				if !reconnect {
					return
				}
				_ = cur.Close()
				// Back off, then try to reopen the (resolved) device until stop.
				if !sleepOrStop(sn.stop, 500*time.Millisecond) {
					return
				}
				nc, oerr := openSerial(dev, baud)
				if oerr != nil {
					if !sleepOrStop(sn.stop, 1500*time.Millisecond) {
						return
					}
					continue
				}
				cur = nc
				sn.mu.Lock()
				sn.src = nc
				sn.reopens++
				sn.mu.Unlock()
			}
		}
	}()
	return id, nil
}

// sleepOrStop waits d or returns false if the stop channel fires first.
func sleepOrStop(stop <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-stop:
		return false
	case <-t.C:
		return true
	}
}

// StartManual opens a session with no live source — bytes are injected via
// FeedSession. Used to pipe a capture (or test) with no hardware. Works on any
// platform.
func (e *Engine) StartManual(driver string) string {
	if driver == "" {
		driver = "modbus_rtu"
	}
	sn := newSniffer(driver)
	e.mu.Lock()
	id := e.nextID("sniff")
	e.sniffers[id] = sn
	e.mu.Unlock()
	return id
}

// FeedSession injects raw bytes into a manual session.
func (e *Engine) FeedSession(id string, b []byte) error {
	e.mu.Lock()
	sn := e.sniffers[id]
	e.mu.Unlock()
	if sn == nil {
		return ErrUnsupported
	}
	sn.Feed(b)
	return nil
}

// SchematicOf returns the current schematic for a live session without stopping.
func (e *Engine) SchematicOf(id, source string) (Schematic, bool) {
	e.mu.Lock()
	sn := e.sniffers[id]
	e.mu.Unlock()
	if sn == nil {
		return Schematic{}, false
	}
	return sn.Schematic(source), true
}

// StopSniff ends a session and returns its final schematic.
func (e *Engine) StopSniff(id, source string) (Schematic, bool) {
	e.mu.Lock()
	sn := e.sniffers[id]
	delete(e.sniffers, id)
	e.mu.Unlock()
	if sn == nil {
		return Schematic{}, false
	}
	if sn.stop != nil {
		close(sn.stop)
		select {
		case <-sn.done:
		case <-time.After(2 * time.Second):
		}
	}
	if sn.src != nil {
		_ = sn.src.Close()
	}
	if sn.dev != "" {
		e.releaseExclusive(sn.dev, id)
	}
	return sn.Schematic(source), true
}

// Sessions lists active sniff session ids.
func (e *Engine) Sessions() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.sniffers))
	for id := range e.sniffers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Scan actively reads a contiguous register range over Modbus-TCP and returns a
// one-shot schematic (current values; read-only). Use for presence/verify; the
// classifier needs time-series from a sniff for kind inference.
func (e *Engine) ScanTCP(addr string, unit byte, fc byte, start, count int, timeout time.Duration) (Schematic, error) {
	cl, err := DialTCP(addr, unit, timeout)
	if err != nil {
		return Schematic{}, err
	}
	defer cl.Close()
	if fc == 0 {
		fc = 3
	}
	vals, err := cl.ReadRegisters(fc, start, count, timeout)
	if err != nil {
		return Schematic{}, err
	}
	regs := make([]RegisterObs, 0, len(vals))
	for i, v := range vals {
		regs = append(regs, RegisterObs{
			Unit: unit, Func: fc, Addr: start + i,
			Samples: 1, Distinct: 1, Min: v, Max: v, Last: v,
			Kind: KindUnknown, Confidence: 0.2,
		})
	}
	return Schematic{Driver: "modbus_tcp", Source: "scan", Registers: regs, Frames: 0, Confidence: 0.2}, nil
}

// ReadTCP reads registers over Modbus-TCP (for machine_read / verify).
func (e *Engine) ReadTCP(addr string, unit byte, fc byte, start, count int, timeout time.Duration) ([]uint16, error) {
	cl, err := DialTCP(addr, unit, timeout)
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	if fc == 0 {
		fc = 3
	}
	return cl.ReadRegisters(fc, start, count, timeout)
}

// WriteTCP writes one holding register over Modbus-TCP then reads it back to
// verify. Caller (ops layer) is responsible for range-clamping + approval.
func (e *Engine) WriteTCP(addr string, unit byte, reg int, val uint16, timeout time.Duration) (uint16, error) {
	cl, err := DialTCP(addr, unit, timeout)
	if err != nil {
		return 0, err
	}
	defer cl.Close()
	if err := cl.WriteSingleRegister(reg, val, timeout); err != nil {
		return 0, err
	}
	rb, err := cl.ReadRegisters(3, reg, 1, timeout)
	if err != nil {
		return 0, err
	}
	if len(rb) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return rb[0], nil
}
