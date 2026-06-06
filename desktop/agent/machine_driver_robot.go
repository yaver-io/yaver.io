package main

// machine_driver_robot.go — surface the robot cell as a read-only machine.Driver
// so arms/crimpers/cut-strip lines all appear in ONE machine_list grid (the
// unified "watch" wall). The robot keeps its own real-time robot_* verbs for
// motion + the move-and-verify loop; this adapter is only the view/watch seam.
// Lives in package main (not machine/) to bridge robot ↔ machine without an
// import cycle.

import (
	"context"
	"sync"
	"time"

	"github.com/yaver-io/agent/machine"
	"github.com/yaver-io/agent/robot"
)

var robotDriverRegisterOnce sync.Once

// maybeRegisterRobotDriver registers the local robot cell (if enabled) as a
// driver under id "robot", exactly once, so machine_list shows it alongside
// Modbus machines. Cheap no-op when no robot is configured here.
func maybeRegisterRobotDriver(eng *machine.Engine) {
	if eng == nil || !robotEnabled() {
		return
	}
	robotDriverRegisterOnce.Do(func() {
		ctrl, err := ensureRobot()
		if err != nil || ctrl == nil {
			return
		}
		eng.RegisterDriver("robot", &robotDriverAdapter{ctrl: ctrl, name: "robot"})
	})
}

// robotDriverAdapter adapts a *robot.Controller to machine.Driver (read-only).
type robotDriverAdapter struct {
	ctrl *robot.Controller
	name string
}

func (a *robotDriverAdapter) Name() string { return a.name }
func (a *robotDriverAdapter) Kind() string { return "robot" }

func (a *robotDriverAdapter) Capabilities() machine.CapSet {
	caps := machine.Caps(machine.CapStatus, machine.CapRead, machine.CapSubscribe)
	if a.ctrl != nil && a.ctrl.Camera != nil && a.ctrl.Camera.Available() {
		caps[machine.CapVision] = true
	}
	return caps
}

func (a *robotDriverAdapter) Connect(ctx context.Context) error { return nil }
func (a *robotDriverAdapter) Close() error                      { return nil }

func (a *robotDriverAdapter) Status(ctx context.Context) (machine.MachineStatus, error) {
	st, _ := a.ctrl.Status(ctx)
	state := "idle"
	if st.EStopped {
		state = "fault"
	} else if !st.Connected {
		state = "off"
	}
	detail := map[string]any{
		"backend": st.Backend, "tool": st.Tool, "estopped": st.EStopped, "cameraOk": st.CameraOK,
	}
	if st.Position != nil {
		detail["position"] = st.Position
		detail["homed"] = st.Position.Homed
	}
	return machine.MachineStatus{
		Name: a.name, Kind: "robot", Driver: "robot_cell",
		Connected: st.Connected, State: state, Detail: detail,
		Caps: a.Capabilities().List(), TS: time.Now().UnixMilli(),
	}, nil
}

func (a *robotDriverAdapter) Browse(ctx context.Context) ([]machine.Tag, error) {
	return []machine.Tag{
		{Name: "x", Unit2: "mm"}, {Name: "y", Unit2: "mm"}, {Name: "z", Unit2: "mm"},
		{Name: "homed"}, {Name: "tool"}, {Name: "estopped"},
	}, nil
}

func (a *robotDriverAdapter) Read(ctx context.Context, refs []machine.TagRef) ([]machine.Sample, error) {
	st, err := a.ctrl.Status(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	out := []machine.Sample{}
	if st.Position != nil {
		out = append(out,
			machine.Sample{Tag: "x", Value: st.Position.X, Unit2: "mm", TS: now},
			machine.Sample{Tag: "y", Value: st.Position.Y, Unit2: "mm", TS: now},
			machine.Sample{Tag: "z", Value: st.Position.Z, Unit2: "mm", TS: now},
		)
		homed := 0.0
		if st.Position.Homed {
			homed = 1
		}
		out = append(out, machine.Sample{Tag: "homed", Value: homed, TS: now})
	}
	estop := 0.0
	if st.EStopped {
		estop = 1
	}
	out = append(out, machine.Sample{Tag: "estopped", Value: estop, TS: now})
	return out, nil
}

func (a *robotDriverAdapter) Subscribe(ctx context.Context, refs []machine.TagRef, opts machine.SubOpts) (<-chan machine.Sample, error) {
	// pollSubscribe is unexported in machine/, so emulate a poll here via Read.
	interval := time.Duration(opts.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	ch := make(chan machine.Sample, 16)
	go func() {
		defer close(ch)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			samples, err := a.Read(ctx, refs)
			if err == nil {
				for _, s := range samples {
					select {
					case ch <- s:
					case <-ctx.Done():
						return
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return ch, nil
}

// Control verbs are not exposed through the generic seam — robots keep their own
// real-time robot_* verbs (motion + move-and-verify). Safety stays there.
func (a *robotDriverAdapter) Write(ctx context.Context, w []machine.TagWrite) error {
	return machine.ErrNotSupported
}
func (a *robotDriverAdapter) Recall(ctx context.Context, program string) error {
	return machine.ErrNotSupported
}
func (a *robotDriverAdapter) SubmitJob(ctx context.Context, job machine.Job) error {
	return machine.ErrNotSupported
}
