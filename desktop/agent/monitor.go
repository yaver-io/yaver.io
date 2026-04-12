package main

// monitor.go — UptimeMonitor: programmatic URL health-checking with ring-buffer
// history, retry logic, and disk persistence. Separate from the CLI-oriented
// Monitor/monitor_cmd.go — this is the embeddable struct used by HTTP handlers
// and MCP tools that need a structured Go API rather than argv parsing.
//
// Types use an "Uptime" prefix to avoid clashing with the MonitorCheck /
// Monitor types declared in monitor_cmd.go.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	uptimeHistorySize   = 1000
	uptimeRetryCount    = 3
	uptimeRetryDelay    = 5 * time.Second
	uptimeHTTPTimeout   = 10 * time.Second
)

// UptimeEntry holds configuration and current status for a monitored URL.
type UptimeEntry struct {
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	IntervalSec    int       `json:"intervalSec"`
	ExpectedStatus int       `json:"expectedStatus"`
	CurrentStatus  string    `json:"currentStatus"` // "up", "down", "unknown"
	LastCheck      time.Time `json:"lastCheck"`
	LastLatencyMs  int64     `json:"lastLatencyMs"`
	UptimePct      float64   `json:"uptimePct"`
	ConsecFails    int       `json:"consecFails"`
}

// UptimeCheck is a single check result stored in the ring buffer.
type UptimeCheck struct {
	Timestamp  time.Time `json:"timestamp"`
	StatusCode int       `json:"statusCode"`
	LatencyMs  int64     `json:"latencyMs"`
	Error      string    `json:"error,omitempty"`
	Up         bool      `json:"up"`
}

// UptimeOverview is a summary of all monitors.
type UptimeOverview struct {
	TotalMonitors int `json:"totalMonitors"`
	Up            int `json:"up"`
	Down          int `json:"down"`
	Unknown       int `json:"unknown"`
}

// uptimeState holds runtime state for a single monitor.
type uptimeState struct {
	entry   UptimeEntry
	history [uptimeHistorySize]UptimeCheck
	histLen int // number of entries filled (capped at uptimeHistorySize)
	histIdx int // next write position
	stopCh  chan struct{}
}

// addCheck appends a result to the ring buffer and refreshes entry stats.
func (us *uptimeState) addCheck(check UptimeCheck) {
	us.history[us.histIdx] = check
	us.histIdx = (us.histIdx + 1) % uptimeHistorySize
	if us.histLen < uptimeHistorySize {
		us.histLen++
	}

	us.entry.LastCheck = check.Timestamp
	us.entry.LastLatencyMs = check.LatencyMs

	if check.Up {
		us.entry.CurrentStatus = "up"
		us.entry.ConsecFails = 0
	} else {
		us.entry.ConsecFails++
		if us.entry.ConsecFails >= uptimeRetryCount {
			us.entry.CurrentStatus = "down"
		}
	}

	// Recalculate uptime % from all stored checks.
	upCount := 0
	for i := 0; i < us.histLen; i++ {
		if us.history[i].Up {
			upCount++
		}
	}
	if us.histLen > 0 {
		us.entry.UptimePct = float64(upCount) / float64(us.histLen) * 100.0
	}
}

// recentChecks returns up to `limit` most-recent checks in chronological order.
func (us *uptimeState) recentChecks(limit int) []UptimeCheck {
	if limit <= 0 || us.histLen == 0 {
		return nil
	}
	if limit > us.histLen {
		limit = us.histLen
	}
	out := make([]UptimeCheck, limit)
	// When the buffer is full, the oldest entry starts at histIdx.
	// When not full, entries start at index 0.
	start := 0
	if us.histLen == uptimeHistorySize {
		start = us.histIdx
	}
	total := us.histLen
	for i := 0; i < limit; i++ {
		idx := (start + (total - limit) + i) % uptimeHistorySize
		out[i] = us.history[idx]
	}
	return out
}

// UptimeMonitor manages a set of URL monitors with configurable intervals,
// in-memory ring-buffer history, and disk persistence.
type UptimeMonitor struct {
	mu         sync.RWMutex
	monitors   map[string]*uptimeState
	client     *http.Client
	stopOnce   sync.Once
	globalStop chan struct{}
}

// NewUptimeMonitor creates a new UptimeMonitor and loads any previously
// saved monitors and history from disk.
func NewUptimeMonitor() *UptimeMonitor {
	um := &UptimeMonitor{
		monitors:   make(map[string]*uptimeState),
		client:     &http.Client{Timeout: uptimeHTTPTimeout},
		globalStop: make(chan struct{}),
	}
	um.loadFromDisk()
	return um
}

