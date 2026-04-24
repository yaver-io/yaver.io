package main

import (
	"encoding/json"
	"net/http"
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

