package main

import (
	"encoding/json"
	"net/http"
)

type cloudEmuBody struct {
	Provider string   `json:"provider"`
	Services []string `json:"services"`
}

func (s *HTTPServer) handleCloudEmuStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b cloudEmuBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpCloudEmuStart(s.dirParam(r), b.Provider, b.Services))
}

func (s *HTTPServer) handleCloudEmuStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b cloudEmuBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpCloudEmuStop(s.dirParam(r), b.Provider, b.Services))
}

func (s *HTTPServer) handleCloudEmuStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpCloudEmuStatus(s.dirParam(r)))
}

func (s *HTTPServer) handleCloudEmuConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpCloudEmuConfig(r.URL.Query().Get("provider")))
}
