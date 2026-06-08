package arm

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FairinoBackend drives a Fairino FR-series cobot over XML-RPC — the same
// transport the official `fairino` Python SDK (Robot.RPC) uses. Default endpoint
// http://<ip>:20003/RPC2.
//
// NEEDS-HARDWARE-VERIFY: the method names below match Fairino's SDK, but the
// exact positional argument layout of MoveJ/MoveL varies across controller
// firmware. They are kept here (not hardcoded deep in logic) and the port/path
// are configurable, so they can be corrected against a real controller without
// touching the control logic. Until verified on metal, prefer the generic_tcp
// driver (fully parametric) or drive joints and watch via robot_camera.
type FairinoBackend struct {
	rpc  *xmlrpcClient
	cfg  Config
	dof  int
	info ArmInfo
}

const fairinoDefaultPort = 20003

func NewFairinoBackend(cfg Config) *FairinoBackend {
	host := strings.TrimSpace(cfg.Addr)
	if host == "" {
		host = "192.168.58.2" // Fairino controller factory default
	}
	port := cfg.Port
	if port == 0 {
		port = fairinoDefaultPort
	}
	url := fmt.Sprintf("http://%s:%d/RPC2", host, port)
	info := cfg.Info
	if len(info.Joints) == 0 {
		info = FairinoDefaults()
	}
	info.Normalize()
	return &FairinoBackend{rpc: newXMLRPCClient(url, 30*time.Second), cfg: cfg, dof: len(info.Joints), info: info}
}

func (b *FairinoBackend) Name() string { return "fairino" }

func (b *FairinoBackend) Connect(ctx context.Context) error {
	// A cheap getter confirms reachability + auth-free RPC.
	if _, err := b.rpc.call(ctx, "GetActualJointPosDegree", 0); err != nil {
		return fmt.Errorf("fairino connect (%s): %w", b.rpc.url, err)
	}
	return nil
}

func (b *FairinoBackend) Close() error { return nil }

func (b *FairinoBackend) Describe(ctx context.Context) (ArmInfo, error) {
	// Read the live joint vector to confirm DOF; keep the (config or default)
	// limit table. Reading per-joint limits off the controller is firmware-
	// specific, so limits stay parametric (config/UI) — exactly the contract.
	info := b.info
	if v, err := b.rpc.call(ctx, "GetActualJointPosDegree", 0); err == nil {
		if n := len(fairinoData(v, b.dof)); n > 0 {
			info.Source = "robot"
			if n != len(info.Joints) {
				// Trust the robot's DOF; pad/truncate the limit table.
				js := make([]JointSpec, n)
				for i := 0; i < n; i++ {
					if i < len(info.Joints) {
						js[i] = info.Joints[i]
					} else {
						js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: -360, Max: 360, Unit: "deg"}
					}
				}
				info.Joints = js
			}
		}
	}
	info.Normalize()
	b.info = info
	b.dof = len(info.Joints)
	return info, nil
}

func (b *FairinoBackend) Status(ctx context.Context) (ArmStatus, error) {
	st := ArmStatus{Backend: b.Name()}
	js, err := b.JointState(ctx)
	if err != nil {
		st.Error = err.Error()
		return st, err
	}
	st.OK, st.Connected, st.Enabled, st.Joints = true, true, true, js
	if p, perr := b.Pose(ctx); perr == nil {
		st.Pose = &p
	}
	return st, nil
}

func (b *FairinoBackend) Enable(ctx context.Context, on bool) error {
	state := 0
	if on {
		state = 1
	}
	_, err := b.rpc.call(ctx, "RobotEnable", state)
	return err
}

func (b *FairinoBackend) JointState(ctx context.Context) ([]JointState, error) {
	v, err := b.rpc.call(ctx, "GetActualJointPosDegree", 0)
	if err != nil {
		return nil, err
	}
	vals := fairinoData(v, b.dof)
	out := make([]JointState, 0, len(vals))
	for i, jv := range vals {
		name := jointName(i)
		if i < len(b.info.Joints) {
			name = b.info.Joints[i].Name
		}
		out = append(out, JointState{Name: name, Position: jv, Unit: "deg"})
	}
	return out, nil
}

