package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yaver-io/agent/testkit"
)

type FinalizeStatus string

const (
	FinalizeQueued    FinalizeStatus = "queued"
	FinalizeRunning   FinalizeStatus = "running"
	FinalizeSatisfied FinalizeStatus = "satisfied"
	FinalizeBlocked   FinalizeStatus = "blocked"
	FinalizeStopped   FinalizeStatus = "stopped"
	FinalizeFailed    FinalizeStatus = "failed"
)

type FinalizeRun struct {
	ID              string         `json:"id"`
	Objective       string         `json:"objective"`
	Runner          string         `json:"runner,omitempty"`
	Model           string         `json:"model,omitempty"`
	Mode            string         `json:"mode,omitempty"`
	WorkDir         string         `json:"workDir,omitempty"`
	TaskID          string         `json:"taskId,omitempty"`
	Status          FinalizeStatus `json:"status"`
	CreatedAt       string         `json:"createdAt"`
	UpdatedAt       string         `json:"updatedAt"`
	LastKickAt      string         `json:"lastKickAt,omitempty"`
	LastValidateAt  string         `json:"lastValidateAt,omitempty"`
	Iteration       int            `json:"iteration"`
	MaxIterations   int            `json:"maxIterations"`
	MaxWallClockMin int            `json:"maxWallClockMin,omitempty"`
	KickIntervalSec int            `json:"kickIntervalSec"`
	TestCommands    []string       `json:"testCommands,omitempty"`
	TestkitRoot     string         `json:"testkitRoot,omitempty"`
	InferTest       bool           `json:"inferTest,omitempty"`
	LastValidation  string         `json:"lastValidation,omitempty"`
	LastError       string         `json:"lastError,omitempty"`
	History         []string       `json:"history,omitempty"`
}

