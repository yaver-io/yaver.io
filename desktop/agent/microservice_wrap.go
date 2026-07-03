package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// microservice_wrap.go is the product-facing layer over companion compute:
// take a repo-local service command, write yaver.companion.yaml, and optionally
// arm it as a durable systemd/launchd-backed process via CompanionEngine.

type MicroserviceWrapRequest struct {
	Repo         string   `json:"repo"`
	Project      string   `json:"project,omitempty"`
	Name         string   `json:"name,omitempty"`
	Command      string   `json:"command,omitempty"`
	Args         []string `json:"args,omitempty"`
	Workdir      string   `json:"workdir,omitempty"`
	Port         int      `json:"port,omitempty"`
	EnvVault     string   `json:"env_vault,omitempty"`
	EnvFile      string   `json:"env_file,omitempty"`
	Durable      *bool    `json:"durable,omitempty"`
	Write        bool     `json:"write,omitempty"`
	Arm          bool     `json:"arm,omitempty"`
	Overwrite    bool     `json:"overwrite,omitempty"`
	UseShell     bool     `json:"use_shell,omitempty"`
	AIWrap       bool     `json:"ai_wrap,omitempty"`
	AIWorkKind   string   `json:"ai_work_kind,omitempty"`
	BaseURLFrom  string   `json:"base_url_from,omitempty"`
	HealthURL    string   `json:"health_url,omitempty"`
	ScheduleCron string   `json:"schedule_cron,omitempty"`
}

type MicroserviceWrapResult struct {
	OK           bool              `json:"ok"`
	Repo         string            `json:"repo"`
	Project      string            `json:"project"`
	ManifestPath string            `json:"manifestPath"`
	Manifest     CompanionManifest `json:"manifest"`
	ManifestYAML string            `json:"manifestYaml"`
	Items        []DetectedItem    `json:"items,omitempty"`
	Existing     bool              `json:"existing"`
	Written      bool              `json:"written"`
	Armed        bool              `json:"armed"`
	Status       *CompanionStatus  `json:"status,omitempty"`
	Warnings     []string          `json:"warnings,omitempty"`
	Next         []string          `json:"next,omitempty"`
}

func MicroserviceDetect(repo, project string) (*MicroserviceWrapResult, error) {
	repo, err := normalizeRepoDir(repo)
	if err != nil {
		return nil, err
	}
	m, items, err := DetectCompanion(repo)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(project) != "" {
		m.Project = sanitizeCompanionName(project)
	}
	applyMicroserviceDefaults(m, repo)
	result := buildMicroserviceResult(repo, m, items)
	result.Existing = microserviceFileExists(filepath.Join(repo, CompanionManifestName))
	result.Next = microserviceNext(result, false, false)
	return result, nil
}

func MicroserviceWrap(s *HTTPServer, req MicroserviceWrapRequest) (*MicroserviceWrapResult, error) {
	repo, err := normalizeRepoDir(req.Repo)
	if err != nil {
		return nil, err
	}

	var m *CompanionManifest
	var items []DetectedItem
	if req.Command == "" {
		m, items, err = DetectCompanion(repo)
		if err != nil {
			return nil, err
		}
	} else {
		m = &CompanionManifest{Version: 1, Project: sanitizeCompanionName(filepath.Base(repo)), repoDir: repo}
	}
	if strings.TrimSpace(req.Project) != "" {
		m.Project = sanitizeCompanionName(req.Project)
	}
	if m.Project == "" {
		m.Project = sanitizeCompanionName(filepath.Base(repo))
	}
	applyMicroserviceDefaults(m, repo)

	if req.Command != "" {
		svc, item := serviceFromWrapRequest(req, repo, m.Project)
		m.Services = []CompanionService{svc}
		items = append(items, item)
	}
	if req.ScheduleCron != "" && req.HealthURL != "" {
		m.Crons = append(m.Crons, CompanionCron{
			Name:       "health-check",
			Schedule:   req.ScheduleCron,
			Idempotent: true,
			CompilesTo: "http_request",
			Request: CompanionRequest{
				Method: "GET",
				URL:    req.HealthURL,
			},
		})
	}
	if req.AIWrap {
		service := req.Name
		if service == "" && len(m.Services) > 0 {
			service = m.Services[0].Name
		}
		m.AIWrap = &CompanionAIWrapper{
			Enabled:  true,
			Service:  sanitizeCompanionName(service),
			WorkKind: strings.TrimSpace(req.AIWorkKind),
		}
		if m.AIWrap.WorkKind == "" {
			m.AIWrap.WorkKind = "ops"
		}
	}
	if req.BaseURLFrom != "" {
		m.Runtime.BaseURLFrom = req.BaseURLFrom
	}

	result := buildMicroserviceResult(repo, m, items)
	manifestPath := filepath.Join(repo, CompanionManifestName)
	result.Existing = microserviceFileExists(manifestPath)
	if result.Existing && req.Write && !req.Overwrite {
		result.Warnings = append(result.Warnings, CompanionManifestName+" already exists; pass overwrite=true to replace it")
		result.Next = microserviceNext(result, false, false)
		return result, nil
	}
	if req.Write {
		if err := os.WriteFile(manifestPath, []byte(result.ManifestYAML), 0o644); err != nil {
			return nil, err
		}
		result.Written = true
	}
	if req.Arm {
		if !req.Write && !microserviceFileExists(manifestPath) {
			return nil, fmt.Errorf("arm requires write=true or an existing %s", CompanionManifestName)
		}
		if s == nil {
			return nil, fmt.Errorf("agent server unavailable")
		}
		loaded, err := LoadCompanionManifest(repo)
		if err != nil {
			return nil, err
		}
		status, err := s.companionEngine().Up(loaded)
		if err != nil {
			return nil, err
		}
		result.Armed = true
		result.Status = &status
		result.Warnings = append(result.Warnings, status.Warnings...)
	}
	result.Next = microserviceNext(result, req.Write, req.Arm)
	return result, nil
}

