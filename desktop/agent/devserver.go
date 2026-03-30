package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── DevServer Interface ───────────────────────────────────────────────

// DevServer is the interface all dev server implementations must satisfy.
// Each framework (Expo, Flutter, Vite, Next.js, etc.) implements this.
type DevServer interface {
	// Name returns the framework identifier ("expo", "flutter", "vite", "nextjs", "custom").
	Name() string
	// Detect returns true if the working directory contains this framework's project.
	Detect(workDir string) bool
	// Start launches the dev server process. Blocks until server is ready or error.
	Start(ctx context.Context, opts DevServerOpts) error
	// Stop terminates the dev server process.
	Stop() error
	// Port returns the local port the dev server is listening on.
	Port() int
	// BundleURL returns the relative URL path the phone should load.
	// For Expo: "/dev/index.bundle?platform=ios&dev=true"
	// For Vite: "/dev/"
	BundleURL(platform string) string
	// SupportsHotReload returns true if the framework can reload without rebuild.
	SupportsHotReload() bool
	// Reload triggers a hot reload if supported.
	Reload() error
	// PreStart sets the name/port/workDir before async Start (for immediate Status).
	PreStart(name string, port int, workDir string)
	// Status returns the current state.
	Status() DevServerStatus
}

// DevServerOpts configures a dev server launch.
type DevServerOpts struct {
	WorkDir  string
	Port     int               // override default port (0 = framework default)
	Platform string            // "ios", "android", "web"
	Env      map[string]string // extra environment variables
	Args     []string          // extra args passed to the dev server command
}

// DevServerStatus is the JSON-serializable status of a dev server.
type DevServerStatus struct {
	Framework  string `json:"framework"`
	Running    bool   `json:"running"`
	Port       int    `json:"port"`
	BundleURL  string `json:"bundleUrl"`
	DeepLink   string `json:"deepLink,omitempty"`
	DevMode    string `json:"devMode,omitempty"`    // "dev-client", "web", "expo-go", "" (for non-Expo)
	StartedAt  string `json:"startedAt,omitempty"`
	Error      string `json:"error,omitempty"`
	PID        int    `json:"pid,omitempty"`
	WorkDir    string `json:"workDir,omitempty"`
	HotReload  bool   `json:"hotReload"`
}

// DevServerEvent is pushed via SSE on /dev/events.
type DevServerEvent struct {
	Type      string `json:"type"`                // "ready", "reload", "error", "stopped", "file_changed"
	Framework string `json:"framework"`
	BundleURL string `json:"bundleUrl,omitempty"`
	DeepLink  string `json:"deepLink,omitempty"`
	Message   string `json:"message,omitempty"`
	Timestamp string `json:"timestamp"`
}

// ─── DevServer Registry ────────────────────────────────────────────────

var (
	devServerRegistry   []DevServer
	devServerRegistryMu sync.Mutex
)

func registerDevServer(ds DevServer) {
	devServerRegistryMu.Lock()
	defer devServerRegistryMu.Unlock()
	devServerRegistry = append(devServerRegistry, ds)
}

// detectDevServer auto-detects the framework for a given directory.
func detectDevServer(workDir string) DevServer {
	devServerRegistryMu.Lock()
	defer devServerRegistryMu.Unlock()
	for _, ds := range devServerRegistry {
		if ds.Detect(workDir) {
			return ds
		}
	}
	return nil
}

// getDevServerByName returns a registered dev server by framework name.
func getDevServerByName(name string) DevServer {
	devServerRegistryMu.Lock()
	defer devServerRegistryMu.Unlock()
	for _, ds := range devServerRegistry {
		if ds.Name() == name {
			return ds
		}
	}
	return nil
}

func init() {
	registerDevServer(&ExpoDevServer{})
	registerDevServer(&ReactNativeDevServer{})
	registerDevServer(&FlutterDevServer{})
	registerDevServer(&ViteDevServer{})
	registerDevServer(&NextDevServer{})
}

// ─── DevServerManager ──────────────────────────────────────────────────

