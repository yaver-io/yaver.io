package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProxyRoute defines a single reverse-proxy mapping from a local domain to a backend target.
type ProxyRoute struct {
	Domain     string `json:"domain"`
	Target     string `json:"target"`      // e.g. "localhost:3000"
	TLS        bool   `json:"tls"`         // whether to terminate TLS with mkcert certs
	PathPrefix string `json:"path_prefix"` // optional path prefix to strip/match (e.g. "/api")
}

// ProxyConfig is the persisted on-disk configuration for the proxy manager.
type ProxyConfig struct {
	Routes []ProxyRoute `json:"routes"`
}

// ProxyRouteStatus combines a route with its observed liveness.
type ProxyRouteStatus struct {
	ProxyRoute
	Reachable bool `json:"reachable"` // whether the target is currently accepting connections
}

// ProxyStatus is a point-in-time snapshot of the proxy manager state.
type ProxyStatus struct {
	Running    bool               `json:"running"`
	CaddyPID   int                `json:"caddy_pid,omitempty"`
	Routes     []ProxyRouteStatus `json:"routes"`
	CaddyFile  string             `json:"caddyfile"`
}

// ProxyManager manages a Caddy-backed reverse proxy for local development.
// All exported methods are safe for concurrent use.
type ProxyManager struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	configPath string // path to proxy.json
	caddyFile  string // path to generated Caddyfile
	pidFile    string // path to caddy PID file
}

// proxyConfigDir returns ~/.yaver (same convention as the rest of the agent).
func proxyConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	dir := filepath.Join(home, ".yaver")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create config dir %s: %w", dir, err)
	}
	return dir, nil
}

// NewProxyManager creates a ProxyManager whose config lives at ~/.yaver/proxy.json.
func NewProxyManager() *ProxyManager {
	dir, err := proxyConfigDir()
	if err != nil {
		// Degrade gracefully — methods will surface the error when called.
		dir = filepath.Join(os.TempDir(), ".yaver")
	}
	return &ProxyManager{
		configPath: filepath.Join(dir, "proxy.json"),
		caddyFile:  filepath.Join(dir, "Caddyfile"),
		pidFile:    filepath.Join(dir, "caddy.pid"),
	}
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// loadConfig reads the persisted proxy config from disk.
// If the file does not exist an empty config is returned (not an error).
func (m *ProxyManager) loadConfig() (*ProxyConfig, error) {
	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return &ProxyConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read proxy config: %w", err)
	}
	var cfg ProxyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse proxy config: %w", err)
	}
	return &cfg, nil
}

