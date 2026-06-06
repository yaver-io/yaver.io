package robot

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// Envelope is the soft-limit work volume (mm). Targets outside are refused,
// never silently clamped-and-moved.
type Envelope struct {
	Xmin, Xmax, Ymin, Ymax, Zmin, Zmax float64
}

// DefaultEnvelope is the Ender-3 build volume (klemens/config.py).
var DefaultEnvelope = Envelope{0, 220, 0, 220, 0, 250}

// Controller ties a Backend + Camera + vision config into the move-and-verify
// protocol. Safe for concurrent callers (one motion at a time).
type Controller struct {
	Backend   Backend
	Camera    Camera
	Vision    VisionConfig
	Env       Envelope
	Companion Companion // optional: torque/force + extra GPIO (docs/yaver-companion-mcu.md)
	// StrictEncoder: e-stop when a move's encoder readback disagrees with the
	// commanded delta (missed steps / blockage). The deterministic, always-on
	// edge closed-loop gate — needs no camera or model.
	StrictEncoder bool
	// EPerTurn calibrates screwdriver rotation (E units per revolution); used by
	// Rotate and by replayed "rotate" steps when the caller omits it.
	EPerTurn float64
	// Screwdriver calibration (camera Z touch-off) + seat torque for terminal
	// blocks. Used by ScrewHome and replayed "screw" steps as the fallback when
	// the step/payload omits them.
	ZSafe, ZEngage, MaxPlunge, TargetTorqueNmm float64

	mu       sync.Mutex
	estopped bool
	homed    bool
}

func NewController(b Backend, cam Camera, vc VisionConfig) *Controller {
	return &Controller{Backend: b, Camera: cam, Vision: vc, Env: DefaultEnvelope}
}

const crossTolMm = 0.5 // encoder vs commanded agreement tolerance

func (c *Controller) Status(ctx context.Context) (Status, error) {
	st, err := c.Backend.Status(ctx)
	st.CameraOK = c.Camera != nil && c.Camera.Available()
	c.mu.Lock()
	st.EStopped = c.estopped
	if st.Position != nil && st.Position.Homed {
		c.homed = true
	}
	c.mu.Unlock()
	return st, err
}

func (c *Controller) EStop(ctx context.Context) error {
	c.mu.Lock()
	c.estopped = true
	c.mu.Unlock()
	return c.Backend.EStop(ctx)
}

func (c *Controller) Reset() {
	c.mu.Lock()
	c.estopped = false
	c.homed = false // re-require homing after an e-stop
	c.mu.Unlock()
}

// Home runs G28 and (optionally) verifies via camera.
func (c *Controller) Home(ctx context.Context, axes, verifyMode, expectation string) MoveResponse {
	if expectation == "" {
		expectation = "carriage moved to the home corner (toward the left endstop and down to the bed)"
	}
	return c.execute(ctx, Action{Kind: "home"}, verifyMode, expectation, func() error {
		if err := c.Backend.Home(ctx, axes); err != nil {
			return err
		}
		c.mu.Lock()
		c.homed = true
		c.mu.Unlock()
		return nil
	}, map[string]float64{}) // home target deltas are not soft-checked
}

// Jog moves relative on one axis.
func (c *Controller) Jog(ctx context.Context, axis string, dist float64, feed int, verifyMode, expectation string) MoveResponse {
	axis = upper1(axis)
	if axis != "X" && axis != "Y" && axis != "Z" {
		return fail("bad_payload", "axis must be X, Y or Z")
	}
	if !c.isHomed(ctx) {
		return fail("not_homed", "home (G28) before relative jog")
	}
	exp := map[string]float64{lower1(axis): dist}
	return c.execute(ctx, Action{Kind: "jog", Axis: axis, Dist: dist, Feed: feed}, verifyMode, expectation, func() error {
		return c.Backend.Jog(ctx, axis, dist, feed)
	}, exp)
}

// Move goes to an absolute target (any subset of axes), soft-limit checked.
func (c *Controller) Move(ctx context.Context, x, y, z *float64, feed int, verifyMode, expectation string) MoveResponse {
	if err := c.checkEnvelope(x, y, z); err != nil {
		return fail("out_of_range", err.Error())
	}
	return c.execute(ctx, Action{Kind: "move", X: x, Y: y, Z: z, Feed: feed}, verifyMode, expectation, func() error {
		return c.Backend.Move(ctx, x, y, z, feed)
	}, nil) // expected delta computed from before/after in execute for moves
}

