package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	Browser   string
	ClipID    string
	Recorder  *WebDriverVideoRecorder
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
			"description": "Start a first-class W3C WebDriver session on this runtime host. Supports Chrome, Firefox, and real Safari (macOS with safaridriver enabled). Does not bypass CAPTCHA or site auth; hand off to the user when needed.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"session_id":     map[string]interface{}{"type": "string", "description": "Optional custom session ID"},
				"browser":        map[string]interface{}{"type": "string", "enum": []string{"chrome", "firefox", "safari"}, "description": "Actual browser to launch. Defaults to chrome; Safari is real Safari, not Playwright WebKit."},
				"url":            map[string]interface{}{"type": "string", "description": "Optional initial URL"},
				"headful":        map[string]interface{}{"type": "boolean", "description": "Show Chrome window visibly"},
				"profile":        map[string]interface{}{"type": "string", "description": "Persistent Chrome profile name or absolute user-data-dir"},
				"width":          map[string]interface{}{"type": "integer", "description": "Viewport width, default 1280"},
				"height":         map[string]interface{}{"type": "integer", "description": "Viewport height, default 800"},
				"record":         map[string]interface{}{"type": "boolean", "description": "Record the actual WebDriver session to an MP4 served through the task/video clip route."},
				"record_seconds": map[string]interface{}{"type": "integer", "description": "Recording safety cap; default 600 seconds."},
			}},
		},
		{
			"name":        "selenium_fix",
			"description": "Install a Chrome-for-Testing ChromeDriver matching the installed Chrome/Chromium major.minor.build into Yaver's owner-only cache, then verify it executes. Use when selenium_status reports missing, quarantined, or mismatched ChromeDriver.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
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
	case "selenium_fix":
		out, err := installMatchingChromeDriver()
		if err != nil {
			return mcpToolError("selenium_fix: " + err.Error())
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
		// Parity with browser_screenshot: emit a first-class MCP image block
		// so vision-capable clients render the pixels. Text-only clients get
		// this rewritten to a text analysis by finalizeMCPResult (mcp_vision.go).
		if b64, _ := out["base64"].(string); b64 != "" {
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": fmt.Sprintf("Screenshot captured from Selenium session %s (%d bytes PNG)", a.SessionID, out["bytes"])},
					{"type": "image", "data": b64, "mimeType": "image/png"},
				},
			}
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
	SessionID     string `json:"session_id"`
	URL           string `json:"url"`
	Headful       bool   `json:"headful"`
	Profile       string `json:"profile"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Browser       string `json:"browser"`
	Record        bool   `json:"record"`
	RecordSeconds int    `json:"record_seconds"`
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
	if remote != "" {
		return map[string]interface{}{
			"ok": true, "driver": "selenium", "remote_url_set": true,
			"safety": "Yaver Selenium uses normal browser automation only. It must not bypass CAPTCHA, auth, paywalls, rate limits, or site access controls.",
		}
	}
	browserPath, browserVersion := installedChromeVersion()
	chromedriver, lookupErr := exec.LookPath("chromedriver")
	driverVersion, driverErr := executableVersion(chromedriver)
	if managedPath, managedVersion := matchingManagedChromeDriver(browserVersion); managedPath != "" {
		chromedriver, driverVersion, driverErr, lookupErr = managedPath, managedVersion, nil, nil
	}
	versionMatch := chromeBuildVersion(driverVersion) != "" && chromeBuildVersion(driverVersion) == chromeBuildVersion(browserVersion)
	ready := lookupErr == nil && driverErr == nil && browserVersion != "" && versionMatch
	errorText := ""
	switch {
	case lookupErr != nil:
		errorText = "ChromeDriver is missing"
	case driverErr != nil:
		errorText = "ChromeDriver is present but cannot execute (on macOS, check Gatekeeper quarantine): " + driverErr.Error()
	case browserVersion == "":
		errorText = "Chrome/Chromium is not installed or cannot report its version"
	case !versionMatch:
		errorText = fmt.Sprintf("ChromeDriver %s does not match browser %s; post-M115 WebDriver requires the same major.minor.build", driverVersion, browserVersion)
	}
	return map[string]interface{}{
		"ok":                   ready,
		"driver":               "selenium",
		"remote_url_set":       false,
		"chromedriver_path":    chromedriver,
		"chromedriver_ready":   driverErr == nil,
		"chromedriver_version": driverVersion,
		"browser_path":         browserPath,
		"browser_version":      browserVersion,
		"version_match":        versionMatch,
		"error":                errorText,
		"install_hint":         "Install a Chrome-for-Testing ChromeDriver matching the installed browser's major.minor.build, or set SELENIUM_REMOTE_URL/YAVER_SELENIUM_REMOTE_URL.",
		"fix_tool":             "selenium_fix",
		"safety":               "Yaver Selenium uses normal browser automation only. It must not bypass CAPTCHA, auth, paywalls, rate limits, or site access controls.",
	}
}

type chromeForTestingBuilds struct {
	Builds map[string]struct {
		Version   string `json:"version"`
		Downloads struct {
			ChromeDriver []struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
			} `json:"chromedriver"`
		} `json:"downloads"`
	} `json:"builds"`
}

func installMatchingChromeDriver() (map[string]interface{}, error) {
	_, browserVersion := installedChromeVersion()
	build := chromeBuildVersion(browserVersion)
	if build == "" {
		return nil, fmt.Errorf("Chrome/Chromium is missing or did not report a major.minor.build version")
	}
	platform, executableName := chromeDriverPlatform()
	if platform == "" {
		return nil, fmt.Errorf("Chrome for Testing does not publish ChromeDriver for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Get("https://googlechromelabs.github.io/chrome-for-testing/latest-patch-versions-per-build-with-downloads.json")
	if err != nil {
		return nil, fmt.Errorf("read Chrome-for-Testing metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Chrome-for-Testing metadata returned HTTP %d", resp.StatusCode)
	}
	metadataBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	version, downloadURL, err := matchingChromeDriverDownload(metadataBytes, build, platform)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(downloadURL)
	if err != nil || u.Scheme != "https" || u.Host != "storage.googleapis.com" || !strings.HasPrefix(u.Path, "/chrome-for-testing-public/") {
		return nil, fmt.Errorf("refusing unexpected ChromeDriver download URL %q", downloadURL)
	}
	download, err := client.Get(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("download ChromeDriver %s: %w", version, err)
	}
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ChromeDriver download returned HTTP %d", download.StatusCode)
	}
	archive, err := io.ReadAll(io.LimitReader(download.Body, 100<<20))
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open ChromeDriver archive: %w", err)
	}
	var binary []byte
	for _, file := range reader.File {
		if filepath.Base(file.Name) != executableName || file.FileInfo().IsDir() {
			continue
		}
		r, openErr := file.Open()
		if openErr != nil {
			return nil, openErr
		}
		binary, err = io.ReadAll(io.LimitReader(r, 100<<20))
		_ = r.Close()
		if err != nil {
			return nil, err
		}
		break
	}
	if len(binary) == 0 {
		return nil, fmt.Errorf("ChromeDriver archive did not contain %s", executableName)
	}

	destination, err := managedChromeDriverPath(version, executableName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(destination), 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".chromedriver-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(binary); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return nil, err
	}
	installedVersion, err := executableVersion(destination)
	if err != nil {
		return nil, fmt.Errorf("installed ChromeDriver cannot execute: %w", err)
	}
	if chromeBuildVersion(installedVersion) != build {
		return nil, fmt.Errorf("installed ChromeDriver %s does not match browser build %s", installedVersion, build)
	}
	return map[string]interface{}{
		"ok": true, "browser_version": browserVersion, "chromedriver_version": installedVersion,
		"chromedriver_path": destination, "source": "Chrome for Testing",
	}, nil
}

func matchingChromeDriverDownload(raw []byte, build, platform string) (string, string, error) {
	var metadata chromeForTestingBuilds
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return "", "", fmt.Errorf("parse Chrome-for-Testing metadata: %w", err)
	}
	entry, ok := metadata.Builds[build]
	if !ok {
		return "", "", fmt.Errorf("Chrome for Testing has no ChromeDriver build matching %s", build)
	}
	for _, download := range entry.Downloads.ChromeDriver {
		if download.Platform == platform && download.URL != "" {
			return entry.Version, download.URL, nil
		}
	}
	return "", "", fmt.Errorf("Chrome for Testing has no %s ChromeDriver for %s", platform, build)
}

func chromeDriverPlatform() (string, string) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "mac-arm64", "chromedriver"
	case "darwin/amd64":
		return "mac-x64", "chromedriver"
	case "linux/amd64":
		return "linux64", "chromedriver"
	case "windows/amd64":
		return "win64", "chromedriver.exe"
	case "windows/386":
		return "win32", "chromedriver.exe"
	default:
		return "", ""
	}
}

func managedChromeDriverPath(version, executableName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".yaver", "browser-drivers", "chromedriver", version, executableName), nil
}

func matchingManagedChromeDriver(browserVersion string) (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}
	root := filepath.Join(home, ".yaver", "browser-drivers", "chromedriver")
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", ""
	}
	executableName := "chromedriver"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	for _, entry := range entries {
		if !entry.IsDir() || chromeBuildVersion(entry.Name()) != chromeBuildVersion(browserVersion) {
			continue
		}
		path := filepath.Join(root, entry.Name(), executableName)
		if version, err := executableVersion(path); err == nil && chromeBuildVersion(version) == chromeBuildVersion(browserVersion) {
			return path, version
		}
	}
	return "", ""
}

func executableVersion(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("executable not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return "", err
	}
	if version := dottedVersion(string(out)); version != "" {
		return version, nil
	}
	return "", fmt.Errorf("version output was not understood")
}

func installedChromeVersion() (string, string) {
	candidates := []string{}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	seen := map[string]bool{}
	for _, path := range candidates {
		if seen[path] {
			continue
		}
		seen[path] = true
		if version, err := executableVersion(path); err == nil {
			return path, version
		}
	}
	return "", ""
}

func dottedVersion(output string) string {
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, " ,;()[]")
		parts := strings.Split(candidate, ".")
		if len(parts) < 3 {
			continue
		}
		valid := true
		for _, part := range parts {
			if part == "" {
				valid = false
				break
			}
			for _, r := range part {
				if r < '0' || r > '9' {
					valid = false
					break
				}
			}
		}
		if valid {
			return candidate
		}
	}
	return ""
}

func chromeBuildVersion(version string) string {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:3], ".")
}

func (m *seleniumMCPManager) start(a seleniumStartArgs) (map[string]interface{}, error) {
	a.Browser = strings.ToLower(strings.TrimSpace(a.Browser))
	if a.Browser == "" {
		a.Browser = "chrome"
	}
	if a.Browser != "chrome" && a.Browser != "firefox" && a.Browser != "safari" {
		return nil, fmt.Errorf("browser must be chrome, firefox, or safari")
	}
	if a.SessionID == "" {
		a.SessionID = fmt.Sprintf("%s-%d", a.Browser, time.Now().UnixMilli()%1000000)
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
	var driver testkit.WebDriver
	var err error
	switch a.Browser {
	case "chrome":
		status := seleniumReadiness()
		if ready, _ := status["ok"].(bool); !ready {
			if reason, _ := status["error"].(string); reason != "" {
				return nil, fmt.Errorf("chrome webdriver capability unavailable: %s", reason)
			}
			return nil, fmt.Errorf("chrome webdriver capability unavailable")
		}
		driverPath, _ := status["chromedriver_path"].(string)
		driver, err = testkit.NewWebDriver("selenium", testkit.ChromeOpts{
			ViewportW: a.Width, ViewportH: a.Height, Headful: a.Headful,
			UserDataDir: profile, DriverPath: driverPath,
		})
		if err == nil {
			err = driver.Launch(ctx)
		}
	case "firefox":
		var d *testkit.FirefoxDriver
		d, err = testkit.NewFirefoxDriver(ctx)
		if err == nil {
			err = d.NewSession(ctx, a.Headful, a.Width, a.Height)
			driver = d
		}
	case "safari":
		var d *testkit.FirefoxDriver
		d, err = testkit.NewSafariDriver(ctx)
		if err == nil {
			err = testkit.NewSafariSession(ctx, d, true, a.Width, a.Height)
			driver = d
		}
	}
	if err != nil {
		if driver != nil {
			driver.Close()
		}
		return nil, err
	}
	sess := &seleniumMCPSession{ID: a.SessionID, Driver: driver, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Headful: a.Headful || a.Browser == "safari", Profile: profile, Width: a.Width, Height: a.Height, Browser: a.Browser}
	m.mu.Lock()
	if old := m.sessions[a.SessionID]; old != nil {
		old.Driver.Close()
	}
	m.sessions[a.SessionID] = sess
	m.mu.Unlock()
	if a.Record {
		vpm := ActiveVibePreviewManager()
		if vpm == nil {
			vpm = NewVibePreviewManager(nil)
		}
		recorder, recordErr := startWebDriverRecording(vpm, driver, a.SessionID, a.RecordSeconds)
		if recordErr != nil {
			_ = m.close(a.SessionID)
			return nil, recordErr
		}
		m.mu.Lock()
		sess.Recorder, sess.ClipID = recorder, recorder.clipID
		m.mu.Unlock()
		if taskID := strings.TrimSpace(os.Getenv("YAVER_TASK_ID")); taskID != "" {
			writeTaskClipMarker(taskID, recorder.clipID)
			if tm := ActiveTaskManager(); tm != nil {
				tm.SetTaskVideoState(taskID, recorder.clipID, "recording")
			}
		}
	}
	if strings.TrimSpace(a.URL) != "" {
		out, navErr := m.navigate(a.SessionID, a.URL)
		if out != nil {
			out["browser"] = a.Browser
			if sess.ClipID != "" {
				out["recording"], out["clip_id"], out["clip_url"] = true, sess.ClipID, "/vibing/preview/clip/"+sess.ClipID
			}
		}
		return out, navErr
	}
	out := map[string]interface{}{"ok": true, "session_id": a.SessionID, "browser": a.Browser, "profile": profile, "headful": sess.Headful, "width": a.Width, "height": a.Height}
	if sess.ClipID != "" {
		out["recording"], out["clip_id"], out["clip_url"] = true, sess.ClipID, "/vibing/preview/clip/"+sess.ClipID
	}
	return out, nil
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

func (m *seleniumMCPManager) execute(sessionID, script string, args ...interface{}) (interface{}, error) {
	sess, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	driver, ok := sess.Driver.(testkit.ScriptWebDriver)
	if !ok {
		return nil, fmt.Errorf("browser engine does not support script execution")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return driver.ExecuteScript(ctx, script, args)
}

func (m *seleniumMCPManager) selectValue(sessionID, selector, value string) (map[string]interface{}, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("selector is required")
	}
	_, err := m.execute(sessionID, `
const el = document.querySelector(arguments[0]);
if (!el) throw new Error('element not found: ' + arguments[0]);
el.value = arguments[1];
el.dispatchEvent(new Event('input', {bubbles:true}));
el.dispatchEvent(new Event('change', {bubbles:true}));
return el.value;`, selector, value)
	if err != nil {
		return nil, err
	}
	return m.snapshot(sessionID)
}

func (m *seleniumMCPManager) scroll(sessionID string, x, y int) (map[string]interface{}, error) {
	_, err := m.execute(sessionID, `window.scrollBy(arguments[0], arguments[1]); return {x:scrollX,y:scrollY};`, x, y)
	if err != nil {
		return nil, err
	}
	return m.snapshot(sessionID)
}

func (m *seleniumMCPManager) waitVisible(sessionID, selector string, timeoutMs int) error {
	if timeoutMs <= 0 {
		timeoutMs = 10000
	}
	if timeoutMs > 60000 {
		timeoutMs = 60000
	}
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		value, err := m.execute(sessionID, `
const el=document.querySelector(arguments[0]);
if(!el) return false;
const s=getComputedStyle(el), r=el.getBoundingClientRect();
return s.display!=='none' && s.visibility!=='hidden' && r.width>0 && r.height>0;`, selector)
		if err != nil {
			return err
		}
		if visible, _ := value.(bool); visible {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("selector %q was not visible within %dms", selector, timeoutMs)
}

func (m *seleniumMCPManager) waitNavigation(sessionID string, timeoutMs int) error {
	sess, err := m.get(sessionID)
	if err != nil {
		return err
	}
	initial := sess.LastURL
	if timeoutMs <= 0 {
		timeoutMs = 10000
	}
	if timeoutMs > 60000 {
		timeoutMs = 60000
	}
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		snap, snapErr := sess.Driver.Snapshot(ctx)
		cancel()
		if snapErr != nil {
			return snapErr
		}
		if snap.URL != "" && snap.URL != initial {
			m.mu.Lock()
			sess.LastURL, sess.Title, sess.UpdatedAt = snap.URL, snap.Title, time.Now().UTC()
			m.mu.Unlock()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("browser URL did not change within %dms", timeoutMs)
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
			"browser":    s.Browser,
			"clip_id":    s.ClipID,
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
	var recordErr error
	if sess.Recorder != nil {
		sess.Recorder.Stop()
		videoStatus := "ready"
		if sess.Recorder.rec.Status != "ready" {
			videoStatus = "failed"
			reason := strings.TrimSpace(sess.Recorder.rec.Err)
			if reason == "" {
				reason = "MP4 was not finalized"
			}
			recordErr = fmt.Errorf("browser recording failed: %s", reason)
		}
		if taskID := strings.TrimSpace(os.Getenv("YAVER_TASK_ID")); taskID != "" {
			if tm := ActiveTaskManager(); tm != nil {
				tm.SetTaskVideoState(taskID, sess.ClipID, videoStatus)
			}
		}
	}
	sess.Driver.Close()
	return recordErr
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

func (m *seleniumMCPManager) has(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[strings.TrimSpace(sessionID)] != nil
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