// DevServerManager manages the active dev server session and event subscribers.
type DevServerManager struct {
	mu      sync.RWMutex
	active  *devServerSession
	subs    []chan DevServerEvent
	subsMu  sync.Mutex

	// Agent's externally reachable URL (for Metro proxy URL).
	// Set by the HTTP server after relay connection is established.
	// Examples: "http://192.168.1.10:18080", "https://public.yaver.io/d/abc123"
	AgentURL string
}

type devServerSession struct {
	server  DevServer
	proxy   *httputil.ReverseProxy
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewDevServerManager creates a new manager.
func NewDevServerManager() *DevServerManager {
	return &DevServerManager{}
}

// Start launches a dev server for the given framework in the given directory.
// For fast frameworks (Vite, Next.js), blocks until ready.
// For slow frameworks (Flutter, Expo), launches async and returns immediately.
func (m *DevServerManager) Start(framework, workDir, platform string, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing session
	if m.active != nil {
		m.active.server.Stop()
		m.active.cancel()
		m.active = nil
	}

	var ds DevServer
	if framework != "" {
		ds = getDevServerByName(framework)
		if ds == nil {
			// Name lookup failed (name set at Start time) — fall back to auto-detection
			ds = detectDevServer(workDir)
		}
		if ds == nil {
			return fmt.Errorf("unknown framework: %s", framework)
		}
	} else {
		ds = detectDevServer(workDir)
		if ds == nil {
			return fmt.Errorf("could not detect framework in %s", workDir)
		}
	}

	// Pre-set name/port/workDir so Status() returns meaningful data immediately
	// (before the async Start goroutine sets them again inside Start())
	frameworkName := framework
	if frameworkName == "" {
		// Derive name from the detected dev server type
		switch ds.(type) {
		case *ExpoDevServer:
			frameworkName = "expo"
		case *ReactNativeDevServer:
			frameworkName = "react-native"
		case *FlutterDevServer:
			frameworkName = "flutter"
		case *ViteDevServer:
			frameworkName = "vite"
		case *NextDevServer:
			frameworkName = "nextjs"
		}
	}
	defaultPort := port
	if defaultPort == 0 {
		switch frameworkName {
		case "expo", "react-native":
			defaultPort = 8081
		case "flutter":
			defaultPort = 9100
		case "vite":
			defaultPort = 5173
		case "nextjs":
			defaultPort = 3000
		}
	}
	ds.PreStart(frameworkName, defaultPort, workDir)

	log.Printf("[dev] Starting %s dev server in %s", frameworkName, workDir)

	ctx, cancel := context.WithCancel(context.Background())
	opts := DevServerOpts{
		WorkDir:  workDir,
		Port:     port,
		Platform: platform,
	}

	// Pass the agent's reachable URL so Metro can tell dev clients to connect
	// through the relay instead of hardcoding the local IP.
	if m.AgentURL != "" {
		opts.Env = map[string]string{
			"YAVER_AGENT_URL": m.AgentURL,
		}
	}

	// Set up the session immediately so Status() returns "starting"
	m.active = &devServerSession{
		server: ds,
		ctx:    ctx,
		cancel: cancel,
	}

	// Emit starting event
	m.emit(DevServerEvent{
		Type:      "starting",
		Framework: frameworkName,
		Message:   fmt.Sprintf("Starting %s dev server...", ds.Name()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Launch start in background — don't block the HTTP response
	go func() {
		if err := ds.Start(ctx, opts); err != nil {
			log.Printf("[dev] %s failed to start: %v", ds.Name(), err)
			m.mu.Lock()
			if m.active != nil && m.active.server == ds {
				m.active.cancel()
				m.active = nil
			}
			m.mu.Unlock()
			m.emit(DevServerEvent{
				Type:      "error",
				Framework: ds.Name(),
				Message:   fmt.Sprintf("Failed to start %s: %v", ds.Name(), err),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
			return
		}

		// Create reverse proxy to the dev server
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", ds.Port()))
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "dev server unavailable", http.StatusBadGateway)
		}

		m.mu.Lock()
		if m.active != nil && m.active.server == ds {
			m.active.proxy = proxy
		}
		m.mu.Unlock()

		log.Printf("[dev] %s ready on port %d", ds.Name(), ds.Port())

		m.emit(DevServerEvent{
			Type:      "ready",
			Framework: ds.Name(),
			BundleURL: ds.BundleURL(platform),
			Message:   fmt.Sprintf("%s dev server ready", ds.Name()),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}()

	return nil
}

// Stop stops the active dev server.
func (m *DevServerManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return fmt.Errorf("no dev server running")
	}

	name := m.active.server.Name()
	m.active.server.Stop()
	m.active.cancel()
	m.active = nil

	m.emit(DevServerEvent{
		Type:      "stopped",
		Framework: name,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	log.Printf("[dev] %s stopped", name)
	return nil
}

// Reload triggers a hot reload on the active dev server.
func (m *DevServerManager) Reload() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == nil {
		return fmt.Errorf("no dev server running")
	}

	if !m.active.server.SupportsHotReload() {
		return fmt.Errorf("%s does not support hot reload", m.active.server.Name())
	}

	if err := m.active.server.Reload(); err != nil {
		return err
	}

	m.emit(DevServerEvent{
		Type:      "reload",
		Framework: m.active.server.Name(),
		BundleURL: m.active.server.BundleURL(""),
		Message:   "Hot reload triggered",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

// Status returns the current dev server status.
func (m *DevServerManager) Status() *DevServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.active == nil {
		return nil
	}

	s := m.active.server.Status()
	return &s
}

// IsRunning returns true if a dev server is active.
func (m *DevServerManager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active != nil
}

// Proxy returns the reverse proxy for the active dev server, or nil.
func (m *DevServerManager) Proxy() *httputil.ReverseProxy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	return m.active.proxy
}

// DevServerPort returns the local port of the active dev server.
func (m *DevServerManager) DevServerPort() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return 0
	}
	return m.active.server.Port()
}

// Subscribe returns a channel that receives dev server events.
func (m *DevServerManager) Subscribe() chan DevServerEvent {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	ch := make(chan DevServerEvent, 16)
	m.subs = append(m.subs, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (m *DevServerManager) Unsubscribe(ch chan DevServerEvent) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for i, s := range m.subs {
		if s == ch {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			close(ch)
			return
		}
	}
}

func (m *DevServerManager) emit(event DevServerEvent) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- event:
		default:
			// Drop if subscriber is slow
		}
	}
}

// ─── Base Dev Server (shared logic) ────────────────────────────────────

// baseDevServer provides shared process management for dev servers.
type baseDevServer struct {
	name      string
	port      int
	cmd       *exec.Cmd
	running   bool
	startedAt time.Time
	workDir   string
	err       string
	mu        sync.Mutex
}

func (b *baseDevServer) Name() string { return b.name }
func (b *baseDevServer) Port() int    { return b.port }

// PreStart sets the name, port, and workDir before the async Start goroutine.
// This ensures Status() returns meaningful data immediately.
func (b *baseDevServer) PreStart(name string, port int, workDir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.name = name
	if port > 0 {
		b.port = port
	}
	b.workDir = workDir
}

func (b *baseDevServer) Status() DevServerStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := DevServerStatus{
		Framework: b.name,
		Running:   b.running,
		Port:      b.port,
		HotReload: true,
		WorkDir:   b.workDir,
		Error:     b.err,
	}
	if b.running {
		s.StartedAt = b.startedAt.UTC().Format(time.RFC3339)
	}
	if b.cmd != nil && b.cmd.Process != nil {
		s.PID = b.cmd.Process.Pid
	}
	return s
}

func (b *baseDevServer) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cmd != nil && b.cmd.Process != nil {
		b.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- b.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			b.cmd.Process.Kill()
		}
	}
	b.running = false
	return nil
}

