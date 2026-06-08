package main

// ops_arm.go — generic multi-DOF ARM cells as native ops verbs, the sibling of
// ops_robot.go (Cartesian/Ender). One parametric layer drives Fairino (XML-RPC),
// Elephant myCobot (binary serial/TCP), PAROL6 (via its headless_commander
// bridge), or ANY robot with a line protocol (generic_tcp/serial) — DOF + joint
// limits are DATA (read from the robot or defined in the UI), never hardcoded.
//
// Yaver is the single management layer: the same verbs + the same camera (the
// box's shared eye, reused from the robot cell so phone-push / webcam / the host
// robot_camera MCP tool all work) + teach-and-repeat for every arm.
//
// Enabled when an arm is configured (vault "robot"/"arm-config", a local file,
// or YAVER_ARM_DRIVER+YAVER_ARM_ADDR). Camera is shared with the robot cell.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/arm"
	"github.com/yaver-io/agent/robot"
)

var (
	armOnce  sync.Once
	armCtrl  *arm.Controller
	armErr   error
	armStore = arm.DefaultProgramStore()
)

const armVaultProject = "robot"
const armVaultConfigName = "arm-config"

func armConfigFilePath() string {
	home, _ := os.UserHomeDir()
	return home + "/.yaver/arm-config.json"
}

var (
	armCfgMu     sync.Mutex
	armCfgCached *arm.Config
)

func armConfigDefault() arm.Config {
	c := arm.Config{
		Driver: strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_ARM_DRIVER"))),
		Addr:   os.Getenv("YAVER_ARM_ADDR"),
		Camera: os.Getenv("YAVER_ARM_CAMERA"),
	}
	c.Normalize()
	return c
}

func armConfigGet() arm.Config {
	armCfgMu.Lock()
	defer armCfgMu.Unlock()
	if armCfgCached != nil {
		return *armCfgCached
	}
	def := armConfigDefault()
	cfg := def
	found := false
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(armVaultProject, armVaultConfigName); gerr == nil && e != nil && e.Value != "" {
			var c arm.Config
			if json.Unmarshal([]byte(e.Value), &c) == nil {
				cfg, found = c, true
			}
		}
	}
	if !found {
		if b, err := os.ReadFile(armConfigFilePath()); err == nil {
			var c arm.Config
			if json.Unmarshal(b, &c) == nil {
				cfg, found = c, true
			}
		}
	}
	cfg.Normalize()
	if cfg.Addr == "" {
		cfg.Addr = def.Addr
	}
	armCfgCached = &cfg
	return cfg
}

func armConfigSave(c arm.Config) error {
	c.Normalize()
	c.UpdatedAt = time.Now().UnixMilli()
	b, _ := json.Marshal(c)
	var vaultErr error
	if vs, err := openVaultOptional(); err == nil {
		vaultErr = vs.Set(VaultEntry{Project: armVaultProject, Name: armVaultConfigName, Category: "custom", Value: string(b), Notes: "Yaver arm cell config (driver + DOF/joints)"})
	} else {
		vaultErr = err
	}
	if vaultErr != nil {
		if ferr := os.WriteFile(armConfigFilePath(), b, 0o600); ferr != nil {
			return ferr
		}
	}
	armCfgMu.Lock()
	armCfgCached = &c
	armCfgMu.Unlock()
	return nil
}

func armEnabled() bool {
	if os.Getenv("YAVER_ARM_DRIVER") != "" || os.Getenv("YAVER_ARM_ADDR") != "" {
		return true
	}
	return armConfigGet().Enabled()
}

// armCamera reuses the box's shared eye: an arm-specific webcam/IP override if
// set, else the robot cell's camera (phone-push "external", webcam, IP cam) so
// the SAME frames feed the arm verify loop, robot_snapshot UI polling, and the
// host robot_camera MCP tool. One camera, every cell.
func armCamera(cfg arm.Config) robot.Camera {
	switch {
	case strings.HasPrefix(cfg.Camera, "http://") || strings.HasPrefix(cfg.Camera, "https://"):
		return robot.NewHTTPCamera(cfg.Camera)
	case cfg.Camera != "" && cfg.Camera != "external" && cfg.Camera != "push":
		return robot.NewGstCamera(cfg.Camera)
	}
	// share the robot cell's camera (incl. the phone-push external buffer)
	if robotEnabled() {
		if rc, _ := ensureRobot(); rc != nil {
			return rc.Camera
		}
	}
	return nil
}

