package main

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Tunnel represents an active localhost tunnel.
type Tunnel struct {
	Port      int       `json:"port"`
	PublicURL string    `json:"publicUrl"`
	Backend   string    `json:"backend"` // "cloudflared", "bore", "ssh"
	CreatedAt time.Time `json:"createdAt"`
	pid       int
	cmd       *exec.Cmd
}

// TunnelRequest represents a single proxied HTTP request captured from access logs.
type TunnelRequest struct {
	Timestamp  string `json:"timestamp"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	StatusCode int    `json:"statusCode"`
	Duration   string `json:"duration"`
}

// tunnelEntry bundles a Tunnel with its captured output.
type tunnelEntry struct {
	tunnel *Tunnel
	logs   *ringBuffer
}

// ExposeManager manages localhost tunnels for public access.
type ExposeManager struct {
	mu      sync.Mutex
	tunnels map[int]*tunnelEntry
}

// NewExposeManager creates a new ExposeManager.
func NewExposeManager() *ExposeManager {
	return &ExposeManager{
		tunnels: make(map[int]*tunnelEntry),
	}
}

// Start creates a public tunnel to localhost:{port}.
// It tries cloudflared first, then bore, then SSH reverse tunnel as a last resort.
func (m *ExposeManager) Start(port int, subdomain string) (*Tunnel, error) {
	m.mu.Lock()
	if entry, exists := m.tunnels[port]; exists {
		t := *entry.tunnel
		m.mu.Unlock()
		return &t, nil
	}
	m.mu.Unlock()

	// Try cloudflared quick tunnel first.
	if path, err := exec.LookPath("cloudflared"); err == nil {
		t, err := m.startCloudflared(path, port)
		if err == nil {
			return t, nil
		}
	}

	// Fall back to bore.
	if path, err := exec.LookPath("bore"); err == nil {
		t, err := m.startBore(path, port)
		if err == nil {
			return t, nil
		}
	}

	// Last resort: SSH reverse tunnel via a well-known free SSH tunnel service.
	t, err := m.startSSHTunnel(port, subdomain)
	if err != nil {
		return nil, fmt.Errorf("all tunnel backends failed; install cloudflared or bore for best results: %w", err)
	}
	return t, nil
}

// startCloudflared launches `cloudflared tunnel --url http://localhost:{port}` and waits
// for the assigned *.trycloudflare.com URL to appear in stdout/stderr.
func (m *ExposeManager) startCloudflared(binaryPath string, port int) (*Tunnel, error) {
	cmd := exec.Command(binaryPath, "tunnel", "--url", fmt.Sprintf("http://localhost:%d", port))

	// cloudflared writes the public URL to stderr.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cloudflared start: %w", err)
	}

	logs := newRingBuffer(500)
	urlCh := make(chan string, 1)

	scanLines := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			logs.write(line)
			// cloudflared prints a line like:
			//   | https://something.trycloudflare.com |
			if strings.Contains(line, ".trycloudflare.com") {
				// Extract the URL from the line.
				parts := strings.Fields(line)
				for _, p := range parts {
					if strings.HasPrefix(p, "https://") && strings.Contains(p, ".trycloudflare.com") {
						select {
						case urlCh <- p:
						default:
						}
					}
				}
			}
		}
	}

	go scanLines(stderr)
	go scanLines(stdout)

	// Wait up to 30 seconds for the URL.
	select {
	case publicURL := <-urlCh:
		t := &Tunnel{
			Port:      port,
			PublicURL: publicURL,
			Backend:   "cloudflared",
			CreatedAt: time.Now(),
			pid:       cmd.Process.Pid,
			cmd:       cmd,
		}
		m.mu.Lock()
		m.tunnels[port] = &tunnelEntry{tunnel: t, logs: logs}
		m.mu.Unlock()
		// Reap the process when it exits.
		go func() { _ = cmd.Wait() }()
		return t, nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("cloudflared did not produce a public URL within 30s")
	}
}

// startBore launches `bore local {port} --to bore.pub` and waits for the public URL.
func (m *ExposeManager) startBore(binaryPath string, port int) (*Tunnel, error) {
	cmd := exec.Command(binaryPath, "local", fmt.Sprintf("%d", port), "--to", "bore.pub")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("bore start: %w", err)
	}

	logs := newRingBuffer(500)
	urlCh := make(chan string, 1)

	scanLines := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			logs.write(line)
			// bore prints something like:
			//   listening at bore.pub:NNNNN
			if strings.Contains(line, "bore.pub:") {
				parts := strings.Fields(line)
				for _, p := range parts {
					if strings.Contains(p, "bore.pub:") {
						// Normalise to a tcp:// URL (bore is TCP-level).
						addr := strings.TrimPrefix(p, "bore.pub:")
						_ = addr
						select {
						case urlCh <- "http://" + p:
						default:
						}
					}
				}
			}
		}
	}

	go scanLines(stdout)
	go scanLines(stderr)

	select {
	case publicURL := <-urlCh:
		t := &Tunnel{
			Port:      port,
			PublicURL: publicURL,
			Backend:   "bore",
			CreatedAt: time.Now(),
			pid:       cmd.Process.Pid,
			cmd:       cmd,
		}
		m.mu.Lock()
		m.tunnels[port] = &tunnelEntry{tunnel: t, logs: logs}
		m.mu.Unlock()
		go func() { _ = cmd.Wait() }()
		return t, nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("bore did not produce a public URL within 30s")
	}
}

