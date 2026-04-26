package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProjectRemote binds a project name (the user-visible label, e.g. "carrotbet")
// to a canonical git remote so the agent can answer eligibility / vibing
// questions even when the project hasn't been cloned to disk yet, or has been
// cloned without a configured remote.
//
// The legacy detection path (`git config --get-regexp ^remote\..*\.url$`) still
// runs first; this registry is consulted only when that returns nothing.
type ProjectRemote struct {
	Name      string `json:"name"`
	RemoteURL string `json:"remoteUrl"`
	Provider  string `json:"provider"` // "github" or "gitlab"
	Host      string `json:"host"`     // e.g. "github.com" / self-hosted
	Repo      string `json:"repo"`     // "owner/repo"
	SetAt     string `json:"setAt"`    // ISO 8601
}

var projectRemotesMu sync.Mutex

func projectRemotesPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "project-remotes.json")
}

func loadProjectRemotes() ([]ProjectRemote, error) {
	data, err := os.ReadFile(projectRemotesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []ProjectRemote
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func saveProjectRemotes(entries []ProjectRemote) error {
	path := projectRemotesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// findProjectRemote returns the registry entry whose Name matches projectName
// case-insensitively. Returns nil when no entry exists.
func findProjectRemote(projectName string) *ProjectRemote {
	target := strings.TrimSpace(projectName)
	if target == "" {
		return nil
	}
	entries, err := loadProjectRemotes()
	if err != nil {
		return nil
	}
	for i := range entries {
		if strings.EqualFold(entries[i].Name, target) {
			return &entries[i]
		}
	}
	return nil
}

// upsertProjectRemote inserts or replaces an entry by Name (case-insensitive).
func upsertProjectRemote(entry ProjectRemote) error {
	projectRemotesMu.Lock()
	defer projectRemotesMu.Unlock()

	entries, _ := loadProjectRemotes()
	for i := range entries {
		if strings.EqualFold(entries[i].Name, entry.Name) {
			entries[i] = entry
			return saveProjectRemotes(entries)
		}
	}
	entries = append(entries, entry)
	return saveProjectRemotes(entries)
}

func deleteProjectRemote(projectName string) error {
	target := strings.TrimSpace(projectName)
	if target == "" {
		return nil
	}
	projectRemotesMu.Lock()
	defer projectRemotesMu.Unlock()

	entries, _ := loadProjectRemotes()
	out := entries[:0]
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, target) {
			continue
		}
		out = append(out, entry)
	}
	return saveProjectRemotes(out)
}

// projectRemoteFromURL parses a git remote URL and returns a populated entry
// (without Name/SetAt). Returns an empty entry when the URL doesn't map to a
// known provider — callers should treat that as a 400.
func projectRemoteFromURL(remoteURL string) ProjectRemote {
	detected := detectRepoFromRemoteURL(strings.TrimSpace(remoteURL))
	if detected.Provider == "" || strings.TrimSpace(detected.Repo) == "" {
		return ProjectRemote{}
	}
	return ProjectRemote{
		RemoteURL: strings.TrimSpace(remoteURL),
		Provider:  string(detected.Provider),
		Host:      detected.Host,
		Repo:      detected.Repo,
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers — registered in httpserver.go under owner-only auth.
// ---------------------------------------------------------------------------

// GET /vibing/project/remote                → list all
// GET /vibing/project/remote?projectName=X  → single (or {} when missing)
func (s *HTTPServer) handleProjectRemoteGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	if name := strings.TrimSpace(r.URL.Query().Get("projectName")); name != "" {
		entry := findProjectRemote(name)
		if entry == nil {
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":      true,
				"found":   false,
				"project": nil,
			})
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"found":   true,
			"project": entry,
		})
		return
	}

	entries, _ := loadProjectRemotes()
	if entries == nil {
		entries = []ProjectRemote{}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"projects": entries,
	})
}

// POST /vibing/project/remote   {projectName, remoteUrl}
func (s *HTTPServer) handleProjectRemoteSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		ProjectName string `json:"projectName"`
		RemoteURL   string `json:"remoteUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	name := strings.TrimSpace(req.ProjectName)
	url := strings.TrimSpace(req.RemoteURL)
	if name == "" || url == "" {
		jsonError(w, http.StatusBadRequest, "projectName and remoteUrl are required")
		return
	}

	entry := projectRemoteFromURL(url)
	if entry.Provider == "" {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("could not parse %q as a GitHub or GitLab remote URL", url))
		return
	}
	entry.Name = name
	entry.SetAt = time.Now().UTC().Format(time.RFC3339)

	if err := upsertProjectRemote(entry); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save project remote: "+err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"project": entry,
	})
}

// DELETE /vibing/project/remote?projectName=X
func (s *HTTPServer) handleProjectRemoteDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("projectName"))
	if name == "" {
		jsonError(w, http.StatusBadRequest, "projectName query param required")
		return
	}
	if err := deleteProjectRemote(name); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to delete: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleProjectRemote is a single mux handler that dispatches by HTTP method
// so the route can be registered under one path.
func (s *HTTPServer) handleProjectRemote(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleProjectRemoteGet(w, r)
	case http.MethodPost:
		s.handleProjectRemoteSet(w, r)
	case http.MethodDelete:
		s.handleProjectRemoteDelete(w, r)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET, POST, or DELETE")
	}
}