// Start launches monitoring goroutines for all registered monitors.
// It is safe to call Start more than once; already-running monitors
// are not restarted.
func (um *UptimeMonitor) Start() {
	um.mu.Lock()
	defer um.mu.Unlock()
	for _, us := range um.monitors {
		if us.stopCh == nil {
			us.stopCh = make(chan struct{})
			go um.runMonitor(us)
		}
	}
}

// Stop halts all monitoring goroutines and persists history to disk.
// After Stop, the UptimeMonitor must not be used again.
func (um *UptimeMonitor) Stop() {
	um.stopOnce.Do(func() {
		close(um.globalStop)
		um.mu.Lock()
		for _, us := range um.monitors {
			if us.stopCh != nil {
				close(us.stopCh)
				us.stopCh = nil
			}
		}
		um.mu.Unlock()
		um.saveHistoryToDisk()
	})
}

// Add registers a URL to be monitored and persists the configuration.
// Returns an error if a monitor with the same name already exists or if
// any argument is invalid.
func (um *UptimeMonitor) Add(name, url string, intervalSec, expectedStatus int) error {
	if name == "" {
		return fmt.Errorf("monitor name cannot be empty")
	}
	if url == "" {
		return fmt.Errorf("monitor URL cannot be empty")
	}
	if intervalSec <= 0 {
		return fmt.Errorf("intervalSec must be positive")
	}
	if expectedStatus < 100 || expectedStatus > 599 {
		return fmt.Errorf("expectedStatus must be a valid HTTP status code")
	}

	um.mu.Lock()
	defer um.mu.Unlock()

	if _, exists := um.monitors[name]; exists {
		return fmt.Errorf("monitor %q already exists", name)
	}

	us := &uptimeState{
		entry: UptimeEntry{
			Name:           name,
			URL:            url,
			IntervalSec:    intervalSec,
			ExpectedStatus: expectedStatus,
			CurrentStatus:  "unknown",
		},
		stopCh: make(chan struct{}),
	}
	um.monitors[name] = us
	go um.runMonitor(us)

	if err := um.saveConfigLocked(); err != nil {
		return fmt.Errorf("monitor added but failed to persist config: %w", err)
	}
	return nil
}

// Remove stops and deletes a named monitor, persisting the change to disk.
func (um *UptimeMonitor) Remove(name string) error {
	um.mu.Lock()
	defer um.mu.Unlock()

	us, exists := um.monitors[name]
	if !exists {
		return fmt.Errorf("monitor %q not found", name)
	}
	if us.stopCh != nil {
		close(us.stopCh)
		us.stopCh = nil
	}
	delete(um.monitors, name)

	if err := um.saveConfigLocked(); err != nil {
		return fmt.Errorf("monitor removed but failed to persist config: %w", err)
	}
	return nil
}

// List returns a snapshot of all monitored entries with current status.
func (um *UptimeMonitor) List() []UptimeEntry {
	um.mu.RLock()
	defer um.mu.RUnlock()

	out := make([]UptimeEntry, 0, len(um.monitors))
	for _, us := range um.monitors {
		out = append(out, us.entry)
	}
	return out
}

// History returns up to `limit` most-recent check results for a named monitor.
func (um *UptimeMonitor) History(name string, limit int) ([]UptimeCheck, error) {
	um.mu.RLock()
	defer um.mu.RUnlock()

	us, exists := um.monitors[name]
	if !exists {
		return nil, fmt.Errorf("monitor %q not found", name)
	}
	return us.recentChecks(limit), nil
}

// Status returns a high-level overview (up/down/unknown counts).
func (um *UptimeMonitor) Status() *UptimeOverview {
	um.mu.RLock()
	defer um.mu.RUnlock()

	ov := &UptimeOverview{TotalMonitors: len(um.monitors)}
	for _, us := range um.monitors {
		switch us.entry.CurrentStatus {
		case "up":
			ov.Up++
		case "down":
			ov.Down++
		default:
			ov.Unknown++
		}
	}
	return ov
}

