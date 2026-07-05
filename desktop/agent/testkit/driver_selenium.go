package testkit

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// seleniumBackend drives Chrome through the W3C WebDriver protocol.
// It uses an already-running Selenium/ChromeDriver endpoint when
// SELENIUM_REMOTE_URL or YAVER_SELENIUM_REMOTE_URL is set; otherwise
// it starts chromedriver from PATH on a random local port.
type seleniumBackend struct {
	opts ChromeOpts
	d    *FirefoxDriver
}

func newSeleniumBackend(opts ChromeOpts) *seleniumBackend {
	return &seleniumBackend{opts: opts}
}

func (s *seleniumBackend) Launch(ctx context.Context) error {
	if remote := seleniumRemoteURL(); remote != "" {
		s.d = &FirefoxDriver{
			baseURL: strings.TrimRight(remote, "/"),
			client:  &http.Client{Timeout: 30 * time.Second},
		}
		return s.newChromeSession(ctx)
	}

	bin, err := exec.LookPath("chromedriver")
	if err != nil {
		return fmt.Errorf("chromedriver not found for Selenium/WebDriver autotest — install ChromeDriver or set SELENIUM_REMOTE_URL/YAVER_SELENIUM_REMOTE_URL to a Selenium server")
	}
	port := pickFreePort()
	cmd := exec.CommandContext(ctx, bin, "--port="+fmt.Sprintf("%d", port))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start chromedriver: %w", err)
	}
	d := &FirefoxDriver{
		binary:  bin,
		port:    port,
		cmd:     cmd,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.ping() {
			s.d = d
			return s.newChromeSession(ctx)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return fmt.Errorf("chromedriver did not start within 5s")
}

func seleniumRemoteURL() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_SELENIUM_REMOTE_URL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("SELENIUM_REMOTE_URL"))
}

func (s *seleniumBackend) newChromeSession(ctx context.Context) error {
	args := []string{
		"--disable-gpu",
		"--mute-audio",
		"--no-sandbox",
	}
	if !s.opts.Headful {
		args = append(args, "--headless=new")
	}
	if s.opts.ViewportW > 0 && s.opts.ViewportH > 0 {
		args = append(args, fmt.Sprintf("--window-size=%d,%d", s.opts.ViewportW, s.opts.ViewportH))
	}
	if strings.TrimSpace(s.opts.UserDataDir) != "" {
		args = append(args, "--user-data-dir="+s.opts.UserDataDir)
	}
	body := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": map[string]interface{}{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]interface{}{
					"args": args,
				},
			},
		},
	}
	resp, err := s.d.post(ctx, "/session", body)
	if err != nil {
		return err
	}
	s.d.sessionID = resp.Value.SessionID
	if s.d.sessionID == "" {
		return fmt.Errorf("selenium chrome session id missing in response")
	}
	if s.opts.ViewportW > 0 && s.opts.ViewportH > 0 {
		_, _ = s.d.post(ctx, "/session/"+s.d.sessionID+"/window/rect", map[string]interface{}{
			"width": s.opts.ViewportW, "height": s.opts.ViewportH,
		})
	}
	return nil
}

func (s *seleniumBackend) Navigate(ctx context.Context, url string) error {
	return s.d.Navigate(ctx, url)
}

func (s *seleniumBackend) Snapshot(ctx context.Context) (Snapshot, error) {
	resp, err := s.d.post(ctx, "/session/"+s.d.sessionID+"/execute/sync", map[string]interface{}{
		"script": domSnapshotJS,
		"args":   []interface{}{},
	})
	if err != nil {
		return Snapshot{}, err
	}
	return parseSnapshotJSON(resp.Value.String)
}

func (s *seleniumBackend) Click(ctx context.Context, selector string) error {
	return s.d.Click(ctx, selector)
}

func (s *seleniumBackend) Fill(ctx context.Context, selector, value string) error {
	return s.d.SendKeys(ctx, selector, value)
}

func (s *seleniumBackend) VisibleText(ctx context.Context, selector string) (string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = "body"
	}
	resp, err := s.d.post(ctx, "/session/"+s.d.sessionID+"/execute/sync", map[string]interface{}{
		"script": `const el = document.querySelector(arguments[0]); return el ? (el.innerText || el.textContent || "") : "";`,
		"args":   []interface{}{selector},
	})
	if err != nil {
		return "", err
	}
	return resp.Value.String, nil
}

func (s *seleniumBackend) Screenshot(ctx context.Context) ([]byte, error) {
	return s.d.Screenshot(ctx)
}

func (s *seleniumBackend) Console() []ConsoleMsg { return nil }

func (s *seleniumBackend) Network() []NetEvent { return nil }

func (s *seleniumBackend) Close() {
	if s.d != nil {
		s.d.Close()
	}
}
