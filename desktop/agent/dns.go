package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	dnsConfigFile  = "dns.json"
	yaverDNSMarker = "# yaver-managed"
	hostsFile      = "/etc/hosts"
)

// DNSEntry represents a single DNS mapping managed by Yaver.
type DNSEntry struct {
	Hostname  string    `json:"hostname"`
	IP        string    `json:"ip"`
	AddedAt   time.Time `json:"added_at"`
	ManagedBy string    `json:"managed_by"`
}

// DNSConfig is the persisted state of all yaver-managed DNS entries.
type DNSConfig struct {
	Entries []DNSEntry `json:"entries"`
}

// DNSManager manages /etc/hosts entries and tracks them in ~/.yaver/dns.json.
type DNSManager struct {
	mu         sync.Mutex
	configPath string
}

// NewDNSManager returns a new DNSManager. Config is stored at ~/.yaver/dns.json.
func NewDNSManager() *DNSManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return &DNSManager{
		configPath: filepath.Join(home, ".yaver", dnsConfigFile),
	}
}

// Add adds a DNS entry to /etc/hosts and records it in dns.json.
// If ip is empty, it defaults to 127.0.0.1.
// Returns a human-readable confirmation string.
func (m *DNSManager) Add(hostname, ip string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if hostname == "" {
		return "", fmt.Errorf("hostname must not be empty")
	}
	if ip == "" {
		ip = "127.0.0.1"
	}

	cfg, err := m.loadConfig()
	if err != nil {
		return "", fmt.Errorf("load dns config: %w", err)
	}

	// Check for duplicate.
	for _, e := range cfg.Entries {
		if e.Hostname == hostname {
			return "", fmt.Errorf("hostname %q is already managed by yaver (ip: %s)", hostname, e.IP)
		}
	}

	if err := m.writeHostsEntry(hostname, ip); err != nil {
		return "", fmt.Errorf("write /etc/hosts: %w", err)
	}

	cfg.Entries = append(cfg.Entries, DNSEntry{
		Hostname:  hostname,
		IP:        ip,
		AddedAt:   time.Now().UTC(),
		ManagedBy: "yaver",
	})
	if err := m.saveConfig(cfg); err != nil {
		return "", fmt.Errorf("save dns config: %w", err)
	}

	return fmt.Sprintf("Added: %s -> %s", hostname, ip), nil
}

// Remove removes a yaver-managed DNS entry from /etc/hosts and dns.json.
// Returns a human-readable confirmation string.
func (m *DNSManager) Remove(hostname string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if hostname == "" {
		return "", fmt.Errorf("hostname must not be empty")
	}

	cfg, err := m.loadConfig()
	if err != nil {
		return "", fmt.Errorf("load dns config: %w", err)
	}

	found := false
	filtered := cfg.Entries[:0]
	for _, e := range cfg.Entries {
		if e.Hostname == hostname {
			found = true
		} else {
			filtered = append(filtered, e)
		}
	}
	if !found {
		return "", fmt.Errorf("hostname %q is not managed by yaver", hostname)
	}

	if err := m.removeHostsEntry(hostname); err != nil {
		return "", fmt.Errorf("remove from /etc/hosts: %w", err)
	}

	cfg.Entries = filtered
	if err := m.saveConfig(cfg); err != nil {
		return "", fmt.Errorf("save dns config: %w", err)
	}

	return fmt.Sprintf("Removed: %s", hostname), nil
}

// List returns all yaver-managed DNS entries from dns.json.
func (m *DNSManager) List() ([]DNSEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load dns config: %w", err)
	}
	return cfg.Entries, nil
}

// Flush removes ALL yaver-managed entries from /etc/hosts, clears dns.json,
// and flushes the OS DNS cache. Returns a human-readable summary.
func (m *DNSManager) Flush() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadConfig()
	if err != nil {
		return "", fmt.Errorf("load dns config: %w", err)
	}

	removed := 0
	for _, e := range cfg.Entries {
		if err := m.removeHostsEntry(e.Hostname); err != nil {
			return "", fmt.Errorf("remove %q from /etc/hosts: %w", e.Hostname, err)
		}
		removed++
	}

	cfg.Entries = []DNSEntry{}
	if err := m.saveConfig(cfg); err != nil {
		return "", fmt.Errorf("save dns config: %w", err)
	}

	cacheMsg := ""
	if err := m.flushDNSCache(); err != nil {
		cacheMsg = fmt.Sprintf(" (DNS cache flush warning: %v)", err)
	} else {
		cacheMsg = " DNS cache flushed."
	}

	return fmt.Sprintf("Removed %d yaver-managed entries.%s", removed, cacheMsg), nil
}

