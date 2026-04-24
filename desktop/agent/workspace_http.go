package main

// workspace_http.go — HTTP handlers that expose the declarative monorepo
// manifest to the web dashboard, mobile app, and MCP clients.
//
// The manifest itself lives at yaver.workspace.yaml in the repo root;
// these endpoints parse it on demand and return a JSON projection.
// Skipping mtime caching for v1 — the file is tiny and re-parsing on
// every request is not the hot path.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceAppView is the JSON shape returned by /workspace/apps.
// Includes kind (derived from stack) + envMissing so the UI can grey
// out apps whose required env vars aren't set on the host.
type WorkspaceAppView struct {
	Name       string            `json:"name"`
	Path       string            `json:"path"`
	AbsPath    string            `json:"absPath,omitempty"`
	Stack      string            `json:"stack,omitempty"`
	Kind       DevServerKind     `json:"kind,omitempty"`
	Framework  string            `json:"framework,omitempty"`
	Depends    []string          `json:"depends,omitempty"`
	Env        []string          `json:"env,omitempty"`
	EnvMissing []string          `json:"envMissing,omitempty"`
	Provider   map[string]string `json:"provider,omitempty"`
	Exists     bool              `json:"exists"`
}

type workspaceResponse struct {
	OK       bool                `json:"ok"`
	Root     string              `json:"root"`
	Path     string              `json:"path"`
	Manifest *WorkspaceManifest  `json:"manifest,omitempty"`
	Apps     []*WorkspaceAppView `json:"apps,omitempty"`
}

// handleWorkspace returns the full parsed manifest plus the resolved
// root path. Used by clients that need the whole manifest (primary
// device hint, shared env list, etc.).
func (s *HTTPServer) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	root := resolveWorkspaceRoot(r, s)
	manifest, path, err := loadWorkspaceManifestForHTTP(root)
	if err != nil {
		jsonReply(w, http.StatusNotFound, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
			"root":  root,
		})
		return
	}

	resp := workspaceResponse{
		OK:       true,
		Root:     root,
		Path:     path,
		Manifest: manifest,
		Apps:     buildAppViews(root, manifest),
	}
	jsonReply(w, http.StatusOK, resp)
}

// handleWorkspaceApps returns just the apps list projection. Cheaper
// for clients that only need the dropdown contents (Web Reload tab).
func (s *HTTPServer) handleWorkspaceApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	root := resolveWorkspaceRoot(r, s)
	manifest, path, err := loadWorkspaceManifestForHTTP(root)
	if err != nil {
		jsonReply(w, http.StatusNotFound, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
			"root":  root,
		})
		return
	}

	apps := buildAppViews(root, manifest)

	// Optional kind filter: /workspace/apps?kind=web,hybrid
	if filter := strings.TrimSpace(r.URL.Query().Get("kind")); filter != "" {
		wanted := map[DevServerKind]bool{}
		for _, k := range strings.Split(filter, ",") {
			wanted[DevServerKind(strings.TrimSpace(k))] = true
		}
		filtered := make([]*WorkspaceAppView, 0, len(apps))
		for _, a := range apps {
			if wanted[a.Kind] {
				filtered = append(filtered, a)
			}
		}
		apps = filtered
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"root": root,
		"path": path,
		"apps": apps,
	})
}

// resolveWorkspaceRoot picks the repo root for manifest lookup.
// Priority: ?root= query param, then taskMgr.workDir, then cwd.
func resolveWorkspaceRoot(r *http.Request, s *HTTPServer) string {
	if root := strings.TrimSpace(r.URL.Query().Get("root")); root != "" {
		if abs, err := filepath.Abs(root); err == nil {
			return abs
		}
		return root
	}
	if s != nil && s.taskMgr != nil && s.taskMgr.workDir != "" {
		return s.taskMgr.workDir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// loadWorkspaceManifestForHTTP wraps LoadWorkspaceManifest and also
// returns the resolved manifest path (for logging / debugging).
func loadWorkspaceManifestForHTTP(root string) (*WorkspaceManifest, string, error) {
	path := filepath.Join(root, WorkspaceManifestPath)
	if WorkspaceManifestPathOverride != "" {
		path = WorkspaceManifestPathOverride
	}
	m, err := LoadWorkspaceManifest(root)
	return m, path, err
}

// buildAppViews projects WorkspaceApp → WorkspaceAppView with derived
// fields (kind, framework, envMissing, exists).
func buildAppViews(root string, m *WorkspaceManifest) []*WorkspaceAppView {
	if m == nil {
		return nil
	}
	out := make([]*WorkspaceAppView, 0, len(m.Apps))
	sharedEnv := m.Shared.Env
	for i := range m.Apps {
		app := &m.Apps[i]
		abs := appAbsPath(root, m, app)
		view := &WorkspaceAppView{
			Name:      app.Name,
			Path:      app.Path,
			AbsPath:   abs,
			Stack:     app.Stack,
			Kind:      StackToDevServerKind(app.Stack),
			Framework: StackToFramework(app.Stack),
			Depends:   app.Depends,
			Env:       app.Env,
			Provider:  app.Provider,
			Exists:    dirExists(abs),
		}
		// Missing env: union of app.Env + shared.Env, minus anything
		// present in the host environment. The web dashboard uses this
		// to surface "⚠ missing VARIABLE" next to an app.
		view.EnvMissing = missingEnvVars(app.Env, sharedEnv)
		out = append(out, view)
	}
	return out
}

func appAbsPath(root string, m *WorkspaceManifest, app *WorkspaceApp) string {
	if filepath.IsAbs(app.Path) {
		return app.Path
	}
	base := root
	if m != nil && m.Workspace.Root != "" && !filepath.IsAbs(m.Workspace.Root) {
		base = filepath.Join(root, m.Workspace.Root)
	} else if m != nil && filepath.IsAbs(m.Workspace.Root) {
		base = m.Workspace.Root
	}
	abs, err := filepath.Abs(filepath.Join(base, app.Path))
	if err != nil {
		return filepath.Join(base, app.Path)
	}
	return abs
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func missingEnvVars(appEnv, sharedEnv []string) []string {
	seen := map[string]bool{}
	var all []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		all = append(all, name)
	}
	for _, e := range appEnv {
		add(e)
	}
	for _, e := range sharedEnv {
		add(e)
	}
	var missing []string
	for _, name := range all {
		if _, ok := os.LookupEnv(name); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}