// Tool toggles the end-effector (screwdriver). No camera verify by default.
func (c *Controller) Tool(ctx context.Context, on bool) MoveResponse {
	c.mu.Lock()
	es := c.estopped
	c.mu.Unlock()
	if es {
		return fail("estopped", "e-stopped; call reset")
	}
	if err := c.Backend.Tool(ctx, on); err != nil {
		return fail("backend", err.Error())
	}
	return MoveResponse{OK: true, Action: &Action{Kind: "tool", On: &on}}
}

// Verify is a camera-only check (no motion).
func (c *Controller) Verify(ctx context.Context, expectation string) MoveResponse {
	if c.Camera == nil || !c.Camera.Available() {
		return fail("no_camera", "no camera available")
	}
	frame, err := c.Camera.Grab(ctx)
	if err != nil {
		return fail("no_camera", err.Error())
	}
	v, err := VerifyMotion(ctx, c.Vision, frame, frame, expectation)
	resp := MoveResponse{OK: true, Action: &Action{Kind: "verify"}, Frames: &Frames{After: jpegDataURL(frame)}}
	if err != nil {
		resp.Verify = &Verdict{Mode: "frames", Expectation: expectation}
		resp.Code = "no_vision"
	} else {
		resp.Verify = &v
	}
	return resp
}

// execute is the shared move-and-verify pipeline (docs/robot-protocol.md §0):
// before-frame → motion → M400 → fresh M114 → after-frame → verdict → cross-check.
func (c *Controller) execute(ctx context.Context, action Action, verifyMode, expectation string, motion func() error, expectedDelta map[string]float64) MoveResponse {
	start := time.Now()
	c.mu.Lock()
	es := c.estopped
	c.mu.Unlock()
	if es {
		return fail("estopped", "e-stopped; call reset before further motion")
	}
	if verifyMode == "" {
		verifyMode = "agent"
	}
	wantFrames := verifyMode == "agent" || verifyMode == "frames" || verifyMode == "true"
	camOK := c.Camera != nil && c.Camera.Available()

	var before []byte
	posBefore, _ := c.Backend.Position(ctx)
	if wantFrames && camOK {
		before, _ = c.Camera.Grab(ctx)
	}

	if err := motion(); err != nil {
		// A motion fault (incl. a serial reset/reconnect) means the position is
		// no longer trustworthy → drop the homed flag so the next jog/job is
		// refused until a re-home. Visible failure over silent continuation.
		c.mu.Lock()
		c.homed = false
		c.mu.Unlock()
		return fail("backend", err.Error())
	}
	// THE rule: wait for motion to actually complete before reading/looking.
	if err := c.Backend.WaitMoves(ctx); err != nil {
		return fail("backend", "M400 wait failed: "+err.Error())
	}
	posAfter, posErr := c.Backend.Position(ctx)

	var after []byte
	if wantFrames && camOK {
		after, _ = c.Camera.Grab(ctx)
	}

	resp := MoveResponse{OK: true, Action: &action, TookMs: time.Since(start).Milliseconds()}
	if posErr == nil {
		resp.Position = &posAfter
	}

	// Encoder cross-check: expected vs observed deltas.
	if action.Kind == "move" {
		expectedDelta = map[string]float64{}
		if action.X != nil {
			expectedDelta["x"] = *action.X - posBefore.X
		}
		if action.Y != nil {
			expectedDelta["y"] = *action.Y - posBefore.Y
		}
		if action.Z != nil {
			expectedDelta["z"] = *action.Z - posBefore.Z
		}
	}
	if len(expectedDelta) > 0 && posErr == nil {
		observed := map[string]float64{
			"x": posAfter.X - posBefore.X,
			"y": posAfter.Y - posBefore.Y,
			"z": posAfter.Z - posBefore.Z,
		}
		agree := true
		obs := map[string]float64{}
		for ax, want := range expectedDelta {
			got := observed[ax]
			obs[ax] = round2(got)
			if math.Abs(got-want) > crossTolMm {
				agree = false
			}
		}
		resp.Cross = &CrossCheck{ExpectedDelta: roundMap(expectedDelta), ObservedDelta: obs, Agree: agree}
		if !agree {
			// The firmware didn't reach the commanded position — the edge's
			// deterministic closed-loop gate trips here (no camera/LLM needed).
			resp.Code = "encoder_mismatch"
			if c.StrictEncoder {
				_ = c.EStop(ctx)
			}
		}
	}

	// Frames + verdict.
	if wantFrames && camOK && before != nil && after != nil {
		resp.Frames = &Frames{Before: jpegDataURL(before), After: jpegDataURL(after)}
		if verifyMode == "frames" {
			resp.Verify = &Verdict{Mode: "frames", Expectation: expectation}
		} else {
			v, err := VerifyMotion(ctx, c.Vision, before, after, expectation)
			if err != nil {
				// Vision unavailable → degrade to frames mode, don't fail the move.
				resp.Verify = &Verdict{Mode: "frames", Expectation: expectation}
				resp.Code = "no_vision"
			} else {
				resp.Verify = &v
				// Vision-gated safety: obstruction latches e-stop.
				if v.Obstruction {
					_ = c.EStop(ctx)
					resp.Code = "obstruction"
				}
			}
		}
	} else if wantFrames && !camOK {
		resp.Code = "no_camera"
	}
	return resp
}

