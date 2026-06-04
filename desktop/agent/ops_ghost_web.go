package main

// ops_ghost_web.go — WEB ghost verbs. The other half of "ghost mode": for
// web-UI ERPs (DIA, Tiger Wings, j-Platform, ERPNext, …) the ghost drives a
// headless browser instead of a native desktop. This is cross-platform via
// chromedp, so it runs directly on an on-prem Raspberry Pi appliance (ARM
// Linux) — no Windows box needed. Native-desktop ERPs (Logo Tiger 3, Mikro,
// Netsis) use the ghost_* desktop verbs on a Windows Slave instead.
//
// Routing is the driver's job (Talos): web ERP → ghost_web_* on the appliance;
// desktop ERP → ghost_* on the Windows Slave. Both are ops verbs, both gated by
// --ghost, both mesh-drivable via the same path.
//
// Wraps the existing BrowserManager (browser.go), created at serve boot
// (main.go). Selector-based (robust) with screenshots for vision fallback.

import (
	"encoding/json"
)

func webGhostForOps(c OpsContext) (*BrowserManager, *OpsResult) {
	if c.Server == nil {
		return nil, &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.ghostEnabled {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "GUI ghost is disabled on this agent; start it with `yaver serve --ghost`"}
	}
	if c.Server.browserMgr == nil {
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: "browser automation unavailable (Chrome/Chromium missing)"}
	}
	return c.Server.browserMgr, nil
}

func webSession(payloadID string) string {
	if payloadID == "" {
		return "ghost"
	}
	return payloadID
}

func webResult(r *BrowserActionResult, err error) OpsResult {
	if err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	out := map[string]interface{}{}
	if r != nil {
		out["url"] = r.URL
		out["title"] = r.Title
		if r.Message != "" {
			out["message"] = r.Message
		}
		if r.ScreenshotB64 != "" {
			out["pngBase64"] = r.ScreenshotB64
		}
	}
	return OpsResult{OK: true, Initial: out}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_open",
		Description: "Open a headless browser session for web-ERP ghosting (runs on any OS incl. a Raspberry Pi appliance). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string", "description": "session name (default 'ghost')"},
			"headful":   map[string]interface{}{"type": "boolean"},
		}),
		Handler:    ghostWebOpenHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_goto",
		Description: "Navigate the web ghost to a URL (auto-opens the session). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
			"url":       map[string]interface{}{"type": "string"},
		}, "url"),
		Handler:    ghostWebGotoHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_click",
		Description: "Click a CSS selector in the web ghost. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
			"selector":  map[string]interface{}{"type": "string"},
		}, "selector"),
		Handler:    ghostWebClickHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_type",
		Description: "Type text into a CSS selector in the web ghost (clear=true to replace). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
			"selector":  map[string]interface{}{"type": "string"},
			"text":      map[string]interface{}{"type": "string"},
			"clear":     map[string]interface{}{"type": "boolean"},
		}, "selector", "text"),
		Handler:    ghostWebTypeHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_screenshot",
		Description: "Screenshot the web ghost page (base64 PNG). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
		}),
		Handler:    ghostWebScreenshotHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_text",
		Description: "Extract text content of a CSS selector in the web ghost. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
			"selector":  map[string]interface{}{"type": "string"},
		}, "selector"),
		Handler:    ghostWebTextHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_web_close",
		Description: "Close a web ghost browser session. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
		}),
		Handler:    ghostWebCloseHandler,
		AllowGuest: false,
	})
}

func ghostWebOpenHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
		Headful   bool   `json:"headful"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if err := bm.OpenSession(webSession(p.SessionID), p.Headful); err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sessionId": webSession(p.SessionID)}}
}

func ghostWebGotoHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
		URL       string `json:"url"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.URL == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "url is required"}
	}
	id := webSession(p.SessionID)
	res, err := bm.Navigate(id, p.URL)
	if err != nil {
		// Auto-open the session once, then retry.
		if openErr := bm.OpenSession(id, false); openErr == nil {
			res, err = bm.Navigate(id, p.URL)
		}
	}
	return webResult(res, err)
}

func ghostWebClickHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
		Selector  string `json:"selector"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	res, err := bm.Click(webSession(p.SessionID), p.Selector)
	return webResult(res, err)
}

func ghostWebTypeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
		Selector  string `json:"selector"`
		Text      string `json:"text"`
		Clear     bool   `json:"clear"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	res, err := bm.Type(webSession(p.SessionID), p.Selector, p.Text, p.Clear)
	return webResult(res, err)
}

func ghostWebScreenshotHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	res, err := bm.Screenshot(webSession(p.SessionID))
	return webResult(res, err)
}

func ghostWebTextHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
		Selector  string `json:"selector"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	text, err := bm.ExtractText(webSession(p.SessionID), p.Selector)
	if err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"text": text}}
}

func ghostWebCloseHandler(c OpsContext, payload json.RawMessage) OpsResult {
	bm, deny := webGhostForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if err := bm.CloseSession(webSession(p.SessionID)); err != nil {
		return OpsResult{OK: false, Code: "ghost_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"closed": webSession(p.SessionID)}}
}