// startProcess launches a command and waits for readiness by polling a URL.
func (b *baseDevServer) startProcess(ctx context.Context, name string, args []string, workDir string, env []string, readyURL string) error {
	b.mu.Lock()
	b.workDir = workDir
	b.mu.Unlock()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)

	// Pipe output to log with [dev] prefix
	logWriter := &devLogWriter{prefix: fmt.Sprintf("[dev:%s]", b.name)}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter

	// Keep stdin open (Flutter needs it for "r" hot reload)
	stdinPipe, _ := cmd.StdinPipe()
	_ = stdinPipe // kept open for Reload()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	b.mu.Lock()
	b.cmd = cmd
	b.startedAt = time.Now()
	b.mu.Unlock()

	// Wait for dev server to become ready (poll health/readiness)
	deadline := time.After(120 * time.Second) // Expo web first build can take 2+ min
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("%s did not become ready within 60s", name)
		case <-ticker.C:
			resp, err := http.Get(readyURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					b.mu.Lock()
					b.running = true
					b.mu.Unlock()
					return nil
				}
			}
		}
	}
}

// devLogWriter writes dev server output to the agent log with a prefix.
type devLogWriter struct {
	prefix string
	buf    []byte
}

func (w *devLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		if strings.TrimSpace(line) != "" {
			log.Printf("%s %s", w.prefix, line)
		}
	}
	return len(p), nil
}