// Rotate drives the screwdriver as a stepper on the freed E-axis: M302 P1 (allow
// cold extrude, no hotend) → M83 (relative E) → G1 E<±turns·ePerTurn> F<rpm>.
// Exact rotation (turns), speed (rpm), direction (ccw → negative). ePerTurn is a
// per-rig calibration (E units per full screwdriver revolution).
func (c *Controller) Rotate(ctx context.Context, turns float64, rpm int, ccw bool, ePerTurn float64) MoveResponse {
	c.mu.Lock()
	es := c.estopped
	c.mu.Unlock()
	if es {
		return fail("estopped", "e-stopped; call reset")
	}
	if ePerTurn <= 0 {
		ePerTurn = c.EPerTurn
	}
	if ePerTurn <= 0 {
		ePerTurn = 1
	}
	if rpm <= 0 {
		rpm = 300
	}
	e := turns * ePerTurn
	if ccw {
		e = -e
	}
	for _, g := range []string{"M302 P1", "M83", fmt.Sprintf("G1 E%s F%d", trimNum(e), rpm)} {
		if err := c.Backend.Raw(ctx, g); err != nil {
			return fail("backend", err.Error())
		}
	}
	_ = c.Backend.WaitMoves(ctx)
	on := true
	return MoveResponse{OK: true, Action: &Action{Kind: "rotate", Dist: turns, Feed: rpm, On: &on}}
}

// screwParamsFromCalibration builds a torque-gated screw cycle at (x,y) from the
// cell's calibration, letting explicit args override. zSafe = travel height,
// zEngage = where the tip meets the head (camera touch-off), maxPlunge = how far
// below engage the driver may travel as the screw seats. target = seat torque.
func (c *Controller) screwParamsFromCalibration(x, y, zEngage, zSafe, target float64) ScrewParams {
	if zSafe <= 0 {
		zSafe = c.ZSafe
	}
	if zSafe <= 0 {
		zSafe = 25
	}
	if zEngage <= 0 {
		zEngage = c.ZEngage
	}
	if zEngage <= 0 {
		zEngage = 5
	}
	plunge := c.MaxPlunge
	if plunge <= 0 {
		plunge = 10
	}
	zmax := zEngage - plunge
	if zmax < c.Env.Zmin {
		zmax = c.Env.Zmin
	}
	if target <= 0 {
		target = c.TargetTorqueNmm
	}
	return ScrewParams{
		X: x, Y: y, Zapproach: zEngage, Zmax: zmax, Step: 0.3, Feed: 60,
		Zsafe: zSafe, TargetTorqueNmm: target, ToolPin: -1, DwellMs: 500, TimeoutSec: 30,
	}
}

// ScrewHome drives the screw at (x,y) HOME to the calibrated seat torque — the
// terminal-block operation. Torque-closed-loop via the companion sensor; halts
// the instant torque ≥ target (screw seated) or at the plunge floor. target ≤ 0
// uses the calibrated default.
func (c *Controller) ScrewHome(ctx context.Context, x, y, target float64, atCurrent bool, verifyMode string) ScrewResult {
	p := c.screwParamsFromCalibration(x, y, 0, 0, target)
	p.AtCurrent = atCurrent
	return c.DriveScrew(ctx, p, verifyMode, "drive the terminal-block screw home to the target torque")
}

// Torque reads the companion's current force/torque (live readout). Errors when
// no companion sensor is configured.
func (c *Controller) Torque(ctx context.Context) (SenseReading, error) {
	if c.Companion == nil {
		return SenseReading{}, fmt.Errorf("no torque sensor (companion) configured")
	}
	return c.Companion.Sense(ctx)
}

