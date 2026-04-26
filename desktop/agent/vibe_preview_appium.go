package main

// vibe_preview_appium.go — Phase 15: WebDriver-driven inspection of RN
// apps in dev mode.
//
// Why bother — Maestro flows (Phase 7) drive the app, but they're write-
// only: tap-tap-swipe with no introspection. Appium gives us a JSON DOM
// of the rendered native view tree on every step, so we can:
//   1. Spot a red-box before the user does (it's a recognizable UIView
//      hierarchy with "RCTRedBox" / "Unhandled JS Exception" text).
//   2. Read accessibility identifiers to drive flows that adapt to
//      layout changes — Maestro flows break when a button moves; an
//      accessibility-id flow doesn't.
//   3. Hand a structured snapshot of the broken state to Claude in
//      autodev's next kick instead of just a stack trace.
//
// Scope of this file: the WebDriver REST client + a "bug hunter"
// goroutine that walks the app for N seconds and emits a vibe-preview
// crash event when a red-box is found. Wiring back into autodev's
// fix-the-bug loop is task-15.next-step.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AppiumClient is a small WebDriver REST client. Default base URL is
// http://127.0.0.1:4723 — the Appium server's standard port. Override
// with NewAppiumClient(baseURL) when running a remote Appium.
type AppiumClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewAppiumClient returns a client pointing at baseURL (defaults to
// localhost). HTTPClient defaults to a 30 s timeout — Appium operations
// can be slow on a cold simulator boot.
func NewAppiumClient(baseURL string) *AppiumClient {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:4723"
	}
	return &AppiumClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// AppiumStartCaps is the per-platform capability bag the WebDriver
// session needs. The full Appium spec has dozens of flags; we expose
// just the ones the bug-hunter needs and let callers pass extras
// through `Extra`.
type AppiumStartCaps struct {
	Platform     string `json:"platformName"`              // "iOS" | "Android"
	Automation   string `json:"automationName,omitempty"`  // "XCUITest" | "UiAutomator2"
	DeviceName   string `json:"appium:deviceName,omitempty"`
	BundleID     string `json:"appium:bundleId,omitempty"` // iOS — sticky-attach to a running app
	AppPackage   string `json:"appium:appPackage,omitempty"` // Android equivalent
	NoReset      bool   `json:"appium:noReset,omitempty"`  // attach instead of relaunch
	Extra        map[string]interface{} `json:"-"`         // passthrough
}

// StartSession opens a new Appium WebDriver session. Returns the session
// ID for use in subsequent calls.
func (c *AppiumClient) StartSession(ctx context.Context, caps AppiumStartCaps) (string, error) {
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": appiumCapsToMap(caps),
		},
	}
	var resp struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := c.do(ctx, "POST", "/session", body, &resp); err != nil {
		return "", err
	}
	if resp.Value.SessionID == "" {
		return "", fmt.Errorf("appium: empty session id")
	}
	return resp.Value.SessionID, nil
}

// StopSession closes the WebDriver session. Idempotent — Appium returns
// 404 for unknown sessions, which we swallow.
func (c *AppiumClient) StopSession(ctx context.Context, sessionID string) error {
	err := c.do(ctx, "DELETE", "/session/"+sessionID, nil, nil)
	if err != nil && strings.Contains(err.Error(), "404") {
		return nil
	}
	return err
}

// PageSource returns the current view-hierarchy XML. The bug hunter
// greps this for red-box markers; advanced flows can parse it.
func (c *AppiumClient) PageSource(ctx context.Context, sessionID string) (string, error) {
	var resp struct {
		Value string `json:"value"`
	}
	if err := c.do(ctx, "GET", "/session/"+sessionID+"/source", nil, &resp); err != nil {
		return "", err
	}
	return resp.Value, nil
}

// Screenshot returns a base64-encoded PNG of the current screen. Same
// shape the chromedp screenshots use, so callers can hand the bytes to
// the existing summary pipeline.
func (c *AppiumClient) Screenshot(ctx context.Context, sessionID string) (string, error) {
	var resp struct {
		Value string `json:"value"`
	}
	if err := c.do(ctx, "GET", "/session/"+sessionID+"/screenshot", nil, &resp); err != nil {
		return "", err
	}
	return resp.Value, nil
}

// Tap does a single tap at (x, y). Coordinates are absolute screen
// pixels; callers should query device size via Status() if they need
// proportional placement.
func (c *AppiumClient) Tap(ctx context.Context, sessionID string, x, y int) error {
	body := map[string]interface{}{
		"actions": []map[string]interface{}{
			{
				"type": "pointer",
				"id":   "finger1",
				"parameters": map[string]string{"pointerType": "touch"},
				"actions": []map[string]interface{}{
					{"type": "pointerMove", "duration": 0, "x": x, "y": y},
					{"type": "pointerDown", "button": 0},
					{"type": "pause", "duration": 50},
					{"type": "pointerUp", "button": 0},
				},
			},
		},
	}
	return c.do(ctx, "POST", "/session/"+sessionID+"/actions", body, nil)
}

