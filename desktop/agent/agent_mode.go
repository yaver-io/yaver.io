package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type AgentGraphStatus string

const (
	AgentGraphQueued    AgentGraphStatus = "queued"
	AgentGraphRunning   AgentGraphStatus = "running"
	AgentGraphCompleted AgentGraphStatus = "completed"
	AgentGraphFailed    AgentGraphStatus = "failed"
	AgentGraphStopped   AgentGraphStatus = "stopped"
)

type AgentNodeStatus string

const (
	AgentNodePending   AgentNodeStatus = "pending"
	AgentNodeRunning   AgentNodeStatus = "running"
	AgentNodeCompleted AgentNodeStatus = "completed"
	AgentNodeFailed    AgentNodeStatus = "failed"
	AgentNodeBlocked   AgentNodeStatus = "blocked"
	AgentNodeStopped   AgentNodeStatus = "stopped"
)

type AgentNodeKind string

const (
	AgentNodeChat      AgentNodeKind = "chat"
	AgentNodeAutodev   AgentNodeKind = "autodev"
	AgentNodeAutoIdeas AgentNodeKind = "autoideas"
	AgentNodeAutotest  AgentNodeKind = "autotest"
)

type AgentGraphNodeSpec struct {
	ID              string        `json:"id"`
	Title           string        `json:"title"`
	Kind            AgentNodeKind `json:"kind"`
	Prompt          string        `json:"prompt,omitempty"`
	DependsOn       []string      `json:"dependsOn,omitempty"`
	Runner          string        `json:"runner,omitempty"`
	Model           string        `json:"model,omitempty"`
	Engine          string        `json:"engine,omitempty"`
	WorkDir         string        `json:"workDir,omitempty"`
	Project         string        `json:"project,omitempty"`
	Target          string        `json:"target,omitempty"`
	Load            string        `json:"load,omitempty"`
	Hours           string        `json:"hours,omitempty"`
	MaxIterations   int           `json:"maxIterations,omitempty"`
	NoAutotest      bool          `json:"noAutotest,omitempty"`
	PreferredDevice string        `json:"preferredDevice,omitempty"`
	AllowedDevices  []string      `json:"allowedDevices,omitempty"`
	AllowedRunners  []string      `json:"allowedRunners,omitempty"`
}

type AgentGraphNodeState struct {
	Spec       AgentGraphNodeSpec  `json:"spec"`
	Status     AgentNodeStatus     `json:"status"`
	TaskID     string              `json:"taskId,omitempty"`
	Summary    string              `json:"summary,omitempty"`
	Error      string              `json:"error,omitempty"`
	StartedAt  string              `json:"startedAt,omitempty"`
	FinishedAt string              `json:"finishedAt,omitempty"`
	Placement  *AgentNodePlacement `json:"placement,omitempty"`
}

type AgentGraphRun struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	WorkDir     string                 `json:"workDir"`
	Template    string                 `json:"template,omitempty"`
	Prompt      string                 `json:"prompt,omitempty"`
	Runner      string                 `json:"runner,omitempty"`
	Model       string                 `json:"model,omitempty"`
	MaxParallel int                    `json:"maxParallel"`
	Status      AgentGraphStatus       `json:"status"`
	Summary     string                 `json:"summary,omitempty"`
	CreatedAt   string                 `json:"createdAt"`
	StartedAt   string                 `json:"startedAt,omitempty"`
	FinishedAt  string                 `json:"finishedAt,omitempty"`
	Nodes       []*AgentGraphNodeState `json:"nodes"`
}

type AgentGraphCreateRequest struct {
	Name            string               `json:"name"`
	WorkDir         string               `json:"workDir"`
	Prompt          string               `json:"prompt,omitempty"`
	Template        string               `json:"template,omitempty"`
	Runner          string               `json:"runner,omitempty"`
	Model           string               `json:"model,omitempty"`
	MaxParallel     int                  `json:"maxParallel,omitempty"`
	PreferredDevice string               `json:"preferredDevice,omitempty"`
	AllowedDevices  []string             `json:"allowedDevices,omitempty"`
	AllowedRunners  []string             `json:"allowedRunners,omitempty"`
	Nodes           []AgentGraphNodeSpec `json:"nodes,omitempty"`
}

type AgentGraphManager struct {
	mu      sync.RWMutex
	runs    map[string]*AgentGraphRun
	taskMgr *TaskManager
	cancel  map[string]context.CancelFunc
}

func NewAgentGraphManager(taskMgr *TaskManager) *AgentGraphManager {
	gm := &AgentGraphManager{
		runs:    map[string]*AgentGraphRun{},
		taskMgr: taskMgr,
		cancel:  map[string]context.CancelFunc{},
	}
	gm.load()
	return gm
}

func agentGraphsStorePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "agent-graphs.json")
	return p, nil
}

func (gm *AgentGraphManager) load() {
	p, err := agentGraphsStorePath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var runs map[string]*AgentGraphRun
	if err := json.Unmarshal(data, &runs); err != nil {
		log.Printf("[agent-graphs] load: %v", err)
		return
	}
	for _, run := range runs {
		if run.Status == AgentGraphRunning {
			run.Status = AgentGraphFailed
			run.Summary = "agent graph interrupted by process restart"
			run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		}
		for _, node := range run.Nodes {
			if node.Status == AgentNodeRunning {
				node.Status = AgentNodeFailed
				node.Error = "node interrupted by process restart"
				node.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			}
		}
	}
	gm.runs = runs
	_ = gm.saveLocked()
}

