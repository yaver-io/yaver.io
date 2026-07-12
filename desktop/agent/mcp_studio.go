package main

import (
	"context"
	"encoding/json"
	"strings"
)

// dispatchStudioMCP exposes the Studio permission-proof workflow as first-class
// MCP tools while reusing the ops implementation that the mobile Studio UI uses.
func dispatchStudioMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "studio_permission_prose", "studio_permission_video", "studio_job_status":
	default:
		return false, nil
	}

	var envelope struct {
		DeviceID string `json:"device_id"`
	}
	_ = json.Unmarshal(arguments, &envelope)

	payload := arguments
	if strings.TrimSpace(envelope.DeviceID) != "" {
		var obj map[string]interface{}
		if err := json.Unmarshal(arguments, &obj); err == nil {
			delete(obj, "device_id")
			if b, err := json.Marshal(obj); err == nil {
				payload = b
			}
		}
	}

	return true, mcpToolJSON(dispatchOps(OpsContext{
		Ctx:    context.Background(),
		Server: s,
		Caller: "owner",
	}, OpsRequest{
		Machine: studioMCPMachine(envelope.DeviceID),
		Verb:    name,
		Payload: payload,
	}))
}

func studioMCPMachine(deviceID string) string {
	if d := strings.TrimSpace(deviceID); d != "" {
		return d
	}
	return "local"
}
