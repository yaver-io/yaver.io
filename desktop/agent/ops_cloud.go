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

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
	registerOpsVerb(opsVerbSpec{
		Name:        "recycle",
		Description: "Recycle a BYO Hetzner box: create a fresh box, health-check it, then snapshot+delete the old one (zero-downtime; rolls back keeping the old box if the new one is unhealthy). Refuses to recycle the device this agent runs on. Destructive — confirm=true required; without it returns the plan (dry-run).",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"targetDeviceId", "oldServerId", "newName"},
			"properties": map[string]interface{}{
				"targetDeviceId": map[string]interface{}{"type": "string"},
				"oldServerId":    map[string]interface{}{"type": "string", "description": "Hetzner numeric id of the box being retired (explicit — never fuzzy-matched)"},
				"newName":        map[string]interface{}{"type": "string"},
				"plan":           map[string]interface{}{"type": "string"},
				"region":         map[string]interface{}{"type": "string"},
				"confirm":        map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRecycleHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "cloud_checkout",
		Description: "Start buying a Yaver managed-cloud box: returns a LemonSqueezy checkout URL to open in a browser. machineType=cpu (RN/Hermes + web + deploy, default) | gpu. Proxies the Convex /billing/yaver-cloud/checkout route with the user's token; the token never appears in the payload.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"machineType": map[string]interface{}{"type": "string", "description": "cpu (default) | gpu"},
				"region":      map[string]interface{}{"type": "string", "description": "eu (default) | us"},
			},
			"additionalProperties": false,
		},
		Handler:    opsCloudCheckoutHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

// opsCloudCheckoutHandler proxies the Convex checkout route so an
// agent can hand the user a pay link. The Convex route is owner/
// preview-gated (isCloudPreviewUser) + needs LemonSqueezy env — a 403
// or config error is surfaced verbatim, never swallowed.
func opsCloudCheckoutHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		MachineType string `json:"machineType"`
		Region      string `json:"region"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return OpsResult{OK: false, Code: "not_authed", Error: "agent not authed (missing convex site url / token) — run `yaver auth`"}
	}
	body, _ := json.Marshal(map[string]string{
		"machineType": p.MachineType,
		"region":      p.Region,
	})
	req, err := newBearerRequest("POST", cfg.ConvexSiteURL+"/billing/yaver-cloud/checkout", cfg.AuthToken, bytes.NewReader(body))
	if err != nil {
		return OpsResult{OK: false, Code: "request_error", Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return OpsResult{OK: false, Code: "convex_unreachable", Error: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return OpsResult{OK: false, Code: "checkout_failed", Error: fmt.Sprintf("checkout HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	var out struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.URL == "" {
		return OpsResult{OK: false, Code: "checkout_failed", Error: "checkout returned no url"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"checkoutUrl": out.URL,
		"mode":        out.Mode,
		"hint":        "open checkoutUrl in a browser to pay; the box auto-provisions on the LemonSqueezy webhook",
	}}
}

// opsRecycleHandler runs the BYO host-recycle state machine in-process
// (unlike provision/destroy which hand off to MCP tools — recycle is
// the whole orchestration and its safety guards must run server-side,
// never be re-implemented by each UI). Token is the user's vault
// Hetzner account token, never a payload field.
func opsRecycleHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var req recycleRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner not connected — /accounts/connect first (BYO token)"}
	}
	res := recycleHost(liveRecycleBackend{token: token}, req)
	if !res.OK {
		return OpsResult{OK: false, Code: "recycle_failed", Error: res.Error, Initial: res}
	}
	return OpsResult{OK: true, Initial: res}
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
