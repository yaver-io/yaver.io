package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const hetznerActionPollLimit = 45

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "hetzner_power",
		Description: "List or change power state for servers in the owner's Hetzner account. The token stays in this endpoint's vault. action=list|power_on|shutdown; mutations require confirm=true. No create, delete, resize, console, or credential return capability.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":   map[string]interface{}{"type": "string", "enum": []string{"list", "power_on", "shutdown"}, "default": "list"},
				"serverId": map[string]interface{}{"type": "string", "description": "Exact Hetzner numeric server id; required for power_on/shutdown."},
				"confirm":  map[string]interface{}{"type": "boolean", "description": "Must be true for power_on/shutdown."},
			},
			"additionalProperties": false,
		},
		Handler:        opsHetznerPowerHandler,
		Streaming:      false,
		AllowCompanion: true,
	})
}

func opsHetznerPowerHandler(c OpsContext, payload json.RawMessage) OpsResult {
	// The legacy /d/<device> reverse proxy can read the HTTP body. Until this
	// verb is carried inside the endpoint E2EE envelope, refuse that transport:
	// infrastructure intent (action + server id) is application plaintext too.
	// Local MCP, LAN/TLS and Yaver Mesh/WireGuard calls do not carry this header.
	if c.RequestHeaders.Get("X-Yaver-Via-Relay") == "1" {
		return OpsResult{
			OK:    false,
			Code:  "secure_transport_required",
			Error: "Hetzner control requires a direct or Yaver Mesh encrypted connection",
		}
	}
	if surfaceFromHeaders(c.RequestHeaders) == SurfaceWeb {
		return OpsResult{
			OK:    false,
			Code:  "unsupported_surface",
			Error: "Hetzner control is disabled in browser surfaces; use a native trusted endpoint",
		}
	}
	var p struct {
		Action   string `json:"action"`
		ServerID string `json:"serverId"`
		Confirm  bool   `json:"confirm"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid Hetzner power request"}
		}
	}
	p.Action = strings.TrimSpace(p.Action)
	if p.Action == "" {
		p.Action = "list"
	}
	token := accountField(ProviderHetzner, "token")
	if token == "" {
		return OpsResult{OK: false, Code: "no_account", Error: "Hetzner is not connected on this trusted endpoint"}
	}
	manager, err := NewCloudDeployManager(".")
	if err != nil {
		return OpsResult{OK: false, Code: "manager_error", Error: "Hetzner client is unavailable"}
	}
	if p.Action == "list" {
		servers, listErr := manager.hetznerListServers(token)
		if listErr != nil {
			return OpsResult{OK: false, Code: "provider_error", Error: "Hetzner server list failed"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"servers": servers}}
	}
	if p.Action != "power_on" && p.Action != "shutdown" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "action must be list, power_on, or shutdown"}
	}
	serverID := strings.TrimSpace(p.ServerID)
	parsedID, parseErr := strconv.ParseUint(serverID, 10, 64)
	if parseErr != nil || parsedID == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "an exact numeric serverId is required"}
	}
	if !p.Confirm {
		return OpsResult{OK: false, Code: "confirmation_required", Error: "confirm=true is required for a power-state change"}
	}
	providerAction := "poweron"
	if p.Action == "shutdown" {
		providerAction = "shutdown"
	}
	if err := hetznerServerPower(token, serverID, providerAction); err != nil {
		return OpsResult{OK: false, Code: "provider_error", Error: "Hetzner power action failed"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"action": p.Action, "serverId": serverID, "verified": true,
	}}
}

func hetznerServerPower(token, serverID, action string) error {
	if action != "poweron" && action != "shutdown" {
		return fmt.Errorf("unsupported power action")
	}
	endpoint := fmt.Sprintf("%s/servers/%s/actions/%s", hetznerAPIBase, url.PathEscape(serverID), action)
	req, err := http.NewRequest(http.MethodPost, endpoint, nil) //nolint:noctx
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	var body struct {
		Action struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"action"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode >= 400 || decodeErr != nil || body.Action.ID <= 0 {
		return fmt.Errorf("provider rejected power action")
	}
	for attempt := 0; attempt < hetznerActionPollLimit; attempt++ {
		switch body.Action.Status {
		case "success":
			return nil
		case "error":
			return fmt.Errorf("provider action failed")
		case "running":
		default:
			return fmt.Errorf("provider returned invalid action state")
		}
		time.Sleep(2 * time.Second)
		statusURL := fmt.Sprintf("%s/actions/%d", hetznerAPIBase, body.Action.ID)
		statusReq, reqErr := http.NewRequest(http.MethodGet, statusURL, nil) //nolint:noctx
		if reqErr != nil {
			return reqErr
		}
		statusReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		statusResp, requestErr := httpClient.Do(statusReq)
		if requestErr != nil {
			return requestErr
		}
		decodeErr = json.NewDecoder(statusResp.Body).Decode(&body)
		statusResp.Body.Close()
		if statusResp.StatusCode >= 400 || decodeErr != nil {
			return fmt.Errorf("provider action status failed")
		}
	}
	return fmt.Errorf("provider action timed out")
}