// startSSHTunnel uses serveo.net (free, no install) via `ssh -R`.
func (m *ExposeManager) startSSHTunnel(port int, subdomain string) (*Tunnel, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("ssh not found in PATH: %w", err)
	}

	// Build remote forwarding spec. If a subdomain is requested use it; otherwise
	// let serveo.net assign one.
	var remoteSpec string
	if subdomain != "" {
		remoteSpec = fmt.Sprintf("%s:80:localhost:%d", subdomain, port)
	} else {
		remoteSpec = fmt.Sprintf("80:localhost:%d", port)
	}

	cmd := exec.Command(sshPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "ServerAliveInterval=30",
		"-R", remoteSpec,
		"serveo.net",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh tunnel start: %w", err)
	}

	logs := newRingBuffer(500)
	urlCh := make(chan string, 1)

	scanLines := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			logs.write(line)
			// serveo prints: Forwarding HTTP traffic from https://XXXX.serveo.net
			if strings.Contains(line, "serveo.net") && strings.Contains(line, "https://") {
				parts := strings.Fields(line)
				for _, p := range parts {
					if strings.HasPrefix(p, "https://") && strings.Contains(p, "serveo.net") {
						select {
						case urlCh <- p:
						default:
						}
					}
				}
			}
		}
	}

	go scanLines(stdout)
	go scanLines(stderr)

	select {
	case publicURL := <-urlCh:
		t := &Tunnel{
			Port:      port,
			PublicURL: publicURL,
			Backend:   "ssh",
			CreatedAt: time.Now(),
			pid:       cmd.Process.Pid,
			cmd:       cmd,
		}
		m.mu.Lock()
		m.tunnels[port] = &tunnelEntry{tunnel: t, logs: logs}
		m.mu.Unlock()
		go func() { _ = cmd.Wait() }()
		return t, nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("SSH tunnel did not produce a public URL within 30s")
	}
}

// Stop terminates the tunnel for the given port.
func (m *ExposeManager) Stop(port int) error {
	m.mu.Lock()
	entry, exists := m.tunnels[port]
	if exists {
		delete(m.tunnels, port)
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active tunnel for port %d", port)
	}
	if entry.tunnel.cmd != nil && entry.tunnel.cmd.Process != nil {
		return entry.tunnel.cmd.Process.Kill()
	}
	return nil
}

// StopAll terminates all active tunnels.
func (m *ExposeManager) StopAll() error {
	m.mu.Lock()
	ports := make([]int, 0, len(m.tunnels))
	for p := range m.tunnels {
		ports = append(ports, p)
	}
	m.mu.Unlock()

	var firstErr error
	for _, p := range ports {
		if err := m.Stop(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// List returns a snapshot of all active tunnels.
func (m *ExposeManager) List() []Tunnel {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Tunnel, 0, len(m.tunnels))
	for _, entry := range m.tunnels {
		t := *entry.tunnel
		// Don't expose internal fields.
		t.cmd = nil
		out = append(out, t)
	}
	return out
}

// Inspect returns recent requests captured from cloudflared's log output for the
// given port. limit specifies the maximum number of entries to return (0 = all).
// Only available when the tunnel backend is "cloudflared".
func (m *ExposeManager) Inspect(port int, limit int) ([]TunnelRequest, error) {
	m.mu.Lock()
	entry, exists := m.tunnels[port]
	m.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("no active tunnel for port %d", port)
	}
	if entry.tunnel.Backend != "cloudflared" {
		return nil, fmt.Errorf("inspect is only available for cloudflared tunnels (backend: %s)", entry.tunnel.Backend)
	}

	lines := entry.logs.read()
	var requests []TunnelRequest

	for _, line := range lines {
		req := parseCloudflaredLogLine(line)
		if req != nil {
			requests = append(requests, *req)
		}
	}

	if limit > 0 && len(requests) > limit {
		requests = requests[len(requests)-limit:]
	}
	return requests, nil
}

// parseCloudflaredLogLine attempts to extract a TunnelRequest from a cloudflared
// log line. cloudflared access log lines look like:
//
//	2024-01-01T00:00:00Z INF Request served method=GET path=/foo status=200 duration=1.23ms
func parseCloudflaredLogLine(line string) *TunnelRequest {
	if !strings.Contains(line, "method=") {
		return nil
	}

	req := &TunnelRequest{}

	// Parse key=value pairs from the log line.
	// Extract timestamp from the start of the line if present.
	parts := strings.Fields(line)
	if len(parts) > 0 {
		// First token is often the timestamp.
		if len(parts[0]) >= 10 && (parts[0][4] == '-' || strings.Contains(parts[0], "T")) {
			req.Timestamp = parts[0]
		}
	}

	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := kv[0], kv[1]
		// Strip surrounding quotes if any.
		val = strings.Trim(val, `"`)
		switch key {
		case "method":
			req.Method = val
		case "path":
			req.Path = val
		case "status":
			_, _ = fmt.Sscanf(val, "%d", &req.StatusCode)
		case "duration":
			req.Duration = val
		}
	}

	if req.Method == "" {
		return nil
	}
	if req.Timestamp == "" {
		req.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	return req
}
