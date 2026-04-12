package main

import (
	"encoding/json"
	"net/http"
)

// handleRelayExposeStart registers a subdomain with the QUIC relay.
// POST /expose/relay/start { "port": 3000, "subdomain": "myapp" }
func (s *HTTPServer) handleRelayExposeStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Port      int    `json:"port"`
		Subdomain string `json:"subdomain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.Port == 0 {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "port is required"})
		return
	}

	if s.relayExposeMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "relay expose not available — agent must be connected to a relay"})
		return
	}

	entry, err := s.relayExposeMgr.Register(req.Subdomain, req.Port)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	jsonReply(w, http.StatusOK, entry)
}

// handleRelayExposeStop stops a relay-based subdomain expose.
// POST /expose/relay/stop { "subdomain": "myapp" }  (empty subdomain = stop all)
func (s *HTTPServer) handleRelayExposeStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Subdomain string `json:"subdomain"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if s.relayExposeMgr != nil {
		if req.Subdomain == "" {
			s.relayExposeMgr.UnregisterAll()
		} else {
			s.relayExposeMgr.Unregister(req.Subdomain)
		}
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleRelayExposeList lists active relay-based expose entries.
// GET /expose/relay/list
func (s *HTTPServer) handleRelayExposeList(w http.ResponseWriter, r *http.Request) {
	if s.relayExposeMgr == nil {
		jsonReply(w, http.StatusOK, []*RelayExposeEntry{})
		return
	}

	jsonReply(w, http.StatusOK, s.relayExposeMgr.List())
}