func ensureArm() (*arm.Controller, error) {
	armOnce.Do(func() {
		cfg := armConfigGet()
		var backend arm.Backend
		var err error
		switch cfg.Driver {
		case "fairino":
			backend = arm.NewFairinoBackend(cfg)
		case "mycobot":
			backend = arm.NewMyCobotBackend(cfg)
		case "bridge":
			backend = arm.NewBridgeArmBackend(cfg, "")
		case "parol6":
			backend = arm.NewBridgeArmBackend(cfg, "http://127.0.0.1:5056")
		case "sim":
			backend = arm.NewSimBackend(cfg)
		default: // generic_tcp / generic_serial / generic
			backend, err = arm.NewGenericArmBackend(cfg)
		}
		if err != nil {
			armErr = err
			return
		}
		// The sim renders its own frames; unless the user pinned a real camera,
		// point the camera at the harness so arm_snapshot / arm_look / verify show
		// the simulation through the exact same path as a hardware webcam.
		cam := armCamera(cfg)
		if sb, ok := backend.(*arm.SimBackend); ok && cam == nil {
			cam = robot.NewHTTPCamera(sb.FrameURL())
		}
		armCtrl = arm.NewController(backend, cam, robot.VisionConfig{}, cfg)
	})
	return armCtrl, armErr
}

func armForOps() (*arm.Controller, *OpsResult) {
	if !armEnabled() {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "no arm configured; set the driver (fairino / mycobot / parol6 / generic_tcp) + addr via arm_config_set"}
	}
	c, err := ensureArm()
	if err != nil || c == nil {
		msg := "arm engine unavailable"
		if err != nil {
			msg = err.Error()
		}
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: msg}
	}
	return c, nil
}

type armPayload struct {
	Joint   string             `json:"joint"`
	Delta   float64            `json:"delta"`
	Targets map[string]float64 `json:"targets"`
	Pose    *arm.Pose          `json:"pose"`
	VelPct  int                `json:"velPct"`
	AccPct  int                `json:"accPct"`
	Verify  string             `json:"verify"`
	Expect  string             `json:"expectation"`
	On      *bool              `json:"on"`
	Name    string             `json:"name"`
	Program *arm.Program       `json:"program"`
	DwellMs int                `json:"dwellMs"`
	Label   string             `json:"label"`
	Cmd     string             `json:"cmd"`
	Prompt  string             `json:"prompt"`
	// force/contact (insert / seat / pull-test)
	Dir         string  `json:"dir"`         // TCP axis "z" / "-z" for a guarded compliant move
	ForceLimitN float64 `json:"forceLimitN"` // stop when |force on dir| reaches this (N)
	MaxDistMm   float64 `json:"maxDistMm"`   // give up after this much travel (mm)
}

func parseArm(p json.RawMessage) armPayload {
	var ap armPayload
	if len(p) > 0 {
		_ = json.Unmarshal(p, &ap)
	}
	return ap
}