func buildMicroserviceResult(repo string, m *CompanionManifest, items []DetectedItem) *MicroserviceWrapResult {
	if m == nil {
		m = &CompanionManifest{Version: 1, Project: sanitizeCompanionName(filepath.Base(repo)), repoDir: repo}
	}
	m.repoDir = repo
	yml, _ := yaml.Marshal(m)
	return &MicroserviceWrapResult{
		OK:           true,
		Repo:         repo,
		Project:      m.Project,
		ManifestPath: filepath.Join(repo, CompanionManifestName),
		Manifest:     *m,
		ManifestYAML: string(yml),
		Items:        items,
	}
}

func applyMicroserviceDefaults(m *CompanionManifest, repo string) {
	if m.Version == 0 {
		m.Version = 1
	}
	if strings.TrimSpace(m.Project) == "" {
		m.Project = sanitizeCompanionName(filepath.Base(repo))
	}
	if strings.TrimSpace(m.Runtime.Bind) == "" {
		m.Runtime.Bind = "device"
	}
	for i := range m.Services {
		if m.Services[i].Workdir == "" && m.Services[i].WorkdirFrom == "" {
			m.Services[i].Workdir = repo
		}
		if m.Services[i].Durable {
			continue
		}
		// Microservice wrapping means "survive reboot" unless explicitly false.
		m.Services[i].Durable = true
	}
}

func serviceFromWrapRequest(req MicroserviceWrapRequest, repo, project string) (CompanionService, DetectedItem) {
	durable := true
	if req.Durable != nil {
		durable = *req.Durable
	}
	name := sanitizeCompanionName(req.Name)
	if name == "" {
		name = sanitizeCompanionName(project)
	}
	workdir := req.Workdir
	if workdir == "" {
		workdir = repo
	} else if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(repo, workdir)
	}
	cmd, args := splitServiceCommand(req.Command, req.Args, req.UseShell)
	svc := CompanionService{
		Name:    name,
		Command: cmd,
		Args:    args,
		Workdir: workdir,
		Port:    req.Port,
		Durable: durable,
	}
	if req.EnvVault != "" {
		svc.EnvFrom = append(svc.EnvFrom, CompanionEnvSource{Vault: req.EnvVault})
	}
	if req.EnvFile != "" {
		svc.EnvFrom = append(svc.EnvFrom, CompanionEnvSource{File: req.EnvFile})
	}
	item := DetectedItem{
		Kind:       "service",
		Name:       name,
		Status:     "configured",
		Confidence: 1,
		Reason:     "explicit microservice wrapper request; Yaver will run this command as a companion service",
	}
	return svc, item
}

func splitServiceCommand(command string, explicitArgs []string, useShell bool) (string, []string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil
	}
	if len(explicitArgs) > 0 {
		return command, explicitArgs
	}
	if useShell || needsShell(command) {
		if runtime.GOOS == "windows" {
			return "cmd", []string{"/C", command}
		}
		return "sh", []string{"-lc", command}
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

func needsShell(command string) bool {
	return strings.ContainsAny(command, "|&;<>()$`*?[]{}~\n")
}

func normalizeRepoDir(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		if cwd := ResolveMCPCwd(); cwd != "" {
			repo = cwd
		}
	}
	if repo == "" {
		return "", fmt.Errorf("repo is required")
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("repo must be a directory: %s", abs)
	}
	return abs, nil
}

func microserviceFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func microserviceNext(result *MicroserviceWrapResult, wrote, armed bool) []string {
	var next []string
	if !wrote {
		next = append(next, "Call microservice_wrap with write=true to save "+CompanionManifestName)
	}
	if wrote && !armed {
		next = append(next, "Call microservice_wrap with arm=true or companion_up to install/start the service")
	}
	if armed {
		next = append(next, "Use microservice_status or companion_status to inspect the running wrapper")
	}
	return next
}

func microserviceMCPTools() []map[string]interface{} {
	repoProp := map[string]interface{}{"type": "string", "description": "Project/repo directory. Defaults to the MCP session working directory."}
	return []map[string]interface{}{
		{
			"name":        "microservice_detect",
			"description": "Analyze a repo for Yaver-wrappable microservices: long-running workers, serverless cron endpoints, and companion services. Read-only; returns proposed yaver.companion.yaml.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo":    repoProp,
					"project": map[string]interface{}{"type": "string", "description": "Optional Yaver companion project slug."},
				},
			},
		},
		{
			"name":        "microservice_wrap",
			"description": "Create or update yaver.companion.yaml for a microservice and optionally arm it as a durable Yaver companion service (systemd user unit on Linux, launchd on macOS).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repo":          repoProp,
					"project":       map[string]interface{}{"type": "string"},
					"name":          map[string]interface{}{"type": "string", "description": "Service name. Defaults to project slug."},
					"command":       map[string]interface{}{"type": "string", "description": "Command to run, e.g. npm run worker. If omitted, Yaver uses detected services."},
					"args":          map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					"workdir":       map[string]interface{}{"type": "string", "description": "Working directory, absolute or repo-relative. Defaults to repo."},
					"port":          map[string]interface{}{"type": "integer"},
					"env_vault":     map[string]interface{}{"type": "string", "description": "Vault project namespace to inject on-device."},
					"env_file":      map[string]interface{}{"type": "string", "description": "Dotenv file path, repo-relative or absolute."},
					"durable":       map[string]interface{}{"type": "boolean", "description": "Default true. Durable services become OS units."},
					"write":         map[string]interface{}{"type": "boolean", "description": "Write yaver.companion.yaml."},
					"arm":           map[string]interface{}{"type": "boolean", "description": "Start/install the companion after writing."},
					"overwrite":     map[string]interface{}{"type": "boolean", "description": "Allow replacing an existing yaver.companion.yaml."},
					"use_shell":     map[string]interface{}{"type": "boolean", "description": "Run the command through sh -lc/cmd /C."},
					"ai_wrap":       map[string]interface{}{"type": "boolean", "description": "Mark this service as an AI-wrapped companion target for agent-run analysis/ops."},
					"ai_work_kind":  map[string]interface{}{"type": "string", "description": "AI wrapper kind, default ops."},
					"base_url_from": map[string]interface{}{"type": "string", "description": "Optional runtime base URL source, e.g. env:SUPABASE_FUNCTIONS_URL."},
					"health_url":    map[string]interface{}{"type": "string", "description": "Optional URL for generated health-check cron."},
					"schedule_cron": map[string]interface{}{"type": "string", "description": "Cron for health_url checks, e.g. */5 * * * *."},
				},
			},
		},
		{
			"name":        "microservice_status",
			"description": "Show the live Yaver companion status for a wrapped microservice project.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name":        "microservice_down",
			"description": "Stop/remove the durable units and schedules for a wrapped microservice project.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"project"},
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string"},
				},
			},
		},
	}
}

func dispatchMicroserviceMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "microservice_detect":
		var args struct {
			Repo    string `json:"repo"`
			Project string `json:"project"`
		}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return true, mcpToolError("invalid arguments: " + err.Error())
		}
		res, err := MicroserviceDetect(args.Repo, args.Project)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(res)
	case "microservice_wrap":
		var req MicroserviceWrapRequest
		if err := json.Unmarshal(arguments, &req); err != nil {
			return true, mcpToolError("invalid arguments: " + err.Error())
		}
		res, err := MicroserviceWrap(s, req)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(res)
	case "microservice_status":
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(arguments, &args)
		if strings.TrimSpace(args.Project) == "" {
			return true, mcpToolError("project is required")
		}
		if s == nil {
			return true, mcpToolError("agent server unavailable")
		}
		status, err := s.companionEngine().Status(args.Project)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(status)
	case "microservice_down":
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(arguments, &args)
		if strings.TrimSpace(args.Project) == "" {
			return true, mcpToolError("project is required")
		}
		if s == nil {
			return true, mcpToolError("agent server unavailable")
		}
		if err := s.companionEngine().Down(args.Project); err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(map[string]interface{}{"ok": true})
	}
	return false, nil
}
