package machine

// driver_modbus.go — the Modbus-TCP Driver: the first concrete machine wrapper,
// reusing the existing TCPClient. Caps: status, read, subscribe (polled), write,
// program, job. The tag map comes from a learned Machine Operating Manual (the
// schematic from a sniff + machine_understand) or is supplied at connect time.
//
// Concurrency: a single TCPClient is not safe for concurrent transactions
// (shared conn + txid), so every operation holds d.mu and reuses one connection,
// reconnecting on error.

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// ModbusConfig configures a ModbusDriver.
type ModbusConfig struct {
	Name       string        // human label / machineKey, e.g. "yh8030h-01"
	Kind       string        // "cut_strip" | "crimp" | "press" | ... (free-form)
	Addr       string        // host:port of the Modbus-TCP slave (e.g. 10.0.0.50:502)
	Unit       byte          // default unit/slave id (default 1)
	Timeout    time.Duration // per-transaction timeout (default 8s)
	Tags       []Tag         // the addressable surface (from the Machine Operating Manual)
	StatusTag  string        // optional tag name whose nonzero raw value ⇒ "running"
	ProgramTag string        // optional tag name to write for Recall(program)
}

// ModbusDriver wraps one Modbus-TCP machine behind the Driver interface.
type ModbusDriver struct {
	cfg    ModbusConfig
	byName map[string]Tag
	mu     sync.Mutex
	cl     *TCPClient
}

// NewModbusDriver builds a driver from config. It does not connect yet.
func NewModbusDriver(cfg ModbusConfig) *ModbusDriver {
	if cfg.Unit == 0 {
		cfg.Unit = 1
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 8 * time.Second
	}
	if cfg.Kind == "" {
		cfg.Kind = "modbus"
	}
	byName := make(map[string]Tag, len(cfg.Tags))
	for _, t := range cfg.Tags {
		if t.Name != "" {
			byName[t.Name] = t
		}
	}
	return &ModbusDriver{cfg: cfg, byName: byName}
}

func (d *ModbusDriver) Name() string { return d.cfg.Name }
func (d *ModbusDriver) Kind() string { return d.cfg.Kind }

func (d *ModbusDriver) Capabilities() CapSet {
	return Caps(CapStatus, CapRead, CapSubscribe, CapWrite, CapProgram, CapJob)
}

// conn returns a live client, dialing (or redialing) under d.mu held by caller.
func (d *ModbusDriver) conn() (*TCPClient, error) {
	if d.cl != nil {
		return d.cl, nil
	}
	cl, err := DialTCP(d.cfg.Addr, d.cfg.Unit, d.cfg.Timeout)
	if err != nil {
		return nil, err
	}
	d.cl = cl
	return cl, nil
}

func (d *ModbusDriver) drop() {
	if d.cl != nil {
		_ = d.cl.Close()
		d.cl = nil
	}
}

func (d *ModbusDriver) Connect(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.conn()
	return err
}

func (d *ModbusDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.drop()
	return nil
}

// resolveTag turns a TagRef into a Tag, by name (from the manual) or by explicit
// address. Func defaults to 3 (holding), Unit to the driver default.
func (d *ModbusDriver) resolveTag(ref TagRef) Tag {
	if ref.Name != "" {
		if t, ok := d.byName[ref.Name]; ok {
			return t
		}
	}
	t := Tag{Name: ref.Name, Addr: ref.Addr, Func: ref.Func, Unit: ref.Unit}
	if t.Func == 0 {
		t.Func = 3
	}
	return t
}

func (d *ModbusDriver) tagFC(t Tag) byte {
	if t.Func == 4 {
		return 4
	}
	return 3
}

func (d *ModbusDriver) tagUnit(t Tag) byte {
	if t.Unit > 0 {
		return byte(t.Unit)
	}
	return d.cfg.Unit
}

// Browse returns the machine's addressable surface — its tag map from the
// Machine Operating Manual supplied at connect time.
func (d *ModbusDriver) Browse(ctx context.Context) ([]Tag, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Tag, len(d.cfg.Tags))
	copy(out, d.cfg.Tags)
	return out, nil
}

// readTag reads one tag's current value (caller holds d.mu).
func (d *ModbusDriver) readTag(t Tag) (Sample, error) {
	cl, err := d.conn()
	if err != nil {
		return Sample{}, err
	}
	vals, err := cl.ReadRegisters(d.tagFC(t), t.Addr, 1, d.cfg.Timeout)
	if err != nil {
		d.drop() // force redial next time on any wire error
		return Sample{}, err
	}
	if len(vals) == 0 {
		return Sample{}, fmt.Errorf("modbus: empty read at %d", t.Addr)
	}
	raw := vals[0]
	return Sample{
		Tag: t.Name, Addr: t.Addr, Func: int(d.tagFC(t)), Unit: int(d.tagUnit(t)),
		Raw: raw, Value: float64(raw) * t.scaleOr(), Unit2: t.Unit2,
		TS: time.Now().UnixMilli(),
	}, nil
}

