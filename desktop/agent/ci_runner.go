package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// CIPipeline is the schema of .yaver/ci.yaml. Minimal on purpose — a vibe
// coder writes a handful of steps, not a 200-line GHA workflow.
type CIPipeline struct {
	Name     string   `yaml:"name,omitempty" json:"name,omitempty"`
	Image    string   `yaml:"image,omitempty" json:"image,omitempty"` // default node:20
	Steps    []CIStep `yaml:"steps" json:"steps"`
	OnFail   string   `yaml:"onFail,omitempty" json:"onFail,omitempty"` // "block-deploy" | "warn"
	Env      map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type CIStep struct {
	Name string `yaml:"name" json:"name"`
	Run  string `yaml:"run" json:"run"`
}

// CIRun is a recorded execution.
type CIRun struct {
	ID         string     `json:"id" yaml:"id"`
	ProjectDir string     `json:"projectDir" yaml:"projectDir"`
	StartedAt  time.Time  `json:"startedAt" yaml:"startedAt"`
	FinishedAt time.Time  `json:"finishedAt,omitempty" yaml:"finishedAt,omitempty"`
	Status     string     `json:"status" yaml:"status"` // running, passed, failed
	Steps      []CIStepRun `json:"steps" yaml:"steps"`
	Trigger    string     `json:"trigger" yaml:"trigger"`
}

type CIStepRun struct {
	Name     string `json:"name" yaml:"name"`
	Status   string `json:"status" yaml:"status"`
	Duration string `json:"duration,omitempty" yaml:"duration,omitempty"`
	ExitCode int    `json:"exitCode" yaml:"exitCode"`
	Output   string `json:"output,omitempty" yaml:"output,omitempty"`
}

var ciMu sync.Mutex

func ciPath(dir string) string { return filepath.Join(dir, ".yaver", "ci.yaml") }
func ciRunsDir(dir string) string { return filepath.Join(dir, ".yaver", "ci-runs") }

// LoadCIPipeline reads .yaver/ci.yaml; returns nil if not present.
func LoadCIPipeline(dir string) (*CIPipeline, error) {
	data, err := os.ReadFile(ciPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var p CIPipeline
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Image == "" {
		p.Image = "node:20"
	}
	if p.OnFail == "" {
		p.OnFail = "block-deploy"
	}
	return &p, nil
}

// RunCI executes the pipeline in a fresh Docker container with the project
// bind-mounted at /workspace. Each step runs sequentially. Returns the full
// run record.
func RunCI(ctx context.Context, dir, trigger string) (*CIRun, error) {
	ciMu.Lock()
	defer ciMu.Unlock()

	p, err := LoadCIPipeline(dir)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf(".yaver/ci.yaml not found — create one with steps: [{name, run}]")
	}

	run := &CIRun{
		ID:         fmt.Sprintf("ci_%s", time.Now().UTC().Format("20060102_150405")),
		ProjectDir: dir,
		StartedAt:  time.Now(),
		Status:     "running",
		Trigger:    trigger,
	}

	for _, step := range p.Steps {
		sr := CIStepRun{Name: step.Name, Status: "running"}
		start := time.Now()

		// docker run --rm -v <dir>:/workspace -w /workspace <image> sh -c '<run>'
		args := []string{"run", "--rm", "-v", dir + ":/workspace", "-w", "/workspace"}
		for k, v := range p.Env {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
		}
		args = append(args, p.Image, "sh", "-c", step.Run)

		cmd := exec.CommandContext(ctx, "docker", args...)
		out, err := cmd.CombinedOutput()
		sr.Output = string(out)
		sr.Duration = time.Since(start).Round(time.Millisecond).String()
		if err != nil {
			sr.Status = "failed"
			if exitErr, ok := err.(*exec.ExitError); ok {
				sr.ExitCode = exitErr.ExitCode()
			} else {
				sr.ExitCode = -1
			}
			run.Steps = append(run.Steps, sr)
			run.Status = "failed"
			run.FinishedAt = time.Now()
			persistCIRun(run)
			AuditLog("", "ci_run", filepath.Base(dir), step.Name, "failed", err.Error(), "")
			if globalNotifyManager != nil {
				globalNotifyManager.NotifyAgentEvent("CI failed", filepath.Base(dir)+" · "+step.Name)
			}
			return run, nil
		}
		sr.Status = "passed"
		sr.ExitCode = 0
		run.Steps = append(run.Steps, sr)
	}

	run.Status = "passed"
	run.FinishedAt = time.Now()
	persistCIRun(run)
	AuditLog("", "ci_run", filepath.Base(dir), run.ID, "passed", "", "")
	return run, nil
}

func persistCIRun(run *CIRun) {
	_ = os.MkdirAll(ciRunsDir(run.ProjectDir), 0o755)
	data, _ := yaml.Marshal(run)
	_ = os.WriteFile(filepath.Join(ciRunsDir(run.ProjectDir), run.ID+".yaml"), data, 0o644)
}

func listCIRuns(dir string) []*CIRun {
	entries, err := os.ReadDir(ciRunsDir(dir))
	if err != nil {
		return nil
	}
	var out []*CIRun
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ciRunsDir(dir), e.Name()))
		if err != nil {
			continue
		}
		var r CIRun
		if yaml.Unmarshal(data, &r) == nil {
			out = append(out, &r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleCIRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	run, err := RunCI(r.Context(), s.dirParam(r), "manual")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "run": run})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *HTTPServer) handleCIList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"runs": listCIRuns(s.dirParam(r))})
}

func (s *HTTPServer) handleCIConfig(w http.ResponseWriter, r *http.Request) {
	dir := s.dirParam(r)
	if r.Method == http.MethodGet {
		p, err := LoadCIPipeline(dir)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	}
	if r.Method == http.MethodPost {
		var p CIPipeline
		_ = json.NewDecoder(r.Body).Decode(&p)
		data, _ := yaml.Marshal(p)
		_ = os.MkdirAll(filepath.Dir(ciPath(dir)), 0o755)
		if err := os.WriteFile(ciPath(dir), data, 0o644); err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}
	jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
}
