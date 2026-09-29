package main

// ops_browser_operator.go — a goal-driven browser agent for ONE machine.
//
// SCOPE: this is the single-PC "just use my computer" lane. It operates a real
// browser on the local host (Chromium via CDP, or Firefox/Safari via W3C
// WebDriver) toward a natural-language goal, one grounded action at a time, and
// returns a step trace. No fleet, no `machine=` proxying, no new UI — the trace
// rides the existing chat/task output.
//
// WHY SELECTORS, NOT PIXELS: the deterministic browser lane is strictly more
// reliable than clicking coordinates on a screenshot, so the model is handed the
// page DOM + URL and returns a CSS selector. It shares the SAME model
// configuration as the desktop operator (newVisionLocator → the user's runner
// provider, on-prem Ollama fallback), so one setting enables both.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const browserOperatorSystemPrompt = `You are a precise web-navigation agent operating a REAL browser on the user's own computer.
You are given a GOAL and the current page (URL + truncated HTML). Decide the SINGLE next action.
Reply with ONLY a compact JSON object, no prose, no markdown:
{"action":"navigate|click|type|scroll|wait|done","url":"...","selector":"css","text":"...","dx":int,"dy":int,"reason":"short"}
- navigate: go to "url".
- click / type: target the element matching the CSS "selector". For type, also set "text".
- scroll: scroll by dx,dy.
- wait: wait for "selector" to become visible.
- done: the current page already satisfies the goal (or it is impossible).
Prefer stable selectors — id, name, [aria-label], [data-*], role/text anchors. Avoid brittle :nth-child chains.
Never fabricate a selector you cannot see in the HTML. Return strictly valid JSON.`

type browserOperatorStep struct {
	Index  int    `json:"index"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

