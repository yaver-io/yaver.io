package main

// mcp_desktop.go — first-class, discoverable "computer use" tools.
//
// WHY THESE EXIST
// The ghost primitives are real and work: screen capture, mouse/keyboard
// injection, and an accessibility tree on macOS/Windows/Linux, with
// element-by-name actuation and closed-loop verification
// (ops_ghost_element.go). But they were only reachable through the generic
// `ops` grand-tool: an MCP client sees ~275 named tools and has to know to call
// `ops_verbs` first to discover that it can drive a desktop at all. The July
// 2026 remote-PC audit named this exactly — "the semantic layer is present but
// unused". These three tools are the advertisement: they make the capability
// visible to the model, so a plain "look at my screen and click Save" works
// without the caller knowing Yaver's verb protocol.
//
// WHAT THEY ARE (and are not)
// Thin adapters over the existing ghost ops verbs, dispatched through
// dispatchOps so the full mesh path is reused (remote `machine` targeting,
// same-LAN/Tailscale/relay candidate selection, auth). The `--ghost` gate is
// UNCHANGED — if the agent was not started with `--ghost`, these return the
// same clear refusal the ops verbs do. No security boundary moves; only
// discoverability changes.
//
// VISION-FIRST
// desktop_screenshot returns a real MCP image block (the robot_camera /
// circuit_plot pattern), so a vision-capable runner (Claude Code / Codex) sees
// pixels. A text-only runner (opencode driving deepseek) is auto-adapted by
// finalizeMCPResult (mcp_vision.go): the image is rewritten to dims + on-device
// OCR + an optional vision-LLM verdict. Both clients are first-class.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// desktopMCPTools returns the named computer-use tools.
func desktopMCPTools() []map[string]interface{} {
	machineProp := func() map[string]interface{} {
		return map[string]interface{}{
			"type":        "string",
			"description": "Owned device id / name / alias to target (default: this machine). Resolves over the same LAN/mesh/relay path as `ops`.",
		}
	}
	return []map[string]interface{}{
		{
			"name": "desktop_screenshot",
			"description": "See the target machine's desktop as an image. Use this to look before acting " +
				"(and to verify an action landed). Returns a viewable image; text-only models receive an " +
				"OCR/description adaptation automatically. Requires the agent to run with --ghost.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"machine": machineProp(),
					"display": map[string]interface{}{
						"type":        "integer",
						"description": "Display index (default 0 = primary). Multi-monitor is not implemented yet.",
					},
				},
			},
		},
		{
			"name": "desktop_elements",
			"description": "List accessibility elements (buttons, fields, menus) in the FOCUSED application on the " +
				"target machine, by name/role, with bounds and a precomputed click point. PREFER this over " +
				"desktop_screenshot + guessing coordinates: it is the ground truth for what is on screen. " +
				"Requires --ghost.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"machine": machineProp(),
					"query":   map[string]interface{}{"type": "string", "description": "Case-insensitive substring of the element's name, value or automationId."},
					"role":    map[string]interface{}{"type": "string", "description": "Exact role filter, e.g. AXButton / Button / push button."},
					"exact":   map[string]interface{}{"type": "boolean", "description": "Require a whole-field match instead of substring."},
				},
			},
		},
		{
			"name": "desktop_act",
			"description": "Drive the target machine's desktop. By name (preferred): action=click_element/type_into_element with `query`, " +
				"optionally `expect` to VERIFY the click landed (fails code=unverified if it did not). By coordinate: click/double_click/move/scroll. " +
				"Also: type/key/launch (start an app by name)/focus (raise a running app). Requires --ghost.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"action"},
				"properties": map[string]interface{}{
					"machine": machineProp(),
					"action": map[string]interface{}{
						"type": "string",
						"enum": []string{"click_element", "type_into_element", "click", "double_click", "move", "scroll", "type", "key", "launch", "focus"},
						"description": "click_element / type_into_element act by element name; click/double_click/move need x,y; " +
							"type/key/scroll take text/keys/dx,dy; launch/focus take app.",
					},
					"query":    map[string]interface{}{"type": "string", "description": "Element name for click_element / type_into_element."},
					"role":     map[string]interface{}{"type": "string", "description": "Optional role filter to disambiguate `query`."},
					"index":    map[string]interface{}{"type": "integer", "description": "Which match to act on when several tie (from desktop_elements)."},
					"expect":   map[string]interface{}{"type": "string", "description": "CLOSED LOOP: an element name that must appear after the action; the verb fails code=unverified if it does not."},
					"x":        map[string]interface{}{"type": "integer", "description": "Screen pixel X (coordinate actions)."},
					"y":        map[string]interface{}{"type": "integer", "description": "Screen pixel Y (coordinate actions)."},
					"button":   map[string]interface{}{"type": "string", "enum": []string{"left", "right", "middle"}},
					"double":   map[string]interface{}{"type": "boolean", "description": "Double-click (coordinate click)."},
					"text":     map[string]interface{}{"type": "string", "description": "Text to type."},
					"keys":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Key chord for action=key, e.g. [\"ctrl\",\"s\"] or [\"enter\"]."},
					"dx":       map[string]interface{}{"type": "integer", "description": "Scroll delta X (action=scroll)."},
					"dy":       map[string]interface{}{"type": "integer", "description": "Scroll delta Y (action=scroll)."},
					"app":      map[string]interface{}{"type": "string", "description": "Application name for action=launch or focus, e.g. \"Safari\"."},
					"clear":    map[string]interface{}{"type": "boolean", "description": "For type_into_element: select-all then overwrite."},
					"noVerify": map[string]interface{}{"type": "boolean", "description": "For type_into_element: skip the read-back check (default false = verify the value landed)."},
				},
			},
		},
		{
			"name": "desktop_operator",
			"description": "One goal, many steps: drive the target machine toward a plain-language goal using the configured " +
				"vision model (observe → decide → act → repeat), bounded by step/time budgets, returning a step trace of " +
				"what was tried and where it stopped. Use this for multi-step tasks; use desktop_act for a single known " +
				"action. Requires --ghost and a vision provider.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"goal"},
				"properties": map[string]interface{}{
					"machine":    machineProp(),
					"goal":       map[string]interface{}{"type": "string", "description": "What to accomplish, in plain language."},
					"maxSteps":   map[string]interface{}{"type": "integer", "description": "Iteration cap (default 8)."},
					"timeoutSec": map[string]interface{}{"type": "integer", "description": "Total wall-clock budget in seconds (default 300)."},
					"display":    map[string]interface{}{"type": "integer", "description": "Display index to capture (default 0 = primary)."},
				},
			},
		},
	}
}

