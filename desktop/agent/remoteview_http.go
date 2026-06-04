package main

// remoteview_http.go — first-class HTTP management surface for remote-view
// software (RustDesk/AnyDesk/VNC), so any UI (Talos desktop, a Yaver panel) can
// list providers, connect, disconnect, and read status without going through the
// ops verb. Thin over the RemoteView registry (remoteview.go); gated by --ghost.

import (
	"encoding/json"
	"net/http"
)

func (s *HTTPServer) remoteViewEnabled(w http.ResponseWriter) bool {
	if !s.ghostEnabled {
		jsonError(w, http.StatusForbidden, "GUI ghost is disabled; start with `yaver serve --ghost`")
		return false
	}
	return true
}

func (s *HTTPServer) handleRemoteViewProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if !s.remoteViewEnabled(w) {
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "providers": listRemoteViews()})
}

func (s *HTTPServer) handleRemoteViewStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if !s.remoteViewEnabled(w) {
		return
	}
	rv, ok := getRemoteView(r.URL.Query().Get("provider"))
	if !ok {
		jsonError(w, http.StatusBadRequest, "unknown remote-view provider")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "status": rv.Status()})
}

func (s *HTTPServer) handleRemoteViewConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if !s.remoteViewEnabled(w) {
		return
	}
	var body struct {
		Provider string            `json:"provider"`
		PeerID   string            `json:"peerId"`
		Password string            `json:"password"`
		Opts     map[string]string `json:"opts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.PeerID == "" {
		jsonError(w, http.StatusBadRequest, "peerId is required")
		return
	}
	rv, ok := getRemoteView(body.Provider)
	if !ok {
		jsonError(w, http.StatusBadRequest, "unknown remote-view provider")
		return
	}
	if err := rv.Connect(body.PeerID, body.Password, body.Opts); err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "status": rv.Status()})
}

func (s *HTTPServer) handleRemoteViewDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if !s.remoteViewEnabled(w) {
		return
	}
	var body struct {
		Provider string `json:"provider"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	rv, ok := getRemoteView(body.Provider)
	if !ok {
		jsonError(w, http.StatusBadRequest, "unknown remote-view provider")
		return
	}
	_ = rv.Disconnect()
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "status": rv.Status()})
}
