package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

// DeployRecord is a single deploy attempt.
type DeployRecord struct {
	ID          string    `json:"id" yaml:"id"`
	ProjectDir  string    `json:"projectDir" yaml:"projectDir"`
	Environment string    `json:"environment,omitempty" yaml:"environment,omitempty"`
	Commit      string    `json:"commit,omitempty" yaml:"commit,omitempty"`
	Message     string    `json:"message,omitempty" yaml:"message,omitempty"`
	Branch      string    `json:"branch,omitempty" yaml:"branch,omitempty"`
	StartedAt   time.Time `json:"startedAt" yaml:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt,omitempty" yaml:"finishedAt,omitempty"`
	Status      string    `json:"status" yaml:"status"` // running, success, failed, rolled-back
	Duration    string    `json:"duration,omitempty" yaml:"duration,omitempty"`
	Logs        []string  `json:"logs" yaml:"logs"`
	PrevCommit  string    `json:"prevCommit,omitempty" yaml:"prevCommit,omitempty"` // for rollback
	Trigger     string    `json:"trigger,omitempty" yaml:"trigger,omitempty"`       // manual, webhook
}

// DeployConfig is .yaver/deploy.yaml — project deploy settings.
type DeployConfig struct {
	Branch        string `yaml:"branch,omitempty"`
	BuildCommand  string `yaml:"buildCommand,omitempty"`
	StartCommand  string `yaml:"startCommand,omitempty"`
	Healthcheck   string `yaml:"healthcheck,omitempty"` // URL or `docker compose ps`
	WebhookSecret string `yaml:"webhookSecret,omitempty"`
	AutoDeploy    bool   `yaml:"autoDeploy,omitempty"`
}

func deployConfigPath(dir string) string { return filepath.Join(dir, ".yaver", "deploy.yaml") }
func deployRecordsDir(dir string) string { return filepath.Join(dir, ".yaver", "deploys") }

func loadDeployConfig(dir string) DeployConfig {
	data, err := os.ReadFile(deployConfigPath(dir))
	if err != nil {
		return DeployConfig{Branch: "main"}
	}
	var c DeployConfig
	_ = yaml.Unmarshal(data, &c)
	if c.Branch == "" {
		c.Branch = "main"
	}
	return c
}

func saveDeployConfig(dir string, c DeployConfig) error {
	_ = os.MkdirAll(filepath.Dir(deployConfigPath(dir)), 0o755)
	data, _ := yaml.Marshal(c)
	return os.WriteFile(deployConfigPath(dir), data, 0o644)
}

var deployMu sync.Mutex

