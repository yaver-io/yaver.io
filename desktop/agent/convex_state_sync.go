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
	"strings"
	"sync"
	"time"
)

// Convex state sync — the agent periodically pushes local state (projects,
// services, deployments, audit events) to yaver.io's Convex backend so the
// dashboard's Overview/Activity/Projects views can render without having to
// fan-out live API calls to every machine.

type convexSyncer struct {
	mu          sync.Mutex
	lifecycleMu sync.Mutex
	convexURL   string
	authToken   string
	deviceID    string
	taskMgr     *TaskManager
	lastAudit   int64 // last pushed audit entry timestamp (unix ns)
	client      *http.Client
	// Payload dedup — agents on quiet machines produce the same
	// {projects, services} every tick. Hash the marshalled payload
	// and skip the Convex call entirely when nothing changed. Saves
	// ~180 calls/hour/user in the common case. Hash is blake2b-sized
	// but we just use fnv for speed — collisions are tolerable since
	// a miss at worst sends one extra payload.
	lastSyncHash uint64
	// Stats for /sync/status visibility.
	lastSyncAt   time.Time
	successCount int
	skippedCount int
	failCount    int
	lastError    string
}

var globalConvexSync *convexSyncer
var convexLifecycleSyncRequests = make(chan struct{}, 1)

// requestConvexLifecycleSync schedules a best-effort out-of-band session/task
// reconciliation after a verified local lifecycle change. The operation that
// changed local state never waits for Convex; a full channel coalesces bursts.
func requestConvexLifecycleSync() {
	if globalConvexSync == nil {
		return
	}
	select {
	case convexLifecycleSyncRequests <- struct{}{}:
	default:
	}
}

// StartConvexStateSync kicks off a 60-second ticker that pushes local state.
// Best-effort: network errors are swallowed (agent keeps working offline).
func StartConvexStateSync(ctx context.Context, taskMgr *TaskManager) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return // not signed in; nothing to sync to
	}
	globalConvexSync = &convexSyncer{
		convexURL: cfg.ConvexSiteURL,
		authToken: cfg.AuthToken,
		deviceID:  cfg.DeviceID,
		taskMgr:   taskMgr,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
	// Initial sync delay preserved — most downstream services need
	// a few seconds to come up after `yaver serve` starts. Registering
	// the task in a go() keeps Start fast and non-blocking.
	go func() {
		time.Sleep(5 * time.Second)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-convexLifecycleSyncRequests:
					// Coalesce a burst of task/session transitions into one snapshot.
					time.Sleep(250 * time.Millisecond)
					syncCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					globalConvexSync.syncLifecycleState(syncCtx)
					cancel()
				}
			}
		}()
		SupervisedGo("convex-state-sync", 60*time.Second, true,
			func(ctx context.Context) error {
				globalConvexSync.syncAll(ctx)
				return nil
			})
	}()
}