type browserOperatorAction struct {
	Action   string `json:"action"`
	URL      string `json:"url"`
	Selector string `json:"selector"`
	Text     string `json:"text"`
	DX       int    `json:"dx"`
	DY       int    `json:"dy"`
	Reason   string `json:"reason"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "browser_operator",
		Description: "Drive a real browser on THIS machine toward a natural-language goal, one grounded action at a " +
			"time (navigate / click / type / scroll / wait), then return a step trace. Engine defaults to chrome; " +
			"firefox and safari use W3C WebDriver. Bounded by step/time budgets. Shares the model config with " +
			"desktop_operator.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"goal":       map[string]interface{}{"type": "string"},
			"engine":     map[string]interface{}{"type": "string", "enum": []string{"chrome", "firefox", "safari"}},
			"startUrl":   map[string]interface{}{"type": "string", "description": "Optional URL to open first."},
			"sessionId":  map[string]interface{}{"type": "string", "description": "Reuse an open browser session; omit to open one."},
			"headful":    map[string]interface{}{"type": "boolean"},
			"profile":    map[string]interface{}{"type": "string", "description": "Persistent profile name (shares cookies/clearance)."},
			"maxSteps":   map[string]interface{}{"type": "integer"},
			"timeoutSec": map[string]interface{}{"type": "integer"},
		}, "goal"),
		Handler:        browserOperatorHandler,
		AllowCompanion: false,
	})
}

// browserOpSession abstracts the two lanes so the loop body is engine-agnostic.
type browserOpSession struct {
	id       string
	selenium bool
	mgr      *BrowserManager
	sel      *seleniumMCPManager
}

func (b *browserOpSession) observe(ctx context.Context) (string, string, error) {
	if b.selenium {
		dom, err := b.sel.dom(b.id)
		if err != nil {
			return "", "", err
		}
		url := ""
		if sess, err := b.sel.get(b.id); err == nil {
			url = sess.LastURL
		}
		return url, truncate(dom, 60000), nil
	}
	dom, err := b.mgr.GetDOM(b.id)
	if err != nil {
		return "", "", err
	}
	url, _ := b.mgr.GetURL(b.id)
	return url, truncate(dom, 60000), nil
}

func (b *browserOpSession) apply(ctx context.Context, a browserOperatorAction) error {
	switch b.selenium {
	case true:
		switch a.Action {
		case "navigate":
			_, err := b.sel.navigate(b.id, a.URL)
			return err
		case "click":
			_, err := b.sel.click(b.id, a.Selector)
			return err
		case "type":
			_, err := b.sel.typeText(b.id, a.Selector, a.Text)
			return err
		case "scroll":
			_, err := b.sel.scroll(b.id, a.DX, a.DY)
			return err
		case "wait":
			return b.sel.waitVisible(b.id, a.Selector, 15000)
		}
	default:
		switch a.Action {
		case "navigate":
			_, err := b.mgr.Navigate(b.id, a.URL)
			return err
		case "click":
			_, err := b.mgr.Click(b.id, a.Selector)
			return err
		case "type":
			_, err := b.mgr.Type(b.id, a.Selector, a.Text, true)
			return err
		case "scroll":
			_, err := b.mgr.Scroll(b.id, a.DX, a.DY)
			return err
		case "wait":
			return b.mgr.WaitFor(b.id, a.Selector, 15000)
		}
	}
	return fmt.Errorf("unsupported browser action %q", a.Action)
}

func browserOperatorHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	var p struct {
		Goal       string `json:"goal"`
		Engine     string `json:"engine"`
		StartURL   string `json:"startUrl"`
		SessionID  string `json:"sessionId"`
		Headful    bool   `json:"headful"`
		Profile    string `json:"profile"`
		MaxSteps   int    `json:"maxSteps"`
		TimeoutSec int    `json:"timeoutSec"`
		BaseURL    string `json:"baseUrl"`
		APIKey     string `json:"apiKey"`
		Model      string `json:"model"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	goal := strings.TrimSpace(p.Goal)
	if goal == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "goal is required"}
	}
	engine := strings.ToLower(strings.TrimSpace(p.Engine))
	if engine == "" {
		engine = "chrome"
	}
	if engine != "chrome" && engine != "firefox" && engine != "safari" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "engine must be chrome, firefox, or safari"}
	}

	loc, err := newVisionLocator(p.BaseURL, p.APIKey, p.Model)
	if err != nil {
		return OpsResult{OK: false, Code: "no_model", Error: "no model configured for browsing: " + err.Error()}
	}

	s := c.Server
	session, err := openBrowserOperatorSession(s, engine, p.SessionID, p.Headful, p.Profile)
	if err != nil {
		return OpsResult{OK: false, Code: "browser_open_failed", Error: err.Error()}
	}

	if p.StartURL != "" {
		if err := session.apply(context.Background(), browserOperatorAction{Action: "navigate", URL: p.StartURL}); err != nil {
			return OpsResult{OK: false, Code: "browser_navigate_failed", Error: err.Error()}
		}
	}

	maxSteps := p.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 10
	}
	timeout := 5 * time.Minute
	if p.TimeoutSec > 0 {
		timeout = time.Duration(p.TimeoutSec) * time.Second
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	steps := make([]browserOperatorStep, 0, maxSteps)
	completed, stoppedReason := false, "budget"
	var prevAction browserOperatorAction
	havePrev := false

	for i := 1; i <= maxSteps && ctx.Err() == nil; i++ {
		url, dom, oerr := session.observe(ctx)
		if oerr != nil {
			steps = append(steps, browserOperatorStep{Index: i, Action: "observe", Error: oerr.Error()})
			stoppedReason = "observe_error"
			break
		}
		act, derr := browserNextAction(ctx, loc, goal, url, dom)
		if derr != nil {
			steps = append(steps, browserOperatorStep{Index: i, Action: "decide", Error: derr.Error()})
			stoppedReason = "decide_error"
			break
		}
		if act.Action == "" || act.Action == "done" {
			steps = append(steps, browserOperatorStep{Index: i, Action: "done", Detail: act.Reason})
			completed = true
			stoppedReason = "done"
			break
		}
		step := browserOperatorStep{Index: i, Action: act.Action, Detail: browserActionDetail(act)}
		if aerr := session.apply(ctx, act); aerr != nil {
			step.Error = aerr.Error()
			steps = append(steps, step)
			stoppedReason = "action_error"
			break
		}
		steps = append(steps, step)
		if havePrev && sameBrowserAction(prevAction, act) {
			stoppedReason = "no_progress"
			break
		}
		prevAction, havePrev = act, true
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"goal":          goal,
		"engine":        engine,
		"sessionId":     session.id,
		"completed":     completed,
		"stoppedReason": stoppedReason,
		"stepCount":     len(steps),
		"steps":         steps,
	}}
}

