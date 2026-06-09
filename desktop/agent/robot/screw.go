package robot

import (
	"context"
	"fmt"
	"time"
)

// ScrewParams describes one torque-gated screw-drive at a pole.
type ScrewParams struct {
	X, Y            float64 // pole location (mm)
	Zapproach       float64 // start of the slow plunge (mm, above the head)
	Zmax            float64 // deepest allowed Z (mm) — hard floor, never plunge past
	Step            float64 // plunge increment (mm), e.g. 0.3
	Feed            int     // plunge feed (mm/min), slow e.g. 60
	Zsafe           float64 // retract height (mm)
	TargetTorqueNmm float64 // seat when companion torque ≥ this
	ToolPin         int     // companion GPIO pin for the driver; <0 → use Marlin tool
	DwellMs         int     // hold at seat torque before release
	TimeoutSec      int     // abort the plunge after this long
	AtCurrent       bool    // skip XY travel — plunge at the current X/Y (linear-rail indexer:
	//                         the rail already positioned the klemens under a fixed screwdriver)
	// Slotted (düz) slot-find: a flat blade has no self-centering recess like a
	// yıldız/Phillips bit, so the bit must SPIN (tool stays on) while creeping Z
	// down SeekMm at SeekFeed to let the blade cam INTO the single slot (yuva)
	// before the torque plunge; SeekDwellMs guarantees ≥1 revolution at contact;
	// Pecks re-tries the plunge from the seek start to clear a stubborn slot.
	// Slotted=false → classic single drive (behaviour unchanged).
	Slotted     bool    // düz slot-find mode
	SeekMm      float64 // creep-down distance while finding the slot (mm)
	SeekFeed    int     // slow feed during the seek (mm/min)
	SeekDwellMs int     // dwell at the seek floor so the blade sweeps into the slot
	Pecks       int     // plunge attempts (1 = no peck); each re-plunges from the seek start
}

// ScrewResult is the torque-closed-loop verdict: did the screw seat AT torque,
// confirmed by the companion's force sensor, and what the camera saw.
type ScrewResult struct {
	OK                bool      `json:"ok"`
	Code              string    `json:"code,omitempty"`
	Error             string    `json:"error,omitempty"`
	Seated            bool      `json:"seated"`
	TargetTorqueNmm   float64   `json:"targetTorqueNmm"`
	MeasuredTorqueNmm float64   `json:"measuredTorqueNmm"`
	FinalZ            float64   `json:"finalZ"`
	Steps             int       `json:"steps"`
	Position          *Position `json:"position,omitempty"`
	Verify            *Verdict  `json:"verify,omitempty"`
	Frames            *Frames   `json:"frames,omitempty"`
	TookMs            int64     `json:"tookMs"`
}

