package cell

import (
	"context"
	"fmt"
	"time"
)

// Phase names the rendezvous state machine states (design §6).
type Phase string

const (
	PhaseApproach  Phase = "approach"
	PhasePresent   Phase = "present"
	PhaseSettle    Phase = "settle"
	PhaseTrigger   Phase = "trigger"
	PhaseActuating Phase = "actuating"
	PhaseVerify    Phase = "verify"
	PhaseWithdraw  Phase = "withdraw"
)

// StationResult reports serving one station for one lead end.
type StationResult struct {
	StationID string      `json:"stationId"`
	Kind      StationKind `json:"kind"`
	OK        bool        `json:"ok"`
	Phase     Phase       `json:"phase"`          // phase reached (where it stopped on failure)
	Code      string      `json:"code,omitempty"` // present_failed | present_verify_failed | trigger_failed | done_timeout | verify_failed | obstruction | estop | ...
	Error     string      `json:"error,omitempty"`
	TookMs    int64       `json:"tookMs"`
}

// Orchestrator drives the arm + stations through the deterministic rendezvous
// state machine. It owns pinch-safety: no trigger until PRESENT is verified, no
// withdraw until DONE (or e-stop). The LLM is never in this loop.
type Orchestrator struct {
	Arm Presenter
	IO  IOFactory
}

// NewOrchestrator builds an orchestrator. A nil IOFactory makes every station
// manual (no electrical trigger, immediate done) — useful for bring-up.
func NewOrchestrator(p Presenter, io IOFactory) *Orchestrator {
	if io == nil {
		io = func(Station) (StationIO, error) { return FuncIO{}, nil }
	}
	return &Orchestrator{Arm: p, IO: io}
}

// ServeStation runs one station's full APPROACH→PRESENT→SETTLE→TRIGGER→
// ACTUATING→VERIFY→WITHDRAW cycle for the lead end the arm is currently holding.
func (o *Orchestrator) ServeStation(ctx context.Context, st Station) StationResult {
	start := time.Now()
	res := StationResult{StationID: st.ID, Kind: st.Kind, Phase: PhaseApproach}
	fail := func(phase Phase, code, msg string) StationResult {
		res.Phase, res.Code, res.Error, res.OK = phase, code, msg, false
		res.TookMs = time.Since(start).Milliseconds()
		return res
	}

	// APPROACH — optional clear pre-pose.
	if st.Approach != nil {
		if out := o.Arm.MoveWaypoint(ctx, *st.Approach); !out.OK {
			if out.Obstruction {
				return fail(PhaseApproach, "obstruction", "obstruction on approach — e-stop latched")
			}
			return fail(PhaseApproach, "approach_failed", out.Error)
		}
	}

	// PRESENT — move to the taught present pose(s).
	res.Phase = PhasePresent
	if len(st.Present) == 0 {
		return fail(PhasePresent, "no_present_pose", "station has no taught present pose")
	}
	for _, wp := range st.Present {
		out := o.Arm.MoveWaypoint(ctx, wp)
		if out.Obstruction {
			return fail(PhasePresent, "obstruction", "obstruction during present — e-stop latched")
		}
		if !out.OK {
			return fail(PhasePresent, "present_failed", out.Error)
		}
	}

	// SETTLE + present verify. INVARIANT: do NOT trigger until the lead end is
	// confirmed in the jaw mouth (vision), forgiven by the station's lead-in funnel.
	res.Phase = PhaseSettle
	if st.Handshake.DwellMs > 0 {
		if err := sleep(ctx, st.Handshake.DwellMs); err != nil {
			return fail(PhaseSettle, "ctx", err.Error())
		}
	}
	if st.PresentExpect != "" {
		out := o.Arm.Verify(ctx, st.PresentExpect)
		if out.Obstruction {
			return fail(PhaseSettle, "obstruction", "obstruction at present — e-stop latched")
		}
		if !out.OK {
			return fail(PhaseSettle, "present_verify_failed", nonEmpty(out.Error, "present pose did not verify"))
		}
	}

	io, err := o.IO(st)
	if err != nil {
		return fail(PhaseTrigger, "io_unavailable", err.Error())
	}

	// TRIGGER — only reached after PRESENT verified.
	res.Phase = PhaseTrigger
	if err := io.Trigger(ctx); err != nil {
		return fail(PhaseTrigger, "trigger_failed", err.Error())
	}

	// ACTUATING — wait for DONE within timeout. INVARIANT: never withdraw while
	// the press may still actuate; a timeout latches e-stop instead of moving.
	res.Phase = PhaseActuating
	if err := o.waitDone(ctx, st, io); err != nil {
		_ = o.Arm.EStop(ctx)
		return fail(PhaseActuating, "done_timeout", err.Error())
	}

	// VERIFY — vision check after the cycle.
	res.Phase = PhaseVerify
	if st.VerifyExpect != "" {
		out := o.Arm.Verify(ctx, st.VerifyExpect)
		if out.Obstruction {
			_ = o.Arm.EStop(ctx)
			return fail(PhaseVerify, "obstruction", "obstruction at verify — e-stop latched")
		}
		if !out.OK {
			return fail(PhaseVerify, "verify_failed", nonEmpty(out.Error, "result did not verify"))
		}
	}

	// WITHDRAW — retract clear of the jaws.
	res.Phase = PhaseWithdraw
	if st.Withdraw != nil {
		if out := o.Arm.MoveWaypoint(ctx, *st.Withdraw); !out.OK {
			return fail(PhaseWithdraw, "withdraw_failed", out.Error)
		}
	}

	res.OK = true
	res.Phase = PhaseWithdraw
	res.TookMs = time.Since(start).Milliseconds()
	return res
}

// waitDone blocks until the station signals completion. Timeout/manual handshakes
// are time-based (no polling); modbus/vision are polled with a timeout.
func (o *Orchestrator) waitDone(ctx context.Context, st Station, io StationIO) error {
	h := st.Handshake
	switch h.Done {
	case DoneTimeout, DoneManual, "":
		ms := h.DwellMs
		if ms <= 0 {
			ms = h.TimeoutMs
		}
		if ms <= 0 {
			return nil // deterministic instant cycle
		}
		return sleep(ctx, ms)
	default: // DoneModbus / DoneVision — poll io.Done with a timeout.
		timeout := h.TimeoutMs
		if timeout <= 0 {
			timeout = 30000
		}
		poll := h.PollMs
		if poll <= 0 {
			poll = 250
		}
		deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
		for {
			done, err := io.Done(ctx)
			if err != nil {
				return fmt.Errorf("station %q done-sense error: %w", st.ID, err)
			}
			if done {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("station %q did not signal done within %dms", st.ID, timeout)
			}
			if err := sleep(ctx, poll); err != nil {
				return err
			}
		}
	}
}

func sleep(ctx context.Context, ms int) error {
	if ms <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(ms) * time.Millisecond):
		return nil
	}
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