// saveConfig persists the proxy config to disk atomically (write to tmp, rename).
func (m *ProxyManager) saveConfig(cfg *ProxyConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal proxy config: %w", err)
	}
	tmp := m.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write proxy config: %w", err)
	}
	if err := os.Rename(tmp, m.configPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("save proxy config: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Caddyfile generation
// ---------------------------------------------------------------------------

// tlsProxyDir returns ~/.yaver/tls — where mkcert certs are expected.
func (m *ProxyManager) tlsProxyDir() string {
	dir, _ := proxyConfigDir()
	return filepath.Join(dir, "tls")
}

// generateCaddyfile produces a Caddyfile string from cfg.
// For TLS routes the per-domain cert/key pair is looked up under ~/.yaver/tls/{domain}/.
// If no domain-specific cert is found, the shared server.pem / server-key.pem is used.
func (m *ProxyManager) generateCaddyfile(cfg *ProxyConfig) string {
	tlsDir := m.tlsProxyDir()
	var buf bytes.Buffer

	// Global options block — disable the Caddy admin API on a random port to
	// avoid conflicts when multiple processes run on the same machine.
	buf.WriteString("{\n")
	buf.WriteString("\tadmin off\n")
	buf.WriteString("}\n\n")

	for _, r := range cfg.Routes {
		certPath, keyPath := m.resolveCerts(tlsDir, r.Domain)
		upstreamTarget := r.Target
		if r.PathPrefix != "" {
			// Ensure the target path prefix is handled correctly.
			upstreamTarget = r.Target
		}

		if r.TLS {
			// HTTP → HTTPS redirect block
			fmt.Fprintf(&buf, "http://%s {\n", r.Domain)
			fmt.Fprintf(&buf, "\tredir https://%s{uri} permanent\n", r.Domain)
			buf.WriteString("}\n\n")

			// HTTPS block
			fmt.Fprintf(&buf, "%s {\n", r.Domain)
			fmt.Fprintf(&buf, "\ttls %s %s\n", certPath, keyPath)
			if r.PathPrefix != "" {
				fmt.Fprintf(&buf, "\thandle_path %s* {\n", r.PathPrefix)
				fmt.Fprintf(&buf, "\t\treverse_proxy %s\n", upstreamTarget)
				buf.WriteString("\t}\n")
			} else {
				fmt.Fprintf(&buf, "\treverse_proxy %s\n", upstreamTarget)
			}
			buf.WriteString("}\n\n")
		} else {
			// Plain HTTP block
			fmt.Fprintf(&buf, "http://%s {\n", r.Domain)
			if r.PathPrefix != "" {
				fmt.Fprintf(&buf, "\thandle_path %s* {\n", r.PathPrefix)
				fmt.Fprintf(&buf, "\t\treverse_proxy %s\n", upstreamTarget)
				buf.WriteString("\t}\n")
			} else {
				fmt.Fprintf(&buf, "\treverse_proxy %s\n", upstreamTarget)
			}
			buf.WriteString("}\n\n")
		}
	}

	return buf.String()
}

// resolveCerts returns the cert/key paths for a given domain.
// Lookup order:
//  1. ~/.yaver/tls/{domain}/cert.pem  +  key.pem
//  2. ~/.yaver/tls/cert.pem           +  key.pem   (shared / mkcert default)
//  3. ~/.yaver/tls/server.pem         +  server-key.pem (self-signed generated by agent)
func (m *ProxyManager) resolveCerts(tlsDir, domain string) (cert, key string) {
	domainCert := filepath.Join(tlsDir, domain, "cert.pem")
	domainKey := filepath.Join(tlsDir, domain, "key.pem")
	if proxyFileExists(domainCert) && proxyFileExists(domainKey) {
		return domainCert, domainKey
	}

	sharedCert := filepath.Join(tlsDir, "cert.pem")
	sharedKey := filepath.Join(tlsDir, "key.pem")
	if proxyFileExists(sharedCert) && proxyFileExists(sharedKey) {
		return sharedCert, sharedKey
	}

	// Fall back to the self-signed cert generated by EnsureTLSCert.
	return filepath.Join(tlsDir, "server.pem"), filepath.Join(tlsDir, "server-key.pem")
}

// writeCaddyfile atomically writes the Caddyfile for cfg.
func (m *ProxyManager) writeCaddyfile(cfg *ProxyConfig) error {
	content := m.generateCaddyfile(cfg)
	tmp := m.caddyFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write Caddyfile: %w", err)
	}
	if err := os.Rename(tmp, m.caddyFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install Caddyfile: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Caddy binary helpers
// ---------------------------------------------------------------------------

// findCaddy locates the caddy binary, attempting auto-install on macOS if missing.
// Returns the resolved path or an error with installation instructions.
func (m *ProxyManager) findCaddy() (string, error) {
	if path, err := exec.LookPath("caddy"); err == nil {
		return path, nil
	}

	// On macOS try Homebrew install automatically.
	if runtime.GOOS == "darwin" {
		if brewPath, err := exec.LookPath("brew"); err == nil {
			fmt.Println("caddy not found — running: brew install caddy")
			install := exec.Command(brewPath, "install", "caddy")
			install.Stdout = os.Stdout
			install.Stderr = os.Stderr
			if err := install.Run(); err != nil {
				return "", fmt.Errorf("brew install caddy failed: %w\nInstall manually: https://caddyserver.com/docs/install", err)
			}
			if path, err := exec.LookPath("caddy"); err == nil {
				return path, nil
			}
		}
	}

	var hint string
	switch runtime.GOOS {
	case "darwin":
		hint = "brew install caddy"
	case "linux":
		hint = "sudo apt install caddy  OR  https://caddyserver.com/docs/install"
	case "windows":
		hint = "scoop install caddy  OR  https://caddyserver.com/docs/install"
	default:
		hint = "https://caddyserver.com/docs/install"
	}
	return "", fmt.Errorf("caddy binary not found — install it with:\n  %s", hint)
}

// savedPID returns the PID stored in the PID file, or 0 if absent/invalid.
func (m *ProxyManager) savedPID() int {
	data, err := os.ReadFile(m.pidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// writePID persists the given PID to the PID file.
func (m *ProxyManager) writePID(pid int) error {
	return os.WriteFile(m.pidFile, []byte(strconv.Itoa(pid)), 0o600)
}

// isRunning reports whether a caddy process owned by this manager is alive.
// It prefers the in-memory cmd, falling back to the PID file.
func (m *ProxyManager) isRunning() (bool, int) {
	if m.cmd != nil && m.cmd.Process != nil {
		pid := m.cmd.Process.Pid
		if proxyProcessAlive(pid) {
			return true, pid
		}
	}
	if pid := m.savedPID(); pid > 0 && proxyProcessAlive(pid) {
		return true, pid
	}
	return false, 0
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Start generates the Caddyfile from the persisted config and launches caddy.
// If caddy is already running it is a no-op (returns current status).
func (m *ProxyManager) Start() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if running, pid := m.isRunning(); running {
		return fmt.Sprintf("caddy is already running (pid %d)", pid), nil
	}

	caddy, err := m.findCaddy()
	if err != nil {
		return "", err
	}

	cfg, err := m.loadConfig()
	if err != nil {
		return "", err
	}

	if err := m.writeCaddyfile(cfg); err != nil {
		return "", err
	}

	cmd := exec.Command(caddy, "run", "--config", m.caddyFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start caddy: %w", err)
	}

	m.cmd = cmd

	if err := m.writePID(cmd.Process.Pid); err != nil {
		// Non-fatal — we still have the in-memory reference.
		fmt.Fprintf(os.Stderr, "warning: could not write PID file: %v\n", err)
	}

	// Reap the process in the background so we don't leave zombies.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		m.mu.Unlock()
		_ = os.Remove(m.pidFile)
	}()

	return fmt.Sprintf("caddy started (pid %d), Caddyfile: %s", cmd.Process.Pid, m.caddyFile), nil
}

// Stop gracefully shuts down the caddy process.
func (m *ProxyManager) Stop() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	running, pid := m.isRunning()
	if !running {
		return "caddy is not running", nil
	}

	// Prefer the in-memory handle for clean shutdown.
	if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Signal(os.Interrupt); err != nil {
			_ = m.cmd.Process.Kill()
		}
		_ = m.cmd.Wait()
		m.cmd = nil
	} else if pid > 0 {
		proc, err := os.FindProcess(pid)
		if err == nil {
			if err := proc.Signal(os.Interrupt); err != nil {
				_ = proc.Kill()
			}
		}
	}

	_ = os.Remove(m.pidFile)
	return fmt.Sprintf("caddy stopped (was pid %d)", pid), nil
}

// Add appends a new proxy route and reloads caddy if it is running.
// domain: the local hostname, e.g. "myapp.local"
// target: the backend, e.g. "localhost:3000"
// tls:    whether to enable TLS termination using the mkcert/self-signed cert
func (m *ProxyManager) Add(domain, target string, tls bool) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("domain must not be empty")
	}
	if target == "" {
		return "", fmt.Errorf("target must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadConfig()
	if err != nil {
		return "", err
	}

	// Idempotent: update existing route if domain already present.
	updated := false
	for i, r := range cfg.Routes {
		if r.Domain == domain {
			cfg.Routes[i].Target = target
			cfg.Routes[i].TLS = tls
			updated = true
			break
		}
	}
	if !updated {
		cfg.Routes = append(cfg.Routes, ProxyRoute{
			Domain: domain,
			Target: target,
			TLS:    tls,
		})
	}

	if err := m.saveConfig(cfg); err != nil {
		return "", err
	}

	action := "added"
	if updated {
		action = "updated"
	}

	if running, _ := m.isRunning(); running {
		if reloadErr := m.reload(cfg); reloadErr != nil {
			return fmt.Sprintf("route %s → %s %s (caddy reload failed: %v)", domain, target, action, reloadErr), reloadErr
		}
		return fmt.Sprintf("route %s → %s %s and caddy reloaded", domain, target, action), nil
	}

	return fmt.Sprintf("route %s → %s %s (caddy not running — start with 'yaver proxy start')", domain, target, action), nil
}

// Remove deletes the route for domain and reloads caddy if running.
func (m *ProxyManager) Remove(domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("domain must not be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadConfig()
	if err != nil {
		return "", err
	}

	original := len(cfg.Routes)
	filtered := cfg.Routes[:0]
	for _, r := range cfg.Routes {
		if r.Domain != domain {
			filtered = append(filtered, r)
		}
	}

	if len(filtered) == original {
		return "", fmt.Errorf("route for domain %q not found", domain)
	}
	cfg.Routes = filtered

	if err := m.saveConfig(cfg); err != nil {
		return "", err
	}

	if running, _ := m.isRunning(); running {
		if reloadErr := m.reload(cfg); reloadErr != nil {
			return fmt.Sprintf("route %s removed (caddy reload failed: %v)", domain, reloadErr), reloadErr
		}
		return fmt.Sprintf("route %s removed and caddy reloaded", domain), nil
	}

	return fmt.Sprintf("route %s removed", domain), nil
}

// List returns all configured proxy routes.
func (m *ProxyManager) List() ([]ProxyRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]ProxyRoute, len(cfg.Routes))
	copy(out, cfg.Routes)
	return out, nil
}

// Status returns a snapshot of whether caddy is running and the liveness of
// each configured target.
func (m *ProxyManager) Status() (*ProxyStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	running, pid := m.isRunning()

	cfg, err := m.loadConfig()
	if err != nil {
		return nil, err
	}

	statuses := make([]ProxyRouteStatus, 0, len(cfg.Routes))
	for _, r := range cfg.Routes {
		statuses = append(statuses, ProxyRouteStatus{
			ProxyRoute: r,
			Reachable:  tcpReachable(r.Target, 500*time.Millisecond),
		})
	}

	return &ProxyStatus{
		Running:   running,
		CaddyPID:  pid,
		Routes:    statuses,
		CaddyFile: m.caddyFile,
	}, nil
}

// ---------------------------------------------------------------------------
// Auto-detection
// ---------------------------------------------------------------------------

// wellKnownPorts are the ports scanned by AutoDetect for running services.
var wellKnownPorts = []struct {
	port    int
	service string
}{
	{3000, "react/next"},
	{3001, "react/next (alt)"},
	{4000, "phoenix/rails"},
	{4200, "angular"},
	{5173, "vite"},
	{5174, "vite (alt)"},
	{8000, "django/python"},
	{8025, "mailpit"},
	{8080, "generic"},
	{8081, "generic (alt)"},
	{9000, "php-fpm/sonarqube"},
	{9090, "prometheus"},
}

// AutoDetect scans for services running on well-known ports and via
// `docker ps`, then returns a list of suggested ProxyRoute values.
// workDir is used only for context (e.g. deriving a suggested domain name
// from the project directory name) and may be empty.
func (m *ProxyManager) AutoDetect(workDir string) ([]ProxyRoute, error) {
	projectName := "app"
	if workDir != "" {
		projectName = sanitizeDomain(filepath.Base(workDir))
	}

	var routes []ProxyRoute
	seen := make(map[int]bool)

	// 1. Scan well-known ports.
	for _, wp := range wellKnownPorts {
		if tcpReachable(fmt.Sprintf("localhost:%d", wp.port), 200*time.Millisecond) {
			seen[wp.port] = true
			domain := fmt.Sprintf("%s-%d.local", projectName, wp.port)
			// Use a cleaner domain for the most common port.
			if wp.port == 3000 {
				domain = fmt.Sprintf("%s.local", projectName)
			} else if wp.port == 5173 {
				domain = fmt.Sprintf("%s-vite.local", projectName)
			} else if wp.port == 8025 {
				domain = "mailpit.local"
			}
			routes = append(routes, ProxyRoute{
				Domain: domain,
				Target: fmt.Sprintf("localhost:%d", wp.port),
				TLS:    false,
			})
		}
	}

	// 2. Check Docker containers for mapped ports.
	dockerRoutes, err := m.detectDockerServices(projectName, seen)
	if err == nil {
		routes = append(routes, dockerRoutes...)
	}

	return routes, nil
}

// detectDockerServices runs `docker ps` and parses port mappings for known
// services (mailpit, postgres, redis, etc.).
func (m *ProxyManager) detectDockerServices(projectName string, alreadySeen map[int]bool) ([]ProxyRoute, error) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return nil, nil // Docker not installed — silently skip.
	}

	out, err := exec.Command(dockerPath, "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Ports}}").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	var routes []ProxyRoute
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		name, image, ports := parts[0], parts[1], parts[2]
		mappedPort := parseFirstDockerPort(ports)
		if mappedPort == 0 || alreadySeen[mappedPort] {
			continue
		}
		alreadySeen[mappedPort] = true

		domain := containerDomain(name, image, mappedPort, projectName)
		routes = append(routes, ProxyRoute{
			Domain: domain,
			Target: fmt.Sprintf("localhost:%d", mappedPort),
			TLS:    false,
		})
	}
	return routes, scanner.Err()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// reload regenerates the Caddyfile and signals caddy to reload its config.
