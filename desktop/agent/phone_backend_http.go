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

// registerPhoneRoutes wires the /phone/projects/* endpoints. Called from
// HTTPServer.registerRoutes.
func (s *HTTPServer) registerPhoneRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/phone/projects/list", s.auth(s.handlePhoneList))
	mux.HandleFunc("/phone/projects/templates", s.auth(s.handlePhoneTemplates))
	mux.HandleFunc("/phone/projects/create", s.auth(s.handlePhoneCreate))
	mux.HandleFunc("/phone/projects/get", s.auth(s.handlePhoneGet))
	mux.HandleFunc("/phone/projects/delete", s.auth(s.handlePhoneDelete))
	mux.HandleFunc("/phone/projects/schema", s.auth(s.handlePhoneSchema))
	mux.HandleFunc("/phone/projects/auth", s.auth(s.handlePhoneAuth))
	mux.HandleFunc("/phone/projects/seed", s.auth(s.handlePhoneSeed))
	mux.HandleFunc("/phone/projects/tables", s.auth(s.handlePhoneTables))
	mux.HandleFunc("/phone/projects/browse", s.auth(s.handlePhoneBrowse))
	mux.HandleFunc("/phone/projects/insert", s.auth(s.handlePhoneInsert))
	mux.HandleFunc("/phone/projects/update", s.auth(s.handlePhoneUpdate))
	mux.HandleFunc("/phone/projects/delete-row", s.auth(s.handlePhoneDeleteRow))
	mux.HandleFunc("/phone/projects/query", s.auth(s.handlePhoneQuery))
	mux.HandleFunc("/phone/projects/export", s.auth(s.handlePhoneExport))
	mux.HandleFunc("/phone/projects/promote", s.auth(s.handlePhonePromote))
	mux.HandleFunc("/phone/projects/receive", s.auth(s.handlePhoneReceive))
	mux.HandleFunc("/phone/projects/oauth", s.auth(s.handlePhoneOAuth))
	mux.HandleFunc("/phone/projects/cost-hint", s.auth(s.handlePhoneCostHint))
}

func (s *HTTPServer) handlePhoneList(w http.ResponseWriter, r *http.Request) {
	projs, err := ListPhoneProjects()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": projs})
}

func (s *HTTPServer) handlePhoneTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": ListPhoneTemplates()})
}

func (s *HTTPServer) handlePhoneCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var spec PhoneCreateSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	p, err := CreatePhoneProject(spec)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrPhoneProjectExists) {
			status = http.StatusConflict
		}
		jsonError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *HTTPServer) handlePhoneGet(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		jsonError(w, http.StatusBadRequest, "slug required")
		return
	}
	p, err := LoadPhoneProject(slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *HTTPServer) handlePhoneDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug string `json:"slug"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Slug == "" {
		body.Slug = r.URL.Query().Get("slug")
	}
	if body.Slug == "" {
		jsonError(w, http.StatusBadRequest, "slug required")
		return
	}
	if err := DeletePhoneProject(body.Slug); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handlePhoneSchema(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		slug := r.URL.Query().Get("slug")
		if slug == "" {
			jsonError(w, http.StatusBadRequest, "slug required")
			return
		}
		p, err := LoadPhoneProject(slug)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p.Schema)
	case http.MethodPost:
		var body struct {
			Slug   string       `json:"slug"`
			Schema *PhoneSchema `json:"schema"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if body.Slug == "" || body.Schema == nil {
			jsonError(w, http.StatusBadRequest, "slug and schema required")
			return
		}
		if err := ApplyPhoneSchema(body.Slug, body.Schema); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		p, _ := LoadPhoneProject(body.Slug)
		writeJSON(w, http.StatusOK, p)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
	}
}

func (s *HTTPServer) handlePhoneAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug string     `json:"slug"`
		Auth *PhoneAuth `json:"auth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Slug == "" || body.Auth == nil {
		jsonError(w, http.StatusBadRequest, "slug and auth required")
		return
	}
	if err := ApplyPhoneAuth(body.Slug, body.Auth); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handlePhoneSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug string    `json:"slug"`
		Seed PhoneSeed `json:"seed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Slug == "" || body.Seed == nil {
		jsonError(w, http.StatusBadRequest, "slug and seed required")
		return
	}
	if err := ApplyPhoneSeed(body.Slug, body.Seed); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handlePhoneTables(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		jsonError(w, http.StatusBadRequest, "slug required")
		return
	}
	adapter, err := PhoneAdapter(slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	tables, err := adapter.ListTables()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tables": tables})
}