// runMonitor is the per-monitor goroutine. It fires an immediate check on
// start then ticks at the configured interval until stopped.
func (um *UptimeMonitor) runMonitor(us *uptimeState) {
	interval := time.Duration(us.entry.IntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Immediate first check.
	um.performCheck(us)

	for {
		select {
		case <-ticker.C:
			um.performCheck(us)
		case <-us.stopCh:
			return
		case <-um.globalStop:
			return
		}
	}
}

// performCheck runs one check with up to uptimeRetryCount attempts separated
// by uptimeRetryDelay, then records the final result.
func (um *UptimeMonitor) performCheck(us *uptimeState) {
	var last UptimeCheck
	for attempt := 0; attempt < uptimeRetryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(uptimeRetryDelay)
		}
		c := um.doHTTPCheck(us.entry.URL, us.entry.ExpectedStatus)
		last = c
		if c.Up {
			break
		}
	}

	um.mu.Lock()
	us.addCheck(last)
	um.mu.Unlock()
}

// doHTTPCheck performs a single HTTP GET against `url` and returns a
// UptimeCheck. The check is considered "up" when the response status
// code matches expectedStatus.
func (um *UptimeMonitor) doHTTPCheck(url string, expectedStatus int) UptimeCheck {
	start := time.Now()
	c := UptimeCheck{Timestamp: start}

	resp, err := um.client.Get(url) //nolint:noctx
	c.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		c.Error = err.Error()
		c.Up = false
		return c
	}
	defer resp.Body.Close()

	c.StatusCode = resp.StatusCode
	c.Up = (resp.StatusCode == expectedStatus)
	if !c.Up {
		c.Error = fmt.Sprintf("expected status %d, got %d", expectedStatus, resp.StatusCode)
	}
	return c
}

// --- disk persistence ---

type uptimeDiskConfig struct {
	Monitors []UptimeEntry `json:"monitors"`
}

type uptimeHistoryDisk struct {
	Histories map[string][]UptimeCheck `json:"histories"`
}

func uptimeConfigPath() string {
	dir, err := ConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".yaver")
	}
	return filepath.Join(dir, "uptime_monitors.json")
}

func uptimeHistoryPath() string {
	dir, err := ConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".yaver")
	}
	return filepath.Join(dir, "uptime_history.json")
}

// saveConfigLocked persists monitor configuration (no history) to disk.
// Caller must hold um.mu (write lock).
func (um *UptimeMonitor) saveConfigLocked() error {
	entries := make([]UptimeEntry, 0, len(um.monitors))
	for _, us := range um.monitors {
		// Save only static config; runtime fields reset on load.
		e := UptimeEntry{
			Name:           us.entry.Name,
			URL:            us.entry.URL,
			IntervalSec:    us.entry.IntervalSec,
			ExpectedStatus: us.entry.ExpectedStatus,
			CurrentStatus:  "unknown",
		}
		entries = append(entries, e)
	}

	cfg := uptimeDiskConfig{Monitors: entries}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	path := uptimeConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// saveHistoryToDisk writes all ring-buffer histories to disk. Best-effort:
// errors are silently ignored (history is informational).
func (um *UptimeMonitor) saveHistoryToDisk() {
	um.mu.RLock()
	defer um.mu.RUnlock()

	histories := make(map[string][]UptimeCheck, len(um.monitors))
	for name, us := range um.monitors {
		histories[name] = us.recentChecks(uptimeHistorySize)
	}

	disk := uptimeHistoryDisk{Histories: histories}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return
	}

	path := uptimeHistoryPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// loadFromDisk reads monitor config and history saved by a previous run.
// Missing files are silently ignored (fresh start).
func (um *UptimeMonitor) loadFromDisk() {
	cfgData, err := os.ReadFile(uptimeConfigPath())
	if err != nil {
		return
	}
	var cfg uptimeDiskConfig
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return
	}

	// Load history (best-effort — missing file is fine).
	var histDisk uptimeHistoryDisk
	if histData, err := os.ReadFile(uptimeHistoryPath()); err == nil {
		_ = json.Unmarshal(histData, &histDisk)
	}

	for _, entry := range cfg.Monitors {
		entry.CurrentStatus = "unknown"
		us := &uptimeState{entry: entry}

		// Restore persisted history so uptime % survives restarts.
		if histDisk.Histories != nil {
			for _, c := range histDisk.Histories[entry.Name] {
				us.addCheck(c)
			}
			// Re-derive CurrentStatus from the last recorded check.
			if us.histLen > 0 {
				lastIdx := (us.histIdx - 1 + uptimeHistorySize) % uptimeHistorySize
				if us.history[lastIdx].Up {
					us.entry.CurrentStatus = "up"
				} else {
					us.entry.CurrentStatus = "down"
				}
			}
		}

		um.monitors[entry.Name] = us
	}
}
