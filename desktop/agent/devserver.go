package main

import (
	"bytes"
	"context"
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
			return fmt.Errorf("unknown framework: %s", framework)
		}
	} else {
		ds = detectDevServer(workDir)
		if ds == nil {
			return fmt.Errorf("could not detect framework in %s", workDir)
		}
	}

	log.Printf("[dev] Starting %s dev server in %s", ds.Name(), workDir)

	ctx, cancel := context.WithCancel(context.Background())
	opts := DevServerOpts{
		WorkDir:  workDir,
		Port:     port,
		Platform: platform,
	}

	if err := ds.Start(ctx, opts); err != nil {
		cancel()
		return fmt.Errorf("start %s: %w", ds.Name(), err)
	}

	// Create reverse proxy to the dev server
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", ds.Port()))
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Don't log proxy errors for every request
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "dev server unavailable", http.StatusBadGateway)
	}

	m.active = &devServerSession{
		server: ds,
		proxy:  proxy,
		ctx:    ctx,
		cancel: cancel,
	}

	log.Printf("[dev] %s ready on port %d", ds.Name(), ds.Port())

	// Emit ready event
	m.emit(DevServerEvent{
		Type:      "ready",
		Framework: ds.Name(),
		BundleURL: ds.BundleURL(platform),
		Message:   fmt.Sprintf("%s dev server ready", ds.Name()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

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

	// Start Expo web — serves the app as a web page for the WebView
	// --web: enables web platform (webpack/metro web)
	// --host 0.0.0.0: bind to all interfaces (needed for proxy + LAN access)
	// No --no-dev: keep dev mode for hot reload
	args := []string{"expo", "start",
		"--web",
		"--port", fmt.Sprintf("%d", e.port),
		"--host", "0.0.0.0",
	}

	// Expo web serves on the same port as Metro
	readyURL := fmt.Sprintf("http://127.0.0.1:%d", e.port)
	return e.startProcess(ctx, "npx", args, opts.WorkDir, nil, readyURL)
}

func (e *ExpoDevServer) BundleURL(platform string) string {
	// Web version served at root — this is what loads in the WebView
	return "/dev/"
}

func (e *ExpoDevServer) SupportsHotReload() bool { return true }

func (e *ExpoDevServer) Reload() error {
	// Metro auto-reloads on file change; this is a manual force
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/reload", e.port))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ─── Flutter Dev Server ────────────────────────────────────────────────

type FlutterDevServer struct {
	baseDevServer
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

	platform := opts.Platform
	if platform == "" {
		platform = "web"
	}

	args := []string{"run", "-d", platform,
		"--web-port", fmt.Sprintf("%d", f.port),
	}

	readyURL := fmt.Sprintf("http://127.0.0.1:%d/", f.port)
	return f.startProcess(ctx, "flutter", args, opts.WorkDir, nil, readyURL)
}

func (f *FlutterDevServer) BundleURL(platform string) string {
	return "/dev/"
}

func (f *FlutterDevServer) SupportsHotReload() bool { return true }

func (f *FlutterDevServer) Reload() error {
	// Flutter hot reload via stdin "r" - needs the process stdin
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cmd != nil && f.cmd.Process != nil {
		// Send "r" to stdin for hot reload
		if stdin, ok := f.cmd.Stdin.(*os.File); ok {
			stdin.WriteString("r\n")
		}
	}
	return nil
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
