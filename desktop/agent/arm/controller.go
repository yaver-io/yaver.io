package arm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yaver-io/agent/robot"
)

// Controller ties a Backend + Camera + vision into the move-and-verify protocol
// for an arbitrary-DOF arm. Soft joint limits come from the (parametric) ArmInfo
// — never hardcoded. Safe for concurrent callers (one motion at a time). It
// reuses robot.Camera + robot.VisionConfig so the camera + host-vision paths are
// identical to the Cartesian cell.
type Controller struct {
	Backend Backend
	Camera  robot.Camera
	Vision  robot.VisionConfig
	Cfg     Config

	mu       sync.Mutex
	estopped bool
	info     *ArmInfo
}

func NewController(b Backend, cam robot.Camera, vc robot.VisionConfig, cfg Config) *Controller {
	cfg.Normalize()
	return &Controller{Backend: b, Camera: cam, Vision: vc, Cfg: cfg}
}

// Describe returns the arm's parametric definition, cached after the first read.
// Prefers what the backend reports (read from the robot); falls back to the
// config table the UI defined.
func (c *Controller) Describe(ctx context.Context) (ArmInfo, error) {
	c.mu.Lock()
	if c.info != nil {
		info := *c.info
		c.mu.Unlock()
		return info, nil
	}
	c.mu.Unlock()

	info, err := c.Backend.Describe(ctx)
	if err != nil || len(info.Joints) == 0 {
		// Backend could not (or chose not to) report — use the configured table.
		info = c.Cfg.Info
		if info.Source == "" {
			info.Source = "config"
		}
	}
	info.Normalize()
	c.mu.Lock()
	c.info = &info
	c.mu.Unlock()
	return info, nil
}

// RefreshDescribe clears the cache (after a config change / reconnect).
func (c *Controller) RefreshDescribe() {
	c.mu.Lock()
	c.info = nil
	c.mu.Unlock()
}

func (c *Controller) Status(ctx context.Context) (ArmStatus, error) {
	st, err := c.Backend.Status(ctx)
	st.CameraOK = c.Camera != nil && c.Camera.Available()
	c.mu.Lock()
	st.EStopped = c.estopped
	c.mu.Unlock()
	return st, err
}

func (c *Controller) isEStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.estopped
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
	c.mu.Unlock()
}

func (c *Controller) Stop(ctx context.Context) error { return c.Backend.Stop(ctx) }

func (c *Controller) Enable(ctx context.Context, on bool) MoveResult {
	if err := c.Backend.Enable(ctx, on); err != nil {
		return MoveResult{OK: false, Code: "backend", Kind: "enable", Error: err.Error()}
	}
	return MoveResult{OK: true, Kind: "enable"}
}

func (c *Controller) State(ctx context.Context) ([]JointState, *Pose, error) {
	js, err := c.Backend.JointState(ctx)
	if err != nil {
		return nil, nil, err
	}
	var pose *Pose
	if p, perr := c.Backend.Pose(ctx); perr == nil {
		pose = &p
	}
	return js, pose, nil
}

// jointSpec returns the parametric spec for a joint name (case-insensitive).
func (c *Controller) jointSpec(ctx context.Context, name string) (JointSpec, bool) {
	info, _ := c.Describe(ctx)
	for _, j := range info.Joints {
		if eqFold(j.Name, name) {
			return j, true
		}
	}
	return JointSpec{}, false
}

// checkLimits refuses any target outside its joint's [Min,Max] (never silently
// clamps — visible refusal over a surprise move). Continuous joints are exempt.
func (c *Controller) checkLimits(ctx context.Context, targets map[string]float64) error {
	info, _ := c.Describe(ctx)
	byName := map[string]JointSpec{}
	for _, j := range info.Joints {
		byName[lowerStr(j.Name)] = j
	}
	for name, v := range targets {
		j, ok := byName[lowerStr(name)]
		if !ok {
			return fmt.Errorf("unknown joint %q (DOF=%d)", name, info.DOF)
		}
		if j.jtype() == JointContinuous {
			continue
		}
		if v < j.Min || v > j.Max {
			return fmt.Errorf("%s=%.3f out of range [%.3f,%.3f] %s", j.Name, v, j.Min, j.Max, j.unit())
		}
	}
	return nil
}

func (c *Controller) velAcc(velPct, accPct int) (int, int) {
	if velPct <= 0 {
		velPct = c.Cfg.DefaultVelPct
	}
	if accPct <= 0 {
		accPct = c.Cfg.DefaultAccPct
	}
	return clampPct(velPct), clampPct(accPct)
}

// MoveJoints (MoveJ) commands absolute joint targets, soft-limit checked and
// camera-verified.
func (c *Controller) MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int, verify, expectation string) MoveResult {
	if c.isEStopped() {
		return MoveResult{OK: false, Code: "estopped", Kind: "movej", Error: "e-stopped; call reset"}
	}
	if len(targets) == 0 {
		return MoveResult{OK: false, Code: "bad_payload", Kind: "movej", Error: "no joint targets"}
	}
	if err := c.checkLimits(ctx, targets); err != nil {
		return MoveResult{OK: false, Code: "out_of_range", Kind: "movej", Error: err.Error()}
	}
	v, a := c.velAcc(velPct, accPct)
	return c.execute(ctx, "movej", verify, expectation, func() error {
		return c.Backend.MoveJoints(ctx, targets, v, a)
	})
}

