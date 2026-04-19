package main

// ops_cloud.go — cloud lifecycle verbs: provision / scale / destroy.
// Each one is a thin hand-off to the existing cloud_* MCP tools so
// agents calling ops uniformly never need to know the domain tool
// names. The handler returns the domain tool + payload template;
// mobile-headless / desktop wiring inside the same session can then
// dispatch the domain tool directly.
//
// We don't just fire-and-forget the domain tool because provision +
// destroy take minutes and deserve to surface a streamId directly.
// The follow-up expansion will have this verb actually call the
// handler in-process rather than pointing at the domain tool. For
// now the pointer pattern is enough for an agent to wire a flow.

import "encoding/json"

type opsProvisionPayload struct {
	Plan    string `json:"plan"`
	Region  string `json:"region,omitempty"`
	SSHKey  string `json:"sshKey,omitempty"`
	Label   string `json:"label,omitempty"`
}

type opsDestroyPayload struct {
	DeviceID string `json:"deviceId"`
	Confirm  bool   `json:"confirm"`
}

type opsScalePayload struct {
	DeviceID string `json:"deviceId"`
	CPU      int    `json:"cpu,omitempty"`
	RAMGb    int    `json:"ramGb,omitempty"`
	GPU      string `json:"gpu,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "provision",
		Description: "Provision a new Yaver-managed cloud machine (Hetzner CPU or GPU, pre-loaded with node/go/rust/docker/expo/eas/yaver). Routes to cloud_provision MCP tool; minutes-long — subscribe to its stream.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"plan"},
			"properties": map[string]interface{}{
				"plan":   map[string]interface{}{"type": "string", "description": "Plan id (cpu-small, gpu-4000, ...). Call cloud_plans to list."},
				"region": map[string]interface{}{"type": "string"},
				"sshKey": map[string]interface{}{"type": "string"},
				"label":  map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsProvisionHandler,
		Streaming:  true,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "scale",
		Description: "Resize a provisioned cloud machine (CPU / RAM / GPU). Routes to cloud_scale MCP tool.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"deviceId"},
			"properties": map[string]interface{}{
				"deviceId": map[string]interface{}{"type": "string"},
				"cpu":      map[string]interface{}{"type": "integer"},
				"ramGb":    map[string]interface{}{"type": "integer"},
				"gpu":      map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsScaleHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "destroy",
		Description: "Decommission a provisioned cloud machine. Requires confirm=true to guard against accidental calls. Routes to cloud_destroy MCP tool.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"deviceId", "confirm"},
			"properties": map[string]interface{}{
				"deviceId": map[string]interface{}{"type": "string"},
				"confirm":  map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsDestroyHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsProvisionHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsProvisionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Plan == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "plan is required"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call cloud_provision MCP tool with these args — returns a streamId; subscribe via /streams/<id> for bring-up progress",
		"mcpTool": "cloud_provision",
		"args":    p,
	}}
}

func opsScaleHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsScalePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.DeviceID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "deviceId required"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call cloud_scale MCP tool",
		"mcpTool": "cloud_scale",
		"args":    p,
	}}
}

func opsDestroyHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsDestroyPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.DeviceID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "deviceId required"}
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "unauthorized", Error: "destroy requires confirm=true"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"hint":    "call cloud_destroy MCP tool",
		"mcpTool": "cloud_destroy",
		"args":    p,
	}}
}