// Status pings the Appium server. Cheap; used by the doctor + the
// bug-hunter's pre-flight to decide whether to launch.
func (c *AppiumClient) Status(ctx context.Context) error {
	return c.do(ctx, "GET", "/status", nil, nil)
}

// ─── Bug hunter ──────────────────────────────────────────────────────────────

// AppiumBugHunter walks an Appium session for `duration` looking for
// red-boxes / native fatal-exception screens, taps to dismiss
// modals, and emits vibe-preview crash events on the project's SSE
// channel when something looks broken.
//
// One hunt per call — meant to run alongside an Appium-driven E2E flow
// (or just by itself, polling). Caller is responsible for opening the
// session beforehand.
//
// Returns the number of distinct crashes found.
func (m *VibePreviewManager) AppiumBugHunter(ctx context.Context, client *AppiumClient, sessionID, project string, duration time.Duration) int {
	if m == nil || client == nil || sessionID == "" {
		return 0
	}
	if duration <= 0 {
		duration = 30 * time.Second
	}
	deadline := time.Now().Add(duration)
	found := 0
	seen := map[string]bool{}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return found
		default:
		}
		src, err := client.PageSource(ctx, sessionID)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		signature, message := DetectAppiumCrashSignal(src)
		if signature != "" && !seen[signature+":"+message] {
			seen[signature+":"+message] = true
			found++
			m.OnCrashDetected(VibeCrashSignal{
				Project: project,
				Source:  "appium-" + signature,
				Message: message,
				Snippet: snippetFromSource(src, message),
			})
		}
		// Poll cadence — RN red-boxes typically render within 1 s of the
		// crash; 2 s polling is fast enough to catch them without
		// hammering Appium.
		time.Sleep(2 * time.Second)
	}
	return found
}

// DetectAppiumCrashSignal scans an XML page-source dump for known
// crash-screen markers and returns (signature, message). Empty signature
// means clean. Exposed so unit tests can exercise the matcher without
// needing an Appium server.
func DetectAppiumCrashSignal(src string) (signature, message string) {
	// React Native red-box: a UIView with "RCTRedBox" or text like
	// "Unhandled JS Exception". The label class differs between iOS +
	// Android RN versions, so match a small set.
	rnHits := []string{
		"RCTRedBox",
		"Unhandled JS Exception",
		"Cannot read property",
		"undefined is not a function",
	}
	for _, h := range rnHits {
		if idx := strings.Index(src, h); idx >= 0 {
			return "rn-redbox", extractAroundIndex(src, idx, 200)
		}
	}
	// Android native fatal — surfaces via system dialog "X has stopped".
	if idx := strings.Index(src, "has stopped"); idx >= 0 {
		return "android-anr", extractAroundIndex(src, idx, 200)
	}
	if idx := strings.Index(src, "isn't responding"); idx >= 0 {
		return "android-anr", extractAroundIndex(src, idx, 200)
	}
	// iOS — backgrounded / crashed apps don't usually surface a native
	// dialog; the page source goes blank or shows the springboard. Any
	// signature in the future arrives here.
	return "", ""
}

// extractAroundIndex returns up to `n` chars of context around `idx`.
// Used to give the crash message a meaningful tail for the dashboard.
func extractAroundIndex(src string, idx, n int) string {
	start := idx
	end := idx + n
	if end > len(src) {
		end = len(src)
	}
	out := src[start:end]
	// Normalise to a single line for the SSE event.
	out = strings.ReplaceAll(out, "\n", " ")
	out = strings.ReplaceAll(out, "\t", " ")
	return strings.TrimSpace(out)
}

func snippetFromSource(src, message string) string {
	// Prefer the message itself (already a tight window); fall back to
	// a first-1-KB slice so the receiver always has something.
	if message != "" {
		return message
	}
	if len(src) > 1024 {
		return src[:1024] + "…"
	}
	return src
}

// ─── HTTP plumbing ───────────────────────────────────────────────────────────

func (c *AppiumClient) do(ctx context.Context, method, path string, body, out interface{}) error {
	url := c.BaseURL + path
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("appium %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("appium %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("appium %s %s: parse response: %w", method, path, err)
		}
	}
	return nil
}

// appiumCapsToMap flattens AppiumStartCaps into the JSON shape the
// WebDriver protocol expects. Extras are merged last so callers can
// override anything the typed fields set.
func appiumCapsToMap(c AppiumStartCaps) map[string]interface{} {
	out := map[string]interface{}{
		"platformName": c.Platform,
	}
	if c.Automation != "" {
		out["appium:automationName"] = c.Automation
	}
	if c.DeviceName != "" {
		out["appium:deviceName"] = c.DeviceName
	}
	if c.BundleID != "" {
		out["appium:bundleId"] = c.BundleID
	}
	if c.AppPackage != "" {
		out["appium:appPackage"] = c.AppPackage
	}
	if c.NoReset {
		out["appium:noReset"] = true
	}
	for k, v := range c.Extra {
		out[k] = v
	}
	return out
}