func (gm *AgentGraphManager) saveLocked() error {
	p, err := agentGraphsStorePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(gm.runs, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (gm *AgentGraphManager) persist() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	_ = gm.saveLocked()
}

func (gm *AgentGraphManager) ListRuns() []*AgentGraphRun {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	out := make([]*AgentGraphRun, 0, len(gm.runs))
	for _, run := range gm.runs {
		cp := *run
		cp.Nodes = make([]*AgentGraphNodeState, len(run.Nodes))
		for i, node := range run.Nodes {
			nc := *node
			cp.Nodes[i] = &nc
		}
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (gm *AgentGraphManager) GetRun(id string) (*AgentGraphRun, bool) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	run, ok := gm.runs[id]
	if !ok {
		return nil, false
	}
	cp := *run
	cp.Nodes = make([]*AgentGraphNodeState, len(run.Nodes))
	for i, node := range run.Nodes {
		nc := *node
		cp.Nodes[i] = &nc
	}
	return &cp, true
}

func (gm *AgentGraphManager) CreateRun(req AgentGraphCreateRequest) (*AgentGraphRun, error) {
	if strings.TrimSpace(req.WorkDir) == "" {
		return nil, errors.New("workDir required")
	}
	if _, err := os.Stat(req.WorkDir); err != nil {
		return nil, fmt.Errorf("workDir does not exist: %w", err)
	}
	nodes := req.Nodes
	if len(nodes) == 0 {
		nodes = buildAgentGraphTemplate(req)
	}
	if len(nodes) == 0 {
		return nil, errors.New("no graph nodes provided")
	}
	if req.MaxParallel <= 0 {
		req.MaxParallel = 2
	}
	if req.MaxParallel > 6 {
		req.MaxParallel = 6
	}
	normalized, err := normalizeAgentNodes(req.WorkDir, req.Runner, req.Model, req.AllowedRunners, nodes)
	if err != nil {
		return nil, err
	}
	if err := validateAgentGraphNodes(normalized); err != nil {
		return nil, err
	}
	placements := planGraphPlacements(req, normalized)

	now := time.Now().UTC().Format(time.RFC3339)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = filepath.Base(req.WorkDir) + "-agent"
	}
	run := &AgentGraphRun{
		ID:          uuid.New().String()[:8],
		Name:        name,
		WorkDir:     req.WorkDir,
		Template:    req.Template,
		Prompt:      req.Prompt,
		Runner:      req.Runner,
		Model:       req.Model,
		MaxParallel: req.MaxParallel,
		Status:      AgentGraphQueued,
		CreatedAt:   now,
	}
	for i, spec := range normalized {
		run.Nodes = append(run.Nodes, &AgentGraphNodeState{
			Spec:      spec,
			Status:    AgentNodePending,
			Placement: placements[i],
		})
	}

	gm.mu.Lock()
	gm.runs[run.ID] = run
	if err := gm.saveLocked(); err != nil {
		gm.mu.Unlock()
		return nil, err
	}
	gm.mu.Unlock()

	gm.StartRun(run.ID)
	cp, _ := gm.GetRun(run.ID)
	return cp, nil
}

func buildAgentGraphTemplate(req AgentGraphCreateRequest) []AgentGraphNodeSpec {
	prompt := strings.TrimSpace(req.Prompt)
	workDir := req.WorkDir
	project := filepath.Base(workDir)
	template := strings.TrimSpace(req.Template)
	if template == "" {
		template = "full"
	}
	switch template {
	case "full", "agent", "default":
		return []AgentGraphNodeSpec{
			{
				ID:      "plan",
				Title:   "Plan Slice",
				Kind:    AgentNodeChat,
				Prompt:  "Analyze the repo and task, then produce a concise implementation plan with likely files, risks, and a recommended slice boundary.\n\nTask:\n" + prompt,
				WorkDir: workDir,
				Project: project,
			},
			{
				ID:        "implement",
				Title:     "Implement",
				Kind:      AgentNodeChat,
				Prompt:    prompt,
				WorkDir:   workDir,
				Project:   project,
				DependsOn: []string{"plan"},
			},
			{
				ID:        "verify",
				Title:     "Verify And Synthesize",
				Kind:      AgentNodeChat,
				Prompt:    "Inspect the repo state after implementation, run or describe the most relevant validation available in this workspace, and summarize any remaining risks or follow-up work needed before shipping.",
				WorkDir:   workDir,
				Project:   project,
				DependsOn: []string{"implement"},
			},
		}
	case "ship":
		return []AgentGraphNodeSpec{
			{ID: "dev", Title: "Auto Dev", Kind: AgentNodeAutodev, Prompt: prompt, WorkDir: workDir, Project: project, MaxIterations: 2},
			{ID: "test", Title: "Auto Test", Kind: AgentNodeAutotest, Prompt: "Run a focused regression pass for the changes produced by this graph and fix any breakage before reporting done.", WorkDir: workDir, Project: project, DependsOn: []string{"dev"}, MaxIterations: 1},
		}
	default:
		return nil
	}
}

func normalizeAgentNodes(defaultWorkDir, defaultRunner, defaultModel string, defaultAllowedRunners []string, nodes []AgentGraphNodeSpec) ([]AgentGraphNodeSpec, error) {
	out := make([]AgentGraphNodeSpec, 0, len(nodes))
	seen := map[string]bool{}
	for i, node := range nodes {
		node.ID = strings.TrimSpace(node.ID)
		if node.ID == "" {
			node.ID = fmt.Sprintf("node-%d", i+1)
		}
		if !isSafeGraphNodeID(node.ID) {
			return nil, fmt.Errorf("invalid node id %q: only letters, digits, '-', '_', '.' allowed (max 64 chars)", node.ID)
		}
		if seen[node.ID] {
			return nil, fmt.Errorf("duplicate node id: %s", node.ID)
		}
		seen[node.ID] = true
		if node.WorkDir == "" {
			node.WorkDir = defaultWorkDir
		}
		if node.Project == "" {
			node.Project = filepath.Base(node.WorkDir)
		}
		if node.Runner == "" && defaultRunner != "" {
			node.Runner = defaultRunner
		}
		if node.Model == "" && defaultModel != "" {
			node.Model = defaultModel
		}
		if len(node.AllowedRunners) == 0 && len(defaultAllowedRunners) > 0 {
			node.AllowedRunners = append([]string{}, defaultAllowedRunners...)
		}
		if node.Title == "" {
			node.Title = strings.Title(string(node.Kind))
		}
		node = applyAgentNodeExecutionPolicy(node)
		out = append(out, node)
	}
	return out, nil
}

// isSafeGraphNodeID blocks path-traversal sequences before the node ID is
// interpolated into on-disk slice worktree paths by graphNodeWorktreePath.
// Only bytes that are safe as a single filesystem path segment are accepted.
func isSafeGraphNodeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	if id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func applyAgentNodeExecutionPolicy(node AgentGraphNodeSpec) AgentGraphNodeSpec {
	if strings.TrimSpace(node.Runner) != "" {
		return node
	}

	var candidates [][2]string
	fallbackRunner := "claude-code"
	fallbackModel := ""
	switch node.Kind {
	case AgentNodeChat:
		candidates = [][2]string{{"claude", "claude-opus-4-6"}, {"codex", ""}, {"opencode", ""}, {"goose", ""}, {"aider", ""}, {"ollama", "qwen2.5-coder:14b"}}
		fallbackModel = "claude-opus-4-6"
	case AgentNodeAutoIdeas:
		candidates = [][2]string{{"claude", "claude-sonnet-4-6"}, {"codex", ""}, {"opencode", ""}, {"goose", ""}, {"aider", ""}, {"ollama", "qwen2.5-coder:14b"}}
		fallbackModel = "claude-sonnet-4-6"
	case AgentNodeAutodev, AgentNodeAutotest:
		candidates = [][2]string{{"codex", ""}, {"claude", "claude-sonnet-4-6"}, {"aider-ollama", "ollama_chat/qwen2.5-coder:14b"}, {"aider", ""}, {"ollama", "qwen2.5-coder:14b"}, {"opencode", ""}}
		fallbackModel = "claude-sonnet-4-6"
	}

	if len(node.AllowedRunners) > 0 {
		candidates, fallbackRunner, fallbackModel = restrictRunnerCandidatesToAllowed(candidates, node.AllowedRunners)
	}

	node.Runner, node.Model = chooseReadyRunner(node.WorkDir, candidates, fallbackRunner, fallbackModel)
	if node.Runner == "claude" {
		node.Runner = "claude-code"
	}
	return node
}

// restrictRunnerCandidatesToAllowed filters the candidate runner list to
// only those in allowed, preserving the original priority order. If the
// intersection is empty, the first allowed runner becomes the fallback so
// the node never silently escapes the allowlist into a runner the user
// explicitly forbade (e.g. --allowed-runners ollama must never pick
// claude-code).
func restrictRunnerCandidatesToAllowed(candidates [][2]string, allowed []string) ([][2]string, string, string) {
	allow := map[string]bool{}
	normalizedAllowed := make([]string, 0, len(allowed))
	for _, a := range allowed {
		n := normalizeRunnerID(a)
		if n == "" {
			continue
		}
		if !allow[n] {
			normalizedAllowed = append(normalizedAllowed, n)
		}
		allow[n] = true
	}
	filtered := make([][2]string, 0, len(candidates))
	for _, c := range candidates {
		if allow[normalizeRunnerID(c[0])] {
			filtered = append(filtered, c)
		}
	}
	fallbackRunner := "claude-code"
	fallbackModel := ""
	if len(normalizedAllowed) > 0 {
		if normalizedAllowed[0] == "claude" {
			fallbackRunner = "claude-code"
		} else {
			fallbackRunner = normalizedAllowed[0]
		}
	}
	if len(filtered) > 0 {
		fallbackModel = filtered[0][1]
	}
	return filtered, fallbackRunner, fallbackModel
}

func chooseReadyRunner(workDir string, candidates [][2]string, fallbackRunner, fallbackModel string) (string, string) {
	for _, candidate := range candidates {
		runnerID := candidate[0]
		cfg := GetRunnerConfig(normalizeRunnerID(runnerID))
		if cfg.RunnerID == "" || cfg.Command == "" {
			continue
		}
		if _, err := osexec.LookPath(cfg.Command); err != nil {
			continue
		}
		if status := DetectRunnerRuntimeStatus(cfg, workDir); !status.Ready {
			continue
		}
		return runnerID, candidate[1]
	}
	return fallbackRunner, fallbackModel
}

func validateAgentGraphNodes(nodes []AgentGraphNodeSpec) error {
	if len(nodes) == 0 {
		return errors.New("graph is empty")
	}
	index := map[string]AgentGraphNodeSpec{}
	for _, node := range nodes {
		switch node.Kind {
		case AgentNodeChat, AgentNodeAutodev, AgentNodeAutoIdeas, AgentNodeAutotest:
		default:
			return fmt.Errorf("unknown node kind %q", node.Kind)
		}
		index[node.ID] = node
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var dfs func(string) error
	dfs = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("dependency cycle at %s", id)
		}
		visiting[id] = true
		node := index[id]
		for _, dep := range node.DependsOn {
			if _, ok := index[dep]; !ok {
				return fmt.Errorf("node %s depends on missing node %s", id, dep)
			}
			if err := dfs(dep); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, node := range nodes {
		if err := dfs(node.ID); err != nil {
			return err
		}
	}
	return nil
}

func (gm *AgentGraphManager) StartRun(id string) error {
	gm.mu.Lock()
	run, ok := gm.runs[id]
	if !ok {
		gm.mu.Unlock()
		return errors.New("graph not found")
	}
	if run.Status == AgentGraphRunning {
		gm.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	gm.cancel[id] = cancel
	run.Status = AgentGraphRunning
	run.StartedAt = time.Now().UTC().Format(time.RFC3339)
	run.FinishedAt = ""
	run.Summary = ""
	for _, node := range run.Nodes {
		node.Status = AgentNodePending
		node.TaskID = ""
		node.Error = ""
		node.Summary = ""
		node.StartedAt = ""
		node.FinishedAt = ""
	}
	if err := gm.saveLocked(); err != nil {
		gm.mu.Unlock()
		return err
	}
	gm.mu.Unlock()

	go gm.runLoop(ctx, id)
	return nil
}

func (gm *AgentGraphManager) StopRun(id string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	run, ok := gm.runs[id]
	if !ok {
		return errors.New("graph not found")
	}
	if cancel := gm.cancel[id]; cancel != nil {
		cancel()
		delete(gm.cancel, id)
	}
	for _, node := range run.Nodes {
		if node.Status == AgentNodePending {
			node.Status = AgentNodeStopped
			node.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}
	run.Status = AgentGraphStopped
	run.Summary = "stopped by user"
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	return gm.saveLocked()
}

func (gm *AgentGraphManager) runLoop(ctx context.Context, id string) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			gm.finishRun(id, AgentGraphStopped, "stopped by user")
			return
		case <-ticker.C:
			if done := gm.tick(id, ctx); done {
				return
			}
		}
	}
}

func (gm *AgentGraphManager) tick(id string, ctx context.Context) bool {
	gm.mu.Lock()
	run, ok := gm.runs[id]
	if !ok {
		gm.mu.Unlock()
		return true
	}
	if run.Status != AgentGraphRunning {
		gm.mu.Unlock()
		return true
	}

	runningCount := 0
	ready := []string{}
	completed := 0
	failed := 0
	stopped := 0

	nodeIndex := map[string]*AgentGraphNodeState{}
	for _, node := range run.Nodes {
		nodeIndex[node.Spec.ID] = node
		switch node.Status {
		case AgentNodeRunning:
			runningCount++
		case AgentNodeCompleted:
			completed++
		case AgentNodeFailed, AgentNodeBlocked:
			failed++
		case AgentNodeStopped:
			stopped++
		}
	}

	for _, node := range run.Nodes {
		if node.Status != AgentNodePending {
			continue
		}
		blocked := false
		waiting := false
		for _, dep := range node.Spec.DependsOn {
			parent := nodeIndex[dep]
			switch parent.Status {
			case AgentNodeCompleted:
			case AgentNodeFailed, AgentNodeBlocked, AgentNodeStopped:
				blocked = true
			default:
				waiting = true
			}
		}
		if blocked {
			node.Status = AgentNodeBlocked
			node.Error = "blocked by failed dependency"
			node.FinishedAt = time.Now().UTC().Format(time.RFC3339)
			failed++
			continue
		}
		if !waiting {
			ready = append(ready, node.Spec.ID)
		}
	}

	if completed == len(run.Nodes) {
		run.Status = AgentGraphCompleted
		run.Summary = "all nodes completed"
		run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = gm.saveLocked()
		delete(gm.cancel, id)
		gm.mu.Unlock()
		return true
	}
	if runningCount == 0 && len(ready) == 0 && failed+stopped > 0 {
		run.Status = AgentGraphFailed
		run.Summary = "one or more nodes failed"
		run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = gm.saveLocked()
		delete(gm.cancel, id)
		gm.mu.Unlock()
		return true
	}

	slots := run.MaxParallel - runningCount
	if slots < 0 {
		slots = 0
	}
	startIDs := []string{}
	if slots > 0 {
		policyState := buildMeshPolicyState(run, nodeIndex)
		for _, nodeID := range ready {
			if slots == 0 {
				break
			}
			node := nodeIndex[nodeID]
			if !policyState.CanStart(node) {
				continue
			}
			node.Status = AgentNodeRunning
			node.StartedAt = time.Now().UTC().Format(time.RFC3339)
			startIDs = append(startIDs, nodeID)
			policyState.Reserve(node)
			slots--
		}
	}
	_ = gm.saveLocked()
	gm.mu.Unlock()

	for _, nodeID := range startIDs {
		go gm.executeNode(ctx, id, nodeID)
	}
	return false
}

func (gm *AgentGraphManager) executeNode(ctx context.Context, runID, nodeID string) {
	run, ok := gm.GetRun(runID)
	if !ok {
		return
	}
	var node *AgentGraphNodeState
	for _, n := range run.Nodes {
		if n.Spec.ID == nodeID {
			node = n
			break
		}
	}
	if node == nil {
		return
	}

	var summary string
	var err error
	switch node.Spec.Kind {
	case AgentNodeChat:
		summary, err = gm.executeChatNode(ctx, runID, node)
	case AgentNodeAutodev, AgentNodeAutoIdeas, AgentNodeAutotest:
		summary, err = gm.executeLoopNode(ctx, runID, node)
	default:
		err = fmt.Errorf("unsupported node kind %s", node.Spec.Kind)
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()
	run, ok = gm.runs[runID]
	if !ok {
		return
	}
	for _, current := range run.Nodes {
		if current.Spec.ID != nodeID {
			continue
		}
		current.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		current.Summary = summary
		if ctx.Err() != nil {
			current.Status = AgentNodeStopped
			current.Error = "stopped"
		} else if err != nil {
			current.Status = AgentNodeFailed
			current.Error = err.Error()
		} else {
			current.Status = AgentNodeCompleted
		}
		break
	}
	_ = gm.saveLocked()
}

func (gm *AgentGraphManager) executeChatNode(ctx context.Context, runID string, node *AgentGraphNodeState) (string, error) {
	if node.Placement != nil && node.Placement.DeviceID != "" && node.Placement.DeviceID != "local" {
		return gm.executeRemoteChatNode(ctx, runID, node)
	}
	workDir, contract, err := prepareGraphNodeSlice(ctx, runID, node.Spec, node.Placement)
	if err != nil {
		return "", err
	}
	prompt := strings.TrimSpace(node.Spec.Prompt)
	title := strings.TrimSpace(node.Spec.Title)
	if title == "" {
		title = prompt
	}
	if title == "" {
		title = "Agent Chat"
	}
	description := prompt
	if description == "" || description == title {
		description = node.Spec.Title
	}
	task, err := gm.taskMgr.CreateTaskWithOptions(title, description, node.Spec.Model, "agent-graph", node.Spec.Runner, "", nil, TaskCreateOptions{
		WorkDir:       workDir,
		SliceContract: contract,
	}, nil)
	if err != nil {
		return "", err
	}
	gm.attachTaskIDByTask(node.Spec.ID, task.ID)

	select {
	case <-ctx.Done():
		_ = gm.taskMgr.StopTask(task.ID)
		return "", ctx.Err()
	case <-task.doneCh:
	}
	final, ok := gm.taskMgr.GetTask(task.ID)
	if !ok {
		return "", errors.New("task disappeared")
	}
	switch final.Status {
	case TaskStatusFinished:
		if strings.TrimSpace(final.ResultText) != "" {
			return final.ResultText, nil
		}
		return "task completed", nil
	case TaskStatusStopped:
		return "", errors.New("task stopped")
	default:
		msg := strings.TrimSpace(final.ResultText)
		if msg == "" {
			msg = strings.TrimSpace(final.Output)
		}
		if msg == "" {
			msg = "task failed"
		}
		return "", errors.New(msg)
	}
}

func (gm *AgentGraphManager) attachTaskIDByTask(nodeID, taskID string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	for _, run := range gm.runs {
		for _, node := range run.Nodes {
			if node.Spec.ID == nodeID && node.Status == AgentNodeRunning {
				node.TaskID = taskID
				_ = gm.saveLocked()
				return
			}
		}
	}
}

func (gm *AgentGraphManager) executeLoopNode(ctx context.Context, runID string, node *AgentGraphNodeState) (string, error) {
	if node.Placement != nil && node.Placement.DeviceID != "" && node.Placement.DeviceID != "local" {
		return gm.executeRemoteLoopNode(ctx, runID, node)
	}
	workDir, contract, err := prepareGraphNodeSlice(ctx, runID, node.Spec, node.Placement)
	if err != nil {
		return "", err
	}
	loopState, err := buildGraphLoopState(runID, node.Spec, workDir, contract)
	if err != nil {
		return "", err
	}
	result := runLoopIteration(ctx, loopState, nil)
	if result == nil {
		return "", errors.New("loop returned no result")
	}
	switch result.Status {
	case "done", "completed":
		return strings.TrimSpace(result.Summary), nil
	case "stopped":
		return strings.TrimSpace(result.Summary), ctx.Err()
	default:
		msg := strings.TrimSpace(result.Summary)
		if msg == "" {
			msg = strings.TrimSpace(result.Err)
		}
		if msg == "" {
			msg = "loop node failed"
		}
		return msg, errors.New(msg)
	}
}

func (gm *AgentGraphManager) executeRemoteChatNode(ctx context.Context, runID string, node *AgentGraphNodeState) (string, error) {
	base, token, err := remoteAgentBaseAndToken(node.Placement.DeviceID)
	if err != nil {
		return "", err
	}
	workDir, contract, err := prepareGraphNodeSlice(ctx, runID, node.Spec, node.Placement)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(node.Spec.Title)
	if title == "" {
		title = strings.TrimSpace(node.Spec.Prompt)
	}
	var createResp struct {
		OK     bool   `json:"ok"`
		TaskID string `json:"taskId"`
	}
	err = remoteAgentJSON(ctx, base, token, http.MethodPost, "/tasks", map[string]interface{}{
		"title":         title,
		"description":   firstGraphNonEmpty(strings.TrimSpace(node.Spec.Prompt), node.Spec.Title),
		"model":         firstGraphNonEmpty(node.Placement.Model, node.Spec.Model),
		"runner":        firstGraphNonEmpty(node.Placement.Runner, node.Spec.Runner),
		"source":        "agent-graph",
		"workDir":       workDir,
		"sliceContract": contract,
	}, &createResp)
	if err != nil {
		return "", err
	}
	gm.attachTaskIDByTask(node.Spec.ID, createResp.TaskID)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = remoteAgentJSON(context.Background(), base, token, http.MethodPost, "/tasks/"+createResp.TaskID+"/stop", map[string]interface{}{}, nil)
			return "", ctx.Err()
		case <-ticker.C:
			var statusResp struct {
				OK   bool     `json:"ok"`
				Task TaskInfo `json:"task"`
			}
			if err := remoteAgentJSON(ctx, base, token, http.MethodGet, "/tasks/"+createResp.TaskID, nil, &statusResp); err != nil {
				return "", err
			}
			switch statusResp.Task.Status {
			case TaskStatusFinished:
				if strings.TrimSpace(statusResp.Task.ResultText) != "" {
					return statusResp.Task.ResultText, nil
				}
				return "task completed", nil
			case TaskStatusFailed:
				msg := strings.TrimSpace(statusResp.Task.ResultText)
				if msg == "" {
					msg = strings.TrimSpace(statusResp.Task.Output)
				}
				if msg == "" {
					msg = "remote task failed"
				}
				return "", errors.New(msg)
			case TaskStatusStopped:
				return "", errors.New("remote task stopped")
			}
		}
	}
}

