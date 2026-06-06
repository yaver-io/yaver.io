package machine

// driver.go — the universal machine-wrapper seam ("Yaver for machines").
//
// Driver is the generalization of robot.Backend to heterogeneous shop-floor
// machines: a Modbus crimp press, an OPC-UA cut-strip line, an S7 PLC, an MQTT
// telemetry feed, or — the moat — a screen-only legacy machine wrapped by
// camera+VLM. One seam, three product verbs: VIEW (Browse), WATCH (Read/
// Subscribe/Status), CONTROL (Write/Recall/SubmitJob — gated at the ops layer).
//
// Implementations declare what they can do via Capabilities(); callers (ops
// verbs, the AI supervisor, the UI) reason over the capability set, never a
// fixed machine "type". A dumb crimp press is {status,vision}; a YH-8030H is
// {status,read,write,program,vision}; a Komax line is {status,read,subscribe,
// job,curve}; a robot cell is surfaced read-only as {status,read,estop}.
//
// See docs/yaver-for-machines-design.md §1. This file is LLM-free and (for the
// interface itself) dependency-free; concrete drivers live in driver_*.go.
//
// BUILT (2026-06-06): this interface + CapSet; ModbusDriver (driver_modbus.go);
// the Engine driver registry (driver_registry.go); TagsFromSchematic (manual.go);
// the robotDriverAdapter + visionDriver (package main); ops verbs machine_connect
// /list/state/browse/read_tags/write_tags/recall/submit_job/disconnect. All
// tested (driver_test.go + main package tests). NEXT: OPC-UA / MQTT-Sparkplug /
// S7 / digital-IO drivers; the web/mobile OEE "watch" wall; native subscriptions.

import (
	"context"
	"errors"
	"sort"
)

// ErrNotSupported is returned by a Driver method whose capability the driver
// does not advertise (e.g. Write on a read-only vision driver). Callers should
// check Capabilities() first; this is the defensive backstop.
var ErrNotSupported = errors.New("machine: capability not supported by this driver")

// Capability is one thing a Driver can do. The set a driver advertises is its
// contract; the ops layer gates writes on the relevant cap being present.
type Capability string

const (
	CapStatus    Capability = "status"    // derived run/idle/fault — even a CT-clamp-only machine has this
	CapRead      Capability = "read"      // point/register read
	CapSubscribe Capability = "subscribe" // native (OPC-UA) or polled emulation
	CapWrite     Capability = "write"     // direct tag/register write — gated
	CapProgram   Capability = "program"   // recall a machine-stored program by number/name
	CapJob       Capability = "job"       // download a job/recipe (WPCS/OPC 40570/CSV)
	CapVision    Capability = "vision"    // VLM-of-HMI read path (the moat)
	CapCurve     Capability = "curve"     // per-crimp force curve / measurement stream
	CapEStop     Capability = "estop"     // commandable safe stop
)

// CapSet is a small set of capabilities with convenience helpers. It marshals as
// a JSON object {"read":true,...}; use List() for a sorted slice in API output.
type CapSet map[Capability]bool

// Caps builds a CapSet from a list of capabilities.
func Caps(ks ...Capability) CapSet {
	c := CapSet{}
	for _, k := range ks {
		c[k] = true
	}
	return c
}

// Has reports whether the capability is present.
func (c CapSet) Has(k Capability) bool { return c[k] }

