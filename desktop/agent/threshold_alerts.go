package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// ThresholdAlert is a "cpu > 80 for 5m" style rule.
type ThresholdAlert struct {
	ID            string  `json:"id"`
	Metric        string  `json:"metric"`        // cpu, ram, disk
	Threshold     float64 `json:"threshold"`     // percent
	DurationSecs  int     `json:"durationSecs"`  // sustained over this window
	Label         string  `json:"label,omitempty"`
	Active        bool    `json:"active"`
	LastFiredAt   *time.Time `json:"lastFiredAt,omitempty"`
}

type alertsStore struct {
	db *sql.DB
	mu sync.Mutex
}

var globalAlerts *alertsStore

func ensureAlerts() (*alertsStore, error) {
	if globalAlerts != nil {
		return globalAlerts, nil
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(base, "alerts.db"))
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS threshold_alerts (
		id TEXT PRIMARY KEY, metric TEXT, threshold REAL, duration_secs INTEGER,
		label TEXT, last_fired DATETIME
	)`)
	globalAlerts = &alertsStore{db: db}
	return globalAlerts, nil
}

func (a *alertsStore) Add(t ThresholdAlert) (*ThresholdAlert, error) {
	if t.ID == "" {
		t.ID = fmt.Sprintf("alert_%d", time.Now().UnixNano())
	}
	if t.DurationSecs <= 0 {
		t.DurationSecs = 300
	}
	if t.Metric == "" {
		return nil, fmt.Errorf("metric required (cpu|ram|disk)")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := a.db.Exec(`INSERT OR REPLACE INTO threshold_alerts VALUES(?, ?, ?, ?, ?, NULL)`,
		t.ID, t.Metric, t.Threshold, t.DurationSecs, t.Label)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (a *alertsStore) Remove(id string) error {
	_, err := a.db.Exec(`DELETE FROM threshold_alerts WHERE id = ?`, id)
	return err
}

func (a *alertsStore) List() ([]ThresholdAlert, error) {
	rows, err := a.db.Query(`SELECT id, metric, threshold, duration_secs, label, last_fired FROM threshold_alerts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThresholdAlert
	for rows.Next() {
		var t ThresholdAlert
		var last sql.NullTime
		var label sql.NullString
		if err := rows.Scan(&t.ID, &t.Metric, &t.Threshold, &t.DurationSecs, &label, &last); err != nil {
			continue
		}
		if label.Valid {
			t.Label = label.String
		}
		if last.Valid {
			t.LastFiredAt = &last.Time
		}
		out = append(out, t)
	}
	return out, nil
}

// CheckAllAlerts is called by the metrics sampler after each sample. Fires
// notifications for rules that are now sustained above threshold (and haven't
// fired within the window yet — to avoid notification storms).
func CheckAllAlerts() {
	a, err := ensureAlerts()
	if err != nil {
		return
	}
	h, err := ensureMetricsHistory()
	if err != nil {
		return
	}
	alerts, err := a.List()
	if err != nil {
		return
	}
	now := time.Now()
	for _, rule := range alerts {
		window := time.Duration(rule.DurationSecs) * time.Second
		if !h.SustainedAbove(rule.Metric, rule.Threshold, window) {
			continue
		}
		// Debounce: don't re-fire within the window.
		if rule.LastFiredAt != nil && now.Sub(*rule.LastFiredAt) < window {
			continue
		}
		label := rule.Label
		if label == "" {
			label = rule.Metric
		}
		msg := fmt.Sprintf("%s ≥ %.0f%% sustained over %s", label, rule.Threshold, window)
		if globalNotifyManager != nil {
			globalNotifyManager.NotifyAgentEvent("Threshold alert", msg)
		}
		AuditLog("", "threshold_alert", rule.Metric, msg, "fired", "", "")
		_, _ = a.db.Exec(`UPDATE threshold_alerts SET last_fired = ? WHERE id = ?`, now, rule.ID)
	}
}

// ---- HTTP ----

func (s *HTTPServer) handleAlertList(w http.ResponseWriter, r *http.Request) {
	a, err := ensureAlerts()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	list, err := a.List()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"alerts": list})
}

func (s *HTTPServer) handleAlertAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	a, err := ensureAlerts()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	var t ThresholdAlert
	_ = json.NewDecoder(r.Body).Decode(&t)
	res, err := a.Add(t)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleAlertRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	a, err := ensureAlerts()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	var b struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := a.Remove(b.ID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
