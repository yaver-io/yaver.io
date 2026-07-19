package main

// autorun_store_http.go — P2P-reachable read surface for the autorun store's
// deploy status (AUTORUN_STORE.md §10.1). The mobile Tasks screen and the web
// autorun view fetch this over the same relay/QUIC transport every other agent
// endpoint uses, so a phone can show "TestFlight is deploying build 451,
// uploading, 3/18 today" without shelling in. Read-only + auth-gated.
//
// The store is local-only (never Convex); this endpoint returns a SUMMARY the
// UI renders — no absolute workdirs beyond the holder line the operator needs.

import (
	"net/http"
	"time"
)

// deployStatusRow is the lean per-target summary the UI consumes.
type deployStatusRow struct {
	Target      string `json:"target"`
	Deploying   bool   `json:"deploying"`
	Holder      string `json:"holder,omitempty"`
	Build       string `json:"build,omitempty"`
	Stage       string `json:"stage,omitempty"`         // archiving|exporting|uploading|submitting
	StartedAt   int64  `json:"startedAt,omitempty"`     // unix seconds
	ElapsedSecs int64  `json:"elapsedSecs,omitempty"`   // for a live "deploying for 4m" label
	UploadsToday int   `json:"uploadsToday"`
	Quota       int    `json:"quota"`
}

// handleAutorunDeployStatus: GET /autoruns/deploy-status
// Returns the cross-target deploy board from the local store. Safe to call from
// any surface — it opens the store read-only and closes it immediately.
func (s *HTTPServer) handleAutorunDeployStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	st, err := openAutorunStore()
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer st.Close()

	now := time.Now().Unix()
	targets := []string{"testflight", "playstore", "convex", "cloudflare-web"}
	rows := make([]deployStatusRow, 0, len(targets))
	for _, t := range targets {
		row := deployStatusRow{Target: t}
		if cur, e := st.CurrentDeployLease(t); e == nil && cur != nil {
			row.Deploying = true
			row.Holder = cur.Holder
			row.Build = cur.Build
			row.Stage = cur.Stage
			row.StartedAt = cur.StartedAt
			row.ElapsedSecs = now - cur.StartedAt
		}
		if used, cap, e := st.DeployQuotaUsed(t); e == nil {
			row.UploadsToday = used
			row.Quota = cap
		}
		rows = append(rows, row)
	}
	jsonReply(w, http.StatusOK, map[string]any{"targets": rows, "at": now})
}
