package machine

// driver_registry.go — the Engine's registry of connected Drivers, plus a shared
// poll-based Subscribe helper so any read-capable driver gets a "watch" stream
// for free (OPC-UA can override with a native subscription later).

import (
	"context"
	"sort"
	"time"
)

// RegisterDriver adds (or replaces) a connected driver under id. Replacing an id
// closes the previous driver so its connection/goroutines are released.
func (e *Engine) RegisterDriver(id string, d Driver) {
	e.mu.Lock()
	prev := e.drivers[id]
	e.drivers[id] = d
	e.mu.Unlock()
	if prev != nil && prev != d {
		_ = prev.Close()
	}
}

// Driver returns the registered driver for id.
func (e *Engine) GetDriver(id string) (Driver, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.drivers[id]
	return d, ok
}

// RemoveDriver unregisters and closes a driver.
func (e *Engine) RemoveDriver(id string) bool {
	e.mu.Lock()
	d := e.drivers[id]
	delete(e.drivers, id)
	e.mu.Unlock()
	if d == nil {
		return false
	}
	_ = d.Close()
	return true
}

// DriverIDs lists registered driver ids, sorted.
func (e *Engine) DriverIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.drivers))
	for id := range e.drivers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// DriverStatuses returns a status snapshot for every registered driver. Each
// Status() call is bounded by perTimeout so one stuck machine can't hang the list.
func (e *Engine) DriverStatuses(ctx context.Context, perTimeout time.Duration) map[string]MachineStatus {
	e.mu.Lock()
	ids := make([]string, 0, len(e.drivers))
	ds := make([]Driver, 0, len(e.drivers))
	for id, d := range e.drivers {
		ids = append(ids, id)
		ds = append(ds, d)
	}
	e.mu.Unlock()
	out := map[string]MachineStatus{}
	for i, d := range ds {
		sctx, cancel := context.WithTimeout(ctx, perTimeout)
		st, err := d.Status(sctx)
		cancel()
		if err != nil {
			st = MachineStatus{
				Name: d.Name(), Kind: d.Kind(), Driver: d.Name(),
				Connected: false, State: "unknown",
				Detail: map[string]any{"error": err.Error()},
				Caps:   d.Capabilities().List(), TS: time.Now().UnixMilli(),
			}
		}
		out[ids[i]] = st
	}
	return out
}

// pollSubscribe emulates a subscription by reading refs every interval until the
// context is cancelled. Drivers with no native subscription (Modbus, vision, the
// robot adapter) return this from Subscribe(); it gives the "watch" surface one
// uniform stream regardless of transport.
func pollSubscribe(ctx context.Context, read func(context.Context) ([]Sample, error), opts SubOpts) <-chan Sample {
	interval := time.Duration(opts.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	ch := make(chan Sample, 16)
	go func() {
		defer close(ch)
		t := time.NewTicker(interval)
		defer t.Stop()
		emit := func() bool {
			rctx, cancel := context.WithTimeout(ctx, interval)
			samples, err := read(rctx)
			cancel()
			if err != nil {
				return ctx.Err() == nil // keep polling on transient read errors
			}
			for _, s := range samples {
				select {
				case ch <- s:
				case <-ctx.Done():
					return false
				}
			}
			return true
		}
		if !emit() { // emit once immediately, then on each tick
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !emit() {
					return
				}
			}
		}
	}()
	return ch
}