// desktopArgs is the union of the three tools' inputs.
type desktopArgs struct {
	Machine    string   `json:"machine"`
	Display    int      `json:"display"`
	Action     string   `json:"action"`
	Query      string   `json:"query"`
	Role       string   `json:"role"`
	Index      int      `json:"index"`
	Expect     string   `json:"expect"`
	Exact      bool     `json:"exact"`
	X          int      `json:"x"`
	Y          int      `json:"y"`
	Button     string   `json:"button"`
	Double     bool     `json:"double"`
	Text       string   `json:"text"`
	Keys       []string `json:"keys"`
	DX         int      `json:"dx"`
	DY         int      `json:"dy"`
	App        string   `json:"app"`
	Clear      bool     `json:"clear"`
	NoVerify   bool     `json:"noVerify"`
	Goal       string   `json:"goal"`
	MaxSteps   int      `json:"maxSteps"`
	TimeoutSec int      `json:"timeoutSec"`
}

// handleDesktopMCPTool dispatches one of the named desktop tools. It always
// goes through dispatchOps so remote targeting and the verb's own gate apply.
func (s *HTTPServer) handleDesktopMCPTool(name string, args json.RawMessage) interface{} {
	var a desktopArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return mcpToolError("invalid desktop arguments: " + err.Error())
		}
	}
	machine := strings.TrimSpace(a.Machine)
	if machine == "" {
		machine = "local"
	}
	octx := OpsContext{Ctx: context.Background(), Server: s, Caller: "owner"}
	run := func(verb string, payload map[string]interface{}) OpsResult {
		raw, _ := json.Marshal(payload)
		return dispatchOps(octx, OpsRequest{Machine: machine, Verb: verb, Payload: raw})
	}

	switch name {
	case "desktop_screenshot":
		payload := map[string]interface{}{}
		if a.Display != 0 {
			payload["display"] = a.Display
		}
		out := run("ghost_screenshot", payload)
		if !out.OK {
			return mcpToolError("desktop_screenshot (" + machine + "): " + opsErrText(out))
		}
		m, _ := out.Initial.(map[string]interface{})
		b64, _ := m["pngBase64"].(string)
		if b64 == "" {
			return mcpToolError("desktop_screenshot (" + machine + "): no image returned")
		}
		text := fmt.Sprintf("Desktop of %s (display %v, %vx%v).", machine, m["display"], m["width"], m["height"])
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": text},
				{"type": "image", "data": b64, "mimeType": "image/png"},
			},
		}

	case "desktop_elements":
		payload := map[string]interface{}{}
		if a.Query != "" {
			payload["query"] = a.Query
		}
		if a.Role != "" {
			payload["role"] = a.Role
		}
		if a.Exact {
			payload["exact"] = true
		}
		return desktopOpsResult(run("ghost_elements", payload), machine)

	case "desktop_act":
		verb, payload, errMsg := desktopActionToVerb(a)
		if errMsg != "" {
			return mcpToolError(errMsg)
		}
		return desktopOpsResult(run(verb, payload), machine)

	case "desktop_operator":
		if strings.TrimSpace(a.Goal) == "" {
			return mcpToolError("desktop_operator: `goal` is required")
		}
		payload := map[string]interface{}{"goal": a.Goal}
		if a.MaxSteps != 0 {
			payload["maxSteps"] = a.MaxSteps
		}
		if a.TimeoutSec != 0 {
			payload["timeoutSec"] = a.TimeoutSec
		}
		if a.Display != 0 {
			payload["display"] = a.Display
		}
		return desktopOpsResult(run("desktop_operator", payload), machine)
	}
	return mcpToolError("unknown desktop tool: " + name)
}

