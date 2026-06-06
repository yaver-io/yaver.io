package main

// ops_robot.go — the robot cell as native agent ops verbs, so a phone (or any
// Commander) drives a machine THROUGH THE YAVER MESH by deviceId:
//
//	callOps("robot_jog", {axis,dist,...}, machine=<robotDeviceId>)
//
// routes via proxyToDevice → relay /d/<id>/ops → this agent → robot.Controller.
// Camera comes back over the same mesh path as base64 from robot_snapshot, so
// there are NO new HTTP routes and NO edits to shared files — everything
// self-registers here. See docs/yaver-robot-fleet-mesh-design.md.
//
// Enabled when YAVER_ROBOT_SERIAL (native Marlin serial, e.g. /dev/ttyUSB0 or a
// termux-usb fd path) or YAVER_ROBOT_BRIDGE is set; otherwise the verbs return a
// clear "not enabled" so non-robot devices in a fleet answer harmlessly.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/robot"
)

var (
	robotOnce sync.Once
	robotCtrl *robot.Controller
	robotErr  error
)

func robotEnabled() bool {
	return os.Getenv("YAVER_ROBOT_SERIAL") != "" || os.Getenv("YAVER_ROBOT_BRIDGE") != ""
}

func robotEnvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func robotAtoi(k string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil {
		return n
	}
	return def
}

// robotOpenSerial raw-configures a tty (115200 8N1) and opens it R/W (stty +
// OpenFile — no serial library, same minimal approach as the rest of the agent).
func robotOpenSerial(dev string) (io.ReadWriteCloser, error) {
	_ = exec.Command("stty", "-F", dev, "115200", "cs8", "-cstopb", "-parenb",
		"-crtscts", "-ixon", "-ixoff", "clocal", "raw", "-echo").Run()
	return os.OpenFile(dev, os.O_RDWR, 0)
}

// ensureRobot lazily builds the Controller. Non-robot agents never pay for it.
func ensureRobot() (*robot.Controller, error) {
	robotOnce.Do(func() {
		cfg := robotConfigGet()
		toolMode := cfg.ToolMode
		if toolMode == "" {
			toolMode = robotEnvOr("YAVER_ROBOT_TOOL", "fan")
		}
		camDev := cfg.Camera
		if camDev == "" {
			camDev = os.Getenv("YAVER_ROBOT_CAMERA")
		}
		cam := robot.NewGstCamera(camDev) // "" → /dev/video0
		var backend robot.Backend
		dev := cfg.Serial
		if dev == "" {
			dev = os.Getenv("YAVER_ROBOT_SERIAL")
		}
		if dev != "" {
			rw, err := robotOpenSerial(dev)
			if err != nil {
				robotErr = fmt.Errorf("open serial %s: %w", dev, err)
				return
			}
			toolPin := cfg.ToolPin
			if toolPin == 0 {
				toolPin = robotAtoi("YAVER_ROBOT_TOOL_PIN", 6)
			}
			sb := robot.NewSerialBackend(rw, toolMode, toolPin)
			sb.Reopen = func() (io.ReadWriteCloser, error) { return robotOpenSerial(dev) }
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			_ = sb.Settle(ctx)
			cancel()
			backend = sb
		} else {
			bb := robot.NewBridgeBackend(os.Getenv("YAVER_ROBOT_BRIDGE"))
			bb.ToolMode = toolMode
			backend = bb
		}
		c := robot.NewController(backend, cam, robot.VisionConfig{})
		// Calibrate external (Fuju) drivers: push steps/mm so the rails move in mm.
		if m92 := cfg.StepsPerMM.M92(); m92 != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			_ = backend.Raw(ctx, m92)
			cancel()
		}
		if cfg.Envelope != nil {
			c.Env = *cfg.Envelope
		}
		c.EPerTurn = cfg.EPerTurn
		c.ZSafe = cfg.ZSafe
		c.ZEngage = cfg.ZEngage
		c.MaxPlunge = cfg.MaxPlunge
		c.TargetTorqueNmm = cfg.TargetTorqueNmm
		c.StrictEncoder = cfg.Strict || os.Getenv("YAVER_ROBOT_STRICT") == "1"
		// Optional torque sensor on a 2nd serial link (INA219 / HX711 companion).
		// Best-effort: a missing companion only disables torque-gated screwing.
		compDev := cfg.Companion
		if compDev == "" {
			compDev = os.Getenv("YAVER_ROBOT_COMPANION")
		}
		if compDev != "" {
			if rw, cerr := robotOpenSerial(compDev); cerr == nil {
				c.Companion = robot.NewLineCompanion(rw)
			}
		}
		robotCtrl = c
	})
	return robotCtrl, robotErr
}

