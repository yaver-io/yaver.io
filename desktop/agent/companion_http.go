package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// companion_http.go — P2P control surface for the companion engine. Mirrors the
// phone_backend route style: owner-authed (s.auth), JSON in/out. Status flows
// straight from the agent to the web/mobile client — no Convex round-trip.

// registerCompanionRoutes wires /companion/*. Called from httpserver.go beside
// registerPhoneRoutes.
func (s *HTTPServer) registerCompanionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/companion/detect", s.auth(s.handleCompanionDetect))
	mux.HandleFunc("/companion/manifest", s.auth(s.handleCompanionManifest))
	mux.HandleFunc("/companion/up", s.auth(s.handleCompanionUp))
	mux.HandleFunc("/companion/down", s.auth(s.handleCompanionDown))
	mux.HandleFunc("/companion/status", s.auth(s.handleCompanionStatus))
	mux.HandleFunc("/companion/list", s.auth(s.handleCompanionListProjects))
	mux.HandleFunc("/companion/cron/list", s.auth(s.handleCompanionCronList))
	mux.HandleFunc("/microservices/detect", s.auth(s.handleMicroserviceDetect))
	mux.HandleFunc("/microservices/wrap", s.auth(s.handleMicroserviceWrap))
	mux.HandleFunc("/microservices/status", s.auth(s.handleMicroserviceStatus))
	mux.HandleFunc("/microservices/down", s.auth(s.handleMicroserviceDown))
}

// companionEngine returns the wired engine, lazily constructing one if the
// serve-time wiring hasn't run (e.g. a narrow test server).
func (s *HTTPServer) companionEngine() *CompanionEngine {
	if s.companion == nil {
		s.companion = &CompanionEngine{sched: s.scheduler, svcs: s.servicesMgr, vault: s.vaultStore}
	}
	return s.companion
}

func (s *HTTPServer) handleCompanionDetect(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		jsonError(w, http.StatusBadRequest, "repo query param required")
		return
	}
	m, items, err := DetectCompanion(repo)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	yamlBytes, _ := yaml.Marshal(m)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":        items,
		"manifest":     m,
		"manifestYaml": string(yamlBytes),
	})
}

// handleCompanionManifest GET reads the repo's current yaver.companion.yaml;
// POST writes a (user-confirmed) manifest YAML to the repo. Writing the repo is
// an explicit user action — detection itself never touches it.
func (s *HTTPServer) handleCompanionManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		repo := strings.TrimSpace(r.URL.Query().Get("repo"))
		if repo == "" {
			jsonError(w, http.StatusBadRequest, "repo query param required")
			return
		}
		data, err := os.ReadFile(filepath.Join(repo, CompanionManifestName))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"exists": false})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"exists": true, "manifestYaml": string(data)})
		return
	}
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
		return
	}
	var body struct {
		Repo         string `json:"repo"`
		ManifestYaml string `json:"manifestYaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Repo) == "" || strings.TrimSpace(body.ManifestYaml) == "" {
		jsonError(w, http.StatusBadRequest, "repo and manifestYaml required")
		return
	}
	// Validate it parses before writing.
	var probe CompanionManifest
	if err := yaml.Unmarshal([]byte(body.ManifestYaml), &probe); err != nil {
		jsonError(w, http.StatusBadRequest, "manifest does not parse: "+err.Error())
		return
	}
	path := filepath.Join(body.Repo, CompanionManifestName)
	if err := os.WriteFile(path, []byte(body.ManifestYaml), 0o644); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "path": path})
}

func (s *HTTPServer) handleCompanionUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Repo) == "" {
		jsonError(w, http.StatusBadRequest, "repo required")
		return
	}
	m, err := LoadCompanionManifest(body.Repo)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	status, err := s.companionEngine().Up(m)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": status})
}

func (s *HTTPServer) handleCompanionDown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Project) == "" {
		jsonError(w, http.StatusBadRequest, "project required")
		return
	}
	if err := s.companionEngine().Down(body.Project); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleCompanionStatus(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		jsonError(w, http.StatusBadRequest, "project query param required")
		return
	}
	status, err := s.companionEngine().Status(project)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": status})
}

func (s *HTTPServer) handleCompanionListProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"projects": listCompanionProjects()})
}

func (s *HTTPServer) handleCompanionCronList(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		jsonError(w, http.StatusBadRequest, "project query param required")
		return
	}
	status, err := s.companionEngine().Status(project)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"crons": status.Crons})
}

func (s *HTTPServer) handleMicroserviceDetect(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	res, err := MicroserviceDetect(repo, project)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleMicroserviceWrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req MicroserviceWrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	res, err := MicroserviceWrap(s, req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleMicroserviceStatus(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		jsonError(w, http.StatusBadRequest, "project query param required")
		return
	}
	status, err := s.companionEngine().Status(project)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": status})
}

func (s *HTTPServer) handleMicroserviceDown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Project) == "" {
		jsonError(w, http.StatusBadRequest, "project required")
		return
	}
	if err := s.companionEngine().Down(body.Project); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// listCompanionProjects enumerates the persisted companion state files.
func listCompanionProjects() []map[string]interface{} {
	out := []map[string]interface{}{}
	d, err := companionStateDir()
	if err != nil {
		return out
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return out
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".state.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(d, ent.Name()))
		if err != nil {
			continue
		}
		var st companionState
		if json.Unmarshal(data, &st) != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"project":   st.Project,
			"repoDir":   st.RepoDir,
			"enabled":   st.Enabled,
			"cronCount": len(st.ScheduleIDs),
			"svcCount":  len(st.UnitNames),
			"updatedAt": st.UpdatedAt,
		})
	}
	return out
}
