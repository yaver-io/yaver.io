package main

// device_broadcast_command MCP tool — generic BlackBox push without
// needing an active dev-server. Lets ANY authenticated MCP client
// (the mobile app's direct-MCP path, Claude Code on a remote dev box,
// ChatGPT Apps over OAuth, etc.) deliver a BlackBoxCommand to a
// specific connected SDK device OR broadcast to all subscribed ones.
//
// Why this exists alongside mobile_hermes_reload:
//   • mobile_hermes_reload requires a running dev-server (it computes
//     native-fingerprint deltas, talks to Metro, etc.) — useful when
//     you're actually iterating on code.
//   • device_broadcast_command does NOT need a dev-server. Just a
//     paired Yaver agent with a BlackBox session for the target device.
//     Lets Phone A trigger a reload (or any other SDK-recognised
//     command) on Phone B via a shared managed-cloud agent — the
//     Phase 8 "no dev box" scenario for users on the road.
//
// Tool surface (mcp_tools.go declares the inputSchema):
//
//   { command: string,            // required — what the SDK listener acts on
//                                 // ("reload", "reload_bundle", "open_app", …)
//     data?: object,              // optional — passed through verbatim
//     target_device_id?: string } // when set, scoped send; else broadcast
//
// Returns:
//
//   { ok: bool,
//     mode: "scoped" | "broadcast" | "no_blackbox",
//     targetDeviceId?: string,
//     reachedSession?: bool,      // true when a scoped session matched
//     error?: string }

import (
	"strings"
)

type deviceBroadcastCommandArgs struct {
	Command        string                 `json:"command"`
	Data           map[string]interface{} `json:"data,omitempty"`
	TargetDeviceID string                 `json:"target_device_id,omitempty"`
}

// runDeviceBroadcastCommand is the underlying dispatcher — split off
// from the MCP wrapper so the test can drive it directly with a
// constructed BlackBoxManager without spinning up the full HTTP stack.
func runDeviceBroadcastCommand(mgr *BlackBoxManager, args deviceBroadcastCommandArgs) map[string]interface{} {
	cmdName := strings.TrimSpace(args.Command)
	if cmdName == "" {
		return map[string]interface{}{
			"ok":    false,
			"mode":  "no_blackbox",
			"error": "command is required",
		}
	}
	if mgr == nil {
		return map[string]interface{}{
			"ok":    false,
			"mode":  "no_blackbox",
			"error": "agent has no BlackBox manager — pair this device first",
		}
	}

	cmd := BlackBoxCommand{Command: cmdName, Data: args.Data}
	target := strings.TrimSpace(args.TargetDeviceID)

	if target != "" {
		reached := mgr.SendCommandToDevice(target, cmd)
		return map[string]interface{}{
			"ok":             true,
			"mode":           "scoped",
			"targetDeviceId": target,
			"reachedSession": reached,
		}
	}

	mgr.BroadcastCommand(cmd)
	return map[string]interface{}{
		"ok":   true,
		"mode": "broadcast",
	}
}

func (s *HTTPServer) mcpDeviceBroadcastCommand(args deviceBroadcastCommandArgs) interface{} {
	return runDeviceBroadcastCommand(s.blackboxMgr, args)
}
