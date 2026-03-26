package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleHealthMon handles GET /healthmon (list) and POST /healthmon (add).
func (s *HTTPServer) handleHealthMon(w http.ResponseWriter, r *http.Request) {
	if s.healthMon == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "health monitor not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		statuses := s.healthMon.ListStatuses()
		jsonReply(w, http.StatusOK, statuses)

	case http.MethodPost:
		var req struct {
			URL             string `json:"url"`
			Label           string `json:"label"`
			Method          string `json:"method"`
			Interval        int    `json:"interval"`
			TimeoutMs       int    `json:"timeoutMs"`
			ExpectStatus    int    `json:"expectStatus"`
			WarnThresholdMs int    `json:"warnThresholdMs"`
			NotifyOnFailure bool   `json:"notifyOnFailure"`
			NotifyOnWarning bool   `json:"notifyOnWarning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if req.URL == "" {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing url"})
			return
		}

		target := s.healthMon.AddTarget(req.URL, req.Label, req.Method, req.Interval, req.TimeoutMs, req.ExpectStatus)
		target.WarnThresholdMs = req.WarnThresholdMs
		target.NotifyOnFailure = req.NotifyOnFailure
		target.NotifyOnWarning = req.NotifyOnWarning
		s.healthMon.persist()
		jsonReply(w, http.StatusOK, target)

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleHealthMonByID handles GET /healthmon/{id}, DELETE /healthmon/{id}, POST /healthmon/{id}/check.
func (s *HTTPServer) handleHealthMonByID(w http.ResponseWriter, r *http.Request) {
	if s.healthMon == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "health monitor not available"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/healthmon/")
	parts := strings.SplitN(path, "/", 2)
	targetID := parts[0]

	if targetID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing target ID"})
		return
	}

	// Sub-route: /healthmon/{id}/check
	if len(parts) > 1 && parts[1] == "check" {
		if r.Method != http.MethodPost {
			jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
			return
		}
		status, ok := s.healthMon.ForceCheck(targetID)
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "target not found"})
			return
		}
		jsonReply(w, http.StatusOK, status)
		return
	}

	switch r.Method {
	case http.MethodGet:
		status, ok := s.healthMon.GetStatus(targetID)
		if !ok {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "target not found"})
			return
		}
		jsonReply(w, http.StatusOK, status)

	case http.MethodDelete:
		if !s.healthMon.RemoveTarget(targetID) {
			jsonReply(w, http.StatusNotFound, map[string]string{"error": "target not found"})
			return
		}
		jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})

	default:
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