// Caller must hold m.mu.
func (m *ProxyManager) reload(cfg *ProxyConfig) error {
	if err := m.writeCaddyfile(cfg); err != nil {
		return err
	}

	caddy, err := m.findCaddy()
	if err != nil {
		return err
	}

	out, err := exec.Command(caddy, "reload", "--config", m.caddyFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy reload: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tcpReachable attempts a TCP dial to addr within timeout.
func tcpReachable(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// proxyProcessAlive returns true if a process with the given PID is currently running.
// It uses a portable approach: on Linux it checks /proc/<pid>; on other platforms
// it tries os.FindProcess and falls back to probing the Caddy admin endpoint.
func proxyProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Linux: /proc/<pid> exists iff the process is alive.
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
			return true
		}
		return false
	}

	// macOS / other Unix: os.FindProcess always succeeds; use kill(pid, 0)
	// by running a short shell command — avoids importing syscall directly.
	if runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd" {
		err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run()
		return err == nil
	}

	// Windows and others: fall back to probing the Caddy admin API.
	return proxyCaddyAdminAlive()
}

// proxyCaddyAdminAlive probes the Caddy admin API (disabled by default in our
// Caddyfile, so this is only a best-effort fallback on Windows).
func proxyCaddyAdminAlive() bool {
	client := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get("http://localhost:2019/config/")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// proxyFileExists returns true if the path exists and is a regular file.
func proxyFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// sanitizeDomain converts an arbitrary string into a valid DNS label.
// Non-alphanumeric characters are replaced with hyphens; leading/trailing
// hyphens are stripped.
func sanitizeDomain(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// parseFirstDockerPort extracts the first host-side port number from a Docker
// port mapping string such as "0.0.0.0:8025->8025/tcp, :::8025->8025/tcp".
func parseFirstDockerPort(ports string) int {
	// Format: "0.0.0.0:HOST_PORT->CONTAINER_PORT/proto"
	for _, segment := range strings.Split(ports, ",") {
		segment = strings.TrimSpace(segment)
		if idx := strings.Index(segment, "->"); idx != -1 {
			hostPart := segment[:idx]
			if colon := strings.LastIndex(hostPart, ":"); colon != -1 {
				if p, err := strconv.Atoi(hostPart[colon+1:]); err == nil && p > 0 {
					return p
				}
			}
		}
	}
	return 0
}

// containerDomain derives a human-friendly .local domain for a Docker container.
func containerDomain(containerName, image string, port int, projectName string) string {
	img := strings.ToLower(image)
	// Strip registry prefix and tag.
	if slash := strings.LastIndex(img, "/"); slash != -1 {
		img = img[slash+1:]
	}
	if colon := strings.Index(img, ":"); colon != -1 {
		img = img[:colon]
	}

	switch {
	case strings.Contains(img, "mailpit") || port == 8025:
		return "mailpit.local"
	case strings.Contains(img, "postgres") || strings.Contains(img, "postgresql"):
		return fmt.Sprintf("%s-postgres.local", projectName)
	case strings.Contains(img, "redis"):
		return fmt.Sprintf("%s-redis.local", projectName)
	case strings.Contains(img, "mysql") || strings.Contains(img, "mariadb"):
		return fmt.Sprintf("%s-mysql.local", projectName)
	case strings.Contains(img, "mongo"):
		return fmt.Sprintf("%s-mongo.local", projectName)
	default:
		name := sanitizeDomain(containerName)
		if name == "" {
			name = sanitizeDomain(img)
		}
		if name == "" {
			name = fmt.Sprintf("service-%d", port)
		}
		return fmt.Sprintf("%s.local", name)
	}
}