// ─── Expo Dev Server ───────────────────────────────────────────────────

type ExpoDevServer struct {
	baseDevServer
	devMode string // "dev-client", "web", "expo-go"
}

func (e *ExpoDevServer) Detect(workDir string) bool {
	pkg := filepath.Join(workDir, "package.json")
	data, err := os.ReadFile(pkg)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "\"expo\"")
}

func (e *ExpoDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	e.name = "expo"
	e.port = opts.Port
	if e.port == 0 {
		e.port = 8081
	}

	// Install deps if needed
	if _, err := os.Stat(filepath.Join(opts.WorkDir, "node_modules")); os.IsNotExist(err) {
		log.Printf("[dev] Installing dependencies in %s...", opts.WorkDir)
		install := exec.CommandContext(ctx, "npm", "install", "--legacy-peer-deps")
		install.Dir = opts.WorkDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
	}

	// Run yaver.config.js if it exists (generates SDK config)
	configScript := filepath.Join(opts.WorkDir, "yaver.config.js")
	if _, err := os.Stat(configScript); err == nil {
		log.Printf("[dev] Running yaver.config.js...")
		gen := exec.CommandContext(ctx, "node", "yaver.config.js")
		gen.Dir = opts.WorkDir
		gen.Stdout = os.Stdout
		gen.Stderr = os.Stderr
		gen.Run() // best-effort
	}

	// Check if a dev client app is already installed on the phone.
	projectHash := strings.ReplaceAll(filepath.Base(opts.WorkDir), " ", "_")
	buildMarker := filepath.Join(yaverBuildsDir(), projectHash+".built")
	hasInstalledBuild := fileExists(buildMarker)

	hasNativeProject := fileExists(filepath.Join(opts.WorkDir, "ios", "Podfile")) ||
		fileExists(filepath.Join(opts.WorkDir, "android", "build.gradle"))

	if hasInstalledBuild && hasNativeProject {
		// Dev client already built → just start Metro in dev-client mode
		log.Printf("[dev:expo] Dev client already installed — starting Metro only")
		e.devMode = "dev-client"
		args := []string{"expo", "start",
			"--dev-client",
			"--port", fmt.Sprintf("%d", e.port),
			"--host", "0.0.0.0",
		}

		// If agent URL is available (relay/direct), set it as Metro's packager proxy
		// so dev clients connect through the relay instead of local IP
		var envSlice []string
		if agentURL, ok := opts.Env["YAVER_AGENT_URL"]; ok && agentURL != "" {
			envSlice = append(envSlice, "EXPO_PACKAGER_PROXY_URL="+agentURL+"/dev")
			log.Printf("[dev:expo] Metro proxy URL set to %s/dev (reachable via relay)", agentURL)
		}

		readyURL := fmt.Sprintf("http://127.0.0.1:%d", e.port)
		return e.startProcess(ctx, "npx", args, opts.WorkDir, envSlice, readyURL)
	}

	if hasNativeProject {
		// First time: build + install dev client on phone
		log.Printf("[dev:expo] Building dev client (first time)...")
		e.devMode = "dev-client"

		device := detectIOSDevice(ctx)
		args := []string{"expo", "run:ios",
			"--port", fmt.Sprintf("%d", e.port),
		}
		if device != "" {
			args = append(args, "--device", device)
			log.Printf("[dev:expo] Target: %s", device)
		}

		logW := &devLogWriter{prefix: "[dev:expo:build]"}
		cmd := exec.CommandContext(ctx, "npx", args...)
		cmd.Dir = opts.WorkDir
		cmd.Stdout = logW
		cmd.Stderr = logW
		buildEnv := append(os.Environ(), fmt.Sprintf("RCT_METRO_PORT=%d", e.port))
		// Set proxy URL so the built dev client connects through relay
		if agentURL, ok := opts.Env["YAVER_AGENT_URL"]; ok && agentURL != "" {
			buildEnv = append(buildEnv, "EXPO_PACKAGER_PROXY_URL="+agentURL+"/dev")
			log.Printf("[dev:expo] Build with proxy URL: %s/dev", agentURL)
		}
		cmd.Env = buildEnv

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("expo run:ios failed to start: %w", err)
		}

		e.mu.Lock()
		e.cmd = cmd
		e.startedAt = time.Now()
		e.running = true
		e.mu.Unlock()

		os.MkdirAll(yaverBuildsDir(), 0755)
		os.WriteFile(buildMarker, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)

		log.Printf("[dev:expo] Build started (PID %d)", cmd.Process.Pid)
		return nil
	}

	// No native project (ios/android dirs) → use web mode for WebView preview
	log.Printf("[dev:expo] No native project found — starting in web mode")
	e.devMode = "web"
	args := []string{"expo", "start",
		"--web",
		"--port", fmt.Sprintf("%d", e.port),
		"--host", "0.0.0.0",
	}
	readyURL := fmt.Sprintf("http://127.0.0.1:%d", e.port)
	return e.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