// Sync ensures that dns.json entries are actually present in /etc/hosts.
// Missing entries are re-added. Orphaned entries (in /etc/hosts but not in
// dns.json) are reported but not touched. Returns a human-readable summary.
func (m *DNSManager) Sync() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, err := m.loadConfig()
	if err != nil {
		return "", fmt.Errorf("load dns config: %w", err)
	}

	hosts, err := m.readHostsFile()
	if err != nil {
		return "", fmt.Errorf("read /etc/hosts: %w", err)
	}

	added := 0
	orphaned := 0
	var msgs []string

	// Check that each entry in dns.json exists in /etc/hosts.
	for _, e := range cfg.Entries {
		if !hostsFileContainsEntry(hosts, e.Hostname) {
			if err := m.writeHostsEntry(e.Hostname, e.IP); err != nil {
				return "", fmt.Errorf("re-add %q to /etc/hosts: %w", e.Hostname, err)
			}
			msgs = append(msgs, fmt.Sprintf("  re-added: %s -> %s", e.Hostname, e.IP))
			added++
		}
	}

	// Check for yaver-managed lines in /etc/hosts that are not in dns.json.
	for _, line := range strings.Split(hosts, "\n") {
		if !strings.Contains(line, yaverDNSMarker) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hostname := fields[1]
		found := false
		for _, e := range cfg.Entries {
			if e.Hostname == hostname {
				found = true
				break
			}
		}
		if !found {
			msgs = append(msgs, fmt.Sprintf("  orphan in /etc/hosts (not in dns.json): %s", hostname))
			orphaned++
		}
	}

	summary := fmt.Sprintf("Sync complete: %d re-added, %d orphaned.", added, orphaned)
	if len(msgs) > 0 {
		summary += "\n" + strings.Join(msgs, "\n")
	}
	return summary, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (m *DNSManager) loadConfig() (*DNSConfig, error) {
	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return &DNSConfig{Entries: []DNSEntry{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.configPath, err)
	}
	var cfg DNSConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", m.configPath, err)
	}
	if cfg.Entries == nil {
		cfg.Entries = []DNSEntry{}
	}
	return &cfg, nil
}

func (m *DNSManager) saveConfig(cfg *DNSConfig) error {
	dir := filepath.Dir(m.configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dns config: %w", err)
	}
	if err := os.WriteFile(m.configPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", m.configPath, err)
	}
	return nil
}

func (m *DNSManager) readHostsFile() (string, error) {
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", hostsFile, err)
	}
	return string(data), nil
}

// writeHostsEntry appends a single entry to /etc/hosts using sudo tee -a so
// that the operation works even when the process is not running as root.
// Format: "<ip>\t<hostname>\t# yaver-managed"
func (m *DNSManager) writeHostsEntry(hostname, ip string) error {
	line := fmt.Sprintf("%s\t%s\t%s\n", ip, hostname, yaverDNSMarker)
	cmd := exec.Command("sudo", "tee", "-a", hostsFile)
	cmd.Stdin = strings.NewReader(line)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo tee -a %s: %w\n%s", hostsFile, err, string(out))
	}
	return nil
}

// removeHostsEntry rewrites /etc/hosts, omitting any yaver-managed line whose
// second field matches hostname.
func (m *DNSManager) removeHostsEntry(hostname string) error {
	content, err := m.readHostsFile()
	if err != nil {
		return err
	}

	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, yaverDNSMarker) {
			fields := strings.Fields(line)
			// fields[0] = ip, fields[1] = hostname, fields[2+] = comment
			if len(fields) >= 2 && fields[1] == hostname {
				continue // drop this line
			}
		}
		kept = append(kept, line)
	}

	newContent := strings.Join(kept, "\n")
	return m.writeHostsFileAtomic(newContent)
}

// writeHostsFileAtomic writes newContent to /etc/hosts via a temp file +
// sudo tee (without -a) to replace the file atomically.
func (m *DNSManager) writeHostsFileAtomic(content string) error {
	// Ensure content ends with a single newline.
	content = strings.TrimRight(content, "\n") + "\n"

	cmd := exec.Command("sudo", "tee", hostsFile)
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo tee %s: %w\n%s", hostsFile, err, string(out))
	}
	return nil
}

// flushDNSCache flushes the OS DNS resolver cache. The approach differs by
// platform.
//
//   - macOS: dscacheutil -flushcache + killall -HUP mDNSResponder
//   - Linux: systemd-resolve --flush-caches (if available); nscd restart as fallback
//   - Other: no-op
func (m *DNSManager) flushDNSCache() error {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sudo", "dscacheutil", "-flushcache").CombinedOutput(); err != nil {
			return fmt.Errorf("dscacheutil -flushcache: %w\n%s", err, string(out))
		}
		if out, err := exec.Command("sudo", "killall", "-HUP", "mDNSResponder").CombinedOutput(); err != nil {
			return fmt.Errorf("killall -HUP mDNSResponder: %w\n%s", err, string(out))
		}
		return nil

	case "linux":
		// Try systemd-resolve first.
		if path, err := exec.LookPath("systemd-resolve"); err == nil {
			if out, err := exec.Command(path, "--flush-caches").CombinedOutput(); err != nil {
				return fmt.Errorf("systemd-resolve --flush-caches: %w\n%s", err, string(out))
			}
			return nil
		}
		// Fall back to restarting nscd if it exists.
		if path, err := exec.LookPath("nscd"); err == nil {
			if out, err := exec.Command("sudo", path, "-i", "hosts").CombinedOutput(); err != nil {
				return fmt.Errorf("nscd -i hosts: %w\n%s", err, string(out))
			}
			return nil
		}
		// Nothing available — not an error; the entry will still work after TTL.
		return nil

	default:
		return nil
	}
}

// hostsFileContainsEntry reports whether the /etc/hosts content already has a
// yaver-managed line for the given hostname.
func hostsFileContainsEntry(content, hostname string) bool {
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, yaverDNSMarker) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == hostname {
			return true
		}
	}
	return false
}
