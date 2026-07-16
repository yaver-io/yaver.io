package main

// wda_client.go — a minimal WebDriverAgent HTTP client.
//
// WebDriverAgent (WDA) is Facebook/Appium's on-device automation
// agent: a tiny XCUITest target that runs ON a physical iPhone and
// exposes an HTTP/JSON API for taps, swipes, text, screenshots,
// window size and an MJPEG screen stream. It is the only sanctioned
// way to drive a *real* iPhone — `xcrun simctl` is simulator-only.
//
// Reaching WDA from the host goes over the usbmuxd TCP tunnel
// (Xcode/`devicectl` set this up; the forwarded localhost port is
// WDA's :8100). We don't hard-code that here — `wdaBaseURL()`
// resolves it (env override for tests + a documented default) so the
// whole client is exercisable against an httptest fake WDA, per the
// repo's "real HTTP server, no mocks" test convention.
//
// Only the verbs runtimeTarget needs are implemented; WDA's surface
// is far larger. Endpoint shapes are classic WDA (the routes Appium
// has shipped unchanged for years), not the W3C /actions pipeline,
// to keep request bodies trivial and stable.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// wdaDefaultBaseURL is WDA's conventional usbmuxd-forwarded address.
const wdaDefaultBaseURL = "http://localhost:8100"

// wdaBaseURL is the WDA endpoint. YAVER_WDA_BASE_URL overrides it —
// used by tests (httptest server) and by advanced setups that forward
// WDA on a non-default port. Production resolves the usbmuxd tunnel
// elsewhere and sets the env, or accepts the default.
func wdaBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_WDA_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return wdaDefaultBaseURL
}

type wdaClient struct {
	baseURL   string
	http      *http.Client
	sessionID string
}

func newWDAClient(baseURL string) *wdaClient {
	return &wdaClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// wdaReply is WDA's envelope. `value` is endpoint-specific; sessionId
// is set on POST /session.
type wdaReply struct {
	Value     json.RawMessage `json:"value"`
	SessionID string          `json:"sessionId"`
}

func (c *wdaClient) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wda %s %s: %w (is WebDriverAgent running + forwarded?)", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("wda %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var reply wdaReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("wda %s %s: bad json: %w", method, path, err)
	}
	if reply.SessionID != "" {
		c.sessionID = reply.SessionID
	}
	return reply.Value, nil
}

// Status hits the unauthenticated /status — used as the reachability
// probe (WDA up + tunnel forwarded).
func (c *wdaClient) Status(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/status", nil)
	return err
}

// EnsureSession creates a WDA session if we don't have one. WDA needs
// an active session for every interaction endpoint.
func (c *wdaClient) EnsureSession(ctx context.Context) error {
	if c.sessionID != "" {
		return nil
	}
	// Empty capabilities → WDA attaches to the foreground app.
	_, err := c.do(ctx, http.MethodPost, "/session", map[string]any{
		"capabilities": map[string]any{},
	})
	if err != nil {
		return err
	}
	if c.sessionID == "" {
		return fmt.Errorf("wda: POST /session returned no sessionId")
	}
	return nil
}

func (c *wdaClient) sessionPath(suffix string) string {
	return "/session/" + c.sessionID + suffix
}

func (c *wdaClient) Tap(ctx context.Context, x, y int) error {
	if err := c.EnsureSession(ctx); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodPost, c.sessionPath("/wda/tap/0"),
		map[string]any{"x": x, "y": y})
	return err
}

func (c *wdaClient) Swipe(ctx context.Context, x1, y1, x2, y2, durationMs int) error {
	if err := c.EnsureSession(ctx); err != nil {
		return err
	}
	dur := float64(durationMs) / 1000.0
	if dur <= 0 {
		dur = 0.25
	}
	_, err := c.do(ctx, http.MethodPost, c.sessionPath("/wda/dragfromtoforduration"),
		map[string]any{
			"fromX": x1, "fromY": y1, "toX": x2, "toY": y2, "duration": dur,
		})
	return err
}