func (s *HTTPServer) handlePhoneBrowse(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	table := r.URL.Query().Get("table")
	if slug == "" || table == "" {
		jsonError(w, http.StatusBadRequest, "slug and table required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	adapter, err := PhoneAdapter(slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	res, err := adapter.Browse(table, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handlePhoneInsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug  string                 `json:"slug"`
		Table string                 `json:"table"`
		Doc   map[string]interface{} `json:"doc"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Slug == "" || body.Table == "" || body.Doc == nil {
		jsonError(w, http.StatusBadRequest, "slug, table, doc required")
		return
	}
	adapter, err := PhoneAdapter(body.Slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	id, err := adapter.Insert(body.Table, body.Doc)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id})
}

func (s *HTTPServer) handlePhoneUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug   string                 `json:"slug"`
		Table  string                 `json:"table"`
		ID     string                 `json:"id"`
		Fields map[string]interface{} `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Slug == "" || body.Table == "" || body.ID == "" {
		jsonError(w, http.StatusBadRequest, "slug, table, id required")
		return
	}
	adapter, err := PhoneAdapter(body.Slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := adapter.Update(body.Table, body.ID, body.Fields); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handlePhoneDeleteRow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug  string `json:"slug"`
		Table string `json:"table"`
		ID    string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Slug == "" || body.Table == "" || body.ID == "" {
		jsonError(w, http.StatusBadRequest, "slug, table, id required")
		return
	}
	adapter, err := PhoneAdapter(body.Slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := adapter.Delete(body.Table, body.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handlePhoneQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug  string                 `json:"slug"`
		Query string                 `json:"query"`
		Args  map[string]interface{} `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Slug == "" || body.Query == "" {
		jsonError(w, http.StatusBadRequest, "slug and query required")
		return
	}
	adapter, err := PhoneAdapter(body.Slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	result, err := adapter.Query(body.Query, body.Args)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"result": result})
}

func (s *HTTPServer) handlePhoneExport(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		jsonError(w, http.StatusBadRequest, "slug required")
		return
	}
	includeData := r.URL.Query().Get("includeData") == "true" || r.URL.Query().Get("includeData") == "1"
	data, err := ExportPhoneProjectWithOptions(slug, PhoneExportOptions{IncludeData: includeData})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	safe := strings.ReplaceAll(Slugify(slug), "/", "")
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tgz"`, safe))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handlePhoneReceive accepts a tgz bundle exported from another agent (mobile
// app's own phone backend, or another yaver serve instance) and materialises
// it here. Used by the mobile app when the developer taps "Deploy to my Mac"
// or "Deploy to Yaver Cloud" — same endpoint, different target agent.
//
// Accepts two body shapes:
//  1. multipart/form-data with a "bundle" file part and optional JSON fields
//     slug, onConflict, skipSeed.
//  2. application/gzip raw body (when the client can't build multipart easily
//     — e.g. a minimal mobile client). Options come from query string.
func (s *HTTPServer) handlePhoneReceive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	const maxBundle = 128 << 20 // 128 MB — phone-originated bundles are tiny
	r.Body = http.MaxBytesReader(w, r.Body, maxBundle)

	var (
		bundle     []byte
		slug       string
		onConflict string
		skipSeed   bool
		err        error
	)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err = r.ParseMultipartForm(32 << 20); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid multipart: "+err.Error())
			return
		}
		slug = strings.TrimSpace(r.FormValue("slug"))
		onConflict = strings.TrimSpace(r.FormValue("onConflict"))
		skipSeed = r.FormValue("skipSeed") == "true" || r.FormValue("skipSeed") == "1"
		file, _, ferr := r.FormFile("bundle")
		if ferr != nil {
			jsonError(w, http.StatusBadRequest, "missing bundle file: "+ferr.Error())
			return
		}
		defer file.Close()
		bundle, err = io.ReadAll(file)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "read bundle: "+err.Error())
			return
		}
	} else {
		slug = strings.TrimSpace(r.URL.Query().Get("slug"))
		onConflict = strings.TrimSpace(r.URL.Query().Get("onConflict"))
		skipSeed = r.URL.Query().Get("skipSeed") == "true" || r.URL.Query().Get("skipSeed") == "1"
		bundle, err = io.ReadAll(r.Body)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
	}

	if err := EnforcePhoneDeployBudget(int64(len(bundle)), 0); err != nil {
		// 413 Payload Too Large is the right status, but a descriptive body
		// matters more — this is what a cost-worried user sees when they
		// tried to push a 100 MB SQLite.
		jsonError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}

	if len(bundle) == 0 {
		jsonError(w, http.StatusBadRequest, "empty bundle")
		return
	}

	proj, err := ImportPhoneProject(bundle, PhoneImportOptions{
		SlugOverride: slug,
		OnConflict:   onConflict,
		SkipSeed:     skipSeed,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrPhoneProjectExists) {
			status = http.StatusConflict
		}
		jsonError(w, status, err.Error())
		return
	}

	// Return enough info for the mobile client to point at this project
	// immediately. The URL is relative; the client knows its own agent base.
	base := fmt.Sprintf("/phone/projects/get?slug=%s", proj.Slug)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"project":   proj,
		"slug":      proj.Slug,
		"localUrl":  base,
		"browseUrl": fmt.Sprintf("/phone/projects/browse?slug=%s", proj.Slug),
	})
}

// handlePhonePromote plans (and optionally runs) a switch-engine migration
// from the phone project's SQLite to any SwitchTarget (Convex/Supabase/etc.).
// Reuses the existing engine verbatim — same snapshot, same 7-day rollback.
func (s *HTTPServer) handlePhonePromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Slug   string `json:"slug"`
		Target string `json:"target"`
		Run    bool   `json:"run"`
		DryRun bool   `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Slug == "" || body.Target == "" {
		jsonError(w, http.StatusBadRequest, "slug and target required")
		return
	}
	dir, err := PhoneProjectDir(body.Slug)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	engine := NewSwitchEngine()
	state, err := engine.Plan(dir, body.Target, body.DryRun)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := engine.Persist(state); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Run {
		if err := engine.Run(state); err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"state": state, "error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"state": state})
}