func (s *convexSyncer) syncAll(ctx context.Context) {
	// Tmux is an independent live inventory. Run it before the batch-payload
	// dedup guard below: on a quiet project that guard returns early for hours,
	// while tmux seats can start/exit every minute. Keeping this call after the
	// guard made mobile show prior sessions until some unrelated project or
	// service state changed.
	s.syncLifecycleState(ctx)

	// Build one combined payload and send it in a single Convex call.
	// Falls back to the legacy per-item mutations only on 404 (old
	// backend without agentSync:batchSync deployed yet) — that gate
	// flips off after the first successful batch so a fresh agent
	// talking to an up-to-date backend never pays the fallback cost.
	payload, err := s.buildBatchPayload()
	if err != nil {
		s.mu.Lock()
		s.lastError = err.Error()
		s.failCount++
		s.mu.Unlock()
		return
	}

	// Dedup: if this payload is byte-for-byte what we sent last tick,
	// don't call Convex at all. The agent's own privacy contract
	// already means we only include changed data — an idle box with
	// no new audit events will sit at this branch indefinitely,
	// producing zero Convex traffic. The 5-minute peer-offline
	// threshold is unaffected because peer presence rides heartbeat,
	// not state sync.
	hash := hashBytes(payload)
	s.mu.Lock()
	skip := hash == s.lastSyncHash && !s.lastSyncAt.IsZero()
	s.mu.Unlock()
	if skip {
		s.mu.Lock()
		s.skippedCount++
		s.lastSyncAt = time.Now()
		s.mu.Unlock()
		return
	}

	var reply map[string]interface{}
	if err := s.callBatch(ctx, payload, &reply); err != nil {
		// Convex returned something we couldn't parse or HTTP-level
		// failure. Fall back to the legacy per-item path so older
		// backends keep working during the rollout. After one
		// success on the new path we never hit this fallback again.
		s.syncProjects(ctx)
		s.syncServices(ctx)
		s.syncRecentActivity(ctx)
		s.mu.Lock()
		s.lastSyncAt = time.Now()
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	s.lastSyncHash = hash
	s.lastSyncAt = time.Now()
	s.successCount++
	s.mu.Unlock()

}

func (s *convexSyncer) syncLifecycleState(ctx context.Context) {
	if s == nil {
		return
	}
	// The ticker and event-triggered path may meet. Serialize cache/snapshot
	// reconciliation so they cannot publish an older view after a newer one.
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.reconcileTaskTombstonesFromConvex(ctx, s.taskMgr)
	// Tasks are the only session contract. The old tmux ledger duplicated this
	// roster, exposed session-name implementation detail, and added a second
	// periodic write. The authenticated /task-snapshots publication removes
	// legacy rows for this device once; no active path republishes them.
	syncTaskSnapshotToConvex(ctx, s.taskMgr)
}

// buildRuntimeProjectCatalog builds the per-machine project catalog that gets
// seeded into Convex: TOP-LEVEL git projects only, each carrying its git
// provider identity (gitRemote → repoName + gitProvider), branch and
// framework. This is what answers "which project (on which git provider) is
// on which machine" on every surface — web Settings, the mobile composer and
// the dashboard. Privacy contract (matches the existing catalog validator):
// names, remotes, branches, frameworks — NEVER absolute filesystem paths.
// A repo with no origin remote has no provider identity and is skipped.
func buildRuntimeProjectCatalog() []map[string]interface{} {
	projects := collapseNestedRepos(listDiscoveredProjects())
	out := make([]map[string]interface{}, 0, len(projects))
	for _, p := range projects {
		info := DetectProjectInfo(p.Path)
		remote := strings.TrimSpace(info.GitRemote)
		if remote == "" {
			continue // no origin → no git provider identity to seed
		}
		repoName, gitProvider := deriveGitProviderIdentity(remote, info.Name)
		out = append(out, map[string]interface{}{
			"projectName": info.Name,
			"repoName":    repoName,
			"gitProvider": gitProvider,
			"gitRemote":   remote,
			"branch":      info.GitBranch,
			"framework":   info.Framework,
		})
		if len(out) >= 50 {
			break
		}
	}
	return out
}

// buildMCPCatalog builds the per-machine external-MCP catalog seeded into
// Convex — the cross-machine view that answers "which MCP server lives on
// which machine" on every surface (web chat composer, mobile composer).
// Privacy contract (mirrors the runtimeProjectCatalog contract): names, URLs
// and cached tool counts only — NEVER auth tokens or headers. A server whose
// tools were never listed this process omits toolCount (surfaces render "0
// tools" fallback), so this never makes a network call: it reads only the
// 60-second cache warm by normal tools/list traffic.
func buildMCPCatalog() []map[string]interface{} {
	servers := enabledExternalServers()
	if len(servers) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(servers))
	for _, s := range servers {
		entry := map[string]interface{}{
			"name":    s.Name,
			"url":     s.URL,
			"enabled": true,
		}
		if v, ok := extMCPCache.Load(s.Name); ok {
			if e, ok := v.(*extToolEntry); ok {
				entry["toolCount"] = len(e.tools)
			}
		}
		out = append(out, entry)
		if len(out) >= 40 {
			break
		}
	}
	return out
}