func (gm *AgentGraphManager) executeRemoteLoopNode(ctx context.Context, runID string, node *AgentGraphNodeState) (string, error) {
	base, token, err := remoteAgentBaseAndToken(node.Placement.DeviceID)
	if err != nil {
		return "", err
	}
	workDir, contract, err := prepareGraphNodeSlice(ctx, runID, node.Spec, node.Placement)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(workDir) == "" {
		workDir = firstNonEmpty(resolveRemoteCurrentWorkDir(ctx, base, token), ".")
	}
	kind := "autodev"
	path := "/autodev/start"
	body := map[string]interface{}{
		"project":        graphRemoteProjectName(runID, node.Spec),
		"work_dir":       workDir,
		"hours":          firstGraphNonEmpty(node.Spec.Hours, "1"),
		"load":           firstGraphNonEmpty(node.Spec.Load, "lite"),
		"prompt":         firstGraphNonEmpty(strings.TrimSpace(node.Spec.Prompt), node.Spec.Title) + formatTaskSliceContract(contract),
		"runner":         firstGraphNonEmpty(node.Placement.Runner, node.Spec.Runner),
		"model":          firstGraphNonEmpty(node.Placement.Model, node.Spec.Model),
		"target":         node.Spec.Target,
		"max_iterations": max(1, node.Spec.MaxIterations),
		"no_autotest":    true,
	}
	switch node.Spec.Kind {
	case AgentNodeAutoIdeas:
		path = "/autoideas/start"
		body = map[string]interface{}{
			"project":     graphRemoteProjectName(runID, node.Spec),
			"work_dir":    workDir,
			"hours":       firstGraphNonEmpty(node.Spec.Hours, "1"),
			"load":        firstGraphNonEmpty(node.Spec.Load, "lite"),
			"prompt":      firstGraphNonEmpty(strings.TrimSpace(node.Spec.Prompt), node.Spec.Title) + formatTaskSliceContract(contract),
			"engine":      graphAutoIdeasEngine(firstGraphNonEmpty(node.Placement.Runner, node.Spec.Runner)),
			"runner":      firstGraphNonEmpty(node.Placement.Runner, node.Spec.Runner),
			"max_batches": max(1, node.Spec.MaxIterations),
		}
	case AgentNodeAutotest:
		kind = "autotest"
		body["kind"] = "autotest"
	default:
		body["kind"] = "autodev"
	}
	var createResp struct {
		OK       bool   `json:"ok"`
		LoopName string `json:"loop_name"`
		Output   string `json:"output"`
		WorkDir  string `json:"work_dir"`
	}
	if err := remoteAgentJSON(ctx, base, token, http.MethodPost, path, body, &createResp); err != nil {
		return "", err
	}
	loopName := createResp.LoopName
	if loopName == "" {
		loopName = graphRemoteProjectName(runID, node.Spec) + "-" + kind
	}
	if node.Spec.Kind == AgentNodeAutoIdeas {
		return waitForRemoteAutoIdeas(ctx, base, token, firstGraphNonEmpty(createResp.WorkDir, workDir), createResp.Output)
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = remoteAgentJSON(context.Background(), base, token, http.MethodPost, "/autodev/loops/"+loopName+"/stop", map[string]interface{}{}, nil)
			return "", ctx.Err()
		case <-ticker.C:
			var loopsResp struct {
				OK    bool             `json:"ok"`
				Loops []autodevLoopRow `json:"loops"`
			}
			if err := remoteAgentJSON(ctx, base, token, http.MethodGet, "/autodev/loops", nil, &loopsResp); err != nil {
				return "", err
			}
			for _, loop := range loopsResp.Loops {
				if loop.Name != loopName {
					continue
				}
				switch loop.Status {
				case string(LoopStatusRunning):
					goto waitNext
				case string(LoopStatusIdle):
					return strings.TrimSpace(loop.LastSummary), nil
				case string(LoopStatusStopped):
					return "", errors.New("remote loop stopped")
				case string(LoopStatusNeedsHuman), string(LoopStatusBudgetHit), string(LoopStatusStuck):
					msg := strings.TrimSpace(loop.LastSummary)
					if msg == "" {
						msg = "remote loop failed"
					}
					return msg, errors.New(msg)
				default:
					return strings.TrimSpace(loop.LastSummary), nil
				}
			}
		waitNext:
		}
	}
}

