package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// YaverUptimeAlert pings an HTTP endpoint on an interval and fires notifications
// via the existing Yaver notification system when a target flips up→down.
type YaverUptimeAlert struct {
	ID              string `json:"id"`
	URL             string `json:"url"`
	Name            string `json:"name"`
	IntervalSeconds int    `json:"intervalSeconds"`
	Timeout         int    `json:"timeoutSeconds"`
	Status          string `json:"status"` // up, down, unknown
	LastCheck       time.Time `json:"lastCheck"`
	LastLatencyMS   int    `json:"lastLatencyMs"`
	AlertOnDown     bool   `json:"alertOnDown"`
}

type uptimeStore struct {
	mu      sync.Mutex
	db      *sql.DB
	cancels map[string]context.CancelFunc
}

var globalUptime *uptimeStore

func ensureUptime() (*uptimeStore, error) {
	if globalUptime != nil {
		return globalUptime, nil
	}
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(base, "uptime.db"))
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS uptime_monitors (
		id TEXT PRIMARY KEY, url TEXT, name TEXT, interval_seconds INTEGER,
		timeout_seconds INTEGER, status TEXT, last_check DATETIME, last_latency_ms INTEGER,
		alert_on_down INTEGER
	)`)
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS uptime_checks (
		monitor_id TEXT, ts DATETIME, status TEXT, latency_ms INTEGER, error TEXT
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_uptime_checks ON uptime_checks(monitor_id, ts DESC)`)
	globalUptime = &uptimeStore{db: db, cancels: map[string]context.CancelFunc{}}
	// Resume all monitors at startup.
	globalUptime.resumeAll()
	return globalUptime, nil
}

func (u *uptimeStore) resumeAll() {
	monitors, _ := u.list()
	for _, m := range monitors {
		u.startWorker(m)
	}
}