type FinalizeStartRequest struct {
	Objective       string   `json:"objective"`
	Runner          string   `json:"runner,omitempty"`
	Model           string   `json:"model,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	WorkDir         string   `json:"workDir,omitempty"`
	TaskID          string   `json:"taskId,omitempty"`
	MaxIterations   int      `json:"maxIterations,omitempty"`
	MaxWallClockMin int      `json:"maxWallClockMin,omitempty"`
	KickIntervalSec int      `json:"kickIntervalSec,omitempty"`
	TestCommands    []string `json:"testCommands,omitempty"`
	TestkitRoot     string   `json:"testkitRoot,omitempty"`
	InferTest       bool     `json:"inferTest,omitempty"`
}

type FinalizeManager struct {
	mu        sync.Mutex
	runs      map[string]*FinalizeRun
	taskMgr   *TaskManager
	path      string
	placement TaskIngressPlacementConfig
}

func NewFinalizeManager(taskMgr *TaskManager) *FinalizeManager {
	dir, _ := ConfigDir()
	m := &FinalizeManager{
		runs:    map[string]*FinalizeRun{},
		taskMgr: taskMgr,
		path:    filepath.Join(dir, "finalize-runs.json"),
	}
	m.load()
	return m
}

func (m *FinalizeManager) SetPlacementConfig(cfg TaskIngressPlacementConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.placement = cfg
}

func (m *FinalizeManager) load() {
	if strings.TrimSpace(m.path) == "" {
		return
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		return
	}
	var runs map[string]*FinalizeRun
	if err := json.Unmarshal(data, &runs); err != nil {
		return
	}
	m.runs = runs
}

func (m *FinalizeManager) saveLocked() error {
	if m.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.runs, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *FinalizeManager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	SupervisedGo("finalize", time.Minute, false, func(ctx context.Context) error {
		m.Tick(ctx)
		return nil
	})
}

func (m *FinalizeManager) StartRun(req FinalizeStartRequest) (*FinalizeRun, error) {
	if strings.TrimSpace(req.Objective) == "" && strings.TrimSpace(req.TaskID) == "" {
		return nil, fmt.Errorf("objective or taskId required")
	}
	if req.MaxIterations <= 0 {
		req.MaxIterations = 20
	}
	if req.KickIntervalSec <= 0 {
		req.KickIntervalSec = 90
	}
	if req.MaxWallClockMin <= 0 {
		req.MaxWallClockMin = 360
	}
	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" && m.taskMgr != nil {
		workDir = m.taskMgr.workDir
	}
	if workDir != "" {
		if abs, err := filepath.Abs(workDir); err == nil {
			workDir = abs
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	run := &FinalizeRun{
		ID:              "fin-" + uuid.New().String()[:8],
		Objective:       strings.TrimSpace(req.Objective),
		Runner:          normalizeRunnerID(req.Runner),
		Model:           strings.TrimSpace(req.Model),
		Mode:            strings.TrimSpace(req.Mode),
		WorkDir:         workDir,
		TaskID:          strings.TrimSpace(req.TaskID),
		Status:          FinalizeQueued,
		CreatedAt:       now,
		UpdatedAt:       now,
		MaxIterations:   req.MaxIterations,
		MaxWallClockMin: req.MaxWallClockMin,
		KickIntervalSec: req.KickIntervalSec,
		TestCommands:    cleanStringSlice(req.TestCommands),
		TestkitRoot:     strings.TrimSpace(req.TestkitRoot),
		InferTest:       req.InferTest,
	}
	if run.InferTest && len(run.TestCommands) == 0 {
		if _, cmd, _ := DetectTestFramework(firstNonEmpty(run.WorkDir, ".")); strings.TrimSpace(cmd) != "" {
			run.TestCommands = []string{cmd}
			run.addHistory("inferred test command: " + cmd)
		}
	}
	if len(run.TestCommands) == 0 && run.TestkitRoot == "" {
		run.addHistory("no validation configured; pass --test-cmd, --testkit-root, or --infer-test for closed-loop satisfaction")
	}
	m.mu.Lock()
	m.runs[run.ID] = run
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	_ = ensureFinalizeSystemdTimer()
	go m.Tick(context.Background())
	return run, nil
}

func cleanStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if v := strings.TrimSpace(s); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (r *FinalizeRun) addHistory(s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	r.History = append(r.History, time.Now().UTC().Format(time.RFC3339)+" "+s)
	if len(r.History) > 100 {
		r.History = r.History[len(r.History)-100:]
	}
}

func (m *FinalizeManager) ListRuns() []*FinalizeRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*FinalizeRun, 0, len(m.runs))
	for _, r := range m.runs {
		cp := *r
		cp.TestCommands = append([]string{}, r.TestCommands...)
		cp.History = append([]string{}, r.History...)
		out = append(out, &cp)
	}
	return out
}

func (m *FinalizeManager) GetRun(id string) (*FinalizeRun, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return nil, false
	}
	cp := *r
	cp.TestCommands = append([]string{}, r.TestCommands...)
	cp.History = append([]string{}, r.History...)
	return &cp, true
}

func (m *FinalizeManager) StopRun(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return fmt.Errorf("finalize run not found: %s", id)
	}
	r.Status = FinalizeStopped
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	r.addHistory("stopped")
	return m.saveLocked()
}

func (m *FinalizeManager) Tick(ctx context.Context) {
	if m == nil || m.taskMgr == nil {
		return
	}
	m.mu.Lock()
	ids := make([]string, 0, len(m.runs))
	for id, r := range m.runs {
		if r.Status == FinalizeQueued || r.Status == FinalizeRunning {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.tickRun(ctx, id)
	}
}

func (m *FinalizeManager) tickRun(ctx context.Context, id string) {
	m.mu.Lock()
	r := m.runs[id]
	if r == nil {
		m.mu.Unlock()
		return
	}
	if r.Status != FinalizeQueued && r.Status != FinalizeRunning {
		m.mu.Unlock()
		return
	}
	if r.MaxWallClockMin > 0 {
		if created, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil && time.Since(created) > time.Duration(r.MaxWallClockMin)*time.Minute {
			r.Status = FinalizeBlocked
			r.LastError = "max wall-clock reached"
			r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			r.addHistory(r.LastError)
			_ = m.saveLocked()
			m.mu.Unlock()
			return
		}
	}
	snap := *r
	m.mu.Unlock()

	if snap.TaskID == "" {
		if deferral, deferred := m.deferTaskToCloudWorkspace(ctx, snap.Runner, snap.WorkDir); deferred {
			m.mu.Lock()
			if rr := m.runs[id]; rr != nil {
				rr.TaskID = deferral.PendingTaskID
				rr.Status = FinalizeBlocked
				rr.LastError = finalizeCloudDeferralText(deferral)
				rr.addHistory(rr.LastError)
				rr.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
				_ = m.saveLocked()
			}
			m.mu.Unlock()
			return
		}
		task, err := m.taskMgr.CreateTaskWithOptions("Finalize: "+truncateForTitle(snap.Objective), finalizeInitialPrompt(&snap), snap.Model, "finalize", snap.Runner, "", nil, TaskCreateOptions{
			WorkDir: snap.WorkDir,
		})
		m.mu.Lock()
		if rr := m.runs[id]; rr != nil {
			if err != nil {
				rr.Status = FinalizeFailed
				rr.LastError = err.Error()
				rr.addHistory("create task failed: " + err.Error())
			} else {
				rr.TaskID = task.ID
				rr.Status = FinalizeRunning
				rr.LastKickAt = time.Now().UTC().Format(time.RFC3339)
				rr.addHistory("created task " + task.ID)
			}
			rr.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = m.saveLocked()
		}
		m.mu.Unlock()
		return
	}

	task, ok := m.taskMgr.GetTask(snap.TaskID)
	if !ok {
		m.updateRun(id, FinalizeFailed, "task not found: "+snap.TaskID, "")
		return
	}
	status := task.Status
	if status == TaskStatusRunning || status == TaskStatusQueued {
		m.updateRun(id, FinalizeRunning, "", "task still running")
		return
	}
	if status == TaskStatusFailed || status == TaskStatusStopped {
		if !m.shouldKick(&snap) {
			return
		}
		m.kickRun(id, "The previous attempt stopped or failed. Inspect the errors, fix the root cause, and continue until validation passes.")
		return
	}
	if status != TaskStatusFinished {
		return
	}

	ok, report := m.validateRun(ctx, &snap)
	m.mu.Lock()
	if rr := m.runs[id]; rr != nil {
		rr.LastValidateAt = time.Now().UTC().Format(time.RFC3339)
		rr.LastValidation = report
		rr.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if ok {
			rr.Status = FinalizeSatisfied
			rr.addHistory("validation satisfied")
		} else if rr.Iteration >= rr.MaxIterations {
			rr.Status = FinalizeBlocked
			rr.LastError = "max iterations reached without satisfying validation"
			rr.addHistory(rr.LastError)
		}
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	if !ok {
		m.kickRun(id, finalizeFollowupPrompt(report))
	}
}

func (m *FinalizeManager) deferTaskToCloudWorkspace(ctx context.Context, runner, workDir string) (*taskIngressCloudDeferral, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	cfg := m.placement
	m.mu.Unlock()
	if strings.TrimSpace(workDir) != "" {
		cfg.WorkDir = workDir
	}
	deferral, deferred, err := deferIngressTaskToCloudWorkspace(ctx, cfg, "finalize", "unknown", runner)
	if err != nil && !deferred {
		log.Printf("[placement] finalize preview skipped before task create: %v", err)
		return nil, false
	}
	if !deferred {
		return nil, false
	}
	if err != nil {
		pendingTaskID := ""
		if deferral != nil {
			pendingTaskID = deferral.PendingTaskID
		}
		log.Printf("[placement] finalize cloud deferral failed for %s: %v", pendingTaskID, err)
		if deferral == nil {
			deferral = &taskIngressCloudDeferral{}
		}
		deferral.Blocker = err.Error()
		return deferral, true
	}
	return deferral, true
}

func finalizeCloudDeferralText(deferral *taskIngressCloudDeferral) string {
	if deferral == nil {
		return "Cloud Workspace is selected for this finalize run, but the handoff is not ready yet."
	}
	if blocker := strings.TrimSpace(deferral.Blocker); blocker != "" {
		return "Cloud Workspace is selected for this finalize run, but it needs attention first: " + blocker
	}
	target := ""
	if deferral.Placement != nil {
		target = strings.TrimSpace(deferral.Placement.TargetDeviceID)
	}
	if target == "" {
		target = "Cloud Workspace"
	}
	return "Cloud Workspace is selected for this finalize run. A pending handoff was queued for " + target + ", so this relay will not run the finalize task while the workspace wakes."
}

func (m *FinalizeManager) shouldKick(r *FinalizeRun) bool {
	if r == nil {
		return false
	}
	if r.Iteration >= r.MaxIterations {
		return false
	}
	if r.LastKickAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, r.LastKickAt)
	if err != nil {
		return true
	}
	return time.Since(t) >= time.Duration(r.KickIntervalSec)*time.Second
}

func (m *FinalizeManager) kickRun(id, prompt string) {
	m.mu.Lock()
	r := m.runs[id]
	if r == nil {
		m.mu.Unlock()
		return
	}
	if !m.shouldKick(r) {
		m.mu.Unlock()
		return
	}
	taskID := r.TaskID
	r.Iteration++
	r.LastKickAt = time.Now().UTC().Format(time.RFC3339)
	r.UpdatedAt = r.LastKickAt
	r.Status = FinalizeRunning
	r.addHistory(fmt.Sprintf("kick %d queued", r.Iteration))
	_ = m.saveLocked()
	m.mu.Unlock()

	_, err := m.taskMgr.ResumeTaskWithOptions(taskID, prompt, nil, TaskResumeOptions{
		RunnerID: r.Runner,
		Model:    r.Model,
		Mode:     r.Mode,
	})
	if err != nil {
		m.updateRun(id, FinalizeFailed, "resume failed: "+err.Error(), "")
	}
}

func (m *FinalizeManager) updateRun(id string, status FinalizeStatus, errText, hist string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runs[id]
	if r == nil {
		return
	}
	if status != "" {
		r.Status = status
	}
	if errText != "" {
		r.LastError = errText
	}
	if hist != "" {
		r.addHistory(hist)
	}
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_ = m.saveLocked()
}

func (m *FinalizeManager) validateRun(ctx context.Context, r *FinalizeRun) (bool, string) {
	if r == nil {
		return false, "no finalize run"
	}
	if len(r.TestCommands) == 0 && strings.TrimSpace(r.TestkitRoot) == "" {
		return false, "no validation configured; cannot mark satisfied"
	}
	var reports []string
	for _, cmd := range r.TestCommands {
		ok, out := runFinalizeCommand(ctx, r.WorkDir, cmd)
		reports = append(reports, "$ "+cmd+"\n"+out)
		if !ok {
			return false, strings.Join(reports, "\n\n")
		}
	}
	if root := strings.TrimSpace(r.TestkitRoot); root != "" {
		if !filepath.IsAbs(root) && strings.TrimSpace(r.WorkDir) != "" {
			root = filepath.Join(r.WorkDir, root)
		}
		ok, out := runFinalizeTestkit(ctx, root)
		reports = append(reports, out)
		if !ok {
			return false, strings.Join(reports, "\n\n")
		}
	}
	return true, strings.Join(reports, "\n\n")
}

func runFinalizeCommand(ctx context.Context, workDir, command string) (bool, string) {
	if strings.TrimSpace(command) == "" {
		return true, ""
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := osexec.CommandContext(cctx, "sh", "-lc", command)
	if strings.TrimSpace(workDir) != "" {
		cmd.Dir = workDir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if len(out) > 20000 {
		out = out[len(out)-20000:]
	}
	if err != nil {
		return false, strings.TrimSpace(out + "\n" + err.Error())
	}
	return true, out
}

func runFinalizeTestkit(ctx context.Context, root string) (bool, string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, err.Error()
	}
	specs, err := testkit.DiscoverSpecs(abs)
	if err != nil {
		return false, "testkit discover: " + err.Error()
	}
	if len(specs) == 0 {
		return false, "testkit discover: no specs found in " + abs
	}
	cctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	suite := testkit.RunSuite(cctx, specs, testkit.RunOptions{}, 1)
	total, passed, failed := suite.Counts()
	report := fmt.Sprintf("testkit %s: total=%d passed=%d failed=%d", abs, total, passed, failed)
	if !suite.Passed() {
		for _, r := range suite.Results {
			if r != nil && !r.Passed {
				report += "\nfailed: " + r.Spec.Name
				if r.Err != nil {
					report += ": " + r.Err.Error()
				}
				break
			}
		}
		return false, report
	}
	return true, report
}

func finalizeInitialPrompt(r *FinalizeRun) string {
	return strings.TrimSpace(`You are running under Yaver finalize mode.

Objective:
` + r.Objective + `

Keep implementing until the objective is actually satisfied. Do not stop just because a partial implementation is present. When you think you are done, run the available validation commands or inspect the testkit specs if relevant. If validation fails, fix the failure and continue. If you hit a menu or a 1/2/3 choice, pick the safe continue/proceed option only when it is clearly non-destructive; otherwise ask through yaver_ask_user.`)
}

func finalizeFollowupPrompt(validation string) string {
	return strings.TrimSpace(`Finalize mode validation did not pass.

Validation output:
` + validation + `

Continue from the current work. Fix the failing validation, run the relevant checks again, and only stop when the checks pass. If a browser, simulator, emulator, or Redroid test is relevant, use the available Yaver/testkit/browser/android tools to validate the real UI instead of relying on static reasoning.`)
}

func truncateForTitle(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

// ensureFinalizeSystemdTimer is build-tagged: the real (systemd) impl lives in
// finalize_systemd.go (//go:build !windows) and a no-op stub in
// finalize_systemd_windows.go — because it references systemd helpers
// (systemdUserUnitDir/systemdExecLine) that don't exist on windows.

func (s *HTTPServer) handleFinalize(w http.ResponseWriter, r *http.Request) {
	if s.finalizeMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "finalize manager unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "runs": s.finalizeMgr.ListRuns()})
	case http.MethodPost:
		var req FinalizeStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		run, err := s.finalizeMgr.StartRun(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]any{"ok": true, "run": run})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleFinalizeByID(w http.ResponseWriter, r *http.Request) {
	if s.finalizeMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "finalize manager unavailable")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/finalize/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "finalize id required")
		return
	}
	if id == "all" && action == "tick" {
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		s.finalizeMgr.Tick(r.Context())
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "runs": s.finalizeMgr.ListRuns()})
		return
	}
	switch action {
	case "":
		run, ok := s.finalizeMgr.GetRun(id)
		if !ok {
			jsonError(w, http.StatusNotFound, "finalize run not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "run": run})
	case "stop":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		if err := s.finalizeMgr.StopRun(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]any{"ok": true})
	case "tick":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		s.finalizeMgr.tickRun(r.Context(), id)
		run, _ := s.finalizeMgr.GetRun(id)
		jsonReply(w, http.StatusOK, map[string]any{"ok": true, "run": run})
	default:
		jsonError(w, http.StatusNotFound, "unknown finalize action")
	}
}

func finalizeLocalTick() error {
	if err := ensureDaemonAlive(); err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return fmt.Errorf("not authenticated")
	}
	req, _ := http.NewRequest(http.MethodPost, localAgentBaseURL()+"/finalize/all/tick", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("tick failed: HTTP %d", resp.StatusCode)
	}
	return nil
}
