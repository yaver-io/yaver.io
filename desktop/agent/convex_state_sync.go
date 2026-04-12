package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Convex state sync — the agent periodically pushes local state (projects,
// services, deployments, audit events) to yaver.io's Convex backend so the
// dashboard's Overview/Activity/Projects views can render without having to
// fan-out live API calls to every machine.

type convexSyncer struct {
	mu          sync.Mutex
	convexURL   string
	authToken   string
	deviceID    string
	lastAudit   int64 // last pushed audit entry timestamp (unix ns)
	client      *http.Client
}

var globalConvexSync *convexSyncer

// StartConvexStateSync kicks off a 60-second ticker that pushes local state.
// Best-effort: network errors are swallowed (agent keeps working offline).
func StartConvexStateSync(ctx context.Context) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return // not signed in; nothing to sync to
	}
	globalConvexSync = &convexSyncer{
		convexURL: cfg.ConvexSiteURL,
		authToken: cfg.AuthToken,
		deviceID:  cfg.DeviceID,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		// Initial sync on startup (give services 5s to come up first).
		time.Sleep(5 * time.Second)
		globalConvexSync.syncAll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				globalConvexSync.syncAll(ctx)
			}
		}
	}()
}

func (s *convexSyncer) syncAll(ctx context.Context) {
	s.syncProjects(ctx)
	s.syncServices(ctx)
	s.syncRecentActivity(ctx)
}

// syncProjects walks every project directory the agent knows about and pushes
// a snapshot of each to Convex via agentSync:upsertProject.
func (s *convexSyncer) syncProjects(ctx context.Context) {
	dirs := discoverProjectDirs()
	for _, dir := range dirs {
		cfg, _ := LoadProjectConfig(dir)
		if cfg == nil {
			continue
		}
		gitBranch := ""
		gitCommit := ""
		if out, err := runIn(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			gitBranch = trimNewline(out)
		}
		if out, err := runIn(dir, "git", "rev-parse", "HEAD"); err == nil {
			gitCommit = trimNewline(out)
		}
		s.callMutation("agentSync:upsertProject", map[string]interface{}{
			"deviceId":   s.deviceID,
			"slug":       filepath.Base(dir),
			"path":       dir,
			"name":       filepath.Base(dir),
			"stack":      cfg.Stack,
			"backend":    string(cfg.Backend),
			"auth":       cfg.Auth,
			"activeEnv":  ActiveEnv(dir),
			"gitBranch":  gitBranch,
			"lastCommit": gitCommit,
			"status":     "running", // best-effort — we assume running if present
		})
	}
}

// syncServices pushes the current services.yaml + runtime status for every
// discovered project.
func (s *convexSyncer) syncServices(ctx context.Context) {
	for _, dir := range discoverProjectDirs() {
		sm := NewServicesManager(dir)
		cfg, err := sm.LoadConfig()
		if err != nil || cfg == nil {
			continue
		}
		statuses, _ := sm.Status()
		statusMap := map[string]ServiceStatus{}
		for _, st := range statuses {
			statusMap[st.Name] = st
		}
		var items []map[string]interface{}
		slug := filepath.Base(dir)
		for name, svc := range cfg.Services {
			st := statusMap[name]
			items = append(items, map[string]interface{}{
				"name":        name,
				"image":       svc.Image,
				"port":        svc.Port,
				"status":      st.Health,
				"projectSlug": slug,
			})
		}
		if len(items) == 0 {
			continue
		}
		s.callMutation("agentSync:upsertServices", map[string]interface{}{
			"deviceId": s.deviceID,
			"services": items,
		})
	}
}

// syncRecentActivity pulls new audit log entries and pushes each to Convex.
func (s *convexSyncer) syncRecentActivity(ctx context.Context) {
	a, err := ensureAudit()
	if err != nil {
		return
	}
	entries, err := a.List(50)
	if err != nil {
		return
	}
	for _, e := range entries {
		ts := e.Timestamp.UnixNano()
		if ts <= s.lastAudit {
			continue
		}
		s.callMutation("agentSync:recordActivity", map[string]interface{}{
			"deviceId":  s.deviceID,
			"action":    e.Action,
			"target":    e.Target,
			"outcome":   e.Outcome,
			"error":     e.Error,
			"timestamp": e.Timestamp.UnixMilli(),
		})
		if ts > s.lastAudit {
			s.lastAudit = ts
		}
	}
}

// callMutation invokes a Convex mutation via the HTTP action endpoint. Silent
// on failure — sync is best-effort.
func (s *convexSyncer) callMutation(path string, args map[string]interface{}) {
	body, _ := json.Marshal(map[string]interface{}{
		"path":   path,
		"args":   args,
		"format": "json",
	})
	req, err := http.NewRequest(http.MethodPost, s.convexURL+"/api/mutation", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.authToken)
	res, err := s.client.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
}

// RecordDeployToConvex is called from finishDeploy to push deploy records
// individually (not just via the audit stream, which is lower fidelity).
func RecordDeployToConvex(rec *DeployRecord) {
	if globalConvexSync == nil {
		return
	}
	args := map[string]interface{}{
		"deviceId":    globalConvexSync.deviceID,
		"projectSlug": filepath.Base(rec.ProjectDir),
		"deployId":    rec.ID,
		"environment": rec.Environment,
		"status":      rec.Status,
		"commit":      rec.Commit,
		"message":     rec.Message,
		"duration":    rec.Duration,
		"startedAt":   rec.StartedAt.UnixMilli(),
	}
	if !rec.FinishedAt.IsZero() {
		args["finishedAt"] = rec.FinishedAt.UnixMilli()
	}
	globalConvexSync.callMutation("agentSync:recordDeploy", args)
}

// discoverProjectDirs scans the user's home + common workspace paths for
// anything that looks like a yaver project (.yaver/config.yaml).
func discoverProjectDirs() []string {
	home, _ := os.UserHomeDir()
	roots := []string{
		filepath.Join(home, "Workspace"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "Code"),
		filepath.Join(home, "src"),
	}
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if _, err := os.Stat(filepath.Join(dir, ".yaver", "config.yaml")); err != nil {
				continue
			}
			if !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	return out
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// Used by other code wanting to opt out temporarily — e.g. during
// destructive restore operations where state will flip rapidly.
func PauseConvexSync() {
	if globalConvexSync == nil {
		return
	}
	globalConvexSync.mu.Lock()
}
func ResumeConvexSync() {
	if globalConvexSync == nil {
		return
	}
	globalConvexSync.mu.Unlock()
}

var _ = fmt.Sprintf // avoid unused-import if fmt isn't otherwise referenced
