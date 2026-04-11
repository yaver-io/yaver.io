package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// WebDriverAgent client for iOS selector-based automation.
//
// WDA is Facebook/Meta's open-source project that runs on an iOS
// simulator or real device and exposes the W3C WebDriver protocol
// over HTTP. It's the same protocol the Firefox/Safari drivers
// already speak, so the client here is tiny.
//
// What ships in Yaver:
//
//   - NewWDADriver connects to a WDA instance already running on
//     port 8100. The dev starts WDA via `xcodebuild test-without-building`
//     or `yaver install wda` (which downloads a pre-built bundle and
//     hands the dev a one-liner).
//   - FindByPredicate / FindByClassChain / Click / SendKeys / Screenshot
//     are enough to drive a typical RN app (find by accessibility id,
//     tap, type, screenshot).
//
// We deliberately don't bundle or compile WDA — solo dev installs
// it once via the documented path. Yaver just knows how to talk to
// it.

// WDADriver is a tiny WDA client. Reuses the FirefoxDriver transport
// because the wire protocol is identical.
type WDADriver struct {
	baseURL   string
	sessionID string
	client    *http.Client
}

// NewWDADriver connects to an already-running WDA instance. Pass
// "" for defaultURL to get the standard http://127.0.0.1:8100.
func NewWDADriver(ctx context.Context, defaultURL string) (*WDADriver, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("WebDriverAgent requires macOS")
	}
	url := defaultURL
	if url == "" {
		url = "http://127.0.0.1:8100"
	}
	d := &WDADriver{
		baseURL: url,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	// Ping /status — WDA returns 200 with a JSON body.
	req, err := http.NewRequestWithContext(ctx, "GET", d.baseURL+"/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("WDA not reachable at %s — start it with `yaver install wda` or xcodebuild", url)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("WDA status %d at %s", resp.StatusCode, url)
	}
	return d, nil
}

// NewSession opens a WDA session against a specific bundle id. If
// bundleID is empty, WDA attaches to whatever app is already in the
// foreground.
func (d *WDADriver) NewSession(ctx context.Context, bundleID string) error {
	caps := map[string]interface{}{}
	if bundleID != "" {
		caps["bundleId"] = bundleID
	}
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": caps,
		},
	}
	resp, err := d.post(ctx, "/session", body)
	if err != nil {
		return err
	}
	d.sessionID = resp.Value.SessionID
	if d.sessionID == "" {
		return fmt.Errorf("WDA session id missing")
	}
	return nil
}

// Click taps an element matched by a WDA predicate or accessibility
// id. Uses the -ios predicate string strategy which is RN-friendly:
//
//   click: name == "Sign In"
//   click: label BEGINSWITH "Welcome"
//   click: accessibilityId=='signin-button'
//
// Bare strings (no operator) are treated as accessibility ids so the
// dev can write `click: signin-button` the same way they do on
// Android with `testID=signin-button`.
func (d *WDADriver) Click(ctx context.Context, selector string) error {
	id, err := d.findElement(ctx, selector)
	if err != nil {
		return err
	}
	_, err = d.post(ctx, "/session/"+d.sessionID+"/element/"+id+"/click", map[string]interface{}{})
	return err
}

// SendKeys types into an element.
func (d *WDADriver) SendKeys(ctx context.Context, selector, text string) error {
	id, err := d.findElement(ctx, selector)
	if err != nil {
		return err
	}
	_, err = d.post(ctx, "/session/"+d.sessionID+"/element/"+id+"/value", map[string]interface{}{
		"value": []string{text},
	})
	return err
}

// Screenshot returns the current screen as a PNG.
func (d *WDADriver) Screenshot(ctx context.Context) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", d.baseURL+"/session/"+d.sessionID+"/screenshot", nil)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wda screenshot %d: %s", resp.StatusCode, string(body))
	}
	var wd struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &wd); err != nil {
		return nil, err
	}
	return base64Decode(wd.Value)
}

// Close ends the WDA session. Doesn't stop WDA itself — the dev
// started it, the dev stops it.
func (d *WDADriver) Close(ctx context.Context) {
	if d.sessionID == "" {
		return
	}
	req, _ := http.NewRequest("DELETE", d.baseURL+"/session/"+d.sessionID, nil)
	if d.client != nil {
		_, _ = d.client.Do(req)
	}
}

// findElement picks the right WDA find-strategy based on the shape
// of the selector:
//
//   name == "Sign In"            → -ios predicate string
//   label BEGINSWITH "..."       → -ios predicate string
//   **/XCUIElementTypeButton     → -ios class chain
//   foo                          → accessibility id (bare)
func (d *WDADriver) findElement(ctx context.Context, selector string) (string, error) {
	using := "accessibility id"
	value := selector
	sel := strings.TrimSpace(selector)
	if strings.Contains(sel, "==") || strings.Contains(sel, " CONTAINS ") || strings.Contains(sel, " BEGINSWITH ") {
		using = "-ios predicate string"
		value = sel
	} else if strings.HasPrefix(sel, "**/") {
		using = "-ios class chain"
		value = sel
	} else if strings.HasPrefix(sel, "testID=") {
		value = strings.TrimPrefix(sel, "testID=")
	} else if strings.HasPrefix(sel, "label=") {
		using = "-ios predicate string"
		value = fmt.Sprintf(`label == "%s"`, strings.TrimPrefix(sel, "label="))
	}
	resp, err := d.post(ctx, "/session/"+d.sessionID+"/element", map[string]interface{}{
		"using": using,
		"value": value,
	})
	if err != nil {
		return "", err
	}
	if resp.Value.ElementID != "" {
		return resp.Value.ElementID, nil
	}
	for _, id := range resp.Value.ElementMap {
		return id, nil
	}
	return "", fmt.Errorf("wda element not found: %s", selector)
}

func (d *WDADriver) post(ctx context.Context, path string, body map[string]interface{}) (*webdriverResponse, error) {
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", d.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("wda %s %d: %s", path, resp.StatusCode, truncate(string(respBody), 200))
	}
	var out webdriverResponse
	_ = json.Unmarshal(respBody, &out)
	return &out, nil
}

// IsWDARunning checks whether WDA is listening on its default port.
// Used by `yaver doctor` so the CI integrations section reports
// accurately.
func IsWDARunning(baseURL string) bool {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8100"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/status", nil)
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// TryStartWDA is a best-effort helper that runs xcodebuild to start
// WDA against a booted simulator UDID. Used by the runner when the
// dev has Xcode installed and WDA isn't already running. Returns a
// running exec.Cmd the caller should eventually kill.
func TryStartWDA(ctx context.Context, udid string, wdaXcodeProj string) (*exec.Cmd, error) {
	if wdaXcodeProj == "" {
		return nil, fmt.Errorf("wda xcodeproj path not configured — pass YAVER_WDA_XCODEPROJ or run `yaver install wda`")
	}
	cmd := exec.CommandContext(ctx,
		"xcodebuild",
		"-project", wdaXcodeProj,
		"-scheme", "WebDriverAgentRunner",
		"-destination", "id="+udid,
		"test-without-building",
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("xcodebuild start: %w", err)
	}
	// Wait up to 60s for WDA to come up on :8100.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if IsWDARunning("") {
			return cmd, nil
		}
		time.Sleep(1 * time.Second)
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("WDA did not come up on :8100 in 60s")
}
