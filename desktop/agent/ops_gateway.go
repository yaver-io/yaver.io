package main

import "encoding/json"

// ops_gateway.go — relay-aware ops wrappers for the Personal Agent Gateway.
//
// The gateway already exposes MCP tools for host agents. Car, watch, TV, and
// phone surfaces need the same semantics over the normal ops path so they get
// machine routing, relay fallback, and one call shape. These handlers are thin
// adapters over the MCP entrypoints; the gateway implementation and safety
// gates stay in one place.

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "gateway_query",
		Description: "Personal Agent Gateway read: run one GET capability against a credentialed connector. Payload {connector, capability, params?}. Read-only; credentials stay in vault.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"connector", "capability"},
			"properties": map[string]interface{}{
				"connector":  map[string]interface{}{"type": "string"},
				"capability": map[string]interface{}{"type": "string"},
				"params":     map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
			},
			"additionalProperties": false,
		},
		Handler: gatewayQueryOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gateway_intent",
		Description: "Personal Agent Gateway intent router: route one natural-language utterance to code, a gateway read, or an action dry-run. Payload {utterance}. ACT never auto-executes.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"required":             []string{"utterance"},
			"properties":           map[string]interface{}{"utterance": map[string]interface{}{"type": "string"}},
			"additionalProperties": false,
		},
		Handler: gatewayIntentOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gateway_act",
		Description: "Personal Agent Gateway action path. Defaults to dry-run and returns act_id; execute=true still respects low/high/financial confirmation gates. Payload {connector, capability, params?, execute?}.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"connector", "capability"},
			"properties": map[string]interface{}{
				"connector":  map[string]interface{}{"type": "string"},
				"capability": map[string]interface{}{"type": "string"},
				"params":     map[string]interface{}{"type": "object", "additionalProperties": map[string]interface{}{"type": "string"}},
				"execute":    map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler: gatewayActOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "gateway_act_confirm",
		Description: "Confirm or decline a pending gateway action dry-run. Payload {act_id, answer}. answer='approve' executes; anything else declines.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"act_id"},
			"properties": map[string]interface{}{
				"act_id": map[string]interface{}{"type": "string"},
				"answer": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: gatewayActConfirmOpsHandler,
	})
}

func gatewayQueryOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Connector  string            `json:"connector"`
		Capability string            `json:"capability"`
		Params     map[string]string `json:"params"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	return gatewayOpsResult(mcpGatewayQuery(p.Connector, p.Capability, p.Params))
}

func gatewayIntentOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Utterance string `json:"utterance"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	return gatewayOpsResult(mcpGatewayIntent(p.Utterance))
}

func gatewayActOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Connector  string            `json:"connector"`
		Capability string            `json:"capability"`
		Params     map[string]string `json:"params"`
		Execute    bool              `json:"execute"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	return gatewayOpsResult(mcpGatewayAct(p.Connector, p.Capability, p.Params, p.Execute))
}

func gatewayActConfirmOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ActID  string `json:"act_id"`
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	return gatewayOpsResult(mcpGatewayActConfirm(p.ActID, p.Answer))
}

func gatewayOpsResult(v interface{}) OpsResult {
	if m, ok := v.(map[string]interface{}); ok {
		if errText, hasErr := m["error"].(string); hasErr && errText != "" {
			return OpsResult{OK: false, Code: "gateway_error", Error: errText, Initial: m}
		}
	}
	return OpsResult{OK: true, Initial: v}
}