func waitForRemoteAutoIdeas(ctx context.Context, base, token, workDir, outputName string) (string, error) {
	outputName = strings.TrimSpace(outputName)
	if outputName == "" {
		outputName = "ideas.md"
	}
	fileTicker := time.NewTicker(3 * time.Second)
	defer fileTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-fileTicker.C:
			var ideasResp struct {
				OK    bool `json:"ok"`
				Items []struct {
					Title string `json:"title"`
				} `json:"items"`
				Raw string `json:"raw"`
			}
			queryPath := fmt.Sprintf("/autoideas/file?work_dir=%s&output=%s", url.QueryEscape(workDir), url.QueryEscape(outputName))
			if err := remoteAgentJSON(ctx, base, token, http.MethodGet, queryPath, nil, &ideasResp); err != nil {
				continue
			}
			if len(ideasResp.Items) > 0 {
				return strings.TrimSpace(ideasResp.Items[0].Title), nil
			}
			if strings.TrimSpace(ideasResp.Raw) != "" {
				return "ideas generated", nil
			}
		}
	}
}

func resolveRemoteCurrentWorkDir(ctx context.Context, base, token string) string {
	var resp struct {
		OK       bool          `json:"ok"`
		Machines []MachineInfo `json:"machines"`
	}
	if err := remoteAgentJSON(ctx, base, token, http.MethodGet, "/console/machines", nil, &resp); err != nil {
		return ""
	}
	for _, machine := range resp.Machines {
		if machine.IsLocal && strings.TrimSpace(machine.CurrentWorkDir) != "" {
			return machine.CurrentWorkDir
		}
	}
	for _, machine := range resp.Machines {
		if strings.TrimSpace(machine.CurrentWorkDir) != "" {
			return machine.CurrentWorkDir
		}
	}
	return ""
}

