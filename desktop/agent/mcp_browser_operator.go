package main

// mcp_browser_operator.go — discoverable entry point for the single-machine
// browser agent (ops_browser_operator.go). Thin adapter that dispatches the
// `browser_operator` ops verb locally and returns its step trace.

import (
	"context"
	"encoding/json"
	"strings"
)

func browserOperatorMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "browser_operator",
			"description": "Drive a real browser on THIS machine toward a plain-language goal, one grounded action " +
				"at a time (navigate / click / type / scroll / wait), and return a step trace. engine defaults to " +
				"chrome; firefox and safari use real W3C WebDriver. Use this for multi-step web tasks; use " +
				"browser_open + browser_click for a single known step.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"goal"},
				"properties": map[string]interface{}{
					"goal":       map[string]interface{}{"type": "string", "description": "What to accomplish, in plain language."},
					"engine":     map[string]interface{}{"type": "string", "enum": []string{"chrome", "firefox", "safari"}},
					"startUrl":   map[string]interface{}{"type": "string", "description": "Optional URL to open first."},
					"sessionId":  map[string]interface{}{"type": "string", "description": "Reuse an open browser session; omit to open one."},
					"headful":    map[string]interface{}{"type": "boolean", "description": "Show the browser window (default headless; Safari is always headful)."},
					"profile":    map[string]interface{}{"type": "string", "description": "Persistent profile name so cookies/clearance persist."},
					"maxSteps":   map[string]interface{}{"type": "integer", "description": "Action cap (default 10)."},
					"timeoutSec": map[string]interface{}{"type": "integer", "description": "Total wall-clock budget (default 300)."},
				},
			},
		},
	}
}

func (s *HTTPServer) handleBrowserOperatorMCP(args json.RawMessage) interface{} {
	var a struct {
		Goal       string `json:"goal"`
		Engine     string `json:"engine"`
		StartURL   string `json:"startUrl"`
		SessionID  string `json:"sessionId"`
		Headful    bool   `json:"headful"`
		Profile    string `json:"profile"`
		MaxSteps   int    `json:"maxSteps"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return mcpToolError("invalid browser_operator arguments: " + err.Error())
		}
	}
	if strings.TrimSpace(a.Goal) == "" {
		return mcpToolError("browser_operator: `goal` is required")
	}
	payload := map[string]interface{}{"goal": a.Goal}
	if a.Engine != "" {
		payload["engine"] = a.Engine
	}
	if a.StartURL != "" {
		payload["startUrl"] = a.StartURL
	}
	if a.SessionID != "" {
		payload["sessionId"] = a.SessionID
	}
	if a.Headful {
		payload["headful"] = true
	}
	if a.Profile != "" {
		payload["profile"] = a.Profile
	}
	if a.MaxSteps != 0 {
		payload["maxSteps"] = a.MaxSteps
	}
	if a.TimeoutSec != 0 {
		payload["timeoutSec"] = a.TimeoutSec
	}
	raw, _ := json.Marshal(payload)
	octx := OpsContext{Ctx: context.Background(), Server: s, Caller: "owner"}
	out := dispatchOps(octx, OpsRequest{Machine: "local", Verb: "browser_operator", Payload: raw})
	if !out.OK {
		return mcpToolError("browser_operator: " + opsErrText(out))
	}
	return desktopOpsResult(out, "local")
}
