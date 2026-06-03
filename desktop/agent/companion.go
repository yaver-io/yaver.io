package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// companion.go — the companion-compute engine.
//
// A serverless project (Convex / Supabase / Cloudflare Workers) declares its
// always-on tail in an on-device `yaver.companion.yaml`: HTTP crons, long-
// running workers, and an optional AI-wrapper service. The engine compiles each
// element onto a primitive Yaver already ships:
//
//   crons[] (HTTP)      -> ScheduledTask in Verb-mode firing companion_http
//   services[] durable  -> ServicesManager binary service + a per-service OS unit
//   services[] !durable -> ServicesManager binary service (dies with the agent)
//
// Crons are reboot-durable for free: the scheduler re-arms them from
// ~/.yaver/schedules.json when the agent's own OS unit restarts it. Durable
// services get their own OS unit (managed_units.go) so they survive even agent
// downtime.
//
// PRIVACY: the manifest references env-interpolated URLs (which embed cron auth
// tokens) and vault secrets. Those are interpolated ONLY at arm-time into
// on-device OpsPayload / OS-unit env. The manifest itself, the schedules.json
// OpsPayload, and the unit files all live on local disk. Convex sees only the
// bookkeeping projection built by buildCompanionUpsertPayload (companion_sync.go).

const companionHTTPVerb = "companion_http"

func init() {
	// companion_http is a Verb-mode target for companion HTTP crons. The MCP
	// `http_request` tool is NOT an ops verb (it lives only in the MCP switch),
	// and the scheduler's routine path dispatches through the ops registry — so
	// crons need a registered verb. Owner-only; the box already has /exec.
	registerOpsVerb(opsVerbSpec{
		Name:        companionHTTPVerb,
		Description: "Fire a companion HTTP cron request (used by yaver.companion.yaml crons).",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"url"},
			"properties": map[string]interface{}{
				"url":     map[string]interface{}{"type": "string"},
				"method":  map[string]interface{}{"type": "string"},
				"headers": map[string]interface{}{"type": "object"},
				"body":    map[string]interface{}{"type": "string"},
			},
		},
		AllowGuest: false,
		Handler:    companionHTTPHandler,
	})
}

func companionHTTPHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(req.URL) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "url is required"}
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	// Do the request directly (not the curl-backed mcpHTTPRequest) so the
	// status code is reliable even on empty-body responses — a failed cron
	// must surface as a failed run, not silently "ok".
	var bodyReader io.Reader
	if req.Body != "" {
		bodyReader = strings.NewReader(req.Body)
	}
	httpReq, err := http.NewRequest(strings.ToUpper(req.Method), req.URL, bodyReader)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_failed", Error: err.Error()}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)) // drain (bounded) to reuse the conn
	ok := resp.StatusCode >= 200 && resp.StatusCode < 400
	out := OpsResult{OK: ok, Initial: map[string]interface{}{"statusCode": resp.StatusCode}}
	if !ok {
		out.Code = "remote_failed"
		out.Error = resp.Status
	}
	return out
}

// --- manifest ---

type CompanionManifest struct {
	Version  int                  `yaml:"version"`
	Project  string               `yaml:"project"`
	Runtime  CompanionRuntime     `yaml:"runtime"`
	EnvFrom  []CompanionEnvSource `yaml:"env_from"`
	Crons    []CompanionCron      `yaml:"crons"`
	Services []CompanionService   `yaml:"services"`
	AIWrap   *CompanionAIWrapper  `yaml:"ai_wrapper"`

	repoDir string `yaml:"-"`
}

type CompanionRuntime struct {
	Bind        string `yaml:"bind"`          // device | managed-cloud
	BaseURLFrom string `yaml:"base_url_from"` // "env:VAR"
}

type CompanionEnvSource struct {
	Vault string `yaml:"vault"` // vault project namespace
	File  string `yaml:"file"`  // dotenv path, repo-relative
}

