package main

import (
	"encoding/json"
	"net/http"
)

func (s *HTTPServer) handleAccountList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpAccountList())
}

type accountConnectBody struct {
	Provider string            `json:"provider"`
	Label    string            `json:"label"`
	Fields   map[string]string `json:"fields"`
}

func (s *HTTPServer) handleAccountConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b accountConnectBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := globalAccountsManager.Connect(AccountProvider(b.Provider), b.Label, b.Fields); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleAccountDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ Provider string `json:"provider"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpAccountDisconnect(b.Provider))
}

func (s *HTTPServer) handleAccountStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpAccountStatus(r.URL.Query().Get("provider")))
}