// detectIOSDevice finds a connected iOS device (USB or wireless).
func detectIOSDevice(ctx context.Context) string {
	// Use xcrun to list devices
	out, err := exec.CommandContext(ctx, "xcrun", "xctrace", "list", "devices").Output()
	if err != nil {
		return ""
	}
	// Parse output: lines like "Kıvanç's iPhone (18.3.1) (00008110-001A515426FB801E)"
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Skip simulators and headers
		if strings.Contains(line, "Simulator") || !strings.Contains(line, "(") {
			continue
		}
		// Extract UDID from parentheses at end
		if idx := strings.LastIndex(line, "("); idx > 0 {
			udid := strings.TrimSuffix(line[idx+1:], ")")
			// UDIDs are hex strings, 24+ chars
			if len(udid) > 20 && !strings.Contains(udid, " ") && !strings.Contains(udid, ".") {
				return udid
			}
		}
	}
	return ""
}

// yaverBuildsDir returns the directory for build markers.
func yaverBuildsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "builds")
}

func (e *ExpoDevServer) BundleURL(platform string) string {
	// In dev-client mode (super-host), return the platform-specific Metro bundle path
	// so the Yaver app can load it natively via the secondary RCTBridge
	if e.devMode == "dev-client" && (platform == "ios" || platform == "android") {
		return fmt.Sprintf("/dev/index.bundle?platform=%s&dev=true&minify=false", platform)
	}
	return "/dev/"
}

func (e *ExpoDevServer) SupportsHotReload() bool { return true }

func (e *ExpoDevServer) Status() DevServerStatus {
	s := e.baseDevServer.Status()
	s.DevMode = e.devMode
	s.BundleURL = "/dev/"
	// Only include deep link for native dev-client mode
	if e.devMode == "dev-client" {
		s.DeepLink = fmt.Sprintf("exp://%s:%d", getLocalIP(), e.port)
	}
	return s
}

