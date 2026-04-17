package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Public data API — the runtime surface a third-party RN/web app hits from
// the end-user's device. Distinct from /phone/projects/* which is owner-
// managed. Routes:
//
//   GET    /data/{slug}/{table}                 — list (paginated)
//   GET    /data/{slug}/{table}/{id}            — fetch one
//   POST   /data/{slug}/{table}                 — insert
//   PATCH  /data/{slug}/{table}/{id}            — partial update
//   DELETE /data/{slug}/{table}/{id}            — remove
//   OPTIONS ...                                  — CORS preflight
//
// Auth: Bearer pp_<slug>_<rand> in Authorization header OR ?api_key=pp_...
// as query param (browsers can't set headers on img/fetch-with-credentials
// in every case, so the query escape hatch is necessary for some web apps).
//
// CORS: permissive by default so a developer's localhost app Just Works.
// Per-project origin allowlist is a Codex follow-up (note in
// PHONE_EXPORT_PIPELINE.md).

// registerPhoneDataRoutes wires /data/* endpoints. Called from registerRoutes.
func (s *HTTPServer) registerPhoneDataRoutes(mux *http.ServeMux) {
	// /data/ prefix — single handler does the path parsing since
	// http.ServeMux doesn't do patterns natively in our Go version.
	mux.HandleFunc("/data/", s.phoneDataRouter)
	// Owner-only token management surface.
	mux.HandleFunc("/phone/projects/tokens", s.auth(s.handlePhoneTokens))
}

// phoneDataRouter is the top-level dispatch for /data/{slug}/{table}[/{id}].
// It's not wrapped in s.auth() because the auth model is different (per-
// project tokens, not the owner bearer). We handle auth inline.
func (s *HTTPServer) phoneDataRouter(w http.ResponseWriter, r *http.Request) {
	writePhoneDataCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Split the path. Accept either /data/<slug>/<table> or
	// /data/<slug>/<table>/<id> — trailing slashes tolerated.
	path := strings.TrimPrefix(r.URL.Path, "/data/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		jsonError(w, http.StatusNotFound, "expected /data/{slug}/{table}[/{id}]")
		return
	}
	slug := parts[0]
	table := parts[1]
	id := ""
	if len(parts) >= 3 {
		id = parts[2]
	}

	// Auth: either Bearer pp_... header or ?api_key= query.
	raw := extractPhoneProjectToken(r)
	if raw == "" {
		jsonError(w, http.StatusUnauthorized, "API key required — mint one in the Yaver app under the project's API Keys tab")
		return
	}
	_, boundSlug, err := ValidatePhoneProjectToken(raw)
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	// Hard scope: a token can ONLY access its own project's rows. No
	// cross-project reads via a guessed slug.
	if boundSlug != slug {
		jsonError(w, http.StatusForbidden, "this API key does not authorize that project")
		return
	}

	adapter, err := PhoneAdapter(slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		if id != "" {
			phoneDataGetOne(w, adapter, table, id)
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		cursor := r.URL.Query().Get("cursor")
		phoneDataList(w, adapter, table, cursor, limit)
	case http.MethodPost:
		phoneDataInsert(w, r, adapter, table)
	case http.MethodPatch, "PUT":
		if id == "" {
			jsonError(w, http.StatusBadRequest, "id required for update — use /data/{slug}/{table}/{id}")
			return
		}
		phoneDataUpdate(w, r, adapter, table, id)
	case http.MethodDelete:
		if id == "" {
			jsonError(w, http.StatusBadRequest, "id required for delete — use /data/{slug}/{table}/{id}")
			return
		}
		phoneDataDelete(w, adapter, table, id)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET / POST / PATCH / DELETE / OPTIONS")
	}
}

// extractPhoneProjectToken reads the token from (in order): Authorization
// Bearer, X-API-Key header, ?api_key= query. Only the pp_ prefix counts.
func extractPhoneProjectToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		t := strings.TrimPrefix(h, "Bearer ")
		if strings.HasPrefix(t, phoneTokenPrefix) {
			return t
		}
	}
	if h := r.Header.Get("X-API-Key"); strings.HasPrefix(h, phoneTokenPrefix) {
		return h
	}
	if q := r.URL.Query().Get("api_key"); strings.HasPrefix(q, phoneTokenPrefix) {
		return q
	}
	return ""
}

// writePhoneDataCORS stamps permissive CORS headers. Simple MVP: allow any
// origin with credentials NOT included (so tokens flow explicitly through
// Authorization/api_key). Per-project origin allowlist is a follow-up.
func writePhoneDataCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Set("Vary", "Origin")
}

// ---- CRUD handlers ----

func phoneDataList(w http.ResponseWriter, adapter BackendAdapter, table, cursor string, limit int) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	res, err := adapter.Browse(table, cursor, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"rows":       res.Rows,
		"nextCursor": res.NextCursor,
	})
}

func phoneDataGetOne(w http.ResponseWriter, adapter BackendAdapter, table, id string) {
	// No native get-one in BackendAdapter and the existing Query method
	// ignores args (just passes the SQL verbatim). Build a safely-quoted
	// literal from the id — `sqliteLiteral` doubles single quotes — so
	// untrusted ids can't inject. Works for SQL backends (all phone-
	// projects currently). If we add Convex later this needs to branch.
	q := fmt.Sprintf(`SELECT * FROM %q WHERE id = %s LIMIT 1`, table, sqliteLiteral(id))
	res, err := adapter.Query(q, nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, ok := res.([]map[string]interface{})
	if !ok || len(rows) == 0 {
		jsonError(w, http.StatusNotFound, "row not found")
		return
	}
	writeJSON(w, http.StatusOK, rows[0])
}

func phoneDataInsert(w http.ResponseWriter, r *http.Request, adapter BackendAdapter, table string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB row cap
	if err != nil {
		jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(body, &doc); err != nil || doc == nil {
		jsonError(w, http.StatusBadRequest, "expected JSON object")
		return
	}
	id, err := adapter.Insert(table, doc)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id})
}

func phoneDataUpdate(w http.ResponseWriter, r *http.Request, adapter BackendAdapter, table, id string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		jsonError(w, http.StatusBadRequest, "expected JSON object")
		return
	}
	if err := adapter.Update(table, id, fields); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func phoneDataDelete(w http.ResponseWriter, adapter BackendAdapter, table, id string) {
	if err := adapter.Delete(table, id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// keep errors import alive even if only used transitively
var _ = errors.New
