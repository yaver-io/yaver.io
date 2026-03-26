package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	healthMonConfigFile   = "healthmon.json"
	healthMonMaxHistory   = 50
	healthMonDefaultInterval = 60
	healthMonDefaultTimeout  = 5000
)

// HealthTarget represents a URL to monitor.
type HealthTarget struct {
	ID           string `json:"id"`
	URL          string `json:"url"`
	Label        string `json:"label,omitempty"`
	Method       string `json:"method,omitempty"`       // GET (default) or HEAD
	Interval     int    `json:"interval"`                // seconds, default 60
	TimeoutMs    int    `json:"timeoutMs"`               // default 5000
	ExpectStatus int    `json:"expectStatus,omitempty"`  // default 200
}

// HealthStatus holds the current status of a monitored target.
type HealthStatus struct {
	TargetID      string       `json:"targetId"`
	URL           string       `json:"url"`
	Label         string       `json:"label,omitempty"`
	Up            bool         `json:"up"`
	StatusCode    int          `json:"statusCode,omitempty"`
	ResponseMs    int64        `json:"responseMs,omitempty"`
	Error         string       `json:"error,omitempty"`
	CheckedAt     string       `json:"checkedAt"`
	UptimePercent float64      `json:"uptimePercent"`
	History       []HealthPing `json:"history,omitempty"`
}

// HealthPing records a single health check result.
type HealthPing struct {
	Up         bool   `json:"up"`
	StatusCode int    `json:"statusCode,omitempty"`
	ResponseMs int64  `json:"responseMs"`
	CheckedAt  string `json:"checkedAt"`
	Error      string `json:"error,omitempty"`
}

// HealthMonitor manages health check targets and their statuses.
type HealthMonitor struct {
	mu         sync.RWMutex
	targets    map[string]*HealthTarget
	statuses   map[string]*HealthStatus
	stopChs    map[string]chan struct{}
	configFile string
}

// NewHealthMonitor creates a new health monitor and loads saved targets.
func NewHealthMonitor() (*HealthMonitor, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}

	hm := &HealthMonitor{
		targets:    make(map[string]*HealthTarget),
		statuses:   make(map[string]*HealthStatus),
		stopChs:    make(map[string]chan struct{}),
		configFile: filepath.Join(dir, healthMonConfigFile),
	}

	// Load saved targets
	if data, err := os.ReadFile(hm.configFile); err == nil {
		var targets []*HealthTarget
		if json.Unmarshal(data, &targets) == nil {
			for _, t := range targets {
				hm.targets[t.ID] = t
				hm.statuses[t.ID] = &HealthStatus{
					TargetID: t.ID,
					URL:      t.URL,
					Label:    t.Label,
				}
			}
			log.Printf("[healthmon] Loaded %d targets from config", len(targets))
		}
	}

	// Start monitoring all targets
	for _, t := range hm.targets {
		hm.startMonitor(t)
	}

	return hm, nil
}

func (hm *HealthMonitor) persist() {
	hm.mu.RLock()
	targets := make([]*HealthTarget, 0, len(hm.targets))
	for _, t := range hm.targets {
		targets = append(targets, t)
	}
	hm.mu.RUnlock()

	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(hm.configFile, data, 0600)
}

// AddTarget adds a new monitoring target and starts checking it.
func (hm *HealthMonitor) AddTarget(url, label, method string, interval, timeoutMs, expectStatus int) *HealthTarget {
	if method == "" {
		method = "GET"
	}
	if interval <= 0 {
		interval = healthMonDefaultInterval
	}
	if timeoutMs <= 0 {
		timeoutMs = healthMonDefaultTimeout
	}
	if expectStatus <= 0 {
		expectStatus = 200
	}

	target := &HealthTarget{
		ID:           uuid.New().String()[:8],
		URL:          url,
		Label:        label,
		Method:       method,
		Interval:     interval,
		TimeoutMs:    timeoutMs,
		ExpectStatus: expectStatus,
	}

	hm.mu.Lock()
	hm.targets[target.ID] = target
	hm.statuses[target.ID] = &HealthStatus{
		TargetID: target.ID,
		URL:      url,
		Label:    label,
	}
	hm.mu.Unlock()

	hm.persist()
	hm.startMonitor(target)

	log.Printf("[healthmon] Added target %s: %s (%s) every %ds", target.ID, url, label, interval)
	return target
}

