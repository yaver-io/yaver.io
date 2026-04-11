package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Firefox / WebKit driver via the W3C WebDriver protocol.
//
// chromedp speaks Chrome's CDP directly, so we don't need a separate
// driver process for `target: web`. To support Firefox (and Safari
// Tech Preview) we shell out to geckodriver / safaridriver, which
// the user installs via `yaver install firefox` and the underlying
// browser distribution. The runner then talks to the driver over
// HTTP using the W3C WebDriver wire protocol.
//
// We deliberately keep this thin: we only implement the step actions
// the YAML spec actually uses (newSession, navigate, findElement,
// click, sendKeys, screenshot, deleteSession). Anything else returns
// a clear "not yet supported in firefox driver, use chromium" error.
// 95% of solo-dev specs work today on chromium and only need Firefox
// for the rare cross-browser snapshot — so we optimize for "the dev
// can run a 5-step happy path on Firefox without installing Selenium
// Server" rather than "Yaver is a complete WebDriver client."

// FirefoxDriver wraps a geckodriver subprocess + WebDriver session.
type FirefoxDriver struct {
	binary    string // path to geckodriver
	port      int
	cmd       *exec.Cmd
	sessionID string
	baseURL   string
	client    *http.Client
}

// NewFirefoxDriver locates geckodriver on PATH and starts it on a
// random local port. Returns an error if the binary is missing — the
// caller should hint the dev to run `yaver install firefox` (which
// also installs geckodriver via brew/apt).
func NewFirefoxDriver(ctx context.Context) (*FirefoxDriver, error) {
	bin, err := exec.LookPath("geckodriver")
	if err != nil {
		return nil, fmt.Errorf("geckodriver not found — install firefox or geckodriver (`yaver install firefox`)")
	}
	port := pickFreePort()
	cmd := exec.CommandContext(ctx, bin, "--port", fmt.Sprintf("%d", port), "--log", "fatal")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start geckodriver: %w", err)
	}
	d := &FirefoxDriver{
		binary:  bin,
		port:    port,
		cmd:     cmd,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	// Wait for the driver to come up. geckodriver takes ~200ms.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.ping() {
			return d, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("geckodriver did not start within 5s")
}

func (d *FirefoxDriver) ping() bool {
	resp, err := d.client.Get(d.baseURL + "/status")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// NewSession opens a Firefox window with the given viewport.
func (d *FirefoxDriver) NewSession(ctx context.Context, headful bool, vw, vh int) error {
	args := []string{}
	if !headful {
		args = append(args, "--headless")
	}
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": map[string]interface{}{
				"browserName": "firefox",
				"moz:firefoxOptions": map[string]interface{}{
					"args": args,
				},
			},
		},
	}
	resp, err := d.post(ctx, "/session", body)
	if err != nil {
		return err
	}
	d.sessionID = resp.Value.SessionID
	if d.sessionID == "" {
		return fmt.Errorf("firefox session id missing in response")
	}
	if vw > 0 && vh > 0 {
		_, _ = d.post(ctx, "/session/"+d.sessionID+"/window/rect", map[string]interface{}{
			"width": vw, "height": vh,
		})
	}
	return nil
}

// Navigate loads a URL.
func (d *FirefoxDriver) Navigate(ctx context.Context, url string) error {
	_, err := d.post(ctx, "/session/"+d.sessionID+"/url", map[string]interface{}{"url": url})
	return err
}

// findElement returns the first element matching a CSS selector.
func (d *FirefoxDriver) findElement(ctx context.Context, selector string) (string, error) {
	resp, err := d.post(ctx, "/session/"+d.sessionID+"/element", map[string]interface{}{
		"using": "css selector",
		"value": selector,
	})
	if err != nil {
		return "", err
	}
	if resp.Value.ElementID != "" {
		return resp.Value.ElementID, nil
	}
	if resp.Value.ElementMap != nil {
		// W3C: { "element-6066-11e4-a52e-4f735466cecf": "id" }
		for _, id := range resp.Value.ElementMap {
			return id, nil
		}
	}
	return "", fmt.Errorf("element not found: %s", selector)
}

