package main

// gateway_local_driver.go — the SELF-DRIVING phone deviceDriver.
//
// A second-hand Android running ONLY the Yaver app (no Pi, no USB-adb host) can
// still be a full gateway clone IF the app can drive its own screen. The Yaver
// app's AccessibilityService provides exactly that — tap/type/read/screenshot —
// and exposes a tiny LOOPBACK control surface that the on-device agent
// (libyaver.so) calls. This driver is the agent-side client of that surface, so
// the SAME gateway code (auth broker, invoke, act, app-sync) runs unchanged on a
// self-driving phone, a redroid container, or an adb-attached phone.
//
// It is the second concrete deviceDriver (alongside redroidDeviceDriver). The
// broker picks it when a connector pins the local device (Connector.Device ==
// localDeviceSentinel, "self").
//
// LOOPBACK CONTROL PROTOCOL (implemented natively by the AccessibilityService;
// see docs/yaver-self-driving-phone.md). All on 127.0.0.1, never network-exposed:
//
//	POST /a11y/launch      {package}            → {ok}
//	POST /a11y/launch-url  {url}                → {ok}
//	POST /a11y/type        {text}               → {ok}
//	POST /a11y/tap         {label} | {x,y}      → {ok}
//	GET  /a11y/texts                            → {nodes:[{text,resourceId,...}]}
//	GET  /a11y/frame                            → image/png bytes
//	GET  /a11y/sms/latest                       → {code}   (consent-gated here too)
//
// KEY DIFFERENCE vs redroid: a physical phone STAYS logged in, so RestoreSnapshot
// succeeds (no re-login) — this sidesteps the golden-snapshot engine entirely.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// localDeviceSentinel is the Connector.Device value that selects the self-driving
// (AccessibilityService) driver instead of an adb serial.
const localDeviceSentinel = "self"

// localA11yControlURL is the on-device AccessibilityService control surface. The
// Yaver app binds it on loopback only; the agent (same device) is the only
// caller.
const localA11yControlURL = "http://127.0.0.1:18092"

// localAccessibilityDriver implements deviceDriver by calling the on-device
// AccessibilityService over its loopback control surface.
type localAccessibilityDriver struct {
	baseURL string
	client  *http.Client
}

// newLocalAccessibilityDriver builds the driver. An empty baseURL ⇒ the default
// loopback control URL; tests pass an httptest server URL.
func newLocalAccessibilityDriver(baseURL string) *localAccessibilityDriver {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = localA11yControlURL
	}
	return &localAccessibilityDriver{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// postJSON posts a JSON body to a control path and checks for a 2xx + {ok:true}.
func (d *localAccessibilityDriver) postJSON(path string, body interface{}) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := d.client.Post(d.baseURL+path, "application/json", bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("a11y %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("a11y %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	// {ok:false} is a soft failure the service reports (e.g. label not found).
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &r) == nil && !r.OK && r.Error != "" {
		return fmt.Errorf("a11y %s: %s", path, r.Error)
	}
	return nil
}

func (d *localAccessibilityDriver) Launch(pkg string) error {
	return d.postJSON("/a11y/launch", map[string]string{"package": pkg})
}

func (d *localAccessibilityDriver) LaunchURL(url string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("a11y launch url: url is required")
	}
	return d.postJSON("/a11y/launch-url", map[string]string{"url": url})
}

func (d *localAccessibilityDriver) Type(text string) error {
	return d.postJSON("/a11y/type", map[string]string{"text": text})
}

func (d *localAccessibilityDriver) Tap(target string) error {
	return d.postJSON("/a11y/tap", map[string]string{"label": target})
}

func (d *localAccessibilityDriver) Frame() ([]byte, error) {
	resp, err := d.client.Get(d.baseURL + "/a11y/frame")
	if err != nil {
		return nil, fmt.Errorf("a11y frame: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("a11y frame: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func (d *localAccessibilityDriver) UiTexts() ([]uiNode, error) {
	resp, err := d.client.Get(d.baseURL + "/a11y/texts")
	if err != nil {
		return nil, fmt.Errorf("a11y texts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("a11y texts: status %d", resp.StatusCode)
	}
	var out struct {
		Nodes []uiNode `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("a11y texts decode: %w", err)
	}
	return out.Nodes, nil
}

// Snapshot returns a stable self-reference. A physical phone IS its own persistent
// state, so there is no container to snapshot — we just record that this clone is
// the local device.
func (d *localAccessibilityDriver) Snapshot() (string, string, error) {
	return "self:" + d.baseURL, "live", nil
}

// RestoreSnapshot succeeds without doing anything: a logged-in physical phone
// stays logged in, so "restore" = "the device is already the session". This is
// only ever called when a snapshot ref exists (saved after a successful login),
// so it correctly takes the fast path and skips re-login.
func (d *localAccessibilityDriver) RestoreSnapshot(instanceID, snapshotID string) error {
	return nil
}

// ReadSMS reads the phone's own inbox via the service, GATED by read_device_sms
// consent (identical policy to the redroid driver — never read SMS without the
// opt-in; ungranted ⇒ "" so the handler escalates to a human gate).
func (d *localAccessibilityDriver) ReadSMS() (string, error) {
	if !consentAllows(consentReadDeviceSms) {
		return "", nil
	}
	resp, err := d.client.Get(d.baseURL + "/a11y/sms/latest")
	if err != nil {
		return "", fmt.Errorf("a11y sms: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("a11y sms: status %d", resp.StatusCode)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("a11y sms decode: %w", err)
	}
	return strings.TrimSpace(out.Code), nil
}