// RemoveTarget stops monitoring and removes a target.
func (hm *HealthMonitor) RemoveTarget(id string) bool {
	hm.mu.Lock()
	_, exists := hm.targets[id]
	if !exists {
		hm.mu.Unlock()
		return false
	}

	// Stop the monitor goroutine
	if stopCh, ok := hm.stopChs[id]; ok {
		close(stopCh)
		delete(hm.stopChs, id)
	}
	delete(hm.targets, id)
	delete(hm.statuses, id)
	hm.mu.Unlock()

	hm.persist()
	log.Printf("[healthmon] Removed target %s", id)
	return true
}

// ListStatuses returns all target statuses.
func (hm *HealthMonitor) ListStatuses() []HealthStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	result := make([]HealthStatus, 0, len(hm.statuses))
	for _, s := range hm.statuses {
		// Return copy without full history for list view
		status := *s
		status.History = nil
		result = append(result, status)
	}
	return result
}

// GetStatus returns the detailed status of a target including history.
func (hm *HealthMonitor) GetStatus(id string) (*HealthStatus, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	s, ok := hm.statuses[id]
	if !ok {
		return nil, false
	}
	// Return a copy
	statusCopy := *s
	statusCopy.History = make([]HealthPing, len(s.History))
	for i, p := range s.History {
		statusCopy.History[i] = p
	}
	return &statusCopy, true
}

// ForceCheck runs an immediate health check for a target.
func (hm *HealthMonitor) ForceCheck(id string) (*HealthStatus, bool) {
	hm.mu.RLock()
	target, ok := hm.targets[id]
	hm.mu.RUnlock()
	if !ok {
		return nil, false
	}

	hm.checkTarget(target)

	hm.mu.RLock()
	status, ok := hm.statuses[id]
	hm.mu.RUnlock()
	if !ok {
		return nil, false
	}

	statusCopy := *status
	return &statusCopy, true
}

func (hm *HealthMonitor) startMonitor(target *HealthTarget) {
	stopCh := make(chan struct{})

	hm.mu.Lock()
	hm.stopChs[target.ID] = stopCh
	hm.mu.Unlock()

	go func() {
		// Run first check immediately
		hm.checkTarget(target)

		ticker := time.NewTicker(time.Duration(target.Interval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				hm.checkTarget(target)
			}
		}
	}()
}

func (hm *HealthMonitor) checkTarget(target *HealthTarget) {
	method := target.Method
	if method == "" {
		method = "GET"
	}
	expectStatus := target.ExpectStatus
	if expectStatus == 0 {
		expectStatus = 200
	}
	timeoutMs := target.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = healthMonDefaultTimeout
	}

	client := &http.Client{
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
	}

	ping := HealthPing{
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	start := time.Now()
	req, err := http.NewRequest(method, target.URL, nil)
	if err != nil {
		ping.Error = err.Error()
		ping.Up = false
	} else {
		req.Header.Set("User-Agent", "Yaver-HealthMon/1.0")
		resp, err := client.Do(req)
		if err != nil {
			ping.Error = err.Error()
			ping.Up = false
		} else {
			resp.Body.Close()
			ping.StatusCode = resp.StatusCode
			ping.ResponseMs = time.Since(start).Milliseconds()
			ping.Up = resp.StatusCode == expectStatus
		}
	}

	hm.mu.Lock()
	status, ok := hm.statuses[target.ID]
	if !ok {
		hm.mu.Unlock()
		return
	}

	status.Up = ping.Up
	status.StatusCode = ping.StatusCode
	status.ResponseMs = ping.ResponseMs
	status.Error = ping.Error
	status.CheckedAt = ping.CheckedAt

	// Add to history ring buffer
	status.History = append(status.History, ping)
	if len(status.History) > healthMonMaxHistory {
		status.History = status.History[len(status.History)-healthMonMaxHistory:]
	}

	// Calculate 24h uptime
	status.UptimePercent = calcUptime(status.History)
	hm.mu.Unlock()
}

// calcUptime calculates uptime percentage from ping history.
func calcUptime(history []HealthPing) float64 {
	if len(history) == 0 {
		return 0
	}

	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	var total, up int
	for _, p := range history {
		t, err := time.Parse(time.RFC3339, p.CheckedAt)
		if err != nil || t.Before(cutoff) {
			continue
		}
		total++
		if p.Up {
			up++
		}
	}

	if total == 0 {
		return 0
	}
	return float64(up) / float64(total) * 100
}
