package main

// mcp_mobile_deploy.go — the `mobile_deploy_to_phone` MCP tool.
//
// One verb the coding agent (Claude Code / Codex / opencode) calls when a
// human says "put my app on my phone." It chains the five Hermes steps a
// normie would otherwise have to discover and order by hand:
//
//   mobile_project_status   (is this RN/Expo, deps installed, Hermes ready?)
//   mobile_project_prepare  (auto-install deps if missing)
//   mobile_hermes_doctor    (blockers + native-module compatibility)
//   mobile_project_build    (compile the Hermes bundle Yaver loads)
//   mobile_hermes_reload    (push the bundle into the Yaver app on the phone)
//
// It is doctor-driven: the doctor already computes projectDir, framework,
// blockers, readiness, and an ordered nextActions list, so we reuse it as
// the planner instead of re-deriving readiness. The tool stops at the
// first hard failure and returns a single `next_action` sentence the agent
// can speak verbatim, plus a per-step trace.
//
// plan_only=true stops before the slow build/reload and just reports the
// ordered remaining steps — handy when the calling agent has a short tool
// timeout. Default executes the whole chain inline.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type mobileDeployToPhoneArgs struct {
	DeviceID  string `json:"device_id"`
	Directory string `json:"directory"`
	Framework string `json:"framework"`
	Platform  string `json:"platform"`
	PlanOnly  bool   `json:"plan_only"`
	Mode      string `json:"mode"`
}

