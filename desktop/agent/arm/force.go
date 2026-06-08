package arm

import (
	"context"
	"fmt"
	"strings"
)

// force.go — the OPTIONAL force/contact capability for arms with a wrist
// force/torque sensor (or a joint-torque estimate). This is the contact phase
// the wire-harness cell needs: insertion (stop on a backstop), seat-to-cage, and
// pull-test (retract + record retention force). See
// docs/yaver-arm-served-harness-cell.md §13 — this closes the "arm.Backend has no
// Wrench/ForceMove" gap.
//
// It is ADDITIVE: ForceBackend embeds Backend, and the Controller type-asserts
// for it. Backends without force (myCobot / generic / bridge) keep compiling
// unchanged and simply report ErrNoForce. The deterministic, bounds-checked
// guards live in the Controller; the LLM never calls ForceMove directly.

// Wrench is a 6-axis force/torque reading at the TCP — forces in newtons,
// torques in newton-metres, in the arm's pose frame.
type Wrench struct {
	Fx float64 `json:"fx"`
	Fy float64 `json:"fy"`
	Fz float64 `json:"fz"`
	Tx float64 `json:"tx"`
	Ty float64 `json:"ty"`
	Tz float64 `json:"tz"`
}

// Axis6 names a translational TCP axis for a guarded compliant move. A leading
// "-" reverses the direction ("-z" = retract along -Z, used for pull-test).
// Rotational force moves are intentionally unsupported here (refused, not faked).
type Axis6 string

// ForceResult reports a guarded compliant move (insert / seat / pull).
type ForceResult struct {
	OK         bool    `json:"ok"`
	Kind       string  `json:"kind,omitempty"`  // "force_move"
	Seated     bool    `json:"seated"`          // |force| reached the limit within maxDist (contact made)
	PeakForceN float64 `json:"peakForceN"`      // max |force| observed along the axis
	TravelMm   float64 `json:"travelMm"`        // distance travelled before stop
	Wrench     *Wrench `json:"wrench,omitempty"`
	Pose       *Pose   `json:"pose,omitempty"`
	Code       string  `json:"code,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// ForceBackend is implemented by arms that expose force/torque. Optional — the
// Controller checks for it at runtime.
type ForceBackend interface {
	Backend

	// Wrench returns the current TCP force/torque; ErrNoForce if no sensor.
	Wrench(ctx context.Context) (Wrench, error)

	// ForceMove moves along TCP axis dir until |force on that axis| reaches
	// limitN OR maxDistMm is travelled, whichever comes first. Deterministic and
	// bounded by the caller (the Controller validates the limits first).
	ForceMove(ctx context.Context, dir Axis6, limitN, maxDistMm float64, velPct int) (ForceResult, error)
}

// ErrNoForce is returned by Wrench/ForceMove on backends without force support.
var ErrNoForce = errConst("this arm has no force/torque (wrist F/T) support")

// Conservative deterministic caps for a guarded contact move. Out-of-range is
// REFUSED, never clamped — a surprise high-force shove is worse than a refusal
// (same posture as checkLimits for joint targets).
const (
	maxForceLimitN  = 100.0 // N — above this, refuse (cobot contact safety)
	maxForceTravel  = 300.0 // mm — refuse a runaway compliant move longer than this
	minForceLimitN  = 0.5   // N
)

// ParseAxis6 splits a direction like "z" / "-z" into (axisIndex 0..2, sign).
func ParseAxis6(dir Axis6) (axis int, sign float64, err error) {
	s := strings.TrimSpace(strings.ToLower(string(dir)))
	sign = 1
	if strings.HasPrefix(s, "-") {
		sign, s = -1, s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	switch s {
	case "x":
		return 0, sign, nil
	case "y":
		return 1, sign, nil
	case "z":
		return 2, sign, nil
	}
	return 0, 0, fmt.Errorf("force-move supports translational axes x/y/z (got %q); rotational not supported", dir)
}

// Wrench returns the current TCP force/torque, or ErrNoForce.
func (c *Controller) Wrench(ctx context.Context) (Wrench, error) {
	fb, ok := c.Backend.(ForceBackend)
	if !ok {
		return Wrench{}, ErrNoForce
	}
	return fb.Wrench(ctx)
}

// ForceMove runs a guarded compliant move along a TCP axis: stop on contact
// (|force| ≥ limitN) or after maxDistMm. Used for insert (seat to a backstop),
// seat-to-cage, and pull-test (dir "-z", record PeakForceN as retention). All
// limits are validated deterministically; e-stop refuses; obstruction is not in
// play here because the move is force-terminated.
func (c *Controller) ForceMove(ctx context.Context, dir Axis6, limitN, maxDistMm float64, velPct int) ForceResult {
	if c.isEStopped() {
		return ForceResult{Kind: "force_move", Code: "estopped", Error: "e-stopped; call reset"}
	}
	fb, ok := c.Backend.(ForceBackend)
	if !ok {
		return ForceResult{Kind: "force_move", Code: "no_force", Error: ErrNoForce.Error()}
	}
	if _, _, err := ParseAxis6(dir); err != nil {
		return ForceResult{Kind: "force_move", Code: "bad_payload", Error: err.Error()}
	}
	if limitN < minForceLimitN || limitN > maxForceLimitN {
		return ForceResult{Kind: "force_move", Code: "out_of_range", Error: fmt.Sprintf("forceLimitN=%.2f out of range [%.1f,%.1f]", limitN, minForceLimitN, maxForceLimitN)}
	}
	if maxDistMm <= 0 || maxDistMm > maxForceTravel {
		return ForceResult{Kind: "force_move", Code: "out_of_range", Error: fmt.Sprintf("maxDistMm=%.2f out of range (0,%.0f]", maxDistMm, maxForceTravel)}
	}
	v, _ := c.velAcc(velPct, 0)
	fr, err := fb.ForceMove(ctx, dir, limitN, maxDistMm, v)
	fr.Kind = "force_move"
	if err != nil {
		fr.OK = false
		if fr.Code == "" {
			fr.Code = "backend"
		}
		fr.Error = err.Error()
		return fr
	}
	if fr.Pose == nil {
		if p, perr := c.Backend.Pose(ctx); perr == nil {
			fr.Pose = &p
		}
	}
	return fr
}