// deriveGitProviderIdentity parses a git remote URL into (repoName, provider).
// Handles ssh (git@host:owner/repo.git), https (https://host/owner/repo) and
// plain scp-ish forms; strips credentials that DetectProjectInfo may have left
// in the host part. repoName falls back to the project's directory name when
// the remote path is unparseable.
func deriveGitProviderIdentity(gitRemote, fallbackName string) (string, string) {
	remote := strings.TrimSpace(gitRemote)
	hostPart := remote
	// Drop scheme (https://, ssh://, git://).
	if idx := strings.Index(hostPart, "://"); idx >= 0 {
		hostPart = hostPart[idx+3:]
	}
	// Drop user@ (git@github.com:...).
	if idx := strings.Index(hostPart, "@"); idx >= 0 {
		hostPart = hostPart[idx+1:]
	}
	// Split host from path — scp form uses ':', https uses '/'.
	path := ""
	if idx := strings.Index(hostPart, ":"); idx >= 0 {
		path = hostPart[idx+1:]
		hostPart = hostPart[:idx]
	} else if idx := strings.Index(hostPart, "/"); idx >= 0 {
		path = hostPart[idx+1:]
		hostPart = hostPart[:idx]
	}
	host := strings.ToLower(hostPart)
	provider := host
	switch {
	case strings.Contains(host, "github"):
		provider = "github"
	case strings.Contains(host, "gitlab"):
		provider = "gitlab"
	}
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	segs := []string{}
	for _, s := range strings.Split(path, "/") {
		if strings.TrimSpace(s) != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return fallbackName, provider
	}
	return segs[len(segs)-1], provider
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
		// Privacy contract: never send absolute filesystem paths to
		// Convex — they contain the user's home-dir username and leak
		// on-disk topology. Clients fetch the real path from this
		// agent's own /projects endpoint (P2P) keyed by slug+deviceId.
		s.callMutation("agentSync:upsertProject", map[string]interface{}{
			"deviceId":   s.deviceID,
			"slug":       filepath.Base(dir),
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

// buildBatchPayload snapshots every per-tick-changeable piece of
// state into one map. Callers hash + dedup on the marshalled bytes
// before sending. Always includes arrays (possibly empty) so that
// "state went from 1 project to 0" still produces a different hash
// than "same 1 project as last tick".
func (s *convexSyncer) buildBatchPayload() ([]byte, error) {
	projects := make([]map[string]interface{}, 0, 4)
	services := make([]map[string]interface{}, 0, 4)
	activity := make([]map[string]interface{}, 0, 4)

	// Projects
	for _, dir := range discoverProjectDirs() {
		cfg, _ := LoadProjectConfig(dir)
		if cfg == nil {
			continue
		}
		gitBranch, gitCommit := "", ""
		if out, err := runIn(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			gitBranch = trimNewline(out)
		}
		if out, err := runIn(dir, "git", "rev-parse", "HEAD"); err == nil {
			gitCommit = trimNewline(out)
		}
		projects = append(projects, map[string]interface{}{
			"slug":       filepath.Base(dir),
			"name":       filepath.Base(dir),
			"stack":      cfg.Stack,
			"backend":    string(cfg.Backend),
			"auth":       cfg.Auth,
			"activeEnv":  ActiveEnv(dir),
			"gitBranch":  gitBranch,
			"lastCommit": gitCommit,
			"status":     "running",
		})
	}

	// Services (pre-flattened; batchSync wipes+inserts for this device)
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
		slug := filepath.Base(dir)
		for name, svc := range cfg.Services {
			st := statusMap[name]
			services = append(services, map[string]interface{}{
				"name":        name,
				"image":       svc.Image,
				"port":        svc.Port,
				"status":      st.Health,
				"projectSlug": slug,
			})
		}
	}

	// Activity — only entries newer than lastAudit. After a successful
	// batch we roll lastAudit forward (handled in callBatch).
	if a, err := ensureAudit(); err == nil {
		entries, _ := a.List(50)
		for _, e := range entries {
			ts := e.Timestamp.UnixNano()
			if ts <= s.lastAudit {
				continue
			}
			activity = append(activity, map[string]interface{}{
				"action":    e.Action,
				"target":    e.Target,
				"outcome":   e.Outcome,
				"error":     e.Error,
				"timestamp": e.Timestamp.UnixMilli(),
			})
		}
	}

	return json.Marshal(map[string]interface{}{
		"deviceId":              s.deviceID,
		"projects":              projects,
		"services":              services,
		"activity":              activity,
		"runtimeProjectCatalog": buildRuntimeProjectCatalog(),
		"mcpCatalog":            buildMCPCatalog(),
	})
}

// callBatch posts a pre-marshalled batchSync args blob to Convex.
// Distinct from callMutation so dedup + test recording interact
// cleanly with the batched path.
func (s *convexSyncer) callBatch(ctx context.Context, args []byte, reply interface{}) error {
	if convexMutationRecorder != nil {
		var a map[string]interface{}
		_ = json.Unmarshal(args, &a)
		convexMutationRecorder("agentSync:batchSync", a)
		// Advance lastAudit optimistically (tests don't exercise the
		// Convex side; they just want the recorded payload).
		s.rollForwardLastAudit(a)
		return nil
	}
	body, _ := json.Marshal(map[string]interface{}{
		"path":   "agentSync:batchSync",
		"args":   json.RawMessage(args),
		"format": "json",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.convexURL+"/api/mutation", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.authToken)
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	bodyBytes, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return fmt.Errorf("batchSync: HTTP %d", res.StatusCode)
	}
	if reply != nil && len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, reply)
	}
	// Advance lastAudit only after a successful send.
	var parsed map[string]interface{}
	if json.Unmarshal(args, &parsed) == nil {
		s.rollForwardLastAudit(parsed)
	}
	return nil
}

