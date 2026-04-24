package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *HTTPServer) handleIncidents(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"incidents": GlobalIncidentStore().List(IncidentFilter{
				Category:        q.Get("category"),
				Severity:        q.Get("severity"),
				Code:            q.Get("code"),
				DeviceID:        q.Get("device"),
				ProjectPath:     q.Get("projectPath"),
				IncludeResolved: q.Get("include_resolved") == "1",
				Limit:           limit,
			}),
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
	}
}

func (s *HTTPServer) handleIncidentsSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"summary": GlobalIncidentStore().Summary(),
	})
}

func (s *HTTPServer) handleIncidentsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, snapshot, cancel := GlobalIncidentStore().Subscribe()
	defer cancel()

	for _, ev := range snapshot {
		if !incidentMatchesStreamFilter(ev, r) {
			continue
		}
		if data, err := json.Marshal(ev); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !incidentMatchesStreamFilter(ev, r) {
				continue
			}
			if data, err := json.Marshal(ev); err == nil {
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}

func (s *HTTPServer) handleIncidentByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/incidents/")
	if strings.TrimSpace(rest) == "" {
		jsonError(w, http.StatusBadRequest, "incident id required")
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch action {
	case "":
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		ev := GlobalIncidentStore().Get(id)
		if ev == nil {
			jsonError(w, http.StatusNotFound, "incident not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "incident": ev})
	case "resolve":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		_ = decodeJSONBody(r, &body)
		if !GlobalIncidentStore().Resolve(id, body.Note) {
			jsonError(w, http.StatusNotFound, "incident not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "reopen":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		if !GlobalIncidentStore().Reopen(id) {
			jsonError(w, http.StatusNotFound, "incident not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusNotFound, "unknown incident action")
	}
}

func incidentMatchesStreamFilter(ev IncidentEvent, r *http.Request) bool {
	q := r.URL.Query()
	if q.Get("category") != "" && ev.Category != q.Get("category") {
		return false
	}
	if q.Get("severity") != "" && string(ev.Severity) != q.Get("severity") {
		return false
	}
	if q.Get("device") != "" && ev.DeviceID != q.Get("device") {
		return false
	}
	if q.Get("projectPath") != "" && ev.ProjectPath != q.Get("projectPath") {
		return false
	}
	if q.Get("include_resolved") != "1" && ev.Resolved {
		return false
	}
	return true
}