type CompanionCron struct {
	Name       string           `yaml:"name"`
	Schedule   string           `yaml:"schedule"`
	Idempotent bool             `yaml:"idempotent"`
	CompilesTo string           `yaml:"compiles_to"` // http_request
	Status     string           `yaml:"status"`      // "" | "proposed"
	Request    CompanionRequest `yaml:"request"`
}

type CompanionRequest struct {
	Method  string            `yaml:"method"`
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`
}

type CompanionService struct {
	Name        string               `yaml:"name"`
	Command     string               `yaml:"command"`
	Args        []string             `yaml:"args"`
	Workdir     string               `yaml:"workdir"`
	WorkdirFrom string               `yaml:"workdir_from"` // "env:VAR"
	Port        int                  `yaml:"port"`
	EnvFrom     []CompanionEnvSource `yaml:"env_from"`
	Durable     bool                 `yaml:"durable"`
}

type CompanionAIWrapper struct {
	Enabled  bool   `yaml:"enabled"`
	Service  string `yaml:"service"`
	WorkKind string `yaml:"work_kind"`
}

// CompanionManifestName is the file the engine looks for at a repo root.
const CompanionManifestName = "yaver.companion.yaml"

// LoadCompanionManifest reads yaver.companion.yaml from repoDir.
func LoadCompanionManifest(repoDir string) (*CompanionManifest, error) {
	path := filepath.Join(repoDir, CompanionManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m CompanionManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", CompanionManifestName, err)
	}
	if strings.TrimSpace(m.Project) == "" {
		return nil, fmt.Errorf("%s: project is required", CompanionManifestName)
	}
	m.repoDir = repoDir
	return &m, nil
}

// --- engine ---

type CompanionEngine struct {
	sched    *Scheduler
	svcs     *ServicesManager
	vault    *VaultStore
	deviceID string
	syncer   *convexSyncer // optional, best-effort bookkeeping
}

// CompanionStatus is the engine's view of one project (P2P payload for the UI).
type CompanionStatus struct {
	Project  string                 `json:"project"`
	Enabled  bool                   `json:"enabled"`
	Crons    []CompanionCronStatus  `json:"crons"`
	Services []CompanionSvcStatus   `json:"services"`
	Warnings []string               `json:"warnings,omitempty"`
}

type CompanionCronStatus struct {
	Name       string `json:"name"`
	Schedule   string `json:"schedule"`
	ScheduleID string `json:"scheduleId,omitempty"`
	Status     string `json:"status"` // scheduled | proposed | completed | failed
	LastOutcome string `json:"lastOutcome,omitempty"`
	NextRunAt  string `json:"nextRunAt,omitempty"`
	LastRunAt  string `json:"lastRunAt,omitempty"`
	Proposed   bool   `json:"proposed,omitempty"`
}

type CompanionSvcStatus struct {
	Name    string `json:"name"`
	Durable bool   `json:"durable"`
	Unit    string `json:"unit,omitempty"`
	Running bool   `json:"running"`
}

// companionState persists what Up armed so Down/Reconcile can find it again.
type companionState struct {
	Project     string            `json:"project"`
	RepoDir     string            `json:"repoDir"`
	Enabled     bool              `json:"enabled"`
	ScheduleIDs map[string]string `json:"scheduleIds"` // cronName -> scheduleId
	UnitNames   map[string]string `json:"unitNames"`   // serviceName -> unit name
	UpdatedAt   string            `json:"updatedAt"`
}

func companionStateDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(dir, "companion")
	if err := os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	return d, nil
}

func companionStatePath(project string) (string, error) {
	d, err := companionStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, sanitizeCompanionName(project)+".state.json"), nil
}

func loadCompanionState(project string) (*companionState, error) {
	p, err := companionStatePath(project)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var st companionState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func saveCompanionState(st *companionState) error {
	p, err := companionStatePath(st.Project)
	if err != nil {
		return err
	}
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(p, data, 0600)
}

func companionScheduleTitle(project, cronName string) string {
	return fmt.Sprintf("companion:%s:%s", sanitizeCompanionName(project), sanitizeCompanionName(cronName))
}

func companionUnitName(project, svcName string) string {
	return "yaver-companion-" + sanitizeCompanionName(project) + "-" + sanitizeCompanionName(svcName)
}

// Up arms all crons and starts all services for a manifest. Idempotent:
// re-running updates existing schedules/units in place (dedupe by title/name).
func (e *CompanionEngine) Up(m *CompanionManifest) (CompanionStatus, error) {
	if e.sched == nil {
		return CompanionStatus{}, fmt.Errorf("scheduler not available")
	}
	status := CompanionStatus{Project: m.Project, Enabled: true}
	st := &companionState{
		Project:     m.Project,
		RepoDir:     m.repoDir,
		Enabled:     true,
		ScheduleIDs: map[string]string{},
		UnitNames:   map[string]string{},
	}

	env, warnings := e.resolveManifestEnv(m)
	status.Warnings = append(status.Warnings, warnings...)
	baseURL := e.resolveBaseURL(m, env)

	// Re-read existing schedules so we can update in place by title.
	existingByTitle := map[string]*ScheduledTask{}
	for _, t := range e.sched.ListSchedules() {
		existingByTitle[t.Title] = t
	}

	for _, c := range m.Crons {
		cs := CompanionCronStatus{Name: c.Name, Schedule: c.Schedule}
		if strings.EqualFold(c.Status, "proposed") {
			// Proposed (e.g. an endpoint that doesn't exist yet) — never armed.
			cs.Status = "proposed"
			cs.Proposed = true
			status.Crons = append(status.Crons, cs)
			continue
		}
		url := interpolateCompanion(c.Request.URL, env, baseURL)
		payload, _ := json.Marshal(map[string]interface{}{
			"url":     url,
			"method":  strings.ToUpper(strings.TrimSpace(orDefault(c.Request.Method, "POST"))),
			"headers": interpolateCompanionMap(c.Request.Headers, env, baseURL),
			"body":    interpolateCompanion(c.Request.Body, env, baseURL),
		})
		title := companionScheduleTitle(m.Project, c.Name)

		if prev, ok := existingByTitle[title]; ok {
			// Update in place — replace payload + schedule, keep history.
			_ = e.sched.applyRoutineUpdate(prev.ID, func(t *ScheduledTask) error {
				t.Verb = companionHTTPVerb
				t.Machine = "local"
				t.OpsPayload = payload
				t.Cron = c.Schedule
				return nil
			})
			st.ScheduleIDs[c.Name] = prev.ID
			cs.ScheduleID = prev.ID
		} else {
			task := &ScheduledTask{
				Title:      title,
				Verb:       companionHTTPVerb,
				Machine:    "local",
				OpsPayload: payload,
				Cron:       c.Schedule,
			}
			if err := e.sched.AddSchedule(task); err != nil {
				cs.Status = "failed"
				status.Warnings = append(status.Warnings, fmt.Sprintf("cron %q: %v", c.Name, err))
				status.Crons = append(status.Crons, cs)
				continue
			}
			st.ScheduleIDs[c.Name] = task.ID
			cs.ScheduleID = task.ID
		}
		cs.Status = "scheduled"
		status.Crons = append(status.Crons, cs)
	}

	for _, svc := range m.Services {
		ss := CompanionSvcStatus{Name: svc.Name, Durable: svc.Durable}
		svcEnv, w := e.resolveServiceEnv(m, svc, env)
		status.Warnings = append(status.Warnings, w...)
		workdir := svc.Workdir
		if strings.HasPrefix(svc.WorkdirFrom, "env:") {
			if v, ok := env[strings.TrimPrefix(svc.WorkdirFrom, "env:")]; ok {
				workdir = v
			}
		}
		if svc.Durable && durableUnitsSupported() {
			binPath, err := exec.LookPath(svc.Command)
			if err != nil {
				ss.Running = false
				status.Warnings = append(status.Warnings, fmt.Sprintf("service %q: %v", svc.Name, err))
				status.Services = append(status.Services, ss)
				continue
			}
			unitName := companionUnitName(m.Project, svc.Name)
			if _, err := writeManagedUnit(ManagedUnit{
				Name: unitName, ExecPath: binPath, Args: svc.Args, WorkDir: workdir, Env: svcEnv,
			}); err != nil {
				status.Warnings = append(status.Warnings, fmt.Sprintf("service %q unit: %v", svc.Name, err))
			} else {
				st.UnitNames[svc.Name] = unitName
				ss.Unit = unitName
				ss.Running = true
			}
		} else {
			// Non-durable (or platform can't do OS units): run as agent child.
			if e.svcs != nil {
				if _, err := e.svcs.startBinaryService(svc.Name, &DevServiceConfig{
					Binary: svc.Command, Command: svc.Command, Args: svc.Args, WorkDir: workdir, Env: svcEnv, Port: svc.Port,
				}); err != nil {
					status.Warnings = append(status.Warnings, fmt.Sprintf("service %q: %v", svc.Name, err))
				} else {
					ss.Running = true
				}
			}
			if svc.Durable && !durableUnitsSupported() {
				status.Warnings = append(status.Warnings,
					fmt.Sprintf("service %q: durable OS units unsupported on this platform; running as agent child", svc.Name))
			}
		}
		status.Services = append(status.Services, ss)
	}

	if err := saveCompanionState(st); err != nil {
		status.Warnings = append(status.Warnings, fmt.Sprintf("state: %v", err))
	}
	e.syncBookkeeping(m.Project, true, status)
	return status, nil
}

// Down removes all armed schedules + units for a project.
func (e *CompanionEngine) Down(project string) error {
	st, err := loadCompanionState(project)
	if err != nil {
		return err
	}
	for _, id := range st.ScheduleIDs {
		if e.sched != nil {
			_ = e.sched.RemoveSchedule(id)
		}
	}
	for svcName, unit := range st.UnitNames {
		_ = removeManagedUnit(unit)
		_ = svcName
	}
	st.Enabled = false
	st.ScheduleIDs = map[string]string{}
	st.UnitNames = map[string]string{}
	_ = saveCompanionState(st)
	e.syncBookkeeping(project, false, CompanionStatus{Project: project, Enabled: false})
	return nil
}

// Status reports the live state of a project from the scheduler + state file.
func (e *CompanionEngine) Status(project string) (CompanionStatus, error) {
	st, err := loadCompanionState(project)
	if err != nil {
		return CompanionStatus{}, err
	}
	status := CompanionStatus{Project: project, Enabled: st.Enabled}
	for name, id := range st.ScheduleIDs {
		cs := CompanionCronStatus{Name: name, ScheduleID: id}
		if e.sched != nil {
			if t, ok := e.sched.GetSchedule(id); ok {
				cs.Schedule = t.Cron
				cs.Status = t.Status
				cs.NextRunAt = t.NextRunAt
				cs.LastRunAt = t.LastRunAt
				if len(t.History) > 0 {
					cs.LastOutcome = t.History[len(t.History)-1].Status
				}
			}
		}
		status.Crons = append(status.Crons, cs)
	}
	for name, unit := range st.UnitNames {
		status.Services = append(status.Services, CompanionSvcStatus{Name: name, Durable: true, Unit: unit, Running: true})
	}
	return status, nil
}

// Reconcile re-applies every known companion project on agent startup. Crons
// are already re-armed by Scheduler.load(); this heals drift (manifest changed
// while the agent was down) and re-verifies durable units. Best-effort.
func (e *CompanionEngine) Reconcile() {
	d, err := companionStateDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".state.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(d, ent.Name()))
		if err != nil {
			continue
		}
		var st companionState
		if err := json.Unmarshal(data, &st); err != nil || !st.Enabled || st.RepoDir == "" {
			continue
		}
		m, err := LoadCompanionManifest(st.RepoDir)
		if err != nil {
			continue // repo moved/removed; leave the persisted schedules as-is
		}
		_, _ = e.Up(m)
	}
}

func (e *CompanionEngine) syncBookkeeping(project string, enabled bool, status CompanionStatus) {
	if e.syncer == nil {
		return
	}
	crons := make([]CompanionCronSummary, 0, len(status.Crons))
	for _, c := range status.Crons {
		if c.Proposed {
			continue
		}
		crons = append(crons, CompanionCronSummary{
			Name: c.Name, Schedule: c.Schedule, LastOutcome: c.LastOutcome,
		})
	}
	payload := buildCompanionUpsertPayload(e.deviceID, project, enabled, crons, len(status.Services))
	e.syncer.callMutation("companion:upsertCompanionProject", payload)
}

// --- env interpolation ---

var companionVarRe = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)

// resolveManifestEnv builds the variable map from env_from sources: dotenv
// files (repo-relative) then vault projects (eager List+Get). Vault entries
// overlay dotenv so a secret in the vault wins over a placeholder in .env.
// Every value resolved here stays on-device — it only ever lands in the
// on-disk OpsPayload / OS-unit env, never in a Convex payload.
func (e *CompanionEngine) resolveManifestEnv(m *CompanionManifest) (map[string]string, []string) {
	return e.resolveEnvSources(m.repoDir, m.EnvFrom, nil)
}

func (e *CompanionEngine) resolveServiceEnv(m *CompanionManifest, svc CompanionService, base map[string]string) (map[string]string, []string) {
	return e.resolveEnvSources(m.repoDir, svc.EnvFrom, base)
}

// resolveEnvSources overlays base + dotenv files + vault projects into one map.
func (e *CompanionEngine) resolveEnvSources(repoDir string, sources []CompanionEnvSource, base map[string]string) (map[string]string, []string) {
	env := map[string]string{}
	for k, v := range base {
		env[k] = v
	}
	var warnings []string
	for _, src := range sources {
		if src.File != "" {
			p := src.File
			if !filepath.IsAbs(p) {
				p = filepath.Join(repoDir, src.File)
			}
			vars, err := parseDotEnv(p)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("env file %s: %v", src.File, err))
			} else {
				for k, v := range vars {
					env[k] = v
				}
			}
		}
		if src.Vault != "" && e.vault != nil {
			for _, sum := range e.vault.List(src.Vault) {
				if entry, err := e.vault.Get(src.Vault, sum.Name); err == nil && entry != nil && !entry.Deleted {
					env[sum.Name] = entry.Value
				}
			}
		}
	}
	return env, warnings
}

func (e *CompanionEngine) resolveBaseURL(m *CompanionManifest, env map[string]string) string {
	if strings.HasPrefix(m.Runtime.BaseURLFrom, "env:") {
		name := strings.TrimPrefix(m.Runtime.BaseURLFrom, "env:")
		if v, ok := env[name]; ok {
			return v
		}
		return os.Getenv(name)
	}
	return ""
}

// interpolateCompanion replaces ${VAR} and ${base_url} in s. Resolution order
// for VAR is dotenv -> os env -> vault project(s). base_url is special-cased.
func interpolateCompanion(s string, env map[string]string, baseURL string) string {
	if s == "" {
		return s
	}
	return companionVarRe.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if name == "base_url" {
			return baseURL
		}
		if v, ok := env[name]; ok {
			return v
		}
		return match // leave unresolved tokens visible rather than blanking
	})
}

func interpolateCompanionMap(in map[string]string, env map[string]string, baseURL string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		out[k] = interpolateCompanion(v, env, baseURL)
	}
	return out
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// parseDotEnv reads a simple KEY=VALUE dotenv file (no export, basic quotes).
func parseDotEnv(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out, nil
}