func robotForOps() (*robot.Controller, *OpsResult) {
	if !robotEnabled() {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "robot is disabled on this device; set YAVER_ROBOT_SERIAL (e.g. /dev/ttyUSB0)"}
	}
	c, err := ensureRobot()
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: "robot engine unavailable: " + err.Error()}
	}
	return c, nil
}

// robotStore persists taught programs on the edge.
var robotStore = robot.DefaultProgramStore()

// --- vault-backed config (profiles, calibration, recipes) ---
//
// The robot config lives in the Yaver VAULT (project "robot", name "config"):
// encrypted per-user, local-first, locks with the auth token — so a wire-harness
// shop's profile + squeeze calibration stays private even on shared infra, with
// NO Talos required. Talos backup is an optional seam (robot_backup). A user can
// run the whole cell standalone, as is.
const robotVaultProject = "robot"
const robotVaultConfigName = "config"

var (
	robotCfgMu     sync.Mutex
	robotCfgCached *robot.Config
)

// robotConfigDefault derives a config from hardware env when the vault has none
// yet — an existing rig keeps working with zero setup.
func robotConfigDefault() robot.Config {
	c := robot.DefaultConfig(robotEnabled(), true)
	c.Serial = os.Getenv("YAVER_ROBOT_SERIAL")
	c.ToolMode = robotEnvOr("YAVER_ROBOT_TOOL", "fan")
	c.ToolPin = robotAtoi("YAVER_ROBOT_TOOL_PIN", 6)
	c.Camera = os.Getenv("YAVER_ROBOT_CAMERA")
	c.EPerTurn = robotEnvFloat("YAVER_ROBOT_E_PER_TURN", 1)
	c.Strict = os.Getenv("YAVER_ROBOT_STRICT") == "1"
	c.Normalize()
	return c
}

// robotConfigFile is the local fallback when the vault is locked/rotated. The
// box itself is the trust boundary (local-first); the vault adds encryption-at-
// rest when available, but the cell must stay usable "as is" regardless.
func robotConfigFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "robot-config.json")
}

// robotConfigGet returns the cached config: vault first (encrypted), then the
// local file, then the hardware default. Never errors.
func robotConfigGet() robot.Config {
	robotCfgMu.Lock()
	defer robotCfgMu.Unlock()
	if robotCfgCached != nil {
		return *robotCfgCached
	}
	def := robotConfigDefault()
	cfg := def
	found := false
	// 1) vault (encrypted, preferred)
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(robotVaultProject, robotVaultConfigName); gerr == nil && e != nil && e.Value != "" {
			var c robot.Config
			if json.Unmarshal([]byte(e.Value), &c) == nil {
				cfg, found = c, true
			}
		}
	}
	// 2) local file fallback (vault locked / no entry)
	if !found {
		if b, err := os.ReadFile(robotConfigFile()); err == nil {
			var c robot.Config
			if json.Unmarshal(b, &c) == nil {
				cfg, found = c, true
			}
		}
	}
	cfg.Normalize()
	if cfg.Serial == "" { // keep the machine-local hardware path
		cfg.Serial = def.Serial
	}
	robotCfgCached = &cfg
	return cfg
}

