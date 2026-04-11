package main

// errors_http.go — HTTP surface for the cross-device errors
// ledger. Backed by errors_store.go's GlobalErrorStore so the
// mobile "Errors" tab and the MCP tools see the same data.
//
// Routes (registered in httpserver.go):
//
//   GET  /errors                 — list open errors (or all with
//                                  ?include_resolved=1)
//   GET  /errors/stats           — dashboard header counters
//   GET  /errors/detail?fp=<fp>  — single record with recent
//                                  samples
//   POST /errors/resolve         — {"fingerprint": "x",
//                                  "note": "optional"}
//   POST /errors/reopen          — {"fingerprint": "x"}

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *HTTPServer) handleErrors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	store := GlobalErrorStore()
	if store == nil {
		jsonError(w, http.StatusInternalServerError, "error store unavailable")
		return
	}
	includeResolved := r.URL.Query().Get("include_resolved") == "1"
	records := store.List(includeResolved)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"errors": records,
		"stats":  store.Stats(),
	})
}

func (s *HTTPServer) handleErrorsStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	store := GlobalErrorStore()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"stats": store.Stats(),
	})
}

func (s *HTTPServer) handleErrorsDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	fp := r.URL.Query().Get("fp")
	if fp == "" {
		jsonError(w, http.StatusBadRequest, "fp required")
		return
	}
	rec := GlobalErrorStore().Get(fp)
	if rec == nil {
		jsonError(w, http.StatusNotFound, "fingerprint not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"error": rec,
	})
}

func (s *HTTPServer) handleErrorsResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Fingerprint string `json:"fingerprint"`
		Note        string `json:"note,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Fingerprint) == "" {
		jsonError(w, http.StatusBadRequest, "fingerprint required")
		return
	}
	if !GlobalErrorStore().MarkResolved(body.Fingerprint, body.Note) {
		jsonError(w, http.StatusNotFound, "fingerprint not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleErrorsReopen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !GlobalErrorStore().Reopen(body.Fingerprint) {
		jsonError(w, http.StatusNotFound, "fingerprint not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}