func (b *FairinoBackend) Pose(ctx context.Context) (Pose, error) {
	v, err := b.rpc.call(ctx, "GetActualTCPPose", 0)
	if err != nil {
		return Pose{}, err
	}
	d := fairinoData(v, 6)
	if len(d) < 6 {
		return Pose{}, ErrNoCartesian
	}
	return Pose{X: d[0], Y: d[1], Z: d[2], Roll: d[3], Pitch: d[4], Yaw: d[5]}, nil
}

func (b *FairinoBackend) MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int) error {
	// Build the full joint vector: current state overlaid with targets (MoveJ
	// needs all joints).
	cur, err := b.JointState(ctx)
	if err != nil {
		return err
	}
	jp := make([]float64, len(cur))
	for i, j := range cur {
		jp[i] = j.Position
		for name, v := range targets {
			if strings.EqualFold(name, j.Name) {
				jp[i] = v
			}
		}
	}
	// MoveJ(joint_pos, tool, user, vel, acc, ovl, exaxis_pos, blendT, offset_flag, offset_pos)
	_, err = b.rpc.call(ctx, "MoveJ", jp, 0, 0,
		float64(clampPct(velPct)), float64(clampPct(accPct)), 100.0,
		[]float64{0, 0, 0, 0}, -1.0, 0, []float64{0, 0, 0, 0, 0, 0})
	return err
}

func (b *FairinoBackend) MoveLinear(ctx context.Context, p Pose, velPct, accPct int) error {
	desc := []float64{p.X, p.Y, p.Z, p.Roll, p.Pitch, p.Yaw}
	// MoveL(desc_pos, tool, user, vel, acc, ovl, blendR, exaxis_pos, search, offset_flag, offset_pos)
	_, err := b.rpc.call(ctx, "MoveL", desc, 0, 0,
		float64(clampPct(velPct)), float64(clampPct(accPct)), 100.0, -1.0,
		[]float64{0, 0, 0, 0}, 0, 0, []float64{0, 0, 0, 0, 0, 0})
	return err
}

// WaitIdle is a no-op: Fairino MoveJ/MoveL block until the motion completes.
func (b *FairinoBackend) WaitIdle(ctx context.Context) error { return nil }

func (b *FairinoBackend) Stop(ctx context.Context) error {
	_, err := b.rpc.call(ctx, "StopMotion")
	return err
}

func (b *FairinoBackend) EStop(ctx context.Context) error {
	_, _ = b.rpc.call(ctx, "StopMotion")
	return b.Enable(ctx, false) // strongest safe action over XML-RPC: stop + disable
}

// FreeDrive toggles Fairino drag-teach (hand-guiding). NEEDS-HARDWARE-VERIFY.
func (b *FairinoBackend) FreeDrive(ctx context.Context, on bool) error {
	state := 0
	if on {
		state = 1
	}
	_, err := b.rpc.call(ctx, "DragTeachSwitch", state)
	return err
}

// Raw runs "Method arg1 arg2 ..." (numeric args parsed as float, else string).
func (b *FairinoBackend) Raw(ctx context.Context, cmd string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty command")
	}
	args := make([]any, 0, len(fields)-1)
	for _, f := range fields[1:] {
		if fv, err := strconv.ParseFloat(f, 64); err == nil {
			args = append(args, fv)
		} else {
			args = append(args, f)
		}
	}
	v, err := b.rpc.call(ctx, fields[0], args...)
	if err != nil {
		return "", err
	}
	return v.Str + fmt.Sprint(v.Floats()), nil
}

// fairinoData normalizes a getter reply to the data slice: Fairino getters often
// prefix an error code, so a reply of length dof+1 has its first element dropped.
func fairinoData(v xmlrpcValue, dof int) []float64 {
	f := v.Floats()
	if dof > 0 && len(f) == dof+1 {
		return f[1:]
	}
	return f
}