// Jog moves one joint by a relative delta (reads current, applies, limit-checks).
func (c *Controller) Jog(ctx context.Context, joint string, delta float64, velPct, accPct int, verify, expectation string) MoveResult {
	if c.isEStopped() {
		return MoveResult{OK: false, Code: "estopped", Kind: "jog", Error: "e-stopped; call reset"}
	}
	spec, ok := c.jointSpec(ctx, joint)
	if !ok {
		return MoveResult{OK: false, Code: "bad_payload", Kind: "jog", Error: "unknown joint " + joint}
	}
	js, err := c.Backend.JointState(ctx)
	if err != nil {
		return MoveResult{OK: false, Code: "backend", Kind: "jog", Error: err.Error()}
	}
	cur, found := 0.0, false
	for _, j := range js {
		if eqFold(j.Name, spec.Name) {
			cur, found = j.Position, true
			break
		}
	}
	if !found {
		return MoveResult{OK: false, Code: "backend", Kind: "jog", Error: "could not read current position of " + spec.Name}
	}
	target := map[string]float64{spec.Name: cur + delta}
	if err := c.checkLimits(ctx, target); err != nil {
		return MoveResult{OK: false, Code: "out_of_range", Kind: "jog", Error: err.Error()}
	}
	v, a := c.velAcc(velPct, accPct)
	r := c.execute(ctx, "jog", verify, expectation, func() error {
		return c.Backend.MoveJoints(ctx, target, v, a)
	})
	return r
}

// MovePose (MoveL) commands an absolute Cartesian pose (linear), camera-verified.
func (c *Controller) MovePose(ctx context.Context, p Pose, velPct, accPct int, verify, expectation string) MoveResult {
	if c.isEStopped() {
		return MoveResult{OK: false, Code: "estopped", Kind: "movel", Error: "e-stopped; call reset"}
	}
	info, _ := c.Describe(ctx)
	if !info.HasCartesian {
		return MoveResult{OK: false, Code: "no_cartesian", Kind: "movel", Error: "this arm has no Cartesian (TCP-pose) support; use joint moves"}
	}
	v, a := c.velAcc(velPct, accPct)
	return c.execute(ctx, "movel", verify, expectation, func() error {
		return c.Backend.MoveLinear(ctx, p, v, a)
	})
}

// Home moves every joint to its configured Home position (generic; no separate
// backend home needed). Limit-checked like any move.
func (c *Controller) Home(ctx context.Context, velPct, accPct int, verify, expectation string) MoveResult {
	info, _ := c.Describe(ctx)
	if len(info.Joints) == 0 {
		return MoveResult{OK: false, Code: "no_joints", Kind: "home", Error: "no joints defined; set the arm's DOF/joints first"}
	}
	targets := map[string]float64{}
	for _, j := range info.Joints {
		targets[j.Name] = j.Home
	}
	if expectation == "" {
		expectation = "the arm moved to its home / zero pose"
	}
	r := c.MoveJoints(ctx, targets, velPct, accPct, verify, expectation)
	r.Kind = "home"
	return r
}

// Verify is a camera-only check (no motion).
func (c *Controller) Verify(ctx context.Context, expectation string) MoveResult {
	if c.Camera == nil || !c.Camera.Available() {
		return MoveResult{OK: false, Code: "no_camera", Kind: "verify", Error: "no camera available"}
	}
	frame, err := c.Camera.Grab(ctx)
	if err != nil {
		return MoveResult{OK: false, Code: "no_camera", Kind: "verify", Error: err.Error()}
	}
	res := MoveResult{OK: true, Kind: "verify", Frames: &Frames{After: jpegDataURL(frame)}}
	v, verr := verifyArmMotion(ctx, c.Vision, frame, frame, expectation)
	if verr != nil {
		res.Verify = &Verdict{Mode: "frames", Expectation: expectation}
		res.Code = "no_vision"
	} else {
		res.Verify = &v
	}
	return res
}

// execute is the shared move-and-verify pipeline: before-frame → motion →
// WaitIdle → fresh state → after-frame → verdict. Mirrors robot.Controller but
// in joint/pose space.
func (c *Controller) execute(ctx context.Context, kind, verify, expectation string, motion func() error) MoveResult {
	start := time.Now()
	if verify == "" {
		verify = "frames"
	}
	wantFrames := verify == "agent" || verify == "frames" || verify == "true"
	camOK := c.Camera != nil && c.Camera.Available()

	var before []byte
	if wantFrames && camOK {
		before, _ = c.Camera.Grab(ctx)
	}
	if err := motion(); err != nil {
		return MoveResult{OK: false, Code: "backend", Kind: kind, Error: err.Error()}
	}
	if err := c.Backend.WaitIdle(ctx); err != nil {
		return MoveResult{OK: false, Code: "backend", Kind: kind, Error: "wait-idle failed: " + err.Error()}
	}

	res := MoveResult{OK: true, Kind: kind, TookMs: time.Since(start).Milliseconds()}
	if js, err := c.Backend.JointState(ctx); err == nil {
		res.Joints = js
	}
	if p, err := c.Backend.Pose(ctx); err == nil {
		res.Pose = &p
	}

	var after []byte
	if wantFrames && camOK {
		after, _ = c.Camera.Grab(ctx)
	}
	if wantFrames && camOK && before != nil && after != nil {
		res.Frames = &Frames{Before: jpegDataURL(before), After: jpegDataURL(after)}
		if verify == "frames" {
			res.Verify = &Verdict{Mode: "frames", Expectation: expectation}
		} else {
			v, err := verifyArmMotion(ctx, c.Vision, before, after, expectation)
			if err != nil {
				res.Verify = &Verdict{Mode: "frames", Expectation: expectation}
				res.Code = "no_vision"
			} else {
				res.Verify = &v
				if v.Obstruction { // vision-gated safety: latch e-stop
					_ = c.EStop(ctx)
					res.Code = "obstruction"
				}
			}
		}
	} else if wantFrames && !camOK {
		res.Code = "no_camera"
	}
	return res
}
