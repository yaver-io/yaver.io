package main

import (
	"encoding/json"
	"net/http"
	"strings"
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