// desktopActionToVerb maps the one `desktop_act` surface onto the existing ghost
// verbs, so there is a single implementation of each action.
func desktopActionToVerb(a desktopArgs) (string, map[string]interface{}, string) {
	hasQuery := strings.TrimSpace(a.Query) != ""
	byName := func(verb string) (string, map[string]interface{}, string) {
		if !hasQuery {
			return "", nil, verb + ": `query` (element name) is required"
		}
		p := map[string]interface{}{"query": a.Query}
		if a.Role != "" {
			p["role"] = a.Role
		}
		if a.Index != 0 {
			p["index"] = a.Index
		}
		if a.Expect != "" {
			p["expect"] = a.Expect
		}
		return verb, p, ""
	}

	switch a.Action {
	case "click_element":
		return byName("ghost_click_element")
	case "type_into_element":
		if !hasQuery {
			return "", nil, "type_into_element: `query` (field name) is required"
		}
		p := map[string]interface{}{"query": a.Query, "text": a.Text}
		if a.Role != "" {
			p["role"] = a.Role
		}
		if a.Index != 0 {
			p["index"] = a.Index
		}
		if a.Clear {
			p["clear"] = true
		}
		if a.NoVerify {
			p["noVerify"] = true
		}
		return "ghost_type_into_element", p, ""
	case "click", "double_click":
		p := map[string]interface{}{"x": a.X, "y": a.Y}
		if a.Button != "" {
			p["button"] = a.Button
		}
		if a.Action == "double_click" || a.Double {
			p["double"] = true
		}
		return "ghost_click", p, ""
	case "move":
		return "ghost_move", map[string]interface{}{"x": a.X, "y": a.Y}, ""
	case "scroll":
		return "ghost_scroll", map[string]interface{}{"dx": a.DX, "dy": a.DY}, ""
	case "type":
		if a.Text == "" {
			return "", nil, "type: `text` is required"
		}
		return "ghost_type", map[string]interface{}{"text": a.Text}, ""
	case "key":
		if len(a.Keys) == 0 {
			return "", nil, "key: `keys` is required, e.g. [\"ctrl\",\"s\"]"
		}
		return "ghost_key", map[string]interface{}{"keys": a.Keys}, ""
	case "launch":
		if strings.TrimSpace(a.App) == "" {
			return "", nil, "launch: `app` is required, e.g. \"Safari\""
		}
		return "ghost_launch_app", map[string]interface{}{"app": a.App}, ""
	case "focus":
		if strings.TrimSpace(a.App) == "" {
			return "", nil, "focus: `app` is required, e.g. \"Safari\""
		}
		return "ghost_focus_app", map[string]interface{}{"app": a.App}, ""
	}
	return "", nil, "unknown action: " + a.Action
}

func opsErrText(out OpsResult) string {
	if out.Code != "" {
		return out.Code + ": " + out.Error
	}
	if out.Error != "" {
		return out.Error
	}
	return "failed"
}

func desktopOpsResult(out OpsResult, machine string) interface{} {
	if !out.OK {
		return mcpToolError("desktop (" + machine + "): " + opsErrText(out))
	}
	if out.Initial == nil {
		return mcpToolResult(`{"ok":true}`)
	}
	b, err := json.MarshalIndent(out.Initial, "", "  ")
	if err != nil {
		return mcpToolResult(`{"ok":true}`)
	}
	return mcpToolResult(string(b))
}