func graphRemoteProjectName(runID string, spec AgentGraphNodeSpec) string {
	project := strings.TrimSpace(spec.Project)
	if project == "" {
		project = "graph"
	}
	return fmt.Sprintf("%s-%s-%s", project, strings.ToLower(runID), strings.ToLower(spec.ID))
}

func graphAutoIdeasEngine(runner string) string {
	switch normalizedPlacementRunner(runner) {
	case "codex":
		return "codex"
	case "claude-code":
		return "claude"
	case "hybrid":
		return "hybrid"
	default:
		return ""
	}
}

func firstGraphNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildGraphLoopState(runID string, spec AgentGraphNodeSpec, workDir string, contract *TaskSliceContract) (*LoopState, error) {
	if workDir == "" {
		return nil, errors.New("node workDir required")
	}
	target := spec.Target
	if target == "" {
		if fileExists(filepath.Join(workDir, "mobile", "ios")) {
			target = "ios-sim"
		} else {
			target = "web"
		}
	}
	runner := spec.Runner
	if runner == "" {
		runner = "claude-code"
	}
	mode := LoopModeDevelop
	switch spec.Kind {
	case AgentNodeAutodev:
		mode = LoopModeDevelop
	case AgentNodeAutoIdeas:
		mode = LoopModeIdeas
	case AgentNodeAutotest:
		mode = LoopModeAutoTest
	}
	loopSpec := LoopSpec{
		Name:   fmt.Sprintf("agent-%s-%s", runID, spec.ID),
		Mode:   mode,
		Target: target,
		Schedule: LoopSchedule{
			Every:         "1m",
			MaxIterations: 1,
			Timeout:       "30m",
		},
		Playtest: LoopPlaytest{
			Enabled: func() *bool {
				v := spec.Kind != AgentNodeAutoIdeas
				return &v
			}(),
			Duration: "2m",
			Fuzzer:   "heuristic",
		},
		Think: LoopThink{
			Runner:         runner,
			Model:          spec.Model,
			PromptInline:   firstGraphNonEmpty(strings.TrimSpace(spec.Prompt), spec.Title) + formatTaskSliceContract(contract),
			MaxEdits:       1,
			MaxKicksPerRun: max(1, spec.MaxIterations),
			RequireGreen:   []string{"typecheck"},
		},
		Ship: LoopShip{
			Branch:       "main",
			CommitPrefix: "agent:",
		},
		Budget: LoopBudget{
			MaxPatchesPerDay:          10,
			MaxCommitsPerDay:          10,
			StopAfterConsecutiveStuck: 2,
		},
		Knobs: LoopKnobs{
			Tone: "neutral",
		},
	}
	if mode == LoopModeAutoTest {
		loopSpec.Think.RequireGreen = []string{"typecheck", "test"}
		loopSpec.Test = LoopTest{Root: "yaver-tests"}
	}
	if err := validateLoopSpec(&loopSpec); err != nil {
		return nil, err
	}
	applyLoopDefaults(&loopSpec)
	return &LoopState{
		ID:        uuid.New().String(),
		Spec:      loopSpec,
		Status:    LoopStatusRunning,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		WorkDir:   workDir,
	}, nil
}

