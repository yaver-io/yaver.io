package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// metricsHistory persists HostMetrics samples to SQLite. 7-day ring. Also
// consulted by the threshold-alerts engine to detect sustained-above-threshold
// windows (not just instant spikes).
type metricsHistory struct {
	db *sql.DB
	mu sync.Mutex
}

var globalMetrics *metricsHistory

func ensureMetricsHistory() (*metricsHistory, error) {
	if globalMetrics != nil {
		return globalMetrics, nil
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(base, "metrics.db"))
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS metrics (
		ts DATETIME, cpu REAL, ram_pct REAL, disk_pct REAL, rx INTEGER, tx INTEGER
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics(ts DESC)`)
	globalMetrics = &metricsHistory{db: db}
	return globalMetrics, nil
}

// Insert records a sample.
func (h *metricsHistory) Insert(m *HostMetrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, _ = h.db.Exec(`INSERT INTO metrics VALUES(?, ?, ?, ?, ?, ?)`,
		time.Now(), m.CPUPct, m.RAMPct, m.DiskPct, m.NetRxBps, m.NetTxBps)
	// Purge older than 7 days.
	_, _ = h.db.Exec(`DELETE FROM metrics WHERE ts < datetime('now', '-7 days')`)
}

// Since returns samples since a given time.
type MetricSample struct {
	TS       time.Time `json:"ts"`
	CPUPct   float64   `json:"cpuPct"`
	RAMPct   float64   `json:"ramPct"`
	DiskPct  float64   `json:"diskPct"`
	NetRxBps int64     `json:"netRxBps"`
	NetTxBps int64     `json:"netTxBps"`
}

func (h *metricsHistory) Since(since time.Time, limit int) ([]MetricSample, error) {
	if limit <= 0 || limit > 100000 {
		limit = 5000
	}
	rows, err := h.db.Query(`SELECT ts, cpu, ram_pct, disk_pct, rx, tx FROM metrics WHERE ts >= ? ORDER BY ts DESC LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MetricSample
	for rows.Next() {
		var s MetricSample
		if err := rows.Scan(&s.TS, &s.CPUPct, &s.RAMPct, &s.DiskPct, &s.NetRxBps, &s.NetTxBps); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// SustainedAbove returns true if the given metric stayed >= threshold for the
// full `window`. Used by the alerts engine.
func (h *metricsHistory) SustainedAbove(metric string, threshold float64, window time.Duration) bool {
	since := time.Now().Add(-window)
	col := map[string]string{
		"cpu": "cpu", "ram": "ram_pct", "disk": "disk_pct",
	}[metric]
	if col == "" {
		return false
	}
	var minVal float64
	var n int
	err := h.db.QueryRow(fmt.Sprintf(`SELECT COALESCE(MIN(%s), -1), COUNT(*) FROM metrics WHERE ts >= ?`, col), since).Scan(&minVal, &n)
	if err != nil || n < 2 {
		return false
	}
	return minVal >= threshold
}

// StartMetricsSampler launches a background ticker that captures host metrics
// every 2 seconds and persists to history.
func StartMetricsSampler(ctx context.Context) {
	h, err := ensureMetricsHistory()
	if err != nil {
		return
	}
	go func() {
		var lastNet map[string]uint64
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sample, next := sampleHostMetrics(ctx, lastNet)
				lastNet = next
				h.Insert(sample)
				// Feed the alert engine.
				CheckAllAlerts()
			}
		}
	}()
}

// ---- HTTP ----

func (s *HTTPServer) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	h, err := ensureMetricsHistory()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	// ?window=1h | 15m | 6h | 24h | 7d
	win := r.URL.Query().Get("window")
	if win == "" {
		win = "1h"
	}
	d, err := time.ParseDuration(win)
	if err != nil {
		d = time.Hour
	}
	samples, err := h.Since(time.Now().Add(-d), 5000)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"samples": samples, "window": win})
}