// rollForwardLastAudit advances the lastAudit watermark to the highest
// timestamp the just-sent payload contains. Keeping this separate from
// callBatch lets test recording advance the cursor too, so the next
// dedup check doesn't include already-sent entries.
func (s *convexSyncer) rollForwardLastAudit(payload map[string]interface{}) {
	entries, ok := payload["activity"].([]interface{})
	if !ok {
		return
	}
	var maxTs int64
	for _, e := range entries {
		m, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		switch ts := m["timestamp"].(type) {
		case float64:
			if int64(ts)*1e6 > maxTs {
				maxTs = int64(ts) * 1e6
			}
		case int64:
			if ts*1e6 > maxTs {
				maxTs = ts * 1e6
			}
		}
	}
	if maxTs > s.lastAudit {
		s.mu.Lock()
		s.lastAudit = maxTs
		s.mu.Unlock()
	}
}

// hashBytes — fast non-cryptographic hash. Collisions are acceptable;
// worst case we send one unnecessary payload.
func hashBytes(b []byte) uint64 {
	// FNV-1a — stdlib, no deps. A collision here means we skip a send
	// when we shouldn't have, which is self-healing on the next tick.
	var h uint64 = 1469598103934665603
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// convexMutationRecorder, if non-nil, is called with every (path,
// args) pair *instead of* making the HTTP request. Tests use this to
// assert nothing confidential leaves the agent. Must only ever be set
// from _test.go.
var convexMutationRecorder func(path string, args map[string]interface{})

// callMutation invokes a Convex mutation via the HTTP action endpoint. Silent
// on failure — sync is best-effort.
func (s *convexSyncer) callMutation(path string, args map[string]interface{}) {
	s.callMutationOK(path, args)
}

// callMutationOK invokes a Convex mutation and reports whether it succeeded.
// callers that mutate local state only on success (e.g. the tmux session
// cache, which must keep un-acked closures to retry) use the boolean;
// fire-and-forget sync keeps using callMutation.
func (s *convexSyncer) callMutationOK(path string, args map[string]interface{}) bool {
	if convexMutationRecorder != nil {
		convexMutationRecorder(path, args)
		s.mu.Lock()
		s.successCount++
		s.mu.Unlock()
		return true
	}
	body, _ := json.Marshal(map[string]interface{}{
		"path":   path,
		"args":   args,
		"format": "json",
	})
	req, err := http.NewRequest(http.MethodPost, s.convexURL+"/api/mutation", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.authToken)
	res, err := s.client.Do(req)
	if err != nil {
		s.mu.Lock()
		s.failCount++
		s.lastError = err.Error()
		s.mu.Unlock()
		return false
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	s.mu.Lock()
	if res.StatusCode >= 400 {
		s.failCount++
		s.lastError = fmt.Sprintf("%s: HTTP %d", path, res.StatusCode)
		s.mu.Unlock()
		return false
	}
	s.successCount++
	s.mu.Unlock()
	return true
}

// SyncStatus exposes a snapshot of the syncer's state for /sync/status.
// Callers can compare successCount + skippedCount across ticks to see
// how effective dedup is — a quiet box should trend towards 100% skips.
type SyncStatus struct {
	Enabled      bool      `json:"enabled"`
	ConvexURL    string    `json:"convexUrl,omitempty"`
	DeviceID     string    `json:"deviceId,omitempty"`
	LastSyncAt   time.Time `json:"lastSyncAt,omitempty"`
	SuccessCount int       `json:"successCount"`
	SkippedCount int       `json:"skippedCount"`
	FailCount    int       `json:"failCount"`
	LastError    string    `json:"lastError,omitempty"`
}

func (s *HTTPServer) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if globalConvexSync == nil {
		writeJSON(w, http.StatusOK, SyncStatus{Enabled: false})
		return
	}
	globalConvexSync.mu.Lock()
	defer globalConvexSync.mu.Unlock()
	writeJSON(w, http.StatusOK, SyncStatus{
		Enabled:      true,
		ConvexURL:    globalConvexSync.convexURL,
		DeviceID:     globalConvexSync.deviceID,
		LastSyncAt:   globalConvexSync.lastSyncAt,
		SuccessCount: globalConvexSync.successCount,
		FailCount:    globalConvexSync.failCount,
		LastError:    globalConvexSync.lastError,
	})
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