// List returns the present capabilities, sorted, as a stable slice for output.
func (c CapSet) List() []Capability {
	out := make([]Capability, 0, len(c))
	for k, ok := range c {
		if ok {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Tag is one addressable point on a machine — a Modbus register, an OPC-UA node,
// or a learned HMI field. The map of tags is the machine's addressable surface
// (the "view"), typically loaded from a learned Machine Operating Manual
// (Talos machineManuals) or a driver's own Browse().
type Tag struct {
	Name     string   `json:"name"`
	Addr     int      `json:"addr"`
	Func     int      `json:"func,omitempty"`  // Modbus function code (3=holding,4=input)
	Unit     int      `json:"unit,omitempty"`  // Modbus unit/slave id
	Node     string   `json:"node,omitempty"`  // OPC-UA node id (protocol-specific addressing)
	Kind     string   `json:"kind,omitempty"`  // setpoint|live|counter|alarm|unknown
	Unit2    string   `json:"unit2,omitempty"` // engineering unit, e.g. "mm"
	Scale    float64  `json:"scale,omitempty"` // raw * scale = engineering value (0 → treated as 1)
	Min      *float64 `json:"min,omitempty"`   // engineering-value safe range (advisory; ops re-checks)
	Max      *float64 `json:"max,omitempty"`
	Writable bool     `json:"writable,omitempty"`
}

// scaleOr returns the tag's scale, defaulting 0 → 1 so an unscaled register
// passes its raw value through unchanged.
func (t Tag) scaleOr() float64 {
	if t.Scale == 0 {
		return 1
	}
	return t.Scale
}

// TagRef selects a tag to read/write — by name (resolved against the driver's
// tag map) or by explicit address.
type TagRef struct {
	Name string `json:"name,omitempty"`
	Addr int    `json:"addr,omitempty"`
	Func int    `json:"func,omitempty"`
	Unit int    `json:"unit,omitempty"`
	Node string `json:"node,omitempty"`
}

// Sample is one read value, both raw and scaled to engineering units.
type Sample struct {
	Tag   string  `json:"tag,omitempty"`
	Addr  int     `json:"addr"`
	Func  int     `json:"func,omitempty"`
	Unit  int     `json:"unit,omitempty"`
	Raw   uint16  `json:"raw"`
	Value float64 `json:"value"`
	Unit2 string  `json:"unit2,omitempty"`
	TS    int64   `json:"ts"`
}

// TagWrite is a single gated write. Value is in engineering units (the driver
// applies the inverse scale); set Raw to override and write a raw register word.
type TagWrite struct {
	Ref   TagRef  `json:"ref"`
	Value float64 `json:"value"`
	Raw   *uint16 `json:"raw,omitempty"`
}

// MachineStatus is the no-control snapshot for the "watch" wall.
type MachineStatus struct {
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Driver    string         `json:"driver"`
	Connected bool           `json:"connected"`
	State     string         `json:"state"` // running|idle|fault|setup|off|unknown
	Detail    map[string]any `json:"detail,omitempty"`
	Caps      []Capability   `json:"caps"`
	TS        int64          `json:"ts"`
}

// SubOpts configures a subscription. For drivers without native subscriptions the
// driver emulates one by polling at IntervalMs (default 1000).
type SubOpts struct {
	IntervalMs int `json:"intervalMs,omitempty"`
}

// Job is a recipe/cutting-list download (CapJob). Params are engineering values
// keyed by tag name; Raw carries protocol-specific extras (e.g. OPC 40570).
type Job struct {
	Program string             `json:"program,omitempty"`
	Params  map[string]float64 `json:"params,omitempty"`
	Raw     map[string]any     `json:"raw,omitempty"`
}

// Driver wraps ONE machine behind a uniform view/watch/control surface. Methods
// whose capability is absent must return ErrNotSupported.
type Driver interface {
	Name() string
	Kind() string
	Capabilities() CapSet

	Connect(ctx context.Context) error
	Close() error
	Status(ctx context.Context) (MachineStatus, error)

	// VIEW
	Browse(ctx context.Context) ([]Tag, error)

	// WATCH
	Read(ctx context.Context, refs []TagRef) ([]Sample, error)
	Subscribe(ctx context.Context, refs []TagRef, opts SubOpts) (<-chan Sample, error)

	// CONTROL (gated at the ops layer)
	Write(ctx context.Context, w []TagWrite) error
	Recall(ctx context.Context, program string) error
	SubmitJob(ctx context.Context, job Job) error
}
