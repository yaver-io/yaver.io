package cell

import (
	"context"

	"github.com/yaver-io/agent/arm"
)

// Outcome is the result of one arm action (a move or a vision check). It is the
// cell-layer's projection of arm.MoveResult so the Orchestrator stays decoupled
// from the arm package internals and is trivial to fake in tests.
type Outcome struct {
	OK          bool   `json:"ok"`
	Code        string `json:"code,omitempty"`
	Error       string `json:"error,omitempty"`
	Obstruction bool   `json:"obstruction,omitempty"`
}

// Presenter moves the arm and runs vision checks. The production implementation
// wraps *arm.Controller (see package main's armPresenter); tests fake it.
type Presenter interface {
	// MoveWaypoint moves to a taught waypoint (joints or pose), camera-verified
	// per the waypoint's own verify setting.
	MoveWaypoint(ctx context.Context, wp arm.Waypoint) Outcome
	// Verify runs a camera-only vision check against an expectation (no motion).
	Verify(ctx context.Context, expectation string) Outcome
	// ForceInsert seats a lead end with a guarded compliant move (push-in
	// termination): move along dir until |force| reaches limitN or maxDistMm.
	// OK means it seated. Backends without force report not-OK.
	ForceInsert(ctx context.Context, dir arm.Axis6, limitN, maxDistMm float64) Outcome
	// EStop latches the arm's safety stop (called on a rendezvous fault).
	EStop(ctx context.Context) error
}

// StationIO triggers a station and senses completion. Built per-station by an
// IOFactory from the Handshake; faked in tests.
type StationIO interface {
	// Trigger actuates the station (assert a coil, or no-op for manual/none).
	Trigger(ctx context.Context) error
	// Done reports whether the cycle has completed (polled by the Orchestrator
	// for modbus/vision handshakes; timeout/manual are handled by dwell).
	Done(ctx context.Context) (bool, error)
}

// FuncIO is the trivial StationIO built from a trigger func + a done func. The
// ops layer composes these from a Handshake (modbus coil write / register poll /
// vision check); a nil func means "no-op trigger" / "immediately done".
type FuncIO struct {
	TrigFn func(ctx context.Context) error
	DoneFn func(ctx context.Context) (bool, error)
}

func (f FuncIO) Trigger(ctx context.Context) error {
	if f.TrigFn == nil {
		return nil
	}
	return f.TrigFn(ctx)
}

func (f FuncIO) Done(ctx context.Context) (bool, error) {
	if f.DoneFn == nil {
		return true, nil
	}
	return f.DoneFn(ctx)
}

// IOFactory builds the StationIO for a station. It keeps the Orchestrator
// hardware-agnostic: the ops layer supplies modbus/vision-backed IO; tests supply
// fakes. A nil factory makes every station manual (no trigger, immediate done).
type IOFactory func(st Station) (StationIO, error)
