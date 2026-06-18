package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (s *HTTPServer) wifiManager() *WiFiHotspotManager {
	if s.wifiHotspotMgr != nil {
		return s.wifiHotspotMgr
	}
	workDir := "."
	if s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" {
		workDir = s.taskMgr.workDir
	}
	s.wifiHotspotMgr = NewWiFiHotspotManager(workDir)
	return s.wifiHotspotMgr
}

func (s *HTTPServer) wifiMeshManager() *WiFiMeshManager {
	if s.wifiMeshMgr != nil {
		return s.wifiMeshMgr
	}
	workDir := "."
	if s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" {
		workDir = s.taskMgr.workDir
	}
	s.wifiMeshMgr = NewWiFiMeshManager(workDir)
	return s.wifiMeshMgr
}

func (s *HTTPServer) handleConsoleWiFiCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	caps, err := s.wifiManager().DetectHardwareCapabilities()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "capabilities": caps})
}

func (s *HTTPServer) handleConsoleWiFiStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	status, err := s.wifiManager().GetStatus()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}

func (s *HTTPServer) handleConsoleWiFiStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var cfg WiFiHotspotConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.wifiManager().StartHotspot(&cfg); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	status, _ := s.wifiManager().GetStatus()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}

func (s *HTTPServer) handleConsoleWiFiStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if err := s.wifiManager().StopHotspot(); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	status, _ := s.wifiManager().GetStatus()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}

func (s *HTTPServer) handleConsoleWiFiClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	clients := s.wifiManager().ListClients()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "clients": clients, "count": len(clients)})
}

func (s *HTTPServer) handleConsoleWiFiKickClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.wifiManager().KickClient(req.MAC); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "kicked": true, "mac": req.MAC})
}

func (s *HTTPServer) handleConsoleWiFiBanClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		MAC           string `json:"mac"`
		DurationHours int    `json:"durationHours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.wifiManager().BanClient(req.MAC, req.DurationHours); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "banned": true, "mac": req.MAC, "durationHours": req.DurationHours})
}

func (s *HTTPServer) handleConsoleWiFiUnbanClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.wifiManager().UnbanClient(req.MAC); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "unbanned": true, "mac": req.MAC})
}

func (s *HTTPServer) handleConsoleWiFiBannedClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	banned := s.wifiManager().GetBannedClients()
	out := make([]map[string]string, 0, len(banned))
	for mac, expiry := range banned {
		entry := map[string]string{"mac": mac, "expiry": "permanent"}
		if !expiry.IsZero() {
			entry["expiry"] = expiry.Format(time.RFC3339)
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "bannedClients": out, "count": len(out)})
}

func (s *HTTPServer) handleConsoleWiFiAPSTAConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.wifiManager().GetAPSTAConfig()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "config": nil, "error": "no config found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "config": cfg})
	case http.MethodPost:
		var cfg WiFiHotspotConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
			return
		}
		if err := s.wifiManager().SetAPSTAConfig(&cfg); err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "saved": true, "config": normalizeWiFiHotspotConfig(&cfg)})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleConsoleWiFiMeshCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	caps, err := s.wifiMeshManager().DetectCapabilities()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "capabilities": caps})
}

func (s *HTTPServer) handleConsoleWiFiMeshStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	status, err := s.wifiMeshManager().Status()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}

func (s *HTTPServer) handleConsoleWiFiMeshStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var cfg WiFiMeshConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.wifiMeshManager().Start(&cfg); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	status, _ := s.wifiMeshManager().Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}

func (s *HTTPServer) handleConsoleWiFiMeshStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if err := s.wifiMeshManager().Stop(); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	status, _ := s.wifiMeshManager().Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}