// openBrowserOperatorSession reuses an existing session if one is named,
// otherwise opens a new one on the requested engine.
func openBrowserOperatorSession(s *HTTPServer, engine, sessionID string, headful bool, profile string) (*browserOpSession, error) {
	selenium := engine == "firefox" || engine == "safari"
	if selenium {
		if sessionID != "" && seleniumMCP.has(sessionID) {
			return &browserOpSession{id: sessionID, selenium: true, sel: seleniumMCP}, nil
		}
		out, err := seleniumMCP.start(seleniumStartArgs{SessionID: sessionID, Browser: engine, Headful: headful, Profile: profile})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", engine, err)
		}
		id, _ := out["session_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("%s: session did not return an id", engine)
		}
		return &browserOpSession{id: id, selenium: true, sel: seleniumMCP}, nil
	}
	// Chrome / CDP.
	if s.browserMgr == nil {
		s.browserMgr = NewBrowserManager()
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("browser-op-%d", time.Now().UnixMilli()%100000)
	}
	if _, err := s.browserMgr.getSession(sessionID); err != nil {
		profileDir := ""
		if p := strings.TrimSpace(profile); p != "" {
			profileDir = profileDirFor(p)
		}
		if err := s.browserMgr.OpenSessionWithViewport(sessionID, headful, "", profileDir, 0, 0); err != nil {
			return nil, err
		}
	}
	return &browserOpSession{id: sessionID, mgr: s.browserMgr}, nil
}

// browserNextAction asks the model for the single next action.
func browserNextAction(ctx context.Context, loc *visionLocator, goal, url, dom string) (browserOperatorAction, error) {
	user := fmt.Sprintf("Goal: %s\n\nCurrent URL: %s\n\nPage HTML (truncated):\n%s\n\nReturn the single next action as JSON.", goal, defaultString(url, "(unknown)"), dom)
	content, err := loc.chat(ctx, []any{
		map[string]any{"role": "system", "content": browserOperatorSystemPrompt},
		map[string]any{"role": "user", "content": user},
	})
	if err != nil {
		return browserOperatorAction{}, err
	}
	return parseBrowserAction(content)
}

// parseBrowserAction tolerates models that wrap JSON in prose/code fences and
// validates that the action carries the field it needs. Pure — unit-tested.
func parseBrowserAction(content string) (browserOperatorAction, error) {
	s := strings.TrimSpace(content)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var a browserOperatorAction
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return browserOperatorAction{}, fmt.Errorf("model returned non-JSON action: %q", content)
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	switch a.Action {
	case "navigate":
		if strings.TrimSpace(a.URL) == "" {
			return a, fmt.Errorf("navigate action without url")
		}
	case "click", "type", "wait":
		if strings.TrimSpace(a.Selector) == "" {
			return a, fmt.Errorf("%s action without selector", a.Action)
		}
	}
	return a, nil
}

func browserActionDetail(a browserOperatorAction) string {
	switch a.Action {
	case "navigate":
		return a.URL
	case "click", "wait":
		return a.Selector
	case "type":
		return a.Selector + " ← " + truncate(a.Text, 80)
	case "scroll":
		return fmt.Sprintf("dx=%d dy=%d", a.DX, a.DY)
	}
	return a.Reason
}

func sameBrowserAction(a, b browserOperatorAction) bool {
	return a.Action == b.Action && a.URL == b.URL && a.Selector == b.Selector &&
		a.Text == b.Text && a.DX == b.DX && a.DY == b.DY
}