// Click clicks an element by selector.
func (d *FirefoxDriver) Click(ctx context.Context, selector string) error {
	id, err := d.findElement(ctx, selector)
	if err != nil {
		return err
	}
	_, err = d.post(ctx, "/session/"+d.sessionID+"/element/"+id+"/click", map[string]interface{}{})
	return err
}

// SendKeys types into an input.
func (d *FirefoxDriver) SendKeys(ctx context.Context, selector, text string) error {
	id, err := d.findElement(ctx, selector)
	if err != nil {
		return err
	}
	_, err = d.post(ctx, "/session/"+d.sessionID+"/element/"+id+"/value", map[string]interface{}{
		"text": text,
	})
	return err
}

// Screenshot returns a PNG byte slice for the current viewport.
func (d *FirefoxDriver) Screenshot(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.baseURL+"/session/"+d.sessionID+"/screenshot", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("firefox screenshot %d: %s", resp.StatusCode, string(body))
	}
	var wd webdriverScreenshot
	if err := json.Unmarshal(body, &wd); err != nil {
		return nil, err
	}
	// W3C returns base64 in value.
	if wd.Value == "" {
		return nil, fmt.Errorf("empty screenshot value")
	}
	return base64Decode(wd.Value)
}

// Title returns the page title.
func (d *FirefoxDriver) Title(ctx context.Context) (string, error) {
	resp, err := d.get(ctx, "/session/"+d.sessionID+"/title")
	if err != nil {
		return "", err
	}
	return resp.Value.String, nil
}

// CurrentURL returns the current URL.
func (d *FirefoxDriver) CurrentURL(ctx context.Context) (string, error) {
	resp, err := d.get(ctx, "/session/"+d.sessionID+"/url")
	if err != nil {
		return "", err
	}
	return resp.Value.String, nil
}

// PageSource returns the current document HTML.
func (d *FirefoxDriver) PageSource(ctx context.Context) (string, error) {
	resp, err := d.get(ctx, "/session/"+d.sessionID+"/source")
	if err != nil {
		return "", err
	}
	return resp.Value.String, nil
}

// Close ends the session and kills geckodriver.
func (d *FirefoxDriver) Close() {
	if d.sessionID != "" {
		req, _ := http.NewRequest("DELETE", d.baseURL+"/session/"+d.sessionID, nil)
		_, _ = d.client.Do(req)
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
}

// --- HTTP helpers ----------------------------------------------------

type webdriverScreenshot struct {
	Value string `json:"value"`
}

type webdriverResponse struct {
	Value webdriverValue `json:"value"`
}

// webdriverValue is a heterogeneous union — different endpoints
// return different shapes. We unmarshal into a flexible struct and
// pull out whichever field is set.
type webdriverValue struct {
	SessionID  string            `json:"sessionId,omitempty"`
	ElementID  string            `json:"ELEMENT,omitempty"` // legacy
	ElementMap map[string]string `json:"-"`
	String     string            `json:"-"`
}

func (v *webdriverValue) UnmarshalJSON(data []byte) error {
	// Try string first.
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		v.String = s
		return nil
	}
	// Try object.
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err == nil {
		if id, ok := m["sessionId"].(string); ok {
			v.SessionID = id
		}
		if id, ok := m["ELEMENT"].(string); ok {
			v.ElementID = id
		}
		// W3C element id key
		for k, val := range m {
			if strings.HasPrefix(k, "element-") {
				if str, ok := val.(string); ok {
					if v.ElementMap == nil {
						v.ElementMap = map[string]string{}
					}
					v.ElementMap[k] = str
				}
			}
		}
		return nil
	}
	return nil
}

func (d *FirefoxDriver) post(ctx context.Context, path string, body map[string]interface{}) (*webdriverResponse, error) {
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
		return nil, fmt.Errorf("firefox %s %d: %s", path, resp.StatusCode, truncate(string(respBody), 200))
	}
	var out webdriverResponse
	_ = json.Unmarshal(respBody, &out)
	return &out, nil
}

func (d *FirefoxDriver) get(ctx context.Context, path string) (*webdriverResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("firefox %s %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	var out webdriverResponse
	_ = json.Unmarshal(body, &out)
	return &out, nil
}