// robotConfigSave persists to the vault when unlocked, else to the local file.
// Either way the cell keeps working — the vault is an encryption bonus, not a
// hard dependency.
func robotConfigSave(c robot.Config) error {
	c.Normalize()
	c.UpdatedAt = time.Now().UnixMilli()
	b, _ := json.Marshal(c)

	var vaultErr error
	if vs, err := openVaultOptional(); err == nil {
		vaultErr = vs.Set(VaultEntry{
			Project:  robotVaultProject,
			Name:     robotVaultConfigName,
			Category: "custom",
			Value:    string(b),
			Notes:    "Yaver robot cell config (profile + calibration)",
		})
	} else {
		vaultErr = err
	}

	if vaultErr != nil {
		// vault locked/rotated → local file so config still saves ("as is")
		if ferr := os.WriteFile(robotConfigFile(), b, 0o600); ferr != nil {
			return fmt.Errorf("vault unavailable (%v) and file write failed: %w", vaultErr, ferr)
		}
	}
	robotCfgMu.Lock()
	robotCfgCached = &c
	robotCfgMu.Unlock()
	return nil
}

// robotGate denies a verb when the active profile lacks the needed module, so a
// screwdriver-only device answers motion verbs cleanly instead of moving nothing.
func robotGate(module string) *OpsResult {
	cfg := robotConfigGet()
	if !cfg.Has(module) {
		return &OpsResult{OK: false, Code: "no_" + module,
			Error: fmt.Sprintf("profile %q has no %s module", cfg.Profile, module)}
	}
	return nil
}

// robotPayload is the superset of every robot verb's payload.
type robotPayload struct {
	Axis        string   `json:"axis"`
	Dist        float64  `json:"dist"`
	X           *float64 `json:"x"`
	Y           *float64 `json:"y"`
	Z           *float64 `json:"z"`
	Feed        int      `json:"feed"`
	Axes        string   `json:"axes"`
	On          *bool    `json:"on"`
	Verify      string   `json:"verify"`
	Expectation string   `json:"expectation"`
	// motor / GPIO / raw
	Turns    float64 `json:"turns"`
	Rpm      int     `json:"rpm"`
	Ccw      bool    `json:"ccw"`
	EPerTurn float64 `json:"ePerTurn"`
	Pin      int     `json:"pin"`
	Value    int     `json:"value"`
	Line     string  `json:"line"`
	// screw / torque
	Target float64 `json:"targetTorqueNmm"`
	// teach / programs
	Name    string         `json:"name"`
	Steps   []robot.Step   `json:"steps"`
	Program *robot.Program `json:"program"`
}

// robotStatusResult embeds Status (its fields stay top-level for back-compat)
// and adds the active profile + modules so the app shows the right controls.
type robotStatusResult struct {
	robot.Status
	Profile         string   `json:"profile"`
	Modules         []string `json:"modules"`
	Label           string   `json:"label,omitempty"`
	Companion       bool     `json:"companion"`                 // torque sensor present
	TargetTorqueNmm float64  `json:"targetTorqueNmm,omitempty"` // calibrated seat torque
	ZEngage         float64  `json:"zEngage,omitempty"`         // calibrated engage height
	ZSafe           float64  `json:"zSafe,omitempty"`           // calibrated travel height
}

