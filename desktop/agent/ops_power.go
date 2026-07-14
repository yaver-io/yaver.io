package main

// Power control as an ops verb.
//
// The reboot capability already existed on three surfaces — the phone's Infra
// tab, the web dashboard's Infra view, and the `infra_power` MCP tool — but all
// three go straight to a machine's own /infra/power route, so the CLI (and any
// MCP client driving the ops grand-tool) had no way to reboot a machine at all.
//
// Registering it as an ops verb closes that with one entry: ops already forwards
// any verb to `--machine=<id>`, so this simultaneously gives us
// `yaver reboot --machine=box`, `yaver ops infra_power --machine=box`, and the
// verb through the `ops` MCP tool — on top of the existing phone/web buttons.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "infra_power",
		Description: "Power-control a machine: reboot the host, or stop the Yaver agent on it. " +
			"Requires confirm=true — a reboot kills every running task, build and runner on that box. " +
			"host_reboot needs root or passwordless sudo on the target; when it doesn't have it, " +
			"infra_summary reports capabilities.hostReboot=false and this verb explains how to grant it.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"action", "confirm"},
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"host_reboot", "agent_shutdown"},
					"description": "host_reboot = reboot the machine. agent_shutdown = stop the Yaver agent, leaving the machine up.",
				},
				"confirm": map[string]interface{}{
					"type":        "boolean",
					"description": "Must be true. Destructive to in-flight work on the target.",
				},
			},
			"additionalProperties": false,
		},
		Handler:    opsInfraPowerHandler,
		Streaming:  false,
		AllowGuest: false, // never let a guest reboot the owner's machine
	})
}

func opsInfraPowerHandler(octx OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	if !req.Confirm {
		return OpsResult{OK: false, Code: "confirm_required",
			Error: "confirm=true is required — this stops every task, build and runner on the machine"}
	}

	switch strings.TrimSpace(req.Action) {
	case "agent_shutdown":
		if octx.Server == nil || octx.Server.onShutdown == nil {
			return OpsResult{OK: false, Code: "unsupported",
				Error: "this agent has no shutdown hook wired"}
		}
		// Answer BEFORE dying, or the caller only ever sees a dropped connection.
		go octx.Server.onShutdown()
		return OpsResult{OK: true, Initial: map[string]interface{}{"action": "agent_shutdown"}}
	case "host_reboot":
		command, err := infraHostReboot()
		if err != nil {
			return OpsResult{OK: false, Code: "reboot_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"action": "host_reboot", "command": command}}
	default:
		return OpsResult{OK: false, Code: "bad_action",
			Error: fmt.Sprintf("unsupported power action %q — use host_reboot or agent_shutdown", req.Action)}
	}
}