func (c *wdaClient) Text(ctx context.Context, text string) error {
	if err := c.EnsureSession(ctx); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodPost, c.sessionPath("/wda/keys"),
		map[string]any{"value": strings.Split(text, "")})
	return err
}

// PressButton maps a friendly name to a WDA hardware button. iOS has
// far fewer buttons than Android keycodes and Apple's WebDriverAgent
// intentionally exposes only the small set that Home/Volume+/Volume-
// covers. tvOS directional remote, watchOS Digital Crown, and visionOS
// pinch need XCUIRemote / XCUITest primitives WDA doesn't proxy —
// callers get an actionable error naming what to install instead of a
// silent no-op. Names are normalised (case + separators) so the
// protocol is forgiving.
func (c *wdaClient) PressButton(ctx context.Context, name string) error {
	if err := c.EnsureSession(ctx); err != nil {
		return err
	}
	wda, ok := wdaButtonName(name)
	if !ok {
		if reason, surface := unsupportedIOSKeyReason(name); reason != "" {
			return fmt.Errorf("key %q is a %s primitive — WebDriverAgent doesn't expose it; %s", name, surface, reason)
		}
		return fmt.Errorf("unsupported key %q for ios-device (WDA buttons: home, volumeup, volumedown)", name)
	}
	_, err := c.do(ctx, http.MethodPost, c.sessionPath("/wda/pressButton"),
		map[string]any{"name": wda})
	return err
}

func wdaButtonName(name string) (string, bool) {
	switch normalisedIOSKey(name) {
	case "home":
		return "home", true
	case "volume_up":
		return "volumeUp", true
	case "volume_down":
		return "volumeDown", true
	}
	return "", false
}

// unsupportedIOSKeyReason returns a (reason, surface) pair when the
// caller asked for a well-known tvOS/watchOS/visionOS control that
// vanilla WDA cannot dispatch. Empty reason = plain "unknown key".
func unsupportedIOSKeyReason(name string) (string, string) {
	switch normalisedIOSKey(name) {
	case "up", "down", "left", "right", "select", "menu", "play_pause":
		return "install an XCUIRemote bridge on the target simulator to enable tvOS directional control", "tvOS remote"
	case "crown_up", "crown_down":
		return "watchOS Digital Crown needs an XCUITest tunnel — the JPEG stream is view-only until then", "watchOS Digital Crown"
	case "pinch", "pinch_in", "pinch_out":
		return "visionOS pinch/gaze needs an XCUITest-with-VisionKit bridge", "visionOS pinch"
	}
	return "", ""
}

func normalisedIOSKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"), " ", "_"))
}

// Screenshot returns a decoded PNG. WDA's /screenshot is
// session-less and returns base64 in `value`.
func (c *wdaClient) Screenshot(ctx context.Context) ([]byte, error) {
	val, err := c.do(ctx, http.MethodGet, "/screenshot", nil)
	if err != nil {
		return nil, err
	}
	var b64 string
	if err := json.Unmarshal(val, &b64); err != nil {
		return nil, fmt.Errorf("wda screenshot: bad value: %w", err)
	}
	png, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("wda screenshot: bad base64: %w", err)
	}
	return png, nil
}

// WindowSize returns the active app's logical size in points.
func (c *wdaClient) WindowSize(ctx context.Context) (w, h int, err error) {
	if err := c.EnsureSession(ctx); err != nil {
		return 0, 0, err
	}
	val, err := c.do(ctx, http.MethodGet, c.sessionPath("/window/size"), nil)
	if err != nil {
		return 0, 0, err
	}
	var size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal(val, &size); err != nil {
		return 0, 0, fmt.Errorf("wda window/size: bad value: %w", err)
	}
	return size.Width, size.Height, nil
}
