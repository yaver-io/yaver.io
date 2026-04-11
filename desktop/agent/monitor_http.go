package main

// monitor_http.go — HTTP surface for `yaver monitor` so the
// mobile Monitor/Uptime tab can list, add, pause, resume, and
// remove uptime checks without shelling into the terminal.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *HTTPServer) handleMonitors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := loadMonitors()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":       true,
			"monitors": list,
		})
	case http.MethodPost:
		var body struct {
			Name     string `json:"name,omitempty"`
			URL      string `json:"url"`
			Interval string `json:"interval,omitempty"`
			Method   string `json:"method,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			jsonError(w, http.StatusBadRequest, "url required")
			return
		}
		if body.Interval == "" {
			body.Interval = "60s"
		}
		if body.Method == "" {
			body.Method = "GET"
		}
		monitorMu.Lock()
		list, err := loadMonitors()
		if err != nil {
			monitorMu.Unlock()
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		m := &Monitor{
			ID:       randomID(),
			Name:     body.Name,
			URL:      body.URL,
			Interval: body.Interval,
			Method:   strings.ToUpper(body.Method),
			State:    "unknown",
			CreatedAt: nowRFC3339(),
		}
		if m.Name == "" {
			m.Name = deriveMonitorName(body.URL)
		}
		list = append(list, m)
		_ = saveMonitors(list)
		monitorMu.Unlock()
		jsonReply(w, http.StatusCreated, map[string]interface{}{
			"ok":      true,
			"monitor": m,
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleMonitorAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/monitors/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "missing monitor id")
		return
	}
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	monitorMu.Lock()
	defer monitorMu.Unlock()

	list, err := loadMonitors()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		filtered := list[:0]
		hit := false
		for _, m := range list {
			if m.ID == id || m.Name == id {
				hit = true
				continue
			}
			filtered = append(filtered, m)
		}
		if !hit {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		_ = saveMonitors(filtered)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})

	case action == "pause" && r.Method == http.MethodPost:
		mutateMonitor(list, id, func(m *Monitor) { m.Paused = true })
		_ = saveMonitors(list)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})

	case action == "resume" && r.Method == http.MethodPost:
		mutateMonitor(list, id, func(m *Monitor) { m.Paused = false })
		_ = saveMonitors(list)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})

	case action == "check" && r.Method == http.MethodPost:
		var target *Monitor
		for _, m := range list {
			if m.ID == id || m.Name == id {
				target = m
				break
			}
		}
		if target == nil {
			jsonError(w, http.StatusNotFound, "monitor not found")
			return
		}
		check := runMonitorProbe(r.Context(), target)
		applyMonitorCheck(target, check)
		_ = saveMonitors(list)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"check": check,
		})

	default:
		jsonError(w, http.StatusNotFound, "unknown monitor action")
	}
}

func mutateMonitor(list []*Monitor, id string, fn func(*Monitor)) {
	for _, m := range list {
		if m.ID == id || m.Name == id {
			fn(m)
		}
	}
}

func randomID() string {
	return uuid.New().String()[:8]
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