func (gm *AgentGraphManager) finishRun(id string, status AgentGraphStatus, summary string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	run, ok := gm.runs[id]
	if !ok {
		return
	}
	run.Status = status
	run.Summary = summary
	run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	delete(gm.cancel, id)
	_ = gm.saveLocked()
}

func runAgentMode(args []string) {
	if len(args) == 0 {
		printAgentUsage()
		return
	}
	if args[0] == "mesh-smoke" {
		runAgentMeshSmoke(args[1:])
		return
	}
	if globalAgentGraphMgr == nil {
		runAgentModeViaDaemon(args)
		return
	}
	switch args[0] {
	case "list", "ls":
		runs := globalAgentGraphMgr.ListRuns()
		if len(runs) == 0 {
			fmt.Println("No agent graphs yet.")
			return
		}
		fmt.Printf("%-10s  %-24s  %-10s  %-8s  %s\n", "ID", "NAME", "STATUS", "NODES", "SUMMARY")
		for _, run := range runs {
			summary := run.Summary
			if len(summary) > 60 {
				summary = summary[:57] + "..."
			}
			fmt.Printf("%-10s  %-24s  %-10s  %-8d  %s\n", run.ID, run.Name, run.Status, len(run.Nodes), summary)
		}
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver agent show <id>")
			os.Exit(1)
		}
		run, ok := globalAgentGraphMgr.GetRun(args[1])
		if !ok {
			fmt.Fprintf(os.Stderr, "agent graph %q not found\n", args[1])
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(run, "", "  ")
		fmt.Println(string(data))
	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver agent stop <id>")
			os.Exit(1)
		}
		if err := globalAgentGraphMgr.StopRun(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "stop: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Stopped agent graph %s\n", args[1])
	case "run", "start":
		fs := flag.NewFlagSet("agent run", flag.ExitOnError)
		name := fs.String("name", "", "graph name")
		workDir := fs.String("work-dir", ".", "project working directory")
		prompt := fs.String("prompt", "", "top-level user goal")
		runner := fs.String("runner", "", "default runner")
		model := fs.String("model", "", "default model")
		template := fs.String("template", "full", "full|ship")
		maxParallel := fs.Int("max-parallel", 2, "max concurrent ready nodes")
		_ = fs.Parse(args[1:])
		if strings.TrimSpace(*prompt) == "" {
			fmt.Fprintln(os.Stderr, "agent run requires --prompt")
			os.Exit(1)
		}
		run, err := globalAgentGraphMgr.CreateRun(AgentGraphCreateRequest{
			Name:        *name,
			WorkDir:     *workDir,
			Prompt:      *prompt,
			Runner:      *runner,
			Model:       *model,
			Template:    *template,
			MaxParallel: *maxParallel,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent run: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Started agent graph %s (%s)\n", run.ID, run.Name)
	default:
		printAgentUsage()
	}
}

func runAgentModeViaDaemon(args []string) {
	switch args[0] {
	case "list", "ls":
		resp, err := localAgentRequest("GET", "/agent/graphs", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent list: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver agent show <id>")
			os.Exit(1)
		}
		resp, err := localAgentRequest("GET", "/agent/graphs/"+args[1], nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent show: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver agent stop <id>")
			os.Exit(1)
		}
		if _, err := localAgentRequest("POST", "/agent/graphs/"+args[1]+"/stop", map[string]interface{}{}); err != nil {
			fmt.Fprintf(os.Stderr, "agent stop: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Stopped agent graph %s\n", args[1])
	case "run", "start":
		fs := flag.NewFlagSet("agent run", flag.ExitOnError)
		name := fs.String("name", "", "graph name")
		workDir := fs.String("work-dir", ".", "project working directory")
		prompt := fs.String("prompt", "", "top-level user goal")
		runner := fs.String("runner", "", "default runner")
		model := fs.String("model", "", "default model")
		template := fs.String("template", "full", "full|ship")
		maxParallel := fs.Int("max-parallel", 2, "max concurrent ready nodes")
		_ = fs.Parse(args[1:])
		if strings.TrimSpace(*prompt) == "" {
			fmt.Fprintln(os.Stderr, "agent run requires --prompt")
			os.Exit(1)
		}
		resp, err := localAgentRequest("POST", "/agent/graphs", map[string]interface{}{
			"name":        *name,
			"workDir":     *workDir,
			"prompt":      *prompt,
			"runner":      *runner,
			"model":       *model,
			"template":    *template,
			"maxParallel": *maxParallel,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent run: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
	default:
		printAgentUsage()
	}
}

func printAgentUsage() {
	fmt.Print(`Yaver agent mode — dependency-aware graph orchestration for chat / autoideas / autodev / autotest.

Usage:
  yaver agent run --work-dir <path> --prompt "<goal>" [--template full|ship] [--runner codex] [--max-parallel 2]
  yaver agent mesh-smoke [--device <id-or-name>]
  yaver agent list
  yaver agent show <id>
  yaver agent stop <id>
`)
}

func (s *HTTPServer) handleAgentGraphs(w http.ResponseWriter, r *http.Request) {
	if s.agentGraphMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "agent graphs unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":   true,
			"runs": s.agentGraphMgr.ListRuns(),
		})
	case http.MethodPost:
		var body AgentGraphCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.WorkDir == "" {
			body.WorkDir = s.taskMgr.workDir
		}
		run, err := s.agentGraphMgr.CreateRun(body)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "run": run})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleAgentGraphByID(w http.ResponseWriter, r *http.Request) {
	if s.agentGraphMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "agent graphs unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/agent/graphs/")
	path = strings.Trim(path, "/")
	if path == "" {
		jsonError(w, http.StatusBadRequest, "graph id required")
		return
	}
	parts := strings.Split(path, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	switch r.Method {
	case http.MethodGet:
		if action != "" {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		run, ok := s.agentGraphMgr.GetRun(id)
		if !ok {
			jsonError(w, http.StatusNotFound, "graph not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "run": run})
	case http.MethodPost:
		switch action {
		case "run":
			if err := s.agentGraphMgr.StartRun(id); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonReply(w, http.StatusAccepted, map[string]interface{}{"ok": true})
		case "stop":
			if err := s.agentGraphMgr.StopRun(id); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			jsonReply(w, http.StatusAccepted, map[string]interface{}{"ok": true})
		default:
			jsonError(w, http.StatusNotFound, "not found")
		}
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

var globalAgentGraphMgr *AgentGraphManager