// Read resolves and reads the given refs. With no refs it reads every named tag
// in the manual (the "current parameter set").
func (d *ModbusDriver) Read(ctx context.Context, refs []TagRef) ([]Sample, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	tags := make([]Tag, 0, len(refs))
	if len(refs) == 0 {
		tags = append(tags, d.cfg.Tags...)
	} else {
		for _, r := range refs {
			tags = append(tags, d.resolveTag(r))
		}
	}
	out := make([]Sample, 0, len(tags))
	var firstErr error
	for _, t := range tags {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		s, err := d.readTag(t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// Subscribe emulates a subscription by polling Read (Modbus has no native push).
func (d *ModbusDriver) Subscribe(ctx context.Context, refs []TagRef, opts SubOpts) (<-chan Sample, error) {
	return pollSubscribe(ctx, func(c context.Context) ([]Sample, error) {
		return d.Read(c, refs)
	}, opts), nil
}

// Write applies gated writes: engineering Value → raw via inverse scale (or Raw
// override), bounds-checked against the tag's safe range, then read-back verified.
// The ops layer performs the primary approval/range gate; this is defense in depth.
func (d *ModbusDriver) Write(ctx context.Context, ws []TagWrite) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, w := range ws {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t := d.resolveTag(w.Ref)
		var raw uint16
		if w.Raw != nil {
			raw = *w.Raw
		} else {
			if t.Min != nil && w.Value < *t.Min {
				return fmt.Errorf("machine: %s value %.3f below safe min %.3f", t.Name, w.Value, *t.Min)
			}
			if t.Max != nil && w.Value > *t.Max {
				return fmt.Errorf("machine: %s value %.3f above safe max %.3f", t.Name, w.Value, *t.Max)
			}
			r := math.Round(w.Value / t.scaleOr())
			if r < 0 || r > 0xFFFF {
				return fmt.Errorf("machine: %s scaled value %.0f out of uint16 range", t.Name, r)
			}
			raw = uint16(r)
		}
		cl, err := d.conn()
		if err != nil {
			return err
		}
		if err := cl.WriteSingleRegister(t.Addr, raw, d.cfg.Timeout); err != nil {
			d.drop()
			return fmt.Errorf("machine: write %s: %w", t.Name, err)
		}
		rb, err := cl.ReadRegisters(3, t.Addr, 1, d.cfg.Timeout)
		if err != nil {
			d.drop()
			return fmt.Errorf("machine: read-back %s: %w", t.Name, err)
		}
		if len(rb) == 0 || rb[0] != raw {
			return fmt.Errorf("machine: %s write not verified (wrote %d, read %v)", t.Name, raw, rb)
		}
	}
	return nil
}

// Recall writes a program-slot number to the configured ProgramTag, if any.
func (d *ModbusDriver) Recall(ctx context.Context, program string) error {
	if d.cfg.ProgramTag == "" {
		return ErrNotSupported
	}
	var slot float64
	if _, err := fmt.Sscanf(program, "%g", &slot); err != nil {
		return fmt.Errorf("machine: recall program must be numeric for Modbus: %q", program)
	}
	return d.Write(ctx, []TagWrite{{Ref: TagRef{Name: d.cfg.ProgramTag}, Value: slot}})
}

// SubmitJob writes each job param to the matching named tag (a recipe push).
func (d *ModbusDriver) SubmitJob(ctx context.Context, job Job) error {
	ws := make([]TagWrite, 0, len(job.Params))
	for name, v := range job.Params {
		ws = append(ws, TagWrite{Ref: TagRef{Name: name}, Value: v})
	}
	if job.Program != "" && d.cfg.ProgramTag != "" {
		if err := d.Recall(ctx, job.Program); err != nil {
			return err
		}
	}
	if len(ws) == 0 {
		return nil
	}
	return d.Write(ctx, ws)
}

// Status reports connection + derived run state (from the optional StatusTag).
func (d *ModbusDriver) Status(ctx context.Context) (MachineStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := MachineStatus{
		Name: d.cfg.Name, Kind: d.cfg.Kind, Driver: "modbus_tcp",
		Caps: d.Capabilities().List(), State: "unknown", TS: time.Now().UnixMilli(),
	}
	cl, err := d.conn()
	if err != nil {
		st.Connected = false
		st.State = "off"
		st.Detail = map[string]any{"error": err.Error()}
		return st, nil
	}
	st.Connected = true
	st.State = "idle"
	if d.cfg.StatusTag != "" {
		if t, ok := d.byName[d.cfg.StatusTag]; ok {
			if vals, e := cl.ReadRegisters(d.tagFC(t), t.Addr, 1, d.cfg.Timeout); e == nil && len(vals) > 0 {
				if vals[0] != 0 {
					st.State = "running"
				}
				st.Detail = map[string]any{d.cfg.StatusTag: vals[0]}
			}
		}
	}
	return st, nil
}
