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
		toolMode := robotEnvOr("YAVER_ROBOT_TOOL", "fan")
		cam := robot.NewGstCamera(os.Getenv("YAVER_ROBOT_CAMERA")) // "" → /dev/video0
		var backend robot.Backend
		if dev := os.Getenv("YAVER_ROBOT_SERIAL"); dev != "" {
			rw, err := robotOpenSerial(dev)
			if err != nil {
				robotErr = fmt.Errorf("open serial %s: %w", dev, err)
				return
			}
			sb := robot.NewSerialBackend(rw, toolMode, robotAtoi("YAVER_ROBOT_TOOL_PIN", 6))
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
		c.StrictEncoder = os.Getenv("YAVER_ROBOT_STRICT") == "1"
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
	// teach / programs
	Name    string         `json:"name"`
	Steps   []robot.Step   `json:"steps"`
	Program *robot.Program `json:"program"`
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
	reg("robot_status", "Robot cell status (position, homed, tool, e-stop, camera)", func(c OpsContext, _ json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		st, _ := ctrl.Status(c.Ctx)
		return OpsResult{OK: true, Initial: st}
	})
	reg("robot_home", "Home the robot (G28), optionally camera-verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Home(c.Ctx, p.Axes, p.Verify, p.Expectation)}
	})
	reg("robot_jog", "Relative jog one axis, camera+encoder verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Jog(c.Ctx, p.Axis, p.Dist, p.Feed, p.Verify, p.Expectation)}
	})
	reg("robot_move", "Absolute move (soft-limit checked), camera+encoder verified", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Move(c.Ctx, p.X, p.Y, p.Z, p.Feed, p.Verify, p.Expectation)}
	})
	reg("robot_tool", "Toggle the end-effector (screwdriver) on/off", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
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
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.Rotate(c.Ctx, p.Turns, p.Rpm, p.Ccw, robotEnvFloat("YAVER_ROBOT_E_PER_TURN", p.EPerTurn))}
	})
	reg("robot_gpio", "Set a board pin (M42 P<pin> S<value>) — driver enable/dir, relay, LED", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := robotForOps()
		if deny != nil {
			return *deny
		}
		p := parseRobot(payload)
		return OpsResult{OK: true, Initial: ctrl.GPIO(c.Ctx, p.Pin, p.Value)}
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
