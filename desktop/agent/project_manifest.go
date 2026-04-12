package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProjectManifest is the full declarative state of a Yaver project. It sits
// at .yaver/project.yaml and is the source-of-truth for `yaver apply`.
type ProjectManifest struct {
	Name     string                         `yaml:"name" json:"name"`
	Backend  BackendKind                    `yaml:"backend,omitempty" json:"backend,omitempty"`
	Stack    string                         `yaml:"stack,omitempty" json:"stack,omitempty"`
	Auth     string                         `yaml:"auth,omitempty" json:"auth,omitempty"`
	Services map[string]*DevServiceConfig   `yaml:"services,omitempty" json:"services,omitempty"`
	Domains  []ManifestDomain               `yaml:"domains,omitempty" json:"domains,omitempty"`
	Deploy   *DeployConfig                  `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Cron     []ManifestCron                 `yaml:"cron,omitempty" json:"cron,omitempty"`
	Env      map[string]string              `yaml:"env,omitempty" json:"env,omitempty"`
}

type ManifestDomain struct {
	Domain   string `yaml:"domain" json:"domain"`
	Upstream string `yaml:"upstream" json:"upstream"`
	Static   string `yaml:"static,omitempty" json:"static,omitempty"`
}

type ManifestCron struct {
	Name     string `yaml:"name" json:"name"`
	Schedule string `yaml:"schedule" json:"schedule"`
	Target   string `yaml:"target" json:"target"`
}

func manifestPath(dir string) string {
	return filepath.Join(dir, ".yaver", "project.yaml")
}

func LoadManifest(dir string) (*ProjectManifest, error) {
	data, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		return nil, err
	}
	var m ProjectManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func SaveManifest(dir string, m *ProjectManifest) error {
	if err := os.MkdirAll(filepath.Dir(manifestPath(dir)), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(dir), data, 0o644)
}

// ApplyResult reports each reconciliation step.
type ApplyResult struct {
	Steps    []string `json:"steps"`
	Diff     []string `json:"diff"`
	Error    string   `json:"error,omitempty"`
}

// ApplyManifest reconciles the declared state with what's actually running.
// Only adds/changes things the manifest asks for; never deletes real resources
// (too dangerous for a solo-dev's prod data) unless drift mode is explicit.
func ApplyManifest(dir string) (*ApplyResult, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	res := &ApplyResult{}

	// 1. Write .yaver/config.yaml to match manifest-level metadata.
	cfg := &YaverProjectConfig{
		Backend: m.Backend, Stack: m.Stack, Auth: m.Auth, Env: m.Env,
	}
	if err := SaveProjectConfig(dir, cfg); err != nil {
		res.Error = "save config: " + err.Error()
		return res, err
	}
	res.Steps = append(res.Steps, "config.yaml reconciled")

	// 2. Services — merge into services.yaml.
	sm := NewServicesManager(dir)
	existing, _ := sm.LoadConfig()
	if existing == nil {
		existing = &DevServicesConfig{Services: map[string]*DevServiceConfig{}}
	}
	for name, svc := range m.Services {
		if existing.Services[name] == nil {
			res.Diff = append(res.Diff, "+ service "+name)
		} else {
			res.Diff = append(res.Diff, "~ service "+name)
		}
		existing.Services[name] = svc
	}
	if err := sm.SaveConfig(existing); err != nil {
		res.Error = "save services: " + err.Error()
		return res, err
	}
	if _, err := sm.Start(); err != nil {
		res.Steps = append(res.Steps, "warn: services start: "+err.Error())
	} else {
		res.Steps = append(res.Steps, fmt.Sprintf("%d services applied", len(m.Services)))
	}

	// 3. Domains.
	for _, d := range m.Domains {
		if _, err := AddDomain(d.Domain, d.Upstream, d.Static, ""); err != nil {
			res.Steps = append(res.Steps, "warn: domain "+d.Domain+": "+err.Error())
		} else {
			res.Diff = append(res.Diff, "+ domain "+d.Domain)
		}
	}

	// 4. Deploy config.
	if m.Deploy != nil {
		if err := saveDeployConfig(dir, *m.Deploy); err != nil {
			res.Steps = append(res.Steps, "warn: save deploy config: "+err.Error())
		} else {
			res.Steps = append(res.Steps, "deploy config reconciled")
		}
	}

	// 5. Cron jobs.
	for _, c := range m.Cron {
		if _, err := CreateScheduledJob(dir, c.Name, c.Schedule, c.Target); err != nil {
			res.Steps = append(res.Steps, "warn: cron "+c.Name+": "+err.Error())
		} else {
			res.Diff = append(res.Diff, "+ cron "+c.Name)
		}
	}

	// 6. Env — written into .env.local under a marker block.
	if len(m.Env) > 0 {
		envPath := filepath.Join(dir, ".env.local")
		existing, _ := os.ReadFile(envPath)
		existingStr := string(existing)
		marker := "# === yaver manifest ==="
		// Strip old block.
		if idx := strings.Index(existingStr, marker); idx >= 0 {
			if end := strings.Index(existingStr[idx:], "\n# === end manifest ==="); end >= 0 {
				existingStr = existingStr[:idx] + existingStr[idx+end+len("\n# === end manifest ==="):]
			}
		}
		// Append new block.
		var sb strings.Builder
		sb.WriteString(strings.TrimRight(existingStr, "\n") + "\n\n" + marker + "\n")
		keys := make([]string, 0, len(m.Env))
		for k := range m.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(k + "=" + m.Env[k] + "\n")
		}
		sb.WriteString("# === end manifest ===\n")
		_ = os.WriteFile(envPath, []byte(sb.String()), 0o644)
		res.Steps = append(res.Steps, fmt.Sprintf("%d env vars reconciled", len(m.Env)))
	}

	return res, nil
}

// DiffManifest returns what ApplyManifest would do without actually applying.
func DiffManifest(dir string) (*ApplyResult, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	res := &ApplyResult{}
	sm := NewServicesManager(dir)
	existing, _ := sm.LoadConfig()
	for name, svc := range m.Services {
		if existing == nil || existing.Services[name] == nil {
			res.Diff = append(res.Diff, "+ service "+name+" ("+svc.Image+":"+fmt.Sprint(svc.Port)+")")
		}
	}
	if existing != nil {
		for name := range existing.Services {
			if _, declared := m.Services[name]; !declared {
				res.Diff = append(res.Diff, "- service "+name+" (drift; not removed without --prune)")
			}
		}
	}
	for _, d := range m.Domains {
		res.Diff = append(res.Diff, "+ domain "+d.Domain+" → "+d.Upstream)
	}
	for _, c := range m.Cron {
		res.Diff = append(res.Diff, "+ cron "+c.Name+" ("+c.Schedule+")")
	}
	return res, nil
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleManifestGet(w http.ResponseWriter, r *http.Request) {
	m, err := LoadManifest(s.dirParam(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *HTTPServer) handleManifestSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var m ProjectManifest
	_ = json.NewDecoder(r.Body).Decode(&m)
	if err := SaveManifest(s.dirParam(r), &m); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleManifestApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	res, err := ApplyManifest(s.dirParam(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleManifestDiff(w http.ResponseWriter, r *http.Request) {
	res, err := DiffManifest(s.dirParam(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}