func (e *ExpoDevServer) Reload() error {
	// Metro auto-reloads on file change; this is a manual force
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/reload", e.port))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ─── React Native (bare) Dev Server ────────────────────────────────────

// ReactNativeDevServer handles bare React Native projects (without Expo).
// Uses `npx react-native start` with Metro bundler, serving the web bundle.
// For RN projects with react-native-web, this enables hot reload via WebView.
type ReactNativeDevServer struct {
	baseDevServer
}

func (rn *ReactNativeDevServer) Name() string { return "react-native" }

func (rn *ReactNativeDevServer) Detect(workDir string) bool {
	pkg := filepath.Join(workDir, "package.json")
	data, err := os.ReadFile(pkg)
	if err != nil {
		return false
	}
	content := string(data)
	// Has react-native but NOT expo (Expo is handled by ExpoDevServer)
	return strings.Contains(content, `"react-native"`) && !strings.Contains(content, `"expo"`)
}

func (rn *ReactNativeDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	rn.name = "react-native"
	rn.port = opts.Port
	if rn.port == 0 {
		rn.port = 8081
	}

	// Install deps if needed
	if _, err := os.Stat(filepath.Join(opts.WorkDir, "node_modules")); os.IsNotExist(err) {
		log.Printf("[dev] Installing dependencies in %s...", opts.WorkDir)
		install := exec.CommandContext(ctx, "npm", "install", "--legacy-peer-deps")
		install.Dir = opts.WorkDir
		install.Stdout = os.Stdout
		install.Stderr = os.Stderr
		if err := install.Run(); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
	}

	// Try npx expo start --web first (works if expo CLI is available, even for bare RN)
	// Fall back to npx react-native start if expo isn't available
	args := []string{"expo", "start",
		"--web",
		"--port", fmt.Sprintf("%d", rn.port),
		"--host", "0.0.0.0",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d", rn.port)
	err := rn.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
	if err != nil {
		// Fallback: use Metro bundler directly
		log.Printf("[dev] Expo CLI not available, falling back to Metro bundler")
		args = []string{"react-native", "start",
			"--port", fmt.Sprintf("%d", rn.port),
			"--host", "0.0.0.0",
		}
		return rn.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
	}
	return nil
}

func (rn *ReactNativeDevServer) BundleURL(platform string) string {
	return "/dev/"
}

func (rn *ReactNativeDevServer) SupportsHotReload() bool { return true }

func (rn *ReactNativeDevServer) Reload() error {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/reload", rn.port))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ─── Flutter Dev Server ────────────────────────────────────────────────

type FlutterDevServer struct {
	baseDevServer
	stdinPipe *stdinWriter
}

// stdinWriter wraps an io.WriteCloser for sending commands to the Flutter process.
type stdinWriter struct {
	w interface{ Write([]byte) (int, error) }
}

func (f *FlutterDevServer) Detect(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, "pubspec.yaml"))
	return err == nil
}

func (f *FlutterDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	f.name = "flutter"
	f.port = opts.Port
	if f.port == 0 {
		f.port = 9100
	}

	// Find a real mobile device (iOS/Android) for native hot reload.
	// Flutter is a mobile framework — run natively, not as web.
	deviceID := opts.Platform
	if deviceID == "" || deviceID == "web" || deviceID == "chrome" || deviceID == "web-server" {
		detected := detectFlutterMobileDevice(ctx)
		if detected != "" {
			deviceID = detected
		} else {
			// No mobile device found — fall back to web-server
			log.Printf("[dev:flutter] No mobile device found, falling back to web-server")
			deviceID = "web-server"
		}
	}

	args := []string{"run", "-d", deviceID}

	// Web-server needs port config; native devices don't
	if deviceID == "web-server" || deviceID == "chrome" {
		args = append(args, "--web-port", fmt.Sprintf("%d", f.port), "--web-hostname", "0.0.0.0")
	}

	log.Printf("[dev:flutter] Starting on device: %s", deviceID)

	if deviceID == "web-server" || deviceID == "chrome" {
		// Web mode — wait for HTTP readiness
		readyURL := fmt.Sprintf("http://127.0.0.1:%d/", f.port)
		return f.startProcessWithStdin(ctx, "flutter", args, opts.WorkDir, nil, readyURL)
	}

	// Native mode — no HTTP readiness check, just wait for "is available" in output
	return f.startNativeProcess(ctx, "flutter", args, opts.WorkDir)
}

// detectFlutterMobileDevice runs `flutter devices --machine` and returns the first iOS/Android device ID.
func detectFlutterMobileDevice(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "flutter", "devices", "--machine").Output()
	if err != nil {
		return ""
	}

	var devices []struct {
		Name           string `json:"name"`
		ID             string `json:"id"`
		TargetPlatform string `json:"targetPlatform"`
	}
	if err := json.Unmarshal(out, &devices); err != nil {
		return ""
	}

	// Prefer iOS, then Android — skip desktop/web
	for _, d := range devices {
		if d.TargetPlatform == "ios" || strings.HasPrefix(d.TargetPlatform, "android") {
			log.Printf("[dev:flutter] Found mobile device: %s (%s) [%s]", d.Name, d.ID, d.TargetPlatform)
			return d.ID
		}
	}
	return ""
}