func (u *uptimeStore) Add(m YaverUptimeAlert) (*YaverUptimeAlert, error) {
	if m.URL == "" {
		return nil, fmt.Errorf("url required")
	}
	if m.ID == "" {
		m.ID = fmt.Sprintf("mon_%d", time.Now().UnixNano())
	}
	if m.IntervalSeconds <= 0 {
		m.IntervalSeconds = 60
	}
	if m.Timeout <= 0 {
		m.Timeout = 10
	}
	if m.Name == "" {
		m.Name = m.URL
	}
	m.Status = "unknown"
	u.mu.Lock()
	_, err := u.db.Exec(`INSERT OR REPLACE INTO uptime_monitors(id, url, name, interval_seconds, timeout_seconds, status, last_check, last_latency_ms, alert_on_down) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.URL, m.Name, m.IntervalSeconds, m.Timeout, m.Status, time.Now(), 0, boolToInt(m.AlertOnDown))
	u.mu.Unlock()
	if err != nil {
		return nil, err
	}
	u.startWorker(&m)
	return &m, nil
}

func (u *uptimeStore) Remove(id string) error {
	u.mu.Lock()
	if cancel, ok := u.cancels[id]; ok {
		cancel()
		delete(u.cancels, id)
	}
	u.mu.Unlock()
	_, err := u.db.Exec(`DELETE FROM uptime_monitors WHERE id = ?`, id)
	return err
}

func (u *uptimeStore) list() ([]*YaverUptimeAlert, error) {
	rows, err := u.db.Query(`SELECT id, url, name, interval_seconds, timeout_seconds, status, last_check, last_latency_ms, alert_on_down FROM uptime_monitors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*YaverUptimeAlert
	for rows.Next() {
		var m YaverUptimeAlert
		var alert int
		if err := rows.Scan(&m.ID, &m.URL, &m.Name, &m.IntervalSeconds, &m.Timeout, &m.Status, &m.LastCheck, &m.LastLatencyMS, &alert); err != nil {
			continue
		}
		m.AlertOnDown = alert == 1
		out = append(out, &m)
	}
	return out, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (u *uptimeStore) startWorker(m *YaverUptimeAlert) {
	u.mu.Lock()
	if cancel, ok := u.cancels[m.ID]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancels[m.ID] = cancel
	u.mu.Unlock()
	go u.run(ctx, *m)
}

func (u *uptimeStore) run(ctx context.Context, m YaverUptimeAlert) {
	check := func() {
		start := time.Now()
		client := &http.Client{Timeout: time.Duration(m.Timeout) * time.Second}
		status := "up"
		errMsg := ""
		latency := 0
		if res, err := client.Get(m.URL); err != nil {
			status = "down"
			errMsg = err.Error()
		} else {
			res.Body.Close()
			latency = int(time.Since(start).Milliseconds())
			if res.StatusCode >= 500 {
				status = "down"
				errMsg = fmt.Sprintf("HTTP %d", res.StatusCode)
			} else if res.StatusCode >= 400 {
				status = "warning"
			}
		}
		prevStatus := ""
		u.mu.Lock()
		_ = u.db.QueryRow(`SELECT status FROM uptime_monitors WHERE id = ?`, m.ID).Scan(&prevStatus)
		_, _ = u.db.Exec(`UPDATE uptime_monitors SET status = ?, last_check = ?, last_latency_ms = ? WHERE id = ?`,
			status, time.Now(), latency, m.ID)
		_, _ = u.db.Exec(`INSERT INTO uptime_checks(monitor_id, ts, status, latency_ms, error) VALUES (?, ?, ?, ?, ?)`,
			m.ID, time.Now(), status, latency, errMsg)
		u.mu.Unlock()

		// Fire alert on up → down transition.
		if m.AlertOnDown && prevStatus == "up" && status == "down" {
			fireUptimeAlert(m, errMsg)
		}
		if m.AlertOnDown && prevStatus == "down" && status == "up" {
			fireUptimeRecovery(m)
		}
	}
	check()
	t := time.NewTicker(time.Duration(m.IntervalSeconds) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

// fireUptimeAlert pushes a notification via the existing NotificationManager.
// That already fans out to mobile push + email + any other channels the user has wired.
func fireUptimeAlert(m YaverUptimeAlert, reason string) {
	body := fmt.Sprintf("%s is DOWN — %s", m.Name, reason)
	notifyAllChannels("Uptime alert", body, map[string]string{
		"kind": "uptime-down", "url": m.URL, "monitor": m.ID,
	})
}

func fireUptimeRecovery(m YaverUptimeAlert) {
	body := fmt.Sprintf("%s recovered", m.Name)
	notifyAllChannels("Uptime recovery", body, map[string]string{
		"kind": "uptime-up", "url": m.URL, "monitor": m.ID,
	})
}

// globalNotifyManager is a package-level reference set during HTTP server init
// so side features (uptime, deploy, backup) can fire real push notifications
// without threading the manager through every call site.
var globalNotifyManager *NotificationManager

func SetGlobalNotifier(nm *NotificationManager) { globalNotifyManager = nm }

// notifyAllChannels routes to the user's configured notification channels
// (mobile push, email, Slack, etc.) via the existing NotificationManager.
func notifyAllChannels(title, body string, data map[string]string) {
	if globalNotifyManager != nil {
		// Use the health-check path — it already fans out to push/email/Slack.
		status := "down"
		if data["kind"] == "uptime-up" {
			status = "up"
		}
		globalNotifyManager.NotifyHealthCheck(title, data["url"], status, 0)
	} else {
		payload, _ := json.Marshal(map[string]interface{}{
			"title": title, "body": body, "data": data, "ts": time.Now().UTC().Format(time.RFC3339),
		})
		fmt.Printf("[notify] %s\n", string(payload))
	}
	// Always mirror to the error tracker so the Errors tab / event feed sees it.
	if globalErrorTracker != nil {
		_ = globalErrorTracker.Ingest(&ErrorEvent{
			Message:     title + ": " + body,
			Fingerprint: "uptime-" + data["monitor"],
			Context:     map[string]interface{}{"kind": data["kind"], "url": data["url"]},
		})
	}
}

// ---- HTTP ----

func (s *HTTPServer) handleUptimeList(w http.ResponseWriter, r *http.Request) {
	u, err := ensureUptime()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	mons, _ := u.list()
	writeJSON(w, http.StatusOK, map[string]interface{}{"monitors": mons})
}

func (s *HTTPServer) handleUptimeAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	u, err := ensureUptime()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	var m YaverUptimeAlert
	_ = json.NewDecoder(r.Body).Decode(&m)
	res, err := u.Add(m)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleUptimeRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	u, err := ensureUptime()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	var b struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := u.Remove(b.ID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
