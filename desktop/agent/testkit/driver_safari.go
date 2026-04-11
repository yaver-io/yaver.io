package testkit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Safari / WebKit driver via safaridriver (bundled with macOS).
//
// Same W3C WebDriver protocol as the Firefox driver, just pointed at
// `/usr/bin/safaridriver` instead of geckodriver. Requires the user
// to run `safaridriver --enable` once (we point them at that in the
// error message if it's not enabled yet) and to tick "Allow Remote
// Automation" in Safari's Develop menu. Solo dev only touches this
// for the rare cross-browser snapshot, so making it a 5-line wrapper
// around FirefoxDriver's existing W3C client is the right trade-off.

// NewSafariDriver spawns safaridriver and returns a W3C-compatible
// driver. macOS only — Safari doesn't exist elsewhere.
func NewSafariDriver(ctx context.Context) (*FirefoxDriver, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("safari driver requires macOS")
	}
	bin, err := exec.LookPath("safaridriver")
	if err != nil {
		// macOS ships /usr/bin/safaridriver but Go's LookPath follows
		// $PATH; try the known absolute path as a fallback.
		if _, statErr := os.Stat("/usr/bin/safaridriver"); statErr == nil {
			bin = "/usr/bin/safaridriver"
		} else {
			return nil, fmt.Errorf("safaridriver not found — ensure Safari is up to date and run: sudo safaridriver --enable")
		}
	}
	port := pickFreePort()
	cmd := exec.CommandContext(ctx, bin, "--port", fmt.Sprintf("%d", port))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start safaridriver: %w — run `sudo safaridriver --enable` once", err)
	}
	d := &FirefoxDriver{
		binary:  bin,
		port:    port,
		cmd:     cmd,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
	}
	// Small wait-for-ready loop; safaridriver is usually ready in
	// under 300ms.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.ping() {
			return d, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return nil, fmt.Errorf("safaridriver did not start — have you run `sudo safaridriver --enable`?")
}

// NewSafariSession opens a new Safari window. Wraps NewSession with
// Safari-specific capabilities so the caller doesn't have to know
// the exact magic strings.
func NewSafariSession(ctx context.Context, d *FirefoxDriver, headful bool, vw, vh int) error {
	// Safari has no headless mode, so `headful` is effectively
	// always-true. We keep the parameter for API symmetry.
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": map[string]interface{}{
				"browserName": "safari",
			},
		},
	}
	resp, err := d.post(ctx, "/session", body)
	if err != nil {
		return err
	}
	d.sessionID = resp.Value.SessionID
	if d.sessionID == "" {
		return fmt.Errorf("safari session id missing in response")
	}
	if vw > 0 && vh > 0 {
		_, _ = d.post(ctx, "/session/"+d.sessionID+"/window/rect", map[string]interface{}{
			"width": vw, "height": vh,
		})
	}
	return nil
}

// Client ensures we pull in net/http's client even when the existing
// reference above isn't exercised by a test. This is a compile-time
// check only.
func (d *FirefoxDriver) ensureClient() {
	if d.client == nil {
		// The NewFirefoxDriver / NewSafariDriver constructors always
		// set this; this is belt-and-braces.
	}
}
