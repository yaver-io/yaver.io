package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// handleVaultList returns vault entry summaries (never values).
//
// Query params:
//   - project: "" (default) → global entries only
//                "*" → every project
//                "<name>" → that project's entries
func (s *HTTPServer) handleVaultList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}

	project := r.URL.Query().Get("project")
	entries := s.vaultStore.List(project)

	// Include distinct projects alongside for UI convenience.
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"entries":  entries,
		"projects": s.vaultStore.ListProjects(),
	})
}

// handleVaultGet returns a single vault entry including its value.
//
// Query params: name (required), project (optional, default "").
func (s *HTTPServer) handleVaultGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}

	name := r.URL.Query().Get("name")
	project := r.URL.Query().Get("project")
	if name == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing 'name' parameter"})
		return
	}

	entry, err := s.vaultStore.Get(project, name)
	if err != nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, entry)
}

// handleVaultSet creates or updates a vault entry.
//
// Body: full VaultEntry JSON (name + value required; project optional).
func (s *HTTPServer) handleVaultSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}

	var entry VaultEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if entry.Name == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing 'name' field"})
		return
	}
	if entry.Value == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing 'value' field"})
		return
	}
	if entry.Category == "" {
		entry.Category = "custom"
	}

	if err := s.vaultStore.Set(entry); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]string{
		"ok":      "true",
		"name":    entry.Name,
		"project": entry.Project,
	})
}

// handleVaultDelete removes a vault entry (soft delete / tombstone).
//
// Query params: name (required), project (optional, default "").
func (s *HTTPServer) handleVaultDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}

	name := r.URL.Query().Get("name")
	project := r.URL.Query().Get("project")
	if name == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing 'name' parameter"})
		return
	}

	if err := s.vaultStore.Delete(project, name); err != nil {
		jsonReply(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleVaultEnv returns a shell-sourceable env script (export KEY=VAL lines)
// merging global entries with the requested project's entries. Project-scoped
// entries win over globals on name collision. Intended use:
//
//	eval "$(curl -sH "Authorization: Bearer $TOKEN" \
//	  http://127.0.0.1:18080/vault/env?project=yaver)"
//
// Query params:
//   - project: required
//   - globals: "1" (default) to include globals, "0" to exclude
func (s *HTTPServer) handleVaultEnv(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}
	project := r.URL.Query().Get("project")
	if project == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing 'project' parameter"})
		return
	}
	includeGlobals := r.URL.Query().Get("globals") != "0"

	script := s.vaultStore.EnvExport(project, includeGlobals)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}

// --- Sync endpoints ---

// handleVaultDigest returns the per-entry (project, name, updatedAt, deleted)
// records of the local vault. No secret values leave this endpoint — it's
// the "anti-entropy handshake" a peer needs before pulling.
func (s *HTTPServer) handleVaultDigest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"entries":  s.vaultStore.Digest(),
		"deviceId": s.deviceID,
	})
}

// handleVaultSync accepts the peer's digest and returns the entries we
// have that are newer than the peer's (or absent from the peer). Body:
//
//	{"digest": [{"name":"X","project":"yaver","updated_at":123}, ...]}
//
// Response:
//
//	{"entries": [VaultEntry, ...], "deviceId": "..."}
//
// The secret values are in the response body — this endpoint is
// owner-auth + rate-limited + blocked for guests / support / SDK tokens.
func (s *HTTPServer) handleVaultSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}
	var body struct {
		Digest []VaultDigestEntry `json:"digest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	newer := s.vaultStore.EntriesNewerThan(body.Digest)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"entries":  newer,
		"deviceId": s.deviceID,
	})
}

// handleVaultPush accepts inbound sync revisions from a peer and applies
// them via Upsert (last-writer-wins by UpdatedAt). Body:
//
//	{"entries": [VaultEntry, ...]}
//
// Response: {"accepted": N, "rejected": M, "errors": [...]}.
func (s *HTTPServer) handleVaultPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}
	var body struct {
		Entries []VaultEntry `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	accepted, rejected := 0, 0
	var errs []string
	for _, e := range body.Entries {
		ok, err := s.vaultStore.Upsert(e)
		if err != nil {
			rejected++
			label := e.Name
			if e.Project != "" {
				label = e.Project + "/" + e.Name
			}
			errs = append(errs, label+": "+err.Error())
			continue
		}
		if ok {
			accepted++
		} else {
			rejected++
		}
	}
	resp := map[string]interface{}{
		"accepted": accepted,
		"rejected": rejected,
	}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	jsonReply(w, http.StatusOK, resp)
}

