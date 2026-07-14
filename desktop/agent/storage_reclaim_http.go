package main

// storage_reclaim_http.go — the HTTP face of storage reclaim, used by the
// phone's Storage panel and the web device-details fold.
//
// POST /storage/reclaim carries the same confirm gate as the ops verb: no
// confirm means dry run. The phone always dry-runs first to render the
// "you'll free 22.4 GB" line the user is approving, then re-posts with
// confirm:true. Two round trips, but the second one is the one the user
// actually consented to — and consent to a number you never showed them
// isn't consent.

import (
	"encoding/json"
	"net/http"
)

// handleStorageScan serves the reclaim plan.
//
//	GET /storage/scan?refresh=1
func (s *HTTPServer) handleStorageScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"scan": scanStorage(refresh),
	})
}

// handleStorageReclaim frees approved targets.
//
//	POST /storage/reclaim {"ids": ["ab12…"], "confirm": true}
//
// Without confirm this is a dry run and returns the plan — never a deletion.
func (s *HTTPServer) handleStorageReclaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		IDs     []string `json:"ids"`
		Confirm bool     `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(req.IDs) == 0 {
		jsonError(w, http.StatusBadRequest, "ids is required; GET /storage/scan first")
		return
	}

	res := performStorageReclaim(req.IDs, !req.Confirm)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"result": res,
	})
}
