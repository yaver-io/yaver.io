package main

// ops_ghost.go — UI ghost verbs. These expose Yaver's cross-OS GUI-automation
// library (desktop/agent/ghost) as ops verbs so a remote Commander (e.g. Talos
// over the mesh) can drive this machine's desktop: screenshot, click, type,
// key, scroll, move — plus a vision one-shot (ghost_locate) that asks an
// OpenAI-compatible model where to act.
//
// Security posture (Phase 1):
//   - Opt-in only: verbs refuse unless the agent was started with --ghost
//     (config.GhostEnabled). The capability is advertised via
//     MachineCapabilities.SupportsGhostUI.
//   - Owner-only: AllowGuest is false on every verb (guests are refused at the
//     dispatcher). A least-privilege "ghost_ui" SDK-token scope is a follow-up
//     in the Talos-driver phase.
//   - Auto-remote: ops verbs route to a target device via dispatchOps/
//     proxyToDevice with no extra wiring, tagged X-Yaver-Proxied-Tool for audit.
//   - Privacy: screenshots/coordinates/keystrokes are returned to the caller
//     but never synced to Convex (see convex_privacy_test.go).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/yaver-io/agent/ghost"
)

// ensureGhost lazily constructs the ghost engine. Non-ghost agents never pay
// for it. Returns ErrUnsupported's wrapped error on a platform without an impl.
func (s *HTTPServer) ensureGhost() (*ghost.Engine, error) {
	s.ghostOnce.Do(func() {
		s.ghostEngine, s.ghostErr = ghost.New()
	})
	return s.ghostEngine, s.ghostErr
}

// ghostEngineForOps centralizes the opt-in + platform gate so every verb fails
// identically and safely.
func ghostEngineForOps(c OpsContext) (*ghost.Engine, *OpsResult) {
	if c.Server == nil {
		return nil, &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.ghostEnabled {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "GUI ghost is disabled on this agent; start it with `yaver serve --ghost`"}
	}
	eng, err := c.Server.ensureGhost()
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: "GUI ghost is not available on this OS yet: " + err.Error()}
	}
	return eng, nil
}

func ghostJSONSchema(props map[string]interface{}, required ...string) map[string]interface{} {
	s := map[string]interface{}{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_screenshot",
		Description: "Capture a screenshot of the target machine's desktop (primary display). Returns a base64 PNG plus width/height. Requires the agent to run with --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"display": map[string]interface{}{"type": "integer", "description": "Display index (default 0 = primary)."},
		}),
		Handler:    ghostScreenshotHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_click",
		Description: "Click at screen pixel (x,y). Optional button (left/right/middle) and double-click. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"x":      map[string]interface{}{"type": "integer"},
			"y":      map[string]interface{}{"type": "integer"},
			"button": map[string]interface{}{"type": "string", "enum": []string{"left", "right", "middle"}},
			"double": map[string]interface{}{"type": "boolean"},
		}, "x", "y"),
		Handler:    ghostClickHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_type",
		Description: "Type Unicode text into the focused control (layout-independent). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"text": map[string]interface{}{"type": "string"},
		}, "text"),
		Handler:    ghostTypeHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_key",
		Description: "Press a key chord, e.g. keys=[\"ctrl\",\"s\"] or [\"enter\"]. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"keys": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
		}, "keys"),
		Handler:    ghostKeyHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_scroll",
		Description: "Scroll the wheel by dx/dy notches (positive dy scrolls up). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"dx": map[string]interface{}{"type": "integer"},
			"dy": map[string]interface{}{"type": "integer"},
		}),
		Handler:    ghostScrollHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_move",
		Description: "Move the mouse cursor to screen pixel (x,y). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"x": map[string]interface{}{"type": "integer"},
			"y": map[string]interface{}{"type": "integer"},
		}, "x", "y"),
		Handler:    ghostMoveHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_windows",
		Description: "List top-level windows via the OS accessibility tree (Phase 2; returns unsupported on Phase 1). Requires --ghost.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     ghostWindowsHandler,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_locate",
		Description: "Vision one-shot: screenshot the desktop and ask an OpenAI-compatible model (OpenRouter / local Ollama-vLLM / any gateway) for the next action toward `instruction`. With execute=true the action is performed. Provider via payload baseUrl/apiKey/model or GHOST_VISION_* / OPENAI_* env. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"instruction": map[string]interface{}{"type": "string", "description": "Natural-language target, e.g. 'click the Save button'."},
			"execute":     map[string]interface{}{"type": "boolean", "description": "If true, perform the located action (default false = locate only)."},
			"display":     map[string]interface{}{"type": "integer"},
			"baseUrl":     map[string]interface{}{"type": "string", "description": "OpenAI-compatible base URL (e.g. http://localhost:11434/v1)."},
			"apiKey":      map[string]interface{}{"type": "string"},
			"model":       map[string]interface{}{"type": "string"},
		}, "instruction"),
		Handler:    ghostLocateHandler,
		AllowGuest: false,
	})
}