// RunDeploy pulls latest, builds, swaps containers, runs health checks, and
// records the result. On failure, auto-rolls-back to the previous commit.
func RunDeploy(projectDir string, trigger string) (*DeployRecord, error) {
	deployMu.Lock()
	defer deployMu.Unlock()

	cfg := loadDeployConfig(projectDir)
	id := fmt.Sprintf("dep_%s", time.Now().Format("20060102_150405"))
	rec := &DeployRecord{
		ID: id, ProjectDir: projectDir, Branch: cfg.Branch,
		StartedAt: time.Now(), Status: "running", Trigger: trigger,
	}
	// Capture previous HEAD so rollback can replay it.
	if prev, err := runIn(projectDir, "git", "rev-parse", "HEAD"); err == nil {
		rec.PrevCommit = strings.TrimSpace(prev)
	}
	persistDeploy(rec)

	step := func(title, bin string, args ...string) bool {
		rec.Logs = append(rec.Logs, "$ "+bin+" "+strings.Join(args, " "))
		out, err := runIn(projectDir, bin, args...)
		rec.Logs = append(rec.Logs, out)
		if err != nil {
			rec.Logs = append(rec.Logs, "FAIL: "+title+": "+err.Error())
			return false
		}
		return true
	}

	// 1. Fetch + checkout + pull
	if !step("git fetch", "git", "fetch", "origin") {
		return finishDeploy(rec, "failed", "fetch"), nil
	}
	if !step("git checkout", "git", "checkout", cfg.Branch) {
		return finishDeploy(rec, "failed", "checkout"), nil
	}
	if !step("git pull", "git", "pull", "--ff-only", "origin", cfg.Branch) {
		return finishDeploy(rec, "failed", "pull"), nil
	}
	if sha, err := runIn(projectDir, "git", "rev-parse", "HEAD"); err == nil {
		rec.Commit = strings.TrimSpace(sha)
	}
	if msg, err := runIn(projectDir, "git", "log", "-1", "--pretty=%s"); err == nil {
		rec.Message = strings.TrimSpace(msg)
	}

	// 2. Build (user-provided or docker-compose default)
	if cfg.BuildCommand != "" {
		if !step("build", "sh", "-c", cfg.BuildCommand) {
			return rollback(rec, "build failed")
		}
	} else if _, err := os.Stat(filepath.Join(projectDir, ".yaver", "docker-compose.yml")); err == nil {
		sm := NewServicesManager(projectDir)
		if msg, err := sm.runDocker("compose", "-p", "yaver-services", "-f", sm.composePath(), "build"); err != nil {
			rec.Logs = append(rec.Logs, "FAIL compose build: "+err.Error()+"\n"+msg)
			return rollback(rec, "compose build failed")
		}
	}

	// 3. Swap / restart services.
	sm := NewServicesManager(projectDir)
	if msg, err := sm.Start(); err != nil {
		rec.Logs = append(rec.Logs, "FAIL services start: "+err.Error()+"\n"+msg)
		return rollback(rec, "services start failed")
	} else {
		rec.Logs = append(rec.Logs, msg)
	}

	// 4. Health check.
	if cfg.Healthcheck != "" {
		if !checkHealth(cfg.Healthcheck) {
			rec.Logs = append(rec.Logs, "healthcheck FAIL: "+cfg.Healthcheck)
			return rollback(rec, "health check failed")
		}
		rec.Logs = append(rec.Logs, "health check PASS: "+cfg.Healthcheck)
	}

	return finishDeploy(rec, "success", ""), nil
}

func rollback(rec *DeployRecord, reason string) (*DeployRecord, error) {
	rec.Logs = append(rec.Logs, "rolling back: "+reason)
	if rec.PrevCommit != "" {
		out, err := runIn(rec.ProjectDir, "git", "checkout", rec.PrevCommit)
		rec.Logs = append(rec.Logs, out)
		if err != nil {
			rec.Logs = append(rec.Logs, "rollback FAIL: "+err.Error())
		} else {
			sm := NewServicesManager(rec.ProjectDir)
			if msg, err := sm.Start(); err == nil {
				rec.Logs = append(rec.Logs, "rollback services restarted: "+msg)
			}
			rec.Logs = append(rec.Logs, "rolled back to "+rec.PrevCommit[:8])
		}
	}
	return finishDeploy(rec, "rolled-back", reason), nil
}

func finishDeploy(rec *DeployRecord, status, _ string) *DeployRecord {
	rec.Status = status
	rec.FinishedAt = time.Now()
	rec.Duration = rec.FinishedAt.Sub(rec.StartedAt).Round(time.Second).String()
	persistDeploy(rec)
	return rec
}

func persistDeploy(rec *DeployRecord) {
	_ = os.MkdirAll(deployRecordsDir(rec.ProjectDir), 0o755)
	data, _ := yaml.Marshal(rec)
	_ = os.WriteFile(filepath.Join(deployRecordsDir(rec.ProjectDir), rec.ID+".yaml"), data, 0o644)
}

