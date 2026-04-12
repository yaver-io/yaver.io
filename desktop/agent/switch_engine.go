package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type SwitchLayer string

const (
	LayerData         SwitchLayer = "data"
	LayerCode         SwitchLayer = "code"
	LayerEnv          SwitchLayer = "env"
	LayerInfra        SwitchLayer = "infra"
	LayerNetwork      SwitchLayer = "network"
	LayerIntegrations SwitchLayer = "integrations"
	LayerVerify       SwitchLayer = "verify"
)

type SwitchStepStatus string

const (
	StepPending SwitchStepStatus = "pending"
	StepSkipped SwitchStepStatus = "skipped"
	StepRunning SwitchStepStatus = "running"
	StepDone    SwitchStepStatus = "done"
	StepFailed  SwitchStepStatus = "failed"
	StepManual  SwitchStepStatus = "manual"
)

type SwitchStep struct {
	ID          string            `json:"id" yaml:"id"`
	Layer       SwitchLayer       `json:"layer" yaml:"layer"`
	Title       string            `json:"title" yaml:"title"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Action      string            `json:"action" yaml:"action"`
	Args        map[string]string `json:"args,omitempty" yaml:"args,omitempty"`
	Status      SwitchStepStatus  `json:"status" yaml:"status"`
	StartedAt   string            `json:"startedAt,omitempty" yaml:"startedAt,omitempty"`
	FinishedAt  string            `json:"finishedAt,omitempty" yaml:"finishedAt,omitempty"`
	Error       string            `json:"error,omitempty" yaml:"error,omitempty"`
	Output      string            `json:"output,omitempty" yaml:"output,omitempty"`
}

type SwitchState struct {
	ID                string           `json:"id" yaml:"id"`
	ProjectDir        string           `json:"projectDir" yaml:"projectDir"`
	FromBackend       BackendKind      `json:"fromBackend" yaml:"fromBackend"`
	FromHost          TargetHost       `json:"fromHost" yaml:"fromHost"`
	To                string           `json:"to" yaml:"to"`
	Complexity        SwitchComplexity `json:"complexity" yaml:"complexity"`
	Status            SwitchStepStatus `json:"status" yaml:"status"`
	Steps             []SwitchStep     `json:"steps" yaml:"steps"`
	CreatedAt         string           `json:"createdAt" yaml:"createdAt"`
	FinishedAt        string           `json:"finishedAt,omitempty" yaml:"finishedAt,omitempty"`
	SnapshotBranch    string           `json:"snapshotBranch,omitempty" yaml:"snapshotBranch,omitempty"`
	SnapshotData      string           `json:"snapshotData,omitempty" yaml:"snapshotData,omitempty"`
	RollbackExpiresAt string           `json:"rollbackExpiresAt,omitempty" yaml:"rollbackExpiresAt,omitempty"`
	DryRun            bool             `json:"dryRun" yaml:"dryRun"`
	RewritePrompt     string           `json:"rewritePrompt,omitempty" yaml:"rewritePrompt,omitempty"`
	TaskID            string           `json:"taskId,omitempty" yaml:"taskId,omitempty"`
}

type SwitchEngine struct{ mu sync.Mutex }

func NewSwitchEngine() *SwitchEngine { return &SwitchEngine{} }

func switchesDir(projectDir string) string {
	return filepath.Join(projectDir, ".yaver", "switches")
}
func snapshotsDir(projectDir string) string {
	return filepath.Join(projectDir, ".yaver", "snapshots")
}

func (e *SwitchEngine) Plan(projectDir, targetID string, dryRun bool) (*SwitchState, error) {
	if projectDir == "" {
		return nil, fmt.Errorf("project directory required")
	}
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, err
	}
	target, err := SwitchTargetByID(targetID)
	if err != nil {
		return nil, err
	}
	complexity := AssessComplexity(cfg.Backend, *target)
	id := fmt.Sprintf("switch_%s", time.Now().Format("20060102_150405"))
	state := &SwitchState{
		ID: id, ProjectDir: projectDir, FromBackend: cfg.Backend, FromHost: HostLocalDocker,
		To: target.ID, Complexity: complexity, Status: StepPending,
		CreatedAt: time.Now().Format(time.RFC3339), DryRun: dryRun,
	}
	state.Steps = planSteps(cfg, target, complexity)
	if complexity == ComplexityHard {
		state.RewritePrompt = buildRewritePrompt(projectDir, cfg, target)
	}
	return state, nil
}

func (e *SwitchEngine) Persist(s *SwitchState) error {
	if err := os.MkdirAll(switchesDir(s.ProjectDir), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(switchesDir(s.ProjectDir), s.ID+".yaml"), data, 0o644)
}

func (e *SwitchEngine) Load(projectDir, id string) (*SwitchState, error) {
	data, err := os.ReadFile(filepath.Join(switchesDir(projectDir), id+".yaml"))
	if err != nil {
		return nil, err
	}
	var s SwitchState
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (e *SwitchEngine) History(projectDir string) ([]*SwitchState, error) {
	dir := switchesDir(projectDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*SwitchState
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".yaml")
		s, err := e.Load(projectDir, id)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (e *SwitchEngine) Run(s *SwitchState) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.Complexity == ComplexityHard {
		s.Status = StepManual
		s.FinishedAt = time.Now().Format(time.RFC3339)
		return e.Persist(s)
	}
	s.Status = StepRunning
	_ = e.Persist(s)
	registry := stepExecutors()
	for i := range s.Steps {
		step := &s.Steps[i]
		if step.Status == StepSkipped || step.Status == StepDone {
			continue
		}
		exec, ok := registry[step.Action]
		if !ok {
			step.Status = StepSkipped
			step.Error = "no executor for action " + step.Action
			continue
		}
		step.Status = StepRunning
		step.StartedAt = time.Now().Format(time.RFC3339)
		_ = e.Persist(s)
		if s.DryRun {
			step.Output = "[dry-run] would execute"
			step.Status = StepDone
			step.FinishedAt = time.Now().Format(time.RFC3339)
			continue
		}
		out, err := exec(s, step)
		step.Output = out
		step.FinishedAt = time.Now().Format(time.RFC3339)
		if err != nil {
			step.Status = StepFailed
			step.Error = err.Error()
			s.Status = StepFailed
			s.FinishedAt = time.Now().Format(time.RFC3339)
			_ = e.Persist(s)
			return fmt.Errorf("step %s failed: %w", step.ID, err)
		}
		step.Status = StepDone
		_ = e.Persist(s)
	}
	s.Status = StepDone
	s.FinishedAt = time.Now().Format(time.RFC3339)
	if s.RollbackExpiresAt == "" {
		s.RollbackExpiresAt = time.Now().AddDate(0, 0, 7).Format(time.RFC3339)
	}
	return e.Persist(s)
}

func planSteps(from *YaverProjectConfig, to *SwitchTarget, complexity SwitchComplexity) []SwitchStep {
	var steps []SwitchStep
	if to.Family != FamilyApp || to.Backend != "" {
		steps = append(steps, SwitchStep{ID: "snapshot", Layer: LayerData, Title: "Snapshot current state (git branch + data dump)", Action: "snapshot", Status: StepPending})
	}
	switch complexity {
	case ComplexityTrivial:
		steps = append(steps,
			SwitchStep{ID: "provision", Layer: LayerInfra, Title: "Provision " + to.Label, Action: "provision", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "migrate-data", Layer: LayerData, Title: "Migrate data to " + to.Label, Action: "migrate-data", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "update-env", Layer: LayerEnv, Title: "Update .env.local", Action: "update-env", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "verify", Layer: LayerVerify, Title: "Smoke test", Action: "verify", Args: map[string]string{"target": to.ID}, Status: StepPending},
		)
	case ComplexityEasy:
		steps = append(steps,
			SwitchStep{ID: "provision", Layer: LayerInfra, Title: "Provision " + to.Label, Action: "provision", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "migrate-data", Layer: LayerData, Title: "Dump + restore database", Action: "migrate-data", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "migrate-auth", Layer: LayerCode, Title: "Update auth layer for new backend", Action: "migrate-auth", Args: map[string]string{"target": to.ID}, Status: StepManual, Description: "Manual unless Better Auth ↔ Better Auth"},
			SwitchStep{ID: "update-env", Layer: LayerEnv, Title: "Update .env.local", Action: "update-env", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "update-oauth", Layer: LayerIntegrations, Title: "Update OAuth redirect URIs", Action: "manual-oauth", Args: map[string]string{"target": to.ID}, Status: StepManual},
			SwitchStep{ID: "verify", Layer: LayerVerify, Title: "Smoke test", Action: "verify", Args: map[string]string{"target": to.ID}, Status: StepPending},
		)
	case ComplexityMedium:
		steps = append(steps,
			SwitchStep{ID: "provision", Layer: LayerInfra, Title: "Provision " + to.Label, Action: "provision", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "schema-translate", Layer: LayerCode, Title: "Translate schema (SQL dialect / types)", Action: "schema-translate", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "migrate-data", Layer: LayerData, Title: "Migrate data with schema mapping", Action: "migrate-data", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "update-env", Layer: LayerEnv, Title: "Update .env.local", Action: "update-env", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "verify", Layer: LayerVerify, Title: "Smoke test", Action: "verify", Args: map[string]string{"target": to.ID}, Status: StepPending},
		)
	case ComplexityHard:
		steps = append(steps,
			SwitchStep{ID: "provision", Layer: LayerInfra, Title: "Provision " + to.Label, Action: "provision", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "emit-rewrite", Layer: LayerCode, Title: "Create AI rewrite task (paradigm switch)", Action: "emit-rewrite", Args: map[string]string{"target": to.ID}, Status: StepPending, Description: "Yaver emits a rewrite prompt; the AI agent executes it."},
			SwitchStep{ID: "data-plan", Layer: LayerData, Title: "Export data as JSON for the agent", Action: "export-for-ai", Args: map[string]string{"target": to.ID}, Status: StepPending},
			SwitchStep{ID: "update-env", Layer: LayerEnv, Title: "Update .env.local", Action: "update-env", Args: map[string]string{"target": to.ID}, Status: StepManual},
			SwitchStep{ID: "verify", Layer: LayerVerify, Title: "Manual verification required", Action: "verify", Args: map[string]string{"target": to.ID}, Status: StepManual},
		)
	}
	return steps
}
