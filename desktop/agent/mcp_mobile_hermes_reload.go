package main

// mobile_hermes_reload MCP tool — thin wrapper over the existing
// POST /dev/reload HTTP handler so any MCP client (Claude Code on a
// remote dev box, the Yaver mobile app's direct-MCP path, ChatGPT
// Apps over OAuth, etc.) can trigger a Hermes hot-reload of the RN
// app under test without going through an LLM round-trip.
//
// This is Phase 2 of REMOTE_MCP_HERMES_RELOAD_PLAN.md. The output
// mirrors what /dev/reload returns plus a small contract guarantee
// for callers that don't know about native-fingerprint nuances:
//
//   {
//     "ok": true|false,
//     "changeClass": "js_only" | "native_rebuild_required" | "unknown",
//     "nativeChangesDetected": bool,
//     "nativeChanges": [ { Path, Reason } ],
//     "error": "..."   // when ok=false
//   }
//
// Optional args (Phase 7 cross-device targeting — wired now so the
// MCP surface is stable even if the server-side filter lands later):
//
//   - target_device_id : string  — when set, only deliver the BlackBox
//     reload command to the matching SDK device id. Empty/omitted =
//     broadcast to all subscribed devices (existing behaviour).
//   - mode             : "dev" | "bundle" (default "dev")

import (
	"fmt"
	"strings"
)

type mobileHermesReloadArgs struct {
	DeviceID       string `json:"device_id,omitempty"`
	TargetDeviceID string `json:"target_device_id,omitempty"`
	Mode           string `json:"mode,omitempty"`
}

func mobileHermesReloadBody(args mobileHermesReloadArgs) map[string]interface{} {
	body := map[string]interface{}{}
	if mode := strings.TrimSpace(args.Mode); mode != "" {
		body["mode"] = mode
	}
	if id := strings.TrimSpace(args.TargetDeviceID); id != "" {
		// The /dev/reload handler does not yet honour a per-device
		// scope (Phase 7 work). Pass the hint through anyway so a
		// future agent rev can pick it up without an MCP-side bump.
		body["targetDeviceId"] = id
	}
	return body
}

func mcpMobileHermesReload(args mobileHermesReloadArgs) interface{} {
	body := mobileHermesReloadBody(args)

	resp, err := localAgentRequest("POST", "/dev/reload", body)
	if err != nil {
		return map[string]interface{}{
			"ok":          false,
			"changeClass": "unknown",
			"error":       fmt.Sprintf("/dev/reload failed: %v", err),
		}
	}

	// /dev/reload already returns the shape we want; just normalise the
	// missing-baseline case so callers don't have to special-case "".
	changeClass, _ := resp["changeClass"].(string)
	if changeClass == "" {
		changeClass = "unknown"
	}
	resp["changeClass"] = changeClass
	if _, hasOk := resp["ok"]; !hasOk {
		resp["ok"] = true
	}
	return resp
}