// handleVaultPeerSync triggers an OUTBOUND vault sync from this agent
// against the user's other devices — mirrors `yaver vault sync` CLI
// over HTTP so mobile + web "Try syncing from peer" buttons can run
// it without shelling out to a terminal. Body (all optional):
//
//	{ "from": "<deviceId>" }   // sync only against this peer
//
// Response: structured per-peer report so the UI can show "pulled 4
// new secrets from Mobiles-Mac-mini.local" etc.
//
//	{
//	  "peers": ["..."],
//	  "results": [{"peer":"229aeb03","pulled":4,"pushed":0,"rejected":0,
//	                "duration_ms":1240,"error":""}, ...],
//	  "totals": {"pulled":4,"pushed":0,"rejected":0,"superseded_local":0}
//	}
func (s *HTTPServer) handleVaultPeerSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if s.vaultStore == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}
	var body struct {
		From string `json:"from"`
	}
	// Empty body is fine — sync against every known peer.
	_ = json.NewDecoder(r.Body).Decode(&body)

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		jsonReply(w, http.StatusUnauthorized, map[string]string{"error": "agent not authenticated"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	peers := []string{}
	if strings.TrimSpace(body.From) != "" {
		peers = append(peers, strings.TrimSpace(body.From))
	} else {
		convex := cfg.ConvexSiteURL
		if convex == "" {
			convex = defaultConvexSiteURL
		}
		devices, err := primaryListDevices(ctx, cfg.AuthToken, convex)
		if err != nil {
			jsonReply(w, http.StatusBadGateway, map[string]string{"error": "list devices: " + err.Error()})
			return
		}
		for _, d := range devices {
			if d.DeviceID != "" && d.DeviceID != cfg.DeviceID {
				peers = append(peers, d.DeviceID)
			}
		}
	}
	if len(peers) == 0 {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"peers":   []string{},
			"results": []map[string]interface{}{},
			"totals":  map[string]int{"pulled": 0, "pushed": 0, "rejected": 0, "superseded_local": 0},
			"note":    "no peer devices found — sync needs at least two devices on the same account",
		})
		return
	}

	type peerResult struct {
		Peer            string `json:"peer"`
		Pulled          int    `json:"pulled"`
		SupersededLocal int    `json:"superseded_local"`
		Pushed          int    `json:"pushed"`
		Rejected        int    `json:"rejected"`
		DurationMs      int64  `json:"duration_ms"`
		Error           string `json:"error,omitempty"`
	}
	results := make([]peerResult, 0, len(peers))
	totals := struct {
		Pulled, Pushed, Rejected, SupersededLocal int
	}{}

	for _, p := range peers {
		rpt, err := vaultSyncWithPeer(ctx, s.vaultStore, p)
		pr := peerResult{
			Peer:            p,
			Pulled:          rpt.Pulled,
			SupersededLocal: rpt.SupersededLocal,
			Pushed:          rpt.Pushed,
			Rejected:        rpt.Rejected,
			DurationMs:      rpt.DurationMs,
		}
		if err != nil {
			pr.Error = err.Error()
		}
		results = append(results, pr)
		totals.Pulled += rpt.Pulled
		totals.Pushed += rpt.Pushed
		totals.Rejected += rpt.Rejected
		totals.SupersededLocal += rpt.SupersededLocal
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"peers":   peers,
		"results": results,
		"totals": map[string]int{
			"pulled":           totals.Pulled,
			"pushed":           totals.Pushed,
			"rejected":         totals.Rejected,
			"superseded_local": totals.SupersededLocal,
		},
	})
}