// Power switches the machine PSU via Marlin PSU control: M80 (on) / M81 (off).
// Only does anything when the board has PSU control wired (PS_ON → PSU relay/
// MOSFET, PSU_CONTROL in firmware) — over plain USB the logic board stays
// powered regardless, so a smart plug/relay is the reliable hard-cut. On power-
// off the homing reference is lost, so we clear the homed flag.
func (c *Controller) Power(ctx context.Context, on bool) MoveResponse {
	cmd := "M81"
	if on {
		cmd = "M80"
	}
	if err := c.Backend.Raw(ctx, cmd); err != nil {
		return fail("backend", err.Error())
	}
	if !on {
		c.mu.Lock()
		c.homed = false
		c.mu.Unlock()
	}
	return MoveResponse{OK: true, Action: &Action{Kind: "power", On: &on}}
}

// MotorsOff releases the steppers (M84) — frees the axes to be moved by hand.
// Clears homing (position is no longer trusted after a manual move).
func (c *Controller) MotorsOff(ctx context.Context) MoveResponse {
	if err := c.Backend.Raw(ctx, "M84"); err != nil {
		return fail("backend", err.Error())
	}
	c.mu.Lock()
	c.homed = false
	c.mu.Unlock()
	return MoveResponse{OK: true, Action: &Action{Kind: "motors_off"}}
}

// GPIO sets a board pin (M42 P<pin> S<value>) — driver enable/direction, a
// relay/MOSFET, an LED. The phone's generic motor/IO control.
func (c *Controller) GPIO(ctx context.Context, pin, value int) MoveResponse {
	c.mu.Lock()
	es := c.estopped
	c.mu.Unlock()
	if es {
		return fail("estopped", "e-stopped; call reset")
	}
	if err := c.Backend.Raw(ctx, fmt.Sprintf("M42 P%d S%d", pin, value)); err != nil {
		return fail("backend", err.Error())
	}
	return MoveResponse{OK: true, Action: &Action{Kind: "gpio"}}
}

// Gcode is a raw G-code passthrough (power users); still e-stop gated.
func (c *Controller) Gcode(ctx context.Context, line string) MoveResponse {
	c.mu.Lock()
	es := c.estopped
	c.mu.Unlock()
	if es {
		return fail("estopped", "e-stopped; call reset")
	}
	if err := c.Backend.Raw(ctx, line); err != nil {
		return fail("backend", err.Error())
	}
	return MoveResponse{OK: true, Action: &Action{Kind: "gcode"}}
}

func (c *Controller) isHomed(ctx context.Context) bool {
	c.mu.Lock()
	h := c.homed
	c.mu.Unlock()
	if h {
		return true
	}
	// Seed from backend status (the bridge may have homed before we attached).
	if st, err := c.Backend.Status(ctx); err == nil && st.Position != nil && st.Position.Homed {
		c.mu.Lock()
		c.homed = true
		c.mu.Unlock()
		return true
	}
	return false
}

func (c *Controller) checkEnvelope(x, y, z *float64) error {
	if x != nil && (*x < c.Env.Xmin || *x > c.Env.Xmax) {
		return fmt.Errorf("X=%.2f out of range [%.0f,%.0f]", *x, c.Env.Xmin, c.Env.Xmax)
	}
	if y != nil && (*y < c.Env.Ymin || *y > c.Env.Ymax) {
		return fmt.Errorf("Y=%.2f out of range [%.0f,%.0f]", *y, c.Env.Ymin, c.Env.Ymax)
	}
	if z != nil && (*z < c.Env.Zmin || *z > c.Env.Zmax) {
		return fmt.Errorf("Z=%.2f out of range [%.0f,%.0f]", *z, c.Env.Zmin, c.Env.Zmax)
	}
	return nil
}

func fail(code, msg string) MoveResponse { return MoveResponse{OK: false, Code: code, Error: msg} }

func upper1(s string) string {
	if s == "" {
		return s
	}
	return string([]byte{toUpper(s[0])})
}
func lower1(s string) string {
	if s == "" {
		return s
	}
	return string([]byte{toLower(s[0])})
}
func toUpper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}
func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
func round2(f float64) float64 { return math.Round(f*100) / 100 }
func roundMap(m map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range m {
		out[k] = round2(v)
	}
	return out
}
