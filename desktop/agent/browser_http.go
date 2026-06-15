package main

// browser_http.go — HTTP endpoints and SSE streaming for browser automation.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleBrowserSessions handles GET (list) and POST (open) on /browser/sessions.
func (s *HTTPServer) handleBrowserSessions(w http.ResponseWriter, r *http.Request) {
	if s.browserMgr == nil {
		http.Error(w, `{"error":"browser automation not available"}`, http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		sessions := s.browserMgr.ListSessions()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"sessions": sessions,
		})

	case http.MethodPost:
		var req struct {
			SessionID string `json:"session_id"`
			Headful   bool   `json:"headful"`
			ProxyURL  string `json:"proxy_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		if req.SessionID == "" {
			req.SessionID = fmt.Sprintf("browser-%d", time.Now().UnixMilli()%100000)
		}
		if err := s.browserMgr.OpenSessionWithProxy(req.SessionID, req.Headful, req.ProxyURL); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"session_id": req.SessionID,
			"headful":    req.Headful,
			"status":     "open",
			"proxy_url":  redactProxyCreds(req.ProxyURL),
		})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleBrowserSessionByID handles GET (screenshot) and DELETE (close) on /browser/sessions/{id}.
func (s *HTTPServer) handleBrowserSessionByID(w http.ResponseWriter, r *http.Request) {
	if s.browserMgr == nil {
		http.Error(w, `{"error":"browser automation not available"}`, http.StatusServiceUnavailable)
		return
	}

	// Extract session ID from path: /browser/sessions/{id}[/screenshot]
	path := strings.TrimPrefix(r.URL.Path, "/browser/sessions/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	if id == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}

	switch {
	case r.Method == http.MethodDelete:
		if err := s.browserMgr.CloseSession(id); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "closed"})

	case r.Method == http.MethodGet && sub == "screenshot":
		result, err := s.browserMgr.Screenshot(id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case r.Method == http.MethodGet:
		// Return session info.
		sessions := s.browserMgr.ListSessions()
		for _, sess := range sessions {
			if sess.ID == id {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(sess)
				return
			}
		}
		http.Error(w, fmt.Sprintf(`{"error":"session %q not found"}`, id), http.StatusNotFound)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// handleBrowserEvents streams SSE events for all browser sessions.
func (s *HTTPServer) handleBrowserEvents(w http.ResponseWriter, r *http.Request) {
	if s.browserMgr == nil {
		http.Error(w, `{"error":"browser automation not available"}`, http.StatusServiceUnavailable)
		return
	}

	// Optional session filter from path: /browser/events/{id}
	filterID := ""
	path := strings.TrimPrefix(r.URL.Path, "/browser/events")
	if len(path) > 1 {
		filterID = strings.TrimPrefix(path, "/")
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.browserMgr.eventCh:
			if !ok {
				return
			}
			if filterID != "" && ev.SessionID != filterID {
				continue
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