func parseRobot(payload json.RawMessage) robotPayload {
	var p robotPayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	return p
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}
	reg("robot_status", "Robot cell status (position, homed, tool, e-stop, camera, profile)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		st, _ := ctrl.Status(c.Ctx)
		cfg := robotConfigGet()
		return OpsResult{OK: true, Initial: robotStatusResult{
			Status:          st,
			Profile:         cfg.Profile,
			Modules:         cfg.ResolvedModules(),
			Label:           cfg.Label,
			Companion:       ctrl.Companion != nil,
			TargetTorqueNmm: cfg.TargetTorqueNmm,
			ZEngage:         cfg.ZEngage,
			ZSafe:           cfg.ZSafe,
		}}
	})
	reg("robot_profiles", "List selectable robot profiles (cartesian / +screwdriver / screwdriver-only)", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"profiles": robot.Profiles()}}
	})
	reg("robot_config_get", "Get this cell's config (profile, modules, calibration) from the vault", func(c OpsContext, _ json.RawMessage) OpsResult {
		cfg := robotConfigGet()
		return OpsResult{OK: true, Initial: map[string]any{"config": cfg, "modules": cfg.ResolvedModules()}}
	})
	reg("robot_config_set", "Set this cell's profile + calibration (saved encrypted in the vault)", func(c OpsContext, payload json.RawMessage) OpsResult {
		var cfg robot.Config
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &cfg); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
		}
		// preserve machine-local hardware path the phone doesn't know
		if cfg.Serial == "" {
			cfg.Serial = robotConfigGet().Serial
		}
		if err := robotConfigSave(cfg); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		saved := robotConfigGet()
		return OpsResult{OK: true, Initial: map[string]any{
			"config": saved, "modules": saved.ResolvedModules(),
			"note": "hardware changes (serial/camera) apply on next agent restart",
		}}
	})
	reg("robot_backup", "Optional: back up config + programs to a Talos target (no-op unless configured)", func(c OpsContext, payload json.RawMessage) OpsResult {
		return robotBackup(c, payload)
	})
	reg("robot_home", "Home the robot (G28), optionally camera-verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleMotion); g != nil {
			return *g
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Home(c.Ctx, p.Axes, p.Verify, p.Expectation)}
	})
	reg("robot_jog", "Relative jog one axis, camera+encoder verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleMotion); g != nil {
			return *g
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Jog(c.Ctx, p.Axis, p.Dist, p.Feed, p.Verify, p.Expectation)}
	})
	reg("robot_move", "Absolute move (soft-limit checked), camera+encoder verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleMotion); g != nil {
			return *g
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Move(c.Ctx, p.X, p.Y, p.Z, p.Feed, p.Verify, p.Expectation)}
	})
	reg("robot_tool", "Toggle the end-effector (screwdriver) on/off", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleTool); g != nil {
			return *g
		}
		p := parseRobot(payload)
		on := p.On != nil && *p.On
		return OpsResult{OK: true, Initial: ctrl.Tool(c.Ctx, on)}
	})
	reg("robot_verify", "Camera-only check (no motion)", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Verify(c.Ctx, p.Expectation)}
	})
	reg("robot_estop", "Latched emergency stop (tool off + M112)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		_ = ctrl.EStop(c.Ctx)
		return OpsResult{OK: true, Initial: map[string]any{"estopped": true}}
	})
	reg("robot_reset", "Clear the e-stop latch (re-requires homing)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		ctrl.Reset()
		return OpsResult{OK: true, Initial: map[string]any{"estopped": false}}
	})
	reg("robot_snapshot", "Single camera JPEG as a data: URL (camera over the mesh)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
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
		return OpsResult{OK: true, Initial: map[string]any{
			"image": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg),
			"bytes": len(jpg),
		}}
	})

	// --- motor / GPIO manipulation ---
	reg("robot_screw_rotate", "Rotate the screwdriver N turns at rpm (ccw to reverse) — E-stepper", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleRotate); g != nil {
			return *g
		}
		p := parseRobot(payload)
		ePerTurn := p.EPerTurn
		if ePerTurn <= 0 {
			ePerTurn = robotConfigGet().EPerTurn // calibrated per-rig, from the vault
		}
		return OpsResult{OK: true, Initial: ctrl.Rotate(c.Ctx, p.Turns, p.Rpm, p.Ccw, ePerTurn)}
	})
	reg("robot_gpio", "Set a board pin (M42 P<pin> S<value>) — driver enable/dir, relay, LED", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleGPIO); g != nil {
			return *g
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.GPIO(c.Ctx, p.Pin, p.Value)}
	})
	reg("robot_torque", "Live torque/force readout from the companion sensor (N·mm)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		r, err := ctrl.Torque(c.Ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "no_companion", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: r}
	})
	reg("robot_screw", "Drive the screw at (x,y) HOME to the calibrated/target torque — terminal blocks", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleTool); g != nil {
			return *g
		}
		p := parseRobot(payload)
		// (x,y) given → travel there then plunge (jig). No (x,y) → plunge in place
		// (linear rail already indexed the klemens under a fixed screwdriver).
		atCurrent := p.X == nil || p.Y == nil
		var x, y float64
		if !atCurrent {
			x, y = *p.X, *p.Y
		}
		return OpsResult{OK: true, Initial: ctrl.ScrewHome(c.Ctx, x, y, p.Target, atCurrent, p.Verify)}
	})
	reg("robot_power", "Power the machine PSU on/off (M80/M81) — needs PSU-control wiring", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		on := p.On != nil && *p.On
		return OpsResult{OK: true, Initial: ctrl.Power(c.Ctx, on)}
	})
	reg("robot_motors_off", "Release the steppers (M84) so the axes move freely by hand", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		return OpsResult{OK: true, Initial: ctrl.MotorsOff(c.Ctx)}
	})
	reg("robot_gcode", "Raw G-code passthrough (power users); e-stop gated", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		if strings.TrimSpace(p.Line) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "line required"}
		}
		return OpsResult{OK: true, Initial: ctrl.Gcode(c.Ctx, p.Line)}
	})

	// --- teach-and-repeat (programs) ---
	reg("robot_program_save", "Save a taught program (name + steps)", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseRobot(payload)
		prog := robot.Program{Name: p.Name, Steps: p.Steps}
		if p.Program != nil {
			prog = *p.Program
		}
		if err := robotStore.Save(prog); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"saved": prog.Name, "steps": len(prog.Steps)}}
	})
	reg("robot_array_build", "Generate + save a klemens fastening program (grid jig or linear rail)", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		if g := robotGate(robot.ModuleMotion); g != nil {
			return *g
		}
		var ap robot.ArrayParams
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &ap); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
		}
		// optionally anchor the array at the current position ("teach the origin")
		var cap struct {
			CaptureOrigin bool `json:"captureOrigin"`
		}
		_ = json.Unmarshal(payload, &cap)
		if cap.CaptureOrigin {
			if pos, err := ctrl.Backend.Position(c.Ctx); err == nil {
				ap.OriginX, ap.OriginY, ap.Origin = pos.X, pos.Y, pos.X
			}
		}
		if ap.TargetTorqueNmm <= 0 {
			ap.TargetTorqueNmm = robotConfigGet().TargetTorqueNmm
		}
		prog, err := robot.BuildKlemensArray(ap)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if err := robotStore.Save(prog); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"saved": prog.Name, "steps": len(prog.Steps), "program": prog}}
	})
	reg("robot_jig_scad", "Generate a printable klemens jig (OpenSCAD) matching a grid — render + print on your own printer", func(c OpsContext, payload json.RawMessage) OpsResult {
		var jp robot.JigParams
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &jp); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
		}
		return OpsResult{OK: true, Initial: map[string]any{"scad": robot.BuildJigSCAD(jp), "filename": "klemens-jig.scad"}}
	})
	reg("robot_program_list", "List taught programs", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"programs": robotStore.List()}}
	})
	reg("robot_program_get", "Get a taught program (name → steps)", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseRobot(payload)
		prog, err := robotStore.Get(p.Name)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: prog}
	})
	reg("robot_program_delete", "Delete a taught program", func(c OpsContext, payload json.RawMessage) OpsResult {
		p := parseRobot(payload)
		if err := robotStore.Delete(p.Name); err != nil {
			return OpsResult{OK: false, Code: "delete_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"deleted": p.Name}}
	})
	reg("robot_program_run", "Replay a taught program, each step camera/encoder-verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		prog := robot.Program{Name: p.Name, Steps: p.Steps}
		if strings.TrimSpace(p.Name) != "" {
			loaded, err := robotStore.Get(p.Name)
			if err != nil {
				return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
			}
			prog = loaded
		}
		return OpsResult{OK: true, Initial: ctrl.RunProgram(c.Ctx, prog, p.Verify)}
	})
}

func robotEnvFloat(k string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
