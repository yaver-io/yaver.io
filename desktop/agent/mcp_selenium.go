package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/testkit"
)

type seleniumMCPSession struct {
	ID        string
	Driver    testkit.WebDriver
	CreatedAt time.Time
	UpdatedAt time.Time
	Headful   bool
	Profile   string
	Width     int
	Height    int
	LastURL   string
	Title     string
}

var seleniumMCP = &seleniumMCPManager{sessions: map[string]*seleniumMCPSession{}}

type seleniumMCPManager struct {
	mu       sync.Mutex
	sessions map[string]*seleniumMCPSession
}

func seleniumMCPTools() []map[string]interface{} {
	commonSession := map[string]interface{}{"type": "string", "description": "Selenium session ID"}
	return []map[string]interface{}{
		{
			"name":        "selenium_status",
			"description": "Check Selenium/WebDriver readiness on this runtime host. Uses SELENIUM_REMOTE_URL/YAVER_SELENIUM_REMOTE_URL when set, otherwise expects chromedriver on PATH.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "selenium_start",
			"description": "Start a first-class Selenium/WebDriver Chrome session on this runtime host. Use for daily browser tasks that should explicitly go through WebDriver instead of CDP. Does not bypass CAPTCHA or site auth; hand off to the user when needed.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string", "description": "Optional custom session ID"},
				"url":        map[string]interface{}{"type": "string", "description": "Optional initial URL"},
				"headful":    map[string]interface{}{"type": "boolean", "description": "Show Chrome window visibly"},
				"profile":    map[string]interface{}{"type": "string", "description": "Persistent Chrome profile name or absolute user-data-dir"},
				"width":      map[string]interface{}{"type": "integer", "description": "Viewport width, default 1280"},
				"height":     map[string]interface{}{"type": "integer", "description": "Viewport height, default 800"},
			}},
		},
		{
			"name":        "selenium_search",
			"description": "Open a search-results page in Selenium. Default provider is Google; also supports Bing and DuckDuckGo. This is normal browser navigation, not scraping or CAPTCHA bypass.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"query"}, "properties": map[string]interface{}{
				"session_id": map[string]interface{}{"type": "string", "description": "Existing session ID. If omitted, a new Selenium session is created."},
				"query":      map[string]interface{}{"type": "string"},
				"provider":   map[string]interface{}{"type": "string", "enum": []string{"google", "bing", "duckduckgo", "ddg"}},
				"headful":    map[string]interface{}{"type": "boolean", "description": "When creating a session, show Chrome visibly"},
				"profile":    map[string]interface{}{"type": "string", "description": "When creating a session, persistent profile name or absolute user-data-dir"},
			}},
		},
		{
			"name":        "selenium_navigate",
			"description": "Navigate an existing Selenium session to a URL.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id", "url"}, "properties": map[string]interface{}{"session_id": commonSession, "url": map[string]interface{}{"type": "string"}}},
		},
		{
			"name":        "selenium_click",
			"description": "Click a CSS selector in a Selenium session.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id", "selector"}, "properties": map[string]interface{}{"session_id": commonSession, "selector": map[string]interface{}{"type": "string"}}},
		},
		{
			"name":        "selenium_type",
			"description": "Type text into a CSS selector in a Selenium session.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id", "selector", "text"}, "properties": map[string]interface{}{"session_id": commonSession, "selector": map[string]interface{}{"type": "string"}, "text": map[string]interface{}{"type": "string"}}},
		},
		{
			"name":        "selenium_snapshot",
			"description": "Return the current Selenium page URL, title, and flattened interactable elements.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"session_id": commonSession}},
		},
		{
			"name":        "selenium_text",
			"description": "Extract visible text from a CSS selector in the Selenium page. Defaults to body.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"session_id": commonSession, "selector": map[string]interface{}{"type": "string", "description": "CSS selector, default body"}}},
		},
		{
			"name":        "selenium_screenshot",
			"description": "Capture a PNG screenshot from a Selenium session. Returns base64 PNG.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"session_id": commonSession}},
		},
		{
			"name":        "selenium_sessions",
			"description": "List active Selenium/WebDriver sessions.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "selenium_close",
			"description": "Close a Selenium/WebDriver session and release Chrome/ChromeDriver.",
			"inputSchema": map[string]interface{}{"type": "object", "required": []string{"session_id"}, "properties": map[string]interface{}{"session_id": commonSession}},
		},
	}
}