// startNativeProcess starts a native Flutter process (no HTTP readiness — watches stdout for "ready" signals).
func (f *FlutterDevServer) startNativeProcess(ctx context.Context, name string, args []string, workDir string) error {
	f.mu.Lock()
	f.workDir = workDir
	f.mu.Unlock()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ())

	// Create stdin pipe for hot reload ("r") and hot restart ("R")
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	f.stdinPipe = &stdinWriter{w: pipe}

	// Capture stdout to detect when app is ready + log output
	logWriter := &devLogWriter{prefix: "[dev:flutter]"}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	f.mu.Lock()
	f.cmd = cmd
	f.startedAt = time.Now()
	// Mark as running immediately for native — the app will build and deploy
	f.running = true
	f.mu.Unlock()

	log.Printf("[dev:flutter] Native process started (PID %d) — building and deploying to device...", cmd.Process.Pid)
	return nil
}

// startProcessWithStdin is like startProcess but saves the stdin pipe for hot reload.
func (f *FlutterDevServer) startProcessWithStdin(ctx context.Context, name string, args []string, workDir string, env []string, readyURL string) error {
	f.mu.Lock()
	f.workDir = workDir
	f.mu.Unlock()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)

	logWriter := &devLogWriter{prefix: fmt.Sprintf("[dev:%s]", f.name)}
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter

	// Create stdin pipe and save it for Reload()
	pipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	f.stdinPipe = &stdinWriter{w: pipe}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}

	f.mu.Lock()
	f.cmd = cmd
	f.startedAt = time.Now()
	f.mu.Unlock()

	// Wait for dev server to become ready
	deadline := time.After(180 * time.Second) // Flutter web first build can take 3+ min
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("%s did not become ready within 180s", name)
		case <-ticker.C:
			resp, err := http.Get(readyURL)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					f.mu.Lock()
					f.running = true
					f.mu.Unlock()
					return nil
				}
			}
		}
	}
}

func (f *FlutterDevServer) BundleURL(platform string) string {
	return "/dev/"
}

func (f *FlutterDevServer) SupportsHotReload() bool { return true }

func (f *FlutterDevServer) Reload() error {
	// Flutter hot reload via stdin "r"
	if f.stdinPipe != nil && f.stdinPipe.w != nil {
		_, err := f.stdinPipe.w.Write([]byte("r\n"))
		return err
	}
	return fmt.Errorf("flutter process stdin not available")
}

// ─── Vite Dev Server ───────────────────────────────────────────────────

type ViteDevServer struct {
	baseDevServer
}

func (v *ViteDevServer) Detect(workDir string) bool {
	for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			return true
		}
	}
	return false
}

func (v *ViteDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	v.name = "vite"
	v.port = opts.Port
	if v.port == 0 {
		v.port = 5173
	}

	args := []string{"vite",
		"--port", fmt.Sprintf("%d", v.port),
		"--host", "0.0.0.0",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/", v.port)
	return v.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

func (v *ViteDevServer) BundleURL(platform string) string { return "/dev/" }
func (v *ViteDevServer) SupportsHotReload() bool           { return true }
func (v *ViteDevServer) Reload() error                     { return nil } // Vite auto-reloads

// ─── Next.js Dev Server ────────────────────────────────────────────────

type NextDevServer struct {
	baseDevServer
}

func (n *NextDevServer) Detect(workDir string) bool {
	for _, name := range []string{"next.config.ts", "next.config.js", "next.config.mjs"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); err == nil {
			return true
		}
	}
	return false
}

func (n *NextDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	n.name = "nextjs"
	n.port = opts.Port
	if n.port == 0 {
		n.port = 3000
	}

	args := []string{"next", "dev",
		"--port", fmt.Sprintf("%d", n.port),
		"--hostname", "0.0.0.0",
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/", n.port)
	return n.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

func (n *NextDevServer) BundleURL(platform string) string { return "/dev/" }
func (n *NextDevServer) SupportsHotReload() bool           { return true }
func (n *NextDevServer) Reload() error                     { return nil } // Next.js auto-reloads