// armDriverCatalog powers the UI driver picker: each selectable driver + a
// default joint table to prefill (so the user sees the right DOF immediately).
func armDriverCatalog() []map[string]any {
	return []map[string]any{
		{"driver": "fairino", "label": "Fairino FR-series (XML-RPC)", "transport": "tcp", "defaultPort": 20003, "info": arm.FairinoDefaults()},
		{"driver": "mycobot", "label": "Elephant myCobot (serial/TCP)", "transport": "serial|tcp", "info": arm.MyCobotDefaults()},
		{"driver": "parol6", "label": "PAROL6 (via headless_commander bridge)", "transport": "bridge", "note": "run scripts/parol6_bridge.py next to the arm"},
		{"driver": "generic_tcp", "label": "Generic line-protocol (TCP)", "transport": "tcp"},
		{"driver": "generic_serial", "label": "Generic line-protocol (serial)", "transport": "serial"},
		{"driver": "bridge", "label": "Custom JSON bridge", "transport": "http"},
		{"driver": "sim", "label": "Simulator (PyBullet, no hardware)", "transport": "sim", "note": "headless physics sim — jog/teach/repeat and SEE the arm move with no robot. pip install pybullet numpy pillow"},
	}
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("arm_drivers", "List supported arm drivers (Fairino / myCobot / PAROL6 / generic) + default joint tables for the UI", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"drivers": armDriverCatalog()}}
	})
	reg("arm_models", "Catalog of known robot models (Fairino, Elephant myCobot, PAROL6, plus a Simulator group) with DOF/joints/payload/reach — pick one to prefill the config", func(c OpsContext, _ json.RawMessage) OpsResult {
		// Hardware models + the simulator catalog in one list so the UI model
		// picker shows a "Simulator" vendor group alongside real robots.
		models := append(arm.RobotModels(), arm.SimModels()...)
		byVendor := arm.RobotModelsByVendor()
		byVendor[arm.SimVendor] = arm.SimModels()
		return OpsResult{OK: true, Initial: map[string]any{
			"models":   models,
			"byVendor": byVendor,
		}}
	})
	reg("arm_config_get", "Get this arm cell's config (driver, addr, DOF/joints, camera)", func(c OpsContext, _ json.RawMessage) OpsResult {
		cfg := armConfigGet()
		return OpsResult{OK: true, Initial: map[string]any{"config": cfg, "enabled": armEnabled()}}
	})
	reg("arm_config_set", "Set the arm cell config — driver + addr + parametric DOF/joints (saved encrypted in the vault)", func(c OpsContext, payload json.RawMessage) OpsResult {
		var cfg arm.Config
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &cfg); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
		}
		if err := armConfigSave(cfg); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"config": armConfigGet(), "note": "hardware changes apply on next agent restart"}}
	})
	reg("arm_describe", "Read the arm's parametric definition (DOF + joint limits) from the robot or config", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		info, err := ctrl.Describe(c.Ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"info": info}}
	})
	reg("arm_status", "Arm status (connected, enabled, e-stop, joints, pose, camera)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		st, _ := ctrl.Status(c.Ctx)
		info, _ := ctrl.Describe(c.Ctx)
		return OpsResult{OK: true, Initial: map[string]any{"status": st, "info": info}}
	})
	reg("arm_state", "Live joint angles + Cartesian pose", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		js, pose, err := ctrl.State(c.Ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"joints": js, "pose": pose}}
	})
	reg("arm_enable", "Enable (power) or disable the arm", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		on := p.On == nil || *p.On // default enable
		return OpsResult{OK: true, Initial: ctrl.Enable(c.Ctx, on)}
	})
	reg("arm_jog", "Jog one joint by a relative delta (deg/mm), soft-limit checked + camera verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		return OpsResult{OK: true, Initial: ctrl.Jog(c.Ctx, p.Joint, p.Delta, p.VelPct, p.AccPct, p.Verify, p.Expect)}
	})
	reg("arm_movej", "MoveJ — absolute joint targets {targets:{J1:..}}, soft-limit checked + verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		return OpsResult{OK: true, Initial: ctrl.MoveJoints(c.Ctx, p.Targets, p.VelPct, p.AccPct, p.Verify, p.Expect)}
	})
	reg("arm_movel", "MoveL — linear move to a Cartesian pose {pose:{x,y,z,roll,pitch,yaw}}", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		if p.Pose == nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "pose required"}
		}
		return OpsResult{OK: true, Initial: ctrl.MovePose(c.Ctx, *p.Pose, p.VelPct, p.AccPct, p.Verify, p.Expect)}
	})
	reg("arm_home", "Move every joint to its configured home position", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		return OpsResult{OK: true, Initial: ctrl.Home(c.Ctx, p.VelPct, p.AccPct, p.Verify, p.Expect)}
	})
	reg("arm_stop", "Stop motion", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		_ = ctrl.Stop(c.Ctx)
		return OpsResult{OK: true, Initial: map[string]any{"stopped": true}}
	})
	reg("arm_estop", "Latched emergency stop (stop + disable)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		_ = ctrl.EStop(c.Ctx)
		return OpsResult{OK: true, Initial: map[string]any{"estopped": true}}
	})
	reg("arm_reset", "Clear the e-stop latch", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		ctrl.Reset()
		return OpsResult{OK: true, Initial: map[string]any{"estopped": false}}
	})

	// --- force / contact (insert, seat-to-backstop, pull-test) ---
	reg("arm_wrench", "Read the TCP force/torque (wrist F/T) — ErrNoForce on arms without a sensor", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		w, err := ctrl.Wrench(c.Ctx)
		if err != nil {
			code := "backend"
			if err == arm.ErrNoForce {
				code = "no_force"
			}
			return OpsResult{OK: false, Code: code, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"wrench": w}}
	})
	reg("arm_force_move", "Guarded compliant move along a TCP axis {dir:\"z\"|\"-z\", forceLimitN, maxDistMm} — insert/seat-to-backstop, or pull-test on a -axis. Stops on contact or travel; bounds-checked + e-stop gated", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		if strings.TrimSpace(p.Dir) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "dir required (e.g. \"z\" to insert, \"-z\" to pull)"}
		}
		fr := ctrl.ForceMove(c.Ctx, arm.Axis6(p.Dir), p.ForceLimitN, p.MaxDistMm, p.VelPct)
		return OpsResult{OK: fr.OK, Code: fr.Code, Error: fr.Error, Initial: fr}
	})

	// --- learning mode: hand-guide + teach-and-repeat ---
	reg("arm_freedrive", "Toggle hand-guiding / leadthrough (learning mode) — move the arm by hand, then capture", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		on := p.On == nil || *p.On
		return OpsResult{OK: true, Initial: ctrl.FreeDrive(c.Ctx, on)}
	})
	reg("arm_teach_capture", "Capture the current pose as a waypoint (while hand-guiding) — returns the waypoint", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		wp, err := ctrl.Capture(c.Ctx, p.VelPct, p.AccPct, p.DwellMs, p.Label)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"waypoint": wp}}
	})
	reg("arm_program_save", "Save a taught program (name + waypoints)", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseArm(payload)
		if p.Program == nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "program required"}
		}
		prog := *p.Program
		if prog.Name == "" {
			prog.Name = p.Name
		}
		if err := armStore.Save(prog); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"saved": prog.Name, "waypoints": len(prog.Waypoints)}}
	})
	reg("arm_program_list", "List taught arm programs", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"programs": armStore.List()}}
	})
	reg("arm_program_get", "Get a taught program", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseArm(payload)
		prog, err := armStore.Get(p.Name)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: prog}
	})
	reg("arm_program_delete", "Delete a taught program", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseArm(payload)
		if err := armStore.Delete(p.Name); err != nil {
			return OpsResult{OK: false, Code: "delete_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"deleted": p.Name}}
	})
	reg("arm_program_run", "Replay a taught program (repeat), each waypoint soft-limit checked + camera verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		prog := arm.Program{Name: p.Name}
		if p.Program != nil {
			prog = *p.Program
		}
		if strings.TrimSpace(prog.Name) != "" && len(prog.Waypoints) == 0 {
			loaded, err := armStore.Get(prog.Name)
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			prog = loaded
		}
		return OpsResult{OK: true, Initial: ctrl.RunProgram(c.Ctx, prog, p.Verify)}
	})

	// --- camera (shared box eye) ---
	reg("arm_snapshot", "Single camera JPEG (the box's shared eye) as a data: URL", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		if ctrl.Camera == nil || !ctrl.Camera.Available() {
			return OpsResult{OK: false, Code: "no_camera", Error: "no camera on this device"}
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 20*time.Second)
		defer cancel()
		jpg, err := ctrl.Camera.Grab(ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "no_camera", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"image": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg), "bytes": len(jpg)}}
	})
	reg("arm_look", "Ask the on-device vision model about the current frame (inspect/fix)", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		if ctrl.Camera == nil || !ctrl.Camera.Available() {
			return OpsResult{OK: false, Code: "no_camera", Error: "no camera frame available"}
		}
		p := parseArm(payload)
		ctx, cancel := context.WithTimeout(c.Ctx, 95*time.Second)
		defer cancel()
		jpg, err := ctrl.Camera.Grab(ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "no_camera", Error: err.Error()}
		}
		res := map[string]any{"image": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg), "bytes": len(jpg)}
		answer, verr := robot.AskVision(ctx, ctrl.Vision, jpg, p.Prompt)
		if verr != nil {
			res["visionError"] = verr.Error()
			return OpsResult{OK: false, Code: "no_vision", Error: verr.Error(), Initial: res}
		}
		res["answer"] = answer
		return OpsResult{OK: true, Initial: res}
	})
	reg("arm_raw", "Backend-specific passthrough (XML-RPC method / TCP line) — power users", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		p := parseArm(payload)
		out, err := ctrl.Backend.Raw(c.Ctx, p.Cmd)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"reply": out}}
	})
}