type mobileDeployStep struct {
	Step    string `json:"step"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
}

type mobileDeployResult struct {
	// OK is true when nothing in the chain hard-failed. Done is true only
	// when the bundle actually reached the phone (reload succeeded).
	OK          bool                `json:"ok"`
	Done        bool                `json:"done"`
	Steps       []mobileDeployStep  `json:"steps"`
	Blockers    []string            `json:"blockers,omitempty"`
	NextActions []map[string]string `json:"next_actions,omitempty"`
	NextAction  string              `json:"next_action"`
	Detail      string              `json:"detail,omitempty"`
}

func (r *mobileDeployResult) add(step string, ok bool, summary string) {
	r.Steps = append(r.Steps, mobileDeployStep{Step: step, OK: ok, Summary: summary})
}

// mobileDeployToPhone runs the full status→prepare→doctor→build→reload
// chain. device_id routes to a remote owned device; empty means this
// machine (the common normie path: agent, project, and daemon all local).
func (s *HTTPServer) mobileDeployToPhone(ctx context.Context, args mobileDeployToPhoneArgs) mobileDeployResult {
	if strings.TrimSpace(args.DeviceID) != "" {
		return s.mobileDeployToPhoneRemote(ctx, args)
	}

	res := mobileDeployResult{}
	platform := strings.TrimSpace(args.Platform)
	if platform == "" {
		platform = "ios"
	}

	// 1) Doctor resolves the project + computes blockers/readiness.
	doctor := mobileHermesDoctor(mobileHermesDoctorInput{Directory: args.Directory})
	projectDir, _ := doctor["projectDir"].(string)
	if strings.TrimSpace(projectDir) == "" {
		res.add("doctor", false, "no Expo / React Native project found")
		res.Blockers = stringSliceFromInterface(doctor["blockers"])
		res.NextActions = nextActionsFromDoctor(doctor)
		res.NextAction = "I couldn't find a React Native or Expo app here. Open the folder that holds your app's package.json (with expo or react-native), or ask me to create a starter app, then try again."
		return res
	}
	res.add("doctor", true, fmt.Sprintf("found %s project at %s", firstNonEmpty(strFromDoctor(doctor, "framework"), "mobile"), projectDir))

	// 2) Install dependencies if the doctor flagged them as missing but
	//    auto-installable, then re-run the doctor to refresh readiness.
	if doctorWantsPrepare(doctor) {
		if _, err := s.mobileProjectPreparePayload(projectDir); err != nil {
			res.add("prepare", false, err.Error())
			res.NextAction = "Installing your app's dependencies failed: " + err.Error() + ". Fix that (usually `npm install` in the app folder) and ask me to deploy again."
			return res
		}
		res.add("prepare", true, "installed project dependencies")
		doctor = mobileHermesDoctor(mobileHermesDoctorInput{Directory: projectDir})
	}

	// 3) Any hard blockers left → stop honestly with the doctor's plan.
	readyToBuild, _ := doctor["readyToBuildHermes"].(bool)
	if !readyToBuild {
		res.Blockers = stringSliceFromInterface(doctor["blockers"])
		res.NextActions = nextActionsFromDoctor(doctor)
		res.add("check", false, "project not ready to build")
		blockerText := strings.Join(res.Blockers, "; ")
		if blockerText == "" {
			blockerText = "the project isn't ready to build yet"
		}
		res.NextAction = "Almost there, but this blocks the build: " + blockerText + ". Resolve it and ask me to deploy again."
		return res
	}
	res.add("check", true, "ready to build the Hermes bundle")

	// plan_only: hand off the slow steps as explicit next calls.
	if args.PlanOnly {
		res.OK = true
		res.NextAction = "Ready to deploy. Call mobile_project_build then mobile_hermes_reload, or call mobile_deploy_to_phone again without plan_only to do both now."
		return res
	}

	// 4) Compile the Hermes bundle (the slow step).
	if _, err := s.mobileProjectBuildPayload(projectDir, args.Framework, platform); err != nil {
		res.add("build", false, err.Error())
		res.NextAction = "The Hermes build failed: " + err.Error() + ". Confirm the app builds locally, then ask me to deploy again."
		return res
	}
	res.add("build", true, "compiled the Hermes bundle")

	// 5) Push the bundle into the Yaver app on the paired phone.
	if errText := reloadError(mcpMobileHermesReload(mobileHermesReloadArgs{Mode: args.Mode})); errText != "" {
		res.add("reload", false, errText)
		res.Detail = errText
		res.NextAction = "Built the bundle, but pushing it to your phone failed. Open the Yaver app on your phone and make sure it's signed in with the same account as this machine, then ask me to reload."
		return res
	}
	res.add("reload", true, "pushed the bundle to the Yaver app on your phone")

	res.OK = true
	res.Done = true
	res.NextAction = "Done — your app is now running inside the Yaver app on your phone. Open Yaver there to see it. After you change code, just ask me to deploy again to reload."
	return res
}

// mobileDeployToPhoneRemote runs the chain against an owned remote device
// by proxying each step to the per-step HTTP endpoints that the individual
// mobile_* tools already use. The doctor runs on the device implicitly via
// build's own readiness errors, so we surface those rather than duplicating
// readiness logic across the wire.
func (s *HTTPServer) mobileDeployToPhoneRemote(ctx context.Context, args mobileDeployToPhoneArgs) mobileDeployResult {
	res := mobileDeployResult{}
	dev := strings.TrimSpace(args.DeviceID)
	platform := strings.TrimSpace(args.Platform)
	if platform == "" {
		platform = "ios"
	}

	if _, err := proxyToDeviceJSON(ctx, "mobile_deploy_to_phone", dev, http.MethodPost, "/mobile/project/prepare", map[string]any{"directory": args.Directory}); err != nil {
		res.add("prepare", false, err.Error())
		res.NextAction = "Couldn't prepare the project on " + dev + ": " + err.Error()
		return res
	}
	res.add("prepare", true, "dependencies ready on "+dev)

	if args.PlanOnly {
		res.OK = true
		res.NextAction = "Ready. Call mobile_project_build then mobile_hermes_reload with device_id=" + dev + "."
		return res
	}

	if _, err := proxyToDeviceJSON(ctx, "mobile_deploy_to_phone", dev, http.MethodPost, "/mobile/project/build", map[string]any{"directory": args.Directory, "framework": args.Framework, "platform": platform}); err != nil {
		res.add("build", false, err.Error())
		res.NextAction = "Hermes build failed on " + dev + ": " + err.Error()
		return res
	}
	res.add("build", true, "compiled the Hermes bundle on "+dev)

	if _, err := proxyToDeviceJSON(ctx, "mobile_deploy_to_phone", dev, http.MethodPost, "/dev/reload", mobileHermesReloadBody(mobileHermesReloadArgs{Mode: args.Mode})); err != nil {
		res.add("reload", false, err.Error())
		res.NextAction = "Built the bundle, but pushing it to your phone failed: " + err.Error() + ". Make sure the Yaver app is open and signed in with the same account."
		return res
	}
	res.add("reload", true, "pushed the bundle to your phone")

	res.OK = true
	res.Done = true
	res.NextAction = "Done — your app is now running inside the Yaver app on your phone."
	return res
}

// reloadError extracts an error string from the mcpMobileHermesReload
// result, or "" when the reload succeeded.
func reloadError(reload interface{}) string {
	m, ok := reload.(map[string]interface{})
	if !ok {
		return ""
	}
	if okFlag, has := m["ok"].(bool); has && !okFlag {
		if errText, _ := m["error"].(string); strings.TrimSpace(errText) != "" {
			return errText
		}
		return "reload failed"
	}
	return ""
}

// nextActionsFromDoctor pulls the ordered {tool,reason} list off a doctor
// result, tolerating both the native []map[string]string shape and a
// JSON-round-tripped []interface{} of maps.
func nextActionsFromDoctor(doctor map[string]interface{}) []map[string]string {
	if raw, ok := doctor["nextActions"].([]map[string]string); ok {
		return raw
	}
	if arr, ok := doctor["nextActions"].([]interface{}); ok {
		out := make([]map[string]string, 0, len(arr))
		for _, it := range arr {
			m, ok := it.(map[string]interface{})
			if !ok {
				continue
			}
			conv := map[string]string{}
			for k, v := range m {
				if sv, ok := v.(string); ok {
					conv[k] = sv
				}
			}
			out = append(out, conv)
		}
		return out
	}
	return nil
}

func doctorWantsPrepare(doctor map[string]interface{}) bool {
	for _, a := range nextActionsFromDoctor(doctor) {
		if a["tool"] == "mobile_project_prepare" {
			return true
		}
	}
	return false
}

func strFromDoctor(doctor map[string]interface{}, key string) string {
	s, _ := doctor[key].(string)
	return s
}