func listDeploys(dir string) []*DeployRecord {
	entries, err := os.ReadDir(deployRecordsDir(dir))
	if err != nil {
		return nil
	}
	var out []*DeployRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(deployRecordsDir(dir), e.Name()))
		if err != nil {
			continue
		}
		var rec DeployRecord
		if yaml.Unmarshal(data, &rec) == nil {
			out = append(out, &rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// RollbackToDeploy restores a project to a prior deploy's commit and restarts services.
func RollbackToDeploy(dir, deployID string) (*DeployRecord, error) {
	var target *DeployRecord
	for _, r := range listDeploys(dir) {
		if r.ID == deployID {
			target = r
			break
		}
	}
	if target == nil || target.Commit == "" {
		return nil, fmt.Errorf("deploy %s not found or missing commit sha", deployID)
	}
	rec := &DeployRecord{
		ID: fmt.Sprintf("rb_%s", time.Now().Format("20060102_150405")),
		ProjectDir: dir, StartedAt: time.Now(), Status: "running", Trigger: "rollback",
		PrevCommit: target.Commit, Message: "Rollback to " + target.Commit[:8],
	}
	out, err := runIn(dir, "git", "checkout", target.Commit)
	rec.Logs = append(rec.Logs, out)
	if err != nil {
		return finishDeploy(rec, "failed", "checkout"), err
	}
	rec.Commit = target.Commit
	sm := NewServicesManager(dir)
	if msg, err := sm.Start(); err != nil {
		rec.Logs = append(rec.Logs, "services start FAIL: "+err.Error())
		return finishDeploy(rec, "failed", "services"), err
	} else {
		rec.Logs = append(rec.Logs, msg)
	}
	return finishDeploy(rec, "success", ""), nil
}

func checkHealth(hc string) bool {
	if strings.HasPrefix(hc, "http://") || strings.HasPrefix(hc, "https://") {
		client := &http.Client{Timeout: 10 * time.Second}
		for i := 0; i < 10; i++ {
			res, err := client.Get(hc)
			if err == nil && res.StatusCode < 500 {
				res.Body.Close()
				return true
			}
			if res != nil {
				res.Body.Close()
			}
			time.Sleep(3 * time.Second)
		}
		return false
	}
	// Shell command health check
	cmd := exec.Command("sh", "-c", hc)
	return cmd.Run() == nil
}

func runIn(dir, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---- Webhook ----

// handleDeployWebhook: GitHub-compatible push webhook. Validates HMAC using
// the project's webhookSecret, then fires a deploy if autoDeploy is enabled.
//
//   POST /deploy/webhook?project=/abs/path
//   Headers: X-Hub-Signature-256: sha256=<hex>
func (s *HTTPServer) handleDeployWebhook(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("project")
	if dir == "" {
		jsonError(w, http.StatusBadRequest, "project required")
		return
	}
	cfg := loadDeployConfig(dir)
	body, _ := io.ReadAll(r.Body)
	if cfg.WebhookSecret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyGithubHMAC(sig, cfg.WebhookSecret, body) {
			jsonError(w, http.StatusUnauthorized, "bad signature")
			return
		}
	}
	if !cfg.AutoDeploy {
		writeJSON(w, http.StatusOK, map[string]interface{}{"received": true, "note": "autoDeploy disabled"})
		return
	}
	go func() {
		_, _ = RunDeploy(dir, "webhook")
	}()
	writeJSON(w, http.StatusOK, map[string]interface{}{"received": true, "triggered": true})
}

func verifyGithubHMAC(sig, secret string, body []byte) bool {
	if !strings.HasPrefix(sig, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	if err != nil {
		return false
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hmac.Equal(h.Sum(nil), want)
}

// ---- HTTP / MCP handlers ----

func (s *HTTPServer) handleDeployRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	dir := s.dirParam(r)
	rec, err := RunDeploy(dir, "manual")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "record": rec})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *HTTPServer) handleDeployList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"deploys": listDeploys(s.dirParam(r))})
}

func (s *HTTPServer) handleDeployRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct{ ID string `json:"id"` }
	_ = json.NewDecoder(r.Body).Decode(&b)
	rec, err := RollbackToDeploy(s.dirParam(r), b.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "record": rec})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *HTTPServer) handleDeployConfig(w http.ResponseWriter, r *http.Request) {
	dir := s.dirParam(r)
	if r.Method == http.MethodGet {
		cfg := loadDeployConfig(dir)
		writeJSON(w, http.StatusOK, cfg)
		return
	}
	if r.Method == http.MethodPost {
		var c DeployConfig
		_ = json.NewDecoder(r.Body).Decode(&c)
		if err := saveDeployConfig(dir, c); err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
		return
	}
	jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
}

func mcpDeployRun(dir string) interface{} {
	rec, err := RunDeploy(dir, "mcp")
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "record": rec}
	}
	return rec
}
func mcpDeployList(dir string) interface{} {
	return map[string]interface{}{"deploys": listDeploys(dir)}
}
func mcpDeployRollback(dir, id string) interface{} {
	rec, err := RollbackToDeploy(dir, id)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "record": rec}
	}
	return rec
}