func ghostUnmarshal(payload json.RawMessage, v interface{}) *OpsResult {
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return &OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	return nil
}

func ghostScreenshotHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Display int `json:"display"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	pngBytes, img, err := eng.CapturePNG(p.Display)
	if err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	b := img.Bounds()
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"display":   p.Display,
		"width":     b.Dx(),
		"height":    b.Dy(),
		"pngBase64": base64.StdEncoding.EncodeToString(pngBytes),
	}}
}

func ghostClickHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		X      int    `json:"x"`
		Y      int    `json:"y"`
		Button string `json:"button"`
		Double bool   `json:"double"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	btn := ghost.Button(p.Button)
	if btn == "" {
		btn = ghost.ButtonLeft
	}
	var err error
	if p.Double {
		err = eng.Input.DoubleClick(btn, p.X, p.Y)
	} else {
		err = eng.Input.Click(btn, p.X, p.Y)
	}
	return ghostActResult(err, map[string]interface{}{"x": p.X, "y": p.Y, "button": string(btn), "double": p.Double})
}

func ghostTypeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Text string `json:"text"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	return ghostActResult(eng.Input.TypeText(p.Text), map[string]interface{}{"chars": len([]rune(p.Text))})
}

func ghostKeyHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Keys []string `json:"keys"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if len(p.Keys) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "keys is required"}
	}
	return ghostActResult(eng.Input.KeyCombo(p.Keys...), map[string]interface{}{"keys": p.Keys})
}

func ghostScrollHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		DX int `json:"dx"`
		DY int `json:"dy"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	return ghostActResult(eng.Input.Scroll(p.DX, p.DY), map[string]interface{}{"dx": p.DX, "dy": p.DY})
}

func ghostMoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	return ghostActResult(eng.Input.Move(p.X, p.Y), map[string]interface{}{"x": p.X, "y": p.Y})
}

func ghostWindowsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	wins, err := eng.Tree.Windows()
	if err != nil {
		return OpsResult{OK: false, Code: "unsupported", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"windows": wins}}
}

func ghostLocateHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := ghostEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Instruction string `json:"instruction"`
		Execute     bool   `json:"execute"`
		Display     int    `json:"display"`
		BaseURL     string `json:"baseUrl"`
		APIKey      string `json:"apiKey"`
		Model       string `json:"model"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.Instruction == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "instruction is required"}
	}
	loc, err := newVisionLocator(p.BaseURL, p.APIKey, p.Model)
	if err != nil {
		return OpsResult{OK: false, Code: "no_vision", Error: err.Error()}
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var act ghost.Action
	if p.Execute {
		act, err = eng.Act(ctx, loc, p.Instruction, p.Display)
	} else {
		var pngBytes []byte
		pngBytes, _, err = eng.CapturePNG(p.Display)
		if err == nil {
			act, err = loc.Locate(ctx, pngBytes, p.Instruction)
		}
	}
	if err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"action": act, "executed": p.Execute}}
}

// ghostActResult is the shared success/failure shape for actuation verbs.
func ghostActResult(err error, info map[string]interface{}) OpsResult {
	if err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: info}
}