// DriveScrew runs the closed-loop screw cycle: travel → tool on → slow plunge
// while polling the companion's torque → stop the instant torque ≥ target (the
// screw is seated) OR at the Zmax floor → dwell → tool off → retract → camera
// verify. This is the feedback the open-loop clutch can't give: confirmation
// that the screw actually seated at the intended torque, not just that the axis
// moved.
func (c *Controller) DriveScrew(ctx context.Context, p ScrewParams, verifyMode, expectation string) ScrewResult {
	start := time.Now()
	c.mu.Lock()
	es := c.estopped
	c.mu.Unlock()
	if es {
		return ScrewResult{Code: "estopped", Error: "e-stopped; call reset"}
	}
	if c.Companion == nil {
		return ScrewResult{Code: "no_companion", Error: "torque-gated screw needs a companion sensor; none configured"}
	}
	if !c.isHomed(ctx) {
		return ScrewResult{Code: "not_homed", Error: "home before driving screws"}
	}
	if p.Zmax < c.Env.Zmin || p.Zapproach > c.Env.Zmax || p.Zmax >= p.Zapproach {
		return ScrewResult{Code: "out_of_range", Error: fmt.Sprintf("bad plunge window approach=%.2f max=%.2f", p.Zapproach, p.Zmax)}
	}
	if p.Step <= 0 {
		p.Step = 0.3
	}
	if p.Feed <= 0 {
		p.Feed = 60
	}
	if p.TimeoutSec <= 0 {
		p.TimeoutSec = 30
	}
	if expectation == "" {
		expectation = "screwdriver plunged into the screw head at the pole"
	}

	// 1) Get to safe Z above the screw. Normally travel to the pole (X,Y); for a
	// linear-rail indexer the rail already positioned the klemens under a fixed
	// screwdriver, so AtCurrent just lifts Z and plunges in place.
	zsafe := p.Zsafe
	if zsafe <= 0 {
		zsafe = p.Zapproach + 10
	}
	var travel MoveResponse
	if p.AtCurrent {
		travel = c.Move(ctx, nil, nil, &zsafe, 3000, "off", "")
	} else {
		travel = c.Move(ctx, &p.X, &p.Y, &zsafe, 3000, "off", "")
	}
	if !travel.OK {
		return ScrewResult{Code: travel.Code, Error: travel.Error}
	}
	var before []byte
	camOK := c.Camera != nil && c.Camera.Available()
	if camOK {
		before, _ = c.Camera.Grab(ctx)
	}

	// 2) Tool on (companion GPIO pin or Marlin tool) + tare the sensor.
	toolOn := func(on bool) error {
		if p.ToolPin >= 0 {
			return c.Companion.GPIO(ctx, p.ToolPin, on)
		}
		return c.Backend.Tool(ctx, on)
	}
	if err := toolOn(true); err != nil {
		return ScrewResult{Code: "backend", Error: "tool on: " + err.Error()}
	}
	_ = c.Companion.Zero(ctx)

	deadline := time.Now().Add(time.Duration(p.TimeoutSec) * time.Second)
	seated := false
	var measured float64
	steps := 0

	// 2b) SLOTTED (düz) SEEK: spin (tool already on) while creeping Z down SeekMm
	// so the flat blade cams INTO the slot (yuva) before the torque plunge. The
	// companion torque is the capture ("yakaladı") signal — a rise means caught.
	zStart := p.Zapproach
	if p.Slotted && p.SeekMm > 0 {
		seekFeed := p.SeekFeed
		if seekFeed <= 0 {
			seekFeed = 40
		}
		zSeek := p.Zapproach - p.SeekMm
		if zSeek < p.Zmax {
			zSeek = p.Zmax
		}
		if r := c.Move(ctx, nil, nil, &zSeek, seekFeed, "off", ""); !r.OK {
			_ = toolOn(false)
			return ScrewResult{Code: r.Code, Error: r.Error}
		}
		_ = c.Backend.WaitMoves(ctx)
		if p.SeekDwellMs > 0 {
			time.Sleep(time.Duration(p.SeekDwellMs) * time.Millisecond)
		}
		zStart = zSeek // continue the torque plunge from where the blade caught
	}

	// 3) Slow stepped plunge, polling torque each step. Pecks re-plunge from
	// zStart to clear a stubborn slot; classic drive uses one pass (Pecks≤1).
	pecks := p.Pecks
	if pecks < 1 {
		pecks = 1
	}
	z := zStart
	for peck := 0; peck < pecks && !seated && time.Now().Before(deadline); peck++ {
		z = zStart
		for z > p.Zmax && time.Now().Before(deadline) {
			z -= p.Step
			if z < p.Zmax {
				z = p.Zmax
			}
			steps++
			if r := c.Move(ctx, nil, nil, &z, p.Feed, "off", ""); !r.OK {
				_ = toolOn(false)
				return ScrewResult{Code: r.Code, Error: r.Error}
			}
			sr, err := c.Companion.Sense(ctx)
			if err == nil {
				measured = sr.TorqueNmm
				if sr.TorqueNmm >= p.TargetTorqueNmm {
					seated = true
					break
				}
			}
			if z <= p.Zmax {
				break
			}
		}
		if !seated && peck < pecks-1 { // peck: lift back to the seek start, clear, re-find
			_ = c.Move(ctx, nil, nil, &zStart, p.Feed, "off", "")
			_ = c.Backend.WaitMoves(ctx)
			time.Sleep(150 * time.Millisecond)
		}
	}

	// 4) Dwell at seat, then tool off + retract.
	if seated && p.DwellMs > 0 {
		_ = c.Backend.WaitMoves(ctx)
		time.Sleep(time.Duration(p.DwellMs) * time.Millisecond)
		if sr, err := c.Companion.Sense(ctx); err == nil && sr.TorqueNmm > measured {
			measured = sr.TorqueNmm
		}
	}
	_ = toolOn(false)
	_ = c.Move(ctx, nil, nil, &zsafe, 900, "off", "")

	pos, _ := c.Backend.Position(ctx)
	res := ScrewResult{
		OK:                true,
		Seated:            seated,
		TargetTorqueNmm:   p.TargetTorqueNmm,
		MeasuredTorqueNmm: measured,
		FinalZ:            z,
		Steps:             steps,
		Position:          &pos,
		TookMs:            time.Since(start).Milliseconds(),
	}
	if !seated {
		res.Code = "not_seated" // torque target never reached within the plunge window
	}

	// 5) Camera confirmation (independent of the torque channel).
	if camOK && (verifyMode == "agent" || verifyMode == "frames" || verifyMode == "true") {
		after, _ := c.Camera.Grab(ctx)
		if before != nil && after != nil {
			res.Frames = &Frames{Before: jpegDataURL(before), After: jpegDataURL(after)}
			if verifyMode == "frames" {
				res.Verify = &Verdict{Mode: "frames", Expectation: expectation}
			} else if v, err := VerifyMotion(ctx, c.Vision, before, after, expectation); err == nil {
				res.Verify = &v
			} else {
				res.Verify = &Verdict{Mode: "frames", Expectation: expectation}
			}
		}
	}
	return res
}