func mcpSeleniumToolCall(name string, args json.RawMessage) interface{} {
	switch name {
	case "selenium_status":
		return mcpToolJSON(seleniumReadiness())
	case "selenium_start":
		var a seleniumStartArgs
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.start(a)
		if err != nil {
			return mcpToolError("selenium_start: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_search":
		var a seleniumSearchArgs
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.search(a)
		if err != nil {
			return mcpToolError("selenium_search: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_navigate":
		var a struct {
			SessionID string `json:"session_id"`
			URL       string `json:"url"`
		}
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.navigate(a.SessionID, a.URL)
		if err != nil {
			return mcpToolError("selenium_navigate: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_click":
		var a struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
		}
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.click(a.SessionID, a.Selector)
		if err != nil {
			return mcpToolError("selenium_click: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_type":
		var a struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
			Text      string `json:"text"`
		}
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.typeText(a.SessionID, a.Selector, a.Text)
		if err != nil {
			return mcpToolError("selenium_type: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_snapshot":
		var a struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.snapshot(a.SessionID)
		if err != nil {
			return mcpToolError("selenium_snapshot: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_text":
		var a struct {
			SessionID string `json:"session_id"`
			Selector  string `json:"selector"`
		}
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.text(a.SessionID, a.Selector)
		if err != nil {
			return mcpToolError("selenium_text: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_screenshot":
		var a struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(args, &a)
		out, err := seleniumMCP.screenshot(a.SessionID)
		if err != nil {
			return mcpToolError("selenium_screenshot: " + err.Error())
		}
		return mcpToolJSON(out)
	case "selenium_sessions":
		return mcpToolJSON(seleniumMCP.list())
	case "selenium_close":
		var a struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(args, &a)
		if err := seleniumMCP.close(a.SessionID); err != nil {
			return mcpToolError("selenium_close: " + err.Error())
		}
		return mcpToolJSON(map[string]interface{}{"ok": true, "session_id": a.SessionID})
	default:
		return mcpToolError("unknown selenium tool: " + name)
	}
}

type seleniumStartArgs struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Headful   bool   `json:"headful"`
	Profile   string `json:"profile"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

type seleniumSearchArgs struct {
	SessionID string `json:"session_id"`
	Query     string `json:"query"`
	Provider  string `json:"provider"`
	Headful   bool   `json:"headful"`
	Profile   string `json:"profile"`
}

func seleniumReadiness() map[string]interface{} {
	remote := strings.TrimSpace(os.Getenv("YAVER_SELENIUM_REMOTE_URL"))
	if remote == "" {
		remote = strings.TrimSpace(os.Getenv("SELENIUM_REMOTE_URL"))
	}
	chromedriver, chromeErr := exec.LookPath("chromedriver")
	return map[string]interface{}{
		"ok":                 remote != "" || chromeErr == nil,
		"driver":             "selenium",
		"remote_url_set":     remote != "",
		"chromedriver_path":  chromedriver,
		"chromedriver_ready": chromeErr == nil,
		"install_hint":       "Install ChromeDriver on PATH, or set SELENIUM_REMOTE_URL/YAVER_SELENIUM_REMOTE_URL to a Selenium server.",
		"safety":             "Yaver Selenium uses normal browser automation only. It must not bypass CAPTCHA, auth, paywalls, rate limits, or site access controls.",
	}
}

func (m *seleniumMCPManager) start(a seleniumStartArgs) (map[string]interface{}, error) {
	if a.SessionID == "" {
		a.SessionID = fmt.Sprintf("selenium-%d", time.Now().UnixMilli()%1000000)
	}
	if a.Width <= 0 {
		a.Width = 1280
	}
	if a.Height <= 0 {
		a.Height = 800
	}
	profile := seleniumProfileDir(a.Profile, a.SessionID)
	if profile != "" {
		if err := os.MkdirAll(profile, 0o755); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	driver, err := testkit.NewWebDriver("selenium", testkit.ChromeOpts{
		ViewportW:   a.Width,
		ViewportH:   a.Height,
		Headful:     a.Headful,
		UserDataDir: profile,
	})
	if err != nil {
		return nil, err
	}
	if err := driver.Launch(ctx); err != nil {
		driver.Close()
		return nil, err
	}
	sess := &seleniumMCPSession{ID: a.SessionID, Driver: driver, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Headful: a.Headful, Profile: profile, Width: a.Width, Height: a.Height}
	m.mu.Lock()
	if old := m.sessions[a.SessionID]; old != nil {
		old.Driver.Close()
	}
	m.sessions[a.SessionID] = sess
	m.mu.Unlock()
	if strings.TrimSpace(a.URL) != "" {
		return m.navigate(a.SessionID, a.URL)
	}
	return map[string]interface{}{"ok": true, "session_id": a.SessionID, "profile": profile, "headful": a.Headful, "width": a.Width, "height": a.Height}, nil
}

func (m *seleniumMCPManager) search(a seleniumSearchArgs) (map[string]interface{}, error) {
	if strings.TrimSpace(a.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if a.SessionID == "" {
		out, err := m.start(seleniumStartArgs{Headful: a.Headful, Profile: a.Profile})
		if err != nil {
			return nil, err
		}
		if id, _ := out["session_id"].(string); id != "" {
			a.SessionID = id
		}
	}
	return m.navigate(a.SessionID, seleniumSearchURL(a.Provider, a.Query))
}

func (m *seleniumMCPManager) navigate(sessionID, rawURL string) (map[string]interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return nil, fmt.Errorf("url is required")
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := sess.Driver.Navigate(ctx, u); err != nil {
		return nil, err
	}
	return m.snapshot(sessionID)
}

func (m *seleniumMCPManager) click(sessionID, selector string) (map[string]interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("selector is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sess.Driver.Click(ctx, selector); err != nil {
		return nil, err
	}
	return m.snapshot(sessionID)
}

func (m *seleniumMCPManager) typeText(sessionID, selector, text string) (map[string]interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("selector is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sess.Driver.Fill(ctx, selector, text); err != nil {
		return nil, err
	}
	return m.snapshot(sessionID)
}

func (m *seleniumMCPManager) snapshot(sessionID string) (map[string]interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snap, err := sess.Driver.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	sess.LastURL = snap.URL
	sess.Title = snap.Title
	sess.UpdatedAt = time.Now().UTC()
	m.mu.Unlock()
	return map[string]interface{}{"ok": true, "session_id": sessionID, "snapshot": snap}, nil
}

func (m *seleniumMCPManager) text(sessionID, selector string) (map[string]interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	text, err := sess.Driver.VisibleText(ctx, selector)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"ok": true, "session_id": sessionID, "selector": defaultString(selector, "body"), "text": truncate(text, 12000)}, nil
}

func (m *seleniumMCPManager) screenshot(sessionID string) (map[string]interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	png, err := sess.Driver.Screenshot(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"ok": true, "session_id": sessionID, "mime": "image/png", "base64": base64.StdEncoding.EncodeToString(png), "bytes": len(png)}, nil
}

func (m *seleniumMCPManager) list() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, map[string]interface{}{
			"session_id": s.ID,
			"created_at": s.CreatedAt,
			"updated_at": s.UpdatedAt,
			"url":        s.LastURL,
			"title":      s.Title,
			"headful":    s.Headful,
			"profile":    s.Profile,
		})
	}
	return map[string]interface{}{"ok": true, "sessions": out}
}

func (m *seleniumMCPManager) close(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session_id is required")
	}
	m.mu.Lock()
	sess := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("unknown selenium session %q", sessionID)
	}
	sess.Driver.Close()
	return nil
}

func (m *seleniumMCPManager) get(sessionID string) (*seleniumMCPSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := m.sessions[sessionID]
	if sess == nil {
		return nil, fmt.Errorf("unknown selenium session %q", sessionID)
	}
	return sess, nil
}

func seleniumSearchURL(provider, query string) string {
	q := url.QueryEscape(strings.TrimSpace(query))
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "bing":
		return "https://www.bing.com/search?q=" + q
	case "duckduckgo", "ddg":
		return "https://duckduckgo.com/?q=" + q
	default:
		return "https://www.google.com/search?q=" + q
	}
}

func seleniumProfileDir(profile, sessionID string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return ""
	}
	if filepath.IsAbs(profile) {
		return profile
	}
	if profile == "default" {
		profile = sessionID
	}
	return profileDirFor("selenium-" + profile)
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
