package main

// ops_sim.go — simulator-specific verbs that sit on top of the generic arm
// layer. The simulator is just an arm.Backend (driver "sim"), so EVERYTHING
// else — arm_jog, arm_movej, arm_home, arm_status, arm_snapshot, arm_look,
// teach/repeat (arm_freedrive → arm_teach_capture → arm_program_run) — already
// works against it unchanged, including the camera (the harness renders frames
// the same snapshot path consumes). These verbs add only what's sim-only:
// browsing the simulator model catalog, hot-swapping the loaded robot, and
// re-homing — none of which a hardware arm can do.

import (
	"encoding/json"

	"github.com/yaver-io/agent/arm"
)

// simBackendForOps resolves the arm controller and asserts the backend is the
// simulator, returning a friendly denial otherwise (the verb only makes sense
// when driver "sim" is configured).
func simBackendForOps() (*arm.Controller, *arm.SimBackend, *OpsResult) {
	ctrl, deny := armForOps()
	if deny != nil {
		return nil, nil, deny
	}
	sb, ok := ctrl.Backend.(*arm.SimBackend)
	if !ok {
		return nil, nil, &OpsResult{OK: false, Code: "unsupported",
			Error: "this verb needs the simulator backend — set driver \"sim\" via arm_config_set (pick a Simulator model)"}
	}
	return ctrl, sb, nil
}

type simPayload struct {
	Model string `json:"model"`
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("sim_models", "Catalog of simulator robot arms (UR5e/UR10e, Franka Panda, KUKA iiwa/iiwa14, Kinova Gen3, built-in 6-DOF) — pick one to jog/teach with no hardware", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{"models": arm.SimModels()}}
	})

	reg("sim_status", "Simulator status — engine, loaded model, render-frame URL (the sim shows through arm_snapshot)", func(c OpsContext, _ json.RawMessage) OpsResult {
		cfg := armConfigGet()
		out := map[string]any{
			"isSim":  cfg.Driver == "sim",
			"engine": cfg.Sim.Engine,
			"model":  cfg.Sim.Model,
			"port":   cfg.Sim.Port,
		}
		if cfg.Driver == "sim" {
			ctrl, sb, deny := simBackendForOps()
			if deny == nil {
				out["frameUrl"] = sb.FrameURL()
				if info, err := ctrl.Describe(c.Ctx); err == nil {
					out["info"] = info
				}
			}
		}
		return OpsResult{OK: true, Initial: out}
	})

	reg("sim_load", "Hot-swap the robot loaded in the running simulator {model:\"desc:ur5e\"|\"builtin:arm6\"|\"pybullet:..\"|\"urdf:<path|url>\"} — no restart; returns the read-back DOF/joints", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, sb, deny := simBackendForOps()
		if deny != nil {
			return *deny
		}
		var p simPayload
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &p)
		}
		if p.Model == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "model required (e.g. \"desc:ur5e\" or a \"urdf:<path|url>\")"}
		}
		info, err := sb.LoadModel(c.Ctx, p.Model)
		if err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		ctrl.RefreshDescribe() // DOF changed; drop the cached description
		return OpsResult{OK: true, Initial: map[string]any{"info": info, "model": p.Model}}
	})

	reg("sim_reset", "Re-home the simulated robot and clear any e-stop (sim-only)", func(c OpsContext, _ json.RawMessage) OpsResult {
		_, sb, deny := simBackendForOps()
		if deny != nil {
			return *deny
		}
		if err := sb.Reset(c.Ctx); err != nil {
			return OpsResult{OK: false, Code: "backend", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"reset": true}}
	})
}
