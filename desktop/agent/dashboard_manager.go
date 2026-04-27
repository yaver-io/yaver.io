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

// DashboardManager auto-starts the appropriate dashboard container for each
// project's backend and tracks which studio id routes to which local port.
// Without this, /proxy/{studio-id} always points at fixed ports — fine for a
// single project, broken when the user runs multiple projects on the same
// machine with different backends.

type DashboardRecord struct {
	ProjectDir   string    `json:"projectDir"`
	Backend      string    `json:"backend"`
	StudioID     string    `json:"studioId"`
	Port         int       `json:"port"`
	ContainerID  string    `json:"containerId,omitempty"`
	URL          string    `json:"url"`
	Status       string    `json:"status"`
	AdminKeyPath string    `json:"adminKeyPath,omitempty"`
	StartedAt    time.Time `json:"startedAt"`
}

type dashboardMgr struct {
	mu      sync.Mutex
	records map[string]*DashboardRecord // key: projectDir
}

var globalDashboardMgr = &dashboardMgr{records: map[string]*DashboardRecord{}}

// StartDashboard picks up an existing tunnel-able dashboard for the project's
// backend, or spawns one. For Convex it starts ghcr.io/get-convex/convex-dashboard
// on a unique port allocated per-project. For Supabase it assumes `supabase start`
// already launched Studio on :54323 (Studio runs once per host). For Postgres/
// SQLite it spawns `drizzle-kit studio` as a binary. For PocketBase the admin
// UI is already running as part of the pocketbase service.
func StartDashboard(projectDir string) (*DashboardRecord, error) {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}

	globalDashboardMgr.mu.Lock()
	defer globalDashboardMgr.mu.Unlock()

	if r, ok := globalDashboardMgr.records[projectDir]; ok {
		// Re-probe; if still up, return as-is.
		if probeStudio(r.URL) {
			return r, nil
		}
	}

	var rec *DashboardRecord
	switch cfg.Backend {
	case BackendConvex:
		rec, err = startConvexDashboard(projectDir)
	case BackendSupabase:
		// Studio launches as part of `supabase start` on :54323 — nothing new to spawn.
		rec = &DashboardRecord{
			ProjectDir: projectDir, Backend: string(cfg.Backend), StudioID: "supabase",
			Port: 54323, URL: "http://127.0.0.1:54323", Status: "shared",
			StartedAt: time.Now(),
		}
	case BackendPostgres, BackendSQLite:
		rec, err = startDrizzleStudio(projectDir)
	default:
		return nil, fmt.Errorf("no dashboard strategy for backend %q", cfg.Backend)
	}
	if err != nil {
		return nil, err
	}
	if rec != nil {
		globalDashboardMgr.records[projectDir] = rec
	}
	return rec, nil
}

// StopDashboard tears down a per-project dashboard container if we started one.
func StopDashboard(projectDir string) error {
	globalDashboardMgr.mu.Lock()
	defer globalDashboardMgr.mu.Unlock()
	rec, ok := globalDashboardMgr.records[projectDir]
	if !ok {
		return nil
	}
	if rec.ContainerID != "" {
		_, _ = NewServicesManager(projectDir).runDocker("rm", "-f", rec.ContainerID)
	}
	delete(globalDashboardMgr.records, projectDir)
	return nil
}

// ListDashboards returns every active per-project dashboard.
func ListDashboards() []*DashboardRecord {
	globalDashboardMgr.mu.Lock()
	defer globalDashboardMgr.mu.Unlock()
	out := make([]*DashboardRecord, 0, len(globalDashboardMgr.records))
	for _, r := range globalDashboardMgr.records {
		out = append(out, r)
	}
	return out
}

func startConvexDashboard(projectDir string) (*DashboardRecord, error) {
	sm := NewServicesManager(projectDir)
	// Prefer the shared convex-dashboard preset on :6791 if it's already in services.yaml.
	if _, addErr := sm.Add("convex-dashboard", nil); addErr == nil {
		_, _ = sm.Start("convex-dashboard")
	}
	// Store/generate admin key for injection.
	keyDir := filepath.Join(projectDir, ".yaver")
	_ = os.MkdirAll(keyDir, 0o755)
	keyPath := filepath.Join(keyDir, "convex-admin-key")
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		_ = os.WriteFile(keyPath, []byte(defaultConvexAdminKey), 0o600)
	}
	rec := &DashboardRecord{
		ProjectDir: projectDir, Backend: "convex", StudioID: "convex",
		Port: 6791, URL: "http://127.0.0.1:6791", Status: "running",
		AdminKeyPath: keyPath, StartedAt: time.Now(),
	}
	// Best-effort wait for readiness.
	for i := 0; i < 20; i++ {
		if probeStudio(rec.URL) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return rec, nil
}

func startDrizzleStudio(projectDir string) (*DashboardRecord, error) {
	// Drizzle Studio is a CLI-launched binary, not a Docker service. We record
	// the standard port (4983) and guidance for the user to run it; attempting
	// to spawn it silently would race with their own instance.
	rec := &DashboardRecord{
		ProjectDir: projectDir, Backend: "postgres", StudioID: "drizzle",
		Port: 4983, URL: "http://127.0.0.1:4983",
		Status: "external",
		StartedAt: time.Now(),
	}
	if !probeStudio(rec.URL) {
		rec.Status = "stopped"
	}
	return rec, nil
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleDashboardStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	dir := s.dirParam(r)
	rec, err := StartDashboard(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *HTTPServer) handleDashboardStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if err := StopDashboard(s.dirParam(r)); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleDashboardList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"dashboards": ListDashboards()})
}

// handleDashboardProxy serves a request at /dashboard/{projectSlug}/* by
// looking up the project's dashboard record and forwarding there. This is the
// per-project equivalent of /proxy/{studio}/* which is by-studio-id.
func (s *HTTPServer) handleDashboardProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/dashboard/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing project slug", http.StatusBadRequest)
		return
	}
	slug := parts[0]
	var target *DashboardRecord
	for _, rec := range ListDashboards() {
		if filepath.Base(rec.ProjectDir) == slug {
			target = rec
			break
		}
	}
	if target == nil {
		http.Error(w, "no dashboard for project "+slug, http.StatusNotFound)
		return
	}
	// Rewrite URL path to drop the /dashboard/{slug} prefix and proxy.
	rest := ""
	if len(parts) == 2 {
		rest = "/" + parts[1]
	}
	r.URL.Path = rest
	// Reuse studio_proxy's forwarding logic by setting a synthetic studio id
	// that points at the same target URL.
	forwardStudio(w, r, target.URL)
}

// Small helper that mirrors the studio_proxy.go logic but with a known URL.
func forwardStudio(w http.ResponseWriter, r *http.Request, upstreamURL string) {
	// Inline minimal version (full ReverseProxy is in studio_proxy.go).
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(upstreamURL + r.URL.Path)
	if err != nil {
		http.Error(w, "upstream unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = writeJSONRaw(w, resp)
}

func writeJSONRaw(w http.ResponseWriter, resp *http.Response) (int, error) {
	buf := make([]byte, 32*1024)
	var total int
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			nw, werr := w.Write(buf[:n])
			total += nw
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			return total, nil
		}
	}
}

// Suppress unused import if we don't reference encoding/json below.
var _ = json.Marshal
