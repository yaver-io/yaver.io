package main

import (
	"context"
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
	Name      string                       `yaml:"name" json:"name"`
	Backend   BackendKind                  `yaml:"backend,omitempty" json:"backend,omitempty"`
	Stack     string                       `yaml:"stack,omitempty" json:"stack,omitempty"`
	Auth      string                       `yaml:"auth,omitempty" json:"auth,omitempty"`
	Runtime   *ManifestRuntimeConfig       `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Placement *ManifestPlacementConfig     `yaml:"placement,omitempty" json:"placement,omitempty"`
	Services  map[string]*DevServiceConfig `yaml:"services,omitempty" json:"services,omitempty"`
	Domains   []ManifestDomain             `yaml:"domains,omitempty" json:"domains,omitempty"`
	Deploy    *DeployConfig                `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Cron      []ManifestCron               `yaml:"cron,omitempty" json:"cron,omitempty"`
	Jobs      []ManifestJob                `yaml:"jobs,omitempty" json:"jobs,omitempty"`
	Env       map[string]string            `yaml:"env,omitempty" json:"env,omitempty"`
}

type ManifestRuntimeConfig struct {
	Frontend *ManifestRuntimeSurface `yaml:"frontend,omitempty" json:"frontend,omitempty"`
	Backend  *ManifestRuntimeBackend `yaml:"backend,omitempty" json:"backend,omitempty"`
	Mobile   *ManifestRuntimeMobile  `yaml:"mobile,omitempty" json:"mobile,omitempty"`
}

type ManifestRuntimeSurface struct {
	Kind          string                  `yaml:"kind,omitempty" json:"kind,omitempty"`
	App           string                  `yaml:"app,omitempty" json:"app,omitempty"`
	Branch        string                  `yaml:"branch,omitempty" json:"branch,omitempty"`
	Domain        string                  `yaml:"domain,omitempty" json:"domain,omitempty"`
	Deployment    string                  `yaml:"deployment,omitempty" json:"deployment,omitempty"`
	Exports       []ManifestRuntimeExport `yaml:"exports,omitempty" json:"exports,omitempty"`
	CredentialRef string                  `yaml:"credential_ref,omitempty" json:"credentialRef,omitempty"`
}

type ManifestRuntimeBackend struct {
	Kind          string                  `yaml:"kind,omitempty" json:"kind,omitempty"`
	App           string                  `yaml:"app,omitempty" json:"app,omitempty"`
	Deployment    string                  `yaml:"deployment,omitempty" json:"deployment,omitempty"`
	ProjectSlug   string                  `yaml:"project_slug,omitempty" json:"projectSlug,omitempty"`
	Exports       []ManifestRuntimeExport `yaml:"exports,omitempty" json:"exports,omitempty"`
	CredentialRef string                  `yaml:"credential_ref,omitempty" json:"credentialRef,omitempty"`
}

type ManifestRuntimeMobile struct {
	App     string                  `yaml:"app,omitempty" json:"app,omitempty"`
	OTA     *ManifestRuntimeOTA     `yaml:"ota,omitempty" json:"ota,omitempty"`
	Exports []ManifestRuntimeExport `yaml:"exports,omitempty" json:"exports,omitempty"`
	Sandbox *ManifestRuntimeSandbox `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
}

type ManifestRuntimeSandbox struct {
	ProjectSlug string                  `yaml:"project_slug,omitempty" json:"projectSlug,omitempty"`
	Exports     []ManifestRuntimeExport `yaml:"exports,omitempty" json:"exports,omitempty"`
}

type ManifestRuntimeOTA struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	Channel  string `yaml:"channel,omitempty" json:"channel,omitempty"`
}

type ManifestRuntimeExport struct {
	Kind          string `yaml:"kind,omitempty" json:"kind,omitempty"`
	App           string `yaml:"app,omitempty" json:"app,omitempty"`
	ProjectSlug   string `yaml:"project_slug,omitempty" json:"projectSlug,omitempty"`
	Target        string `yaml:"target,omitempty" json:"target,omitempty"`
	CredentialRef string `yaml:"credential_ref,omitempty" json:"credentialRef,omitempty"`
}

type ManifestPlacementConfig struct {
	Roles       map[string]ManifestMachineRole `yaml:"roles,omitempty" json:"roles,omitempty"`
	Assignments map[string]string              `yaml:"assignments,omitempty" json:"assignments,omitempty"`
	Policy      ManifestPlacementPolicy        `yaml:"policy,omitempty" json:"policy,omitempty"`
}

type ManifestMachineRole struct {
	Mode         string   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Capabilities []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

type ManifestPlacementPolicy struct {
	PreferOwned       bool    `yaml:"prefer_owned,omitempty" json:"preferOwned,omitempty"`
	AllowManagedCloud bool    `yaml:"allow_managed_cloud,omitempty" json:"allowManagedCloud,omitempty"`
	MonthlyBudgetUSD  float64 `yaml:"monthly_budget_usd,omitempty" json:"monthlyBudgetUsd,omitempty"`
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

type ManifestJob struct {
	ID          string                 `yaml:"id,omitempty" json:"id,omitempty"`
	Kind        string                 `yaml:"kind,omitempty" json:"kind,omitempty"`
	MachineRole string                 `yaml:"machine_role,omitempty" json:"machineRole,omitempty"`
	Schedule    *ManifestJobSchedule   `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	Monitor     *ManifestMonitorConfig `yaml:"monitor,omitempty" json:"monitor,omitempty"`
	Convex      *ManifestConvexJob     `yaml:"convex,omitempty" json:"convex,omitempty"`
	Steps       []ManifestJobStep      `yaml:"steps,omitempty" json:"steps,omitempty"`
}

type ManifestJobSchedule struct {
	Cron     string `yaml:"cron,omitempty" json:"cron,omitempty"`
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	RunAt    string `yaml:"run_at,omitempty" json:"runAt,omitempty"`
}

type ManifestMonitorConfig struct {
	URL                string `yaml:"url,omitempty" json:"url,omitempty"`
	Interval           string `yaml:"interval,omitempty" json:"interval,omitempty"`
	AlertAfterFailures int    `yaml:"alert_after_failures,omitempty" json:"alertAfterFailures,omitempty"`
}

type ManifestConvexJob struct {
	Function string                 `yaml:"function,omitempty" json:"function,omitempty"`
	Args     map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"`
}

type ManifestJobStep struct {
	Kind string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Run  string `yaml:"run,omitempty" json:"run,omitempty"`
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
	Steps []string `json:"steps"`
	Diff  []string `json:"diff"`
	Error string   `json:"error,omitempty"`
}

type ProjectRuntimeProviderInput struct {
	Provider string            `json:"provider"`
	Label    string            `json:"label,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

type ProjectRuntimePhonePromotion struct {
	Slug   string `json:"slug"`
	Target string `json:"target"`
	Run    bool   `json:"run,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
}

type ProjectRuntimeApplyRequest struct {
	Name             string                         `json:"name,omitempty"`
	PhoneSlug        string                         `json:"phoneSlug,omitempty"`
	Backend          BackendKind                    `json:"backend,omitempty"`
	Stack            string                         `json:"stack,omitempty"`
	Auth             string                         `json:"auth,omitempty"`
	Runtime          *ManifestRuntimeConfig         `json:"runtime,omitempty"`
	Placement        *ManifestPlacementConfig       `json:"placement,omitempty"`
	Jobs             []ManifestJob                  `json:"jobs,omitempty"`
	Domains          []ManifestDomain               `json:"domains,omitempty"`
	Env              map[string]string              `json:"env,omitempty"`
	Providers        []ProjectRuntimeProviderInput  `json:"providers,omitempty"`
	PhonePromotions  []ProjectRuntimePhonePromotion `json:"phonePromotions,omitempty"`
	RunManifestApply bool                           `json:"runManifestApply,omitempty"`
	DryRun           bool                           `json:"dryRun,omitempty"`
}

type ProjectRuntimePlannedAction struct {
	Kind    string `json:"kind"`
	Target  string `json:"target,omitempty"`
	Details string `json:"details,omitempty"`
}

type ProjectRuntimeApplyResponse struct {
	OK              bool                          `json:"ok"`
	Actions         []ProjectRuntimePlannedAction `json:"actions,omitempty"`
	ManifestSaved   bool                          `json:"manifestSaved,omitempty"`
	AccountsApplied []string                      `json:"accountsApplied,omitempty"`
	ManifestApply   *ApplyResult                  `json:"manifestApply,omitempty"`
	PhoneSwitches   []map[string]interface{}      `json:"phoneSwitches,omitempty"`
	Summary         *ProjectRuntimeSummary        `json:"summary,omitempty"`
	Error           string                        `json:"error,omitempty"`
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

func loadManifestOptional(dir string) (*ProjectManifest, error) {
	m, err := LoadManifest(dir)
	if err == nil {
		return m, nil
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	return nil, err
}

func MergeProjectRuntimeManifest(base *ProjectManifest, req ProjectRuntimeApplyRequest) *ProjectManifest {
	if base == nil {
		base = &ProjectManifest{}
	}
	out := *base
	if strings.TrimSpace(req.Name) != "" {
		out.Name = strings.TrimSpace(req.Name)
	}
	if req.Backend != "" {
		out.Backend = req.Backend
	}
	if strings.TrimSpace(req.Stack) != "" {
		out.Stack = strings.TrimSpace(req.Stack)
	}
	if strings.TrimSpace(req.Auth) != "" {
		out.Auth = strings.TrimSpace(req.Auth)
	}
	if req.Runtime != nil {
		runtimeCopy := *req.Runtime
		out.Runtime = &runtimeCopy
	}
	if req.Placement != nil {
		placementCopy := *req.Placement
		out.Placement = &placementCopy
	}
	if req.Jobs != nil {
		out.Jobs = append([]ManifestJob(nil), req.Jobs...)
	}
	if req.Domains != nil {
		out.Domains = append([]ManifestDomain(nil), req.Domains...)
	}
	if req.Env != nil {
		mergedEnv := map[string]string{}
		for k, v := range out.Env {
			mergedEnv[k] = v
		}
		for k, v := range req.Env {
			mergedEnv[k] = v
		}
		out.Env = mergedEnv
	}
	return &out
}

func PlanProjectRuntimeMutation(dir string, req ProjectRuntimeApplyRequest) (*ProjectRuntimeApplyResponse, *ProjectManifest, error) {
	existing, err := loadManifestOptional(dir)
	if err != nil {
		return nil, nil, err
	}
	merged := MergeProjectRuntimeManifest(existing, req)
	resp := &ProjectRuntimeApplyResponse{
		OK: false,
	}
	for _, provider := range req.Providers {
		if strings.TrimSpace(provider.Provider) == "" {
			continue
		}
		label := strings.TrimSpace(provider.Label)
		if label == "" {
			label = strings.TrimSpace(provider.Provider)
		}
		resp.Actions = append(resp.Actions, ProjectRuntimePlannedAction{
			Kind:    "connect-account",
			Target:  strings.TrimSpace(provider.Provider),
			Details: "Store provider credentials in the Yaver account vault",
		})
		_ = label
	}
	resp.Actions = append(resp.Actions, ProjectRuntimePlannedAction{
		Kind:    "save-manifest",
		Target:  manifestPath(dir),
		Details: "Merge runtime, placement, job, and export changes into .yaver/project.yaml",
	})
	if req.RunManifestApply {
		resp.Actions = append(resp.Actions, ProjectRuntimePlannedAction{
			Kind:    "apply-manifest",
			Target:  dir,
			Details: "Reconcile services, domains, deploy config, cron, and env with the merged manifest",
		})
	}
	for _, promotion := range req.PhonePromotions {
		if strings.TrimSpace(promotion.Slug) == "" || strings.TrimSpace(promotion.Target) == "" {
			continue
		}
		details := "Plan phone sandbox promotion with the switch engine"
		if promotion.Run {
			details = "Plan and run phone sandbox promotion with the switch engine"
		}
		resp.Actions = append(resp.Actions, ProjectRuntimePlannedAction{
			Kind:    "phone-promote",
			Target:  strings.TrimSpace(promotion.Slug) + " -> " + strings.TrimSpace(promotion.Target),
			Details: details,
		})
	}
	return resp, merged, nil
}

func ApplyProjectRuntimeMutation(ctx context.Context, s *HTTPServer, dir string, req ProjectRuntimeApplyRequest) (*ProjectRuntimeApplyResponse, error) {
	if strings.TrimSpace(dir) == "" && strings.TrimSpace(req.PhoneSlug) != "" {
		resolvedDir, err := PhoneProjectDir(req.PhoneSlug)
		if err != nil {
			return nil, err
		}
		dir = resolvedDir
	}
	resp, merged, err := PlanProjectRuntimeMutation(dir, req)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		resp.OK = true
		resp.Summary = buildProjectRuntimeSummaryFromManifest(ctx, s, dir, merged)
		return resp, nil
	}

	for _, provider := range req.Providers {
		if strings.TrimSpace(provider.Provider) == "" {
			continue
		}
		if err := globalAccountsManager.Connect(AccountProvider(provider.Provider), provider.Label, provider.Fields); err != nil {
			resp.Error = err.Error()
			return resp, err
		}
		resp.AccountsApplied = append(resp.AccountsApplied, strings.TrimSpace(provider.Provider))
	}

	if err := SaveManifest(dir, merged); err != nil {
		resp.Error = err.Error()
		return resp, err
	}
	resp.ManifestSaved = true

	if req.RunManifestApply {
		applyRes, err := ApplyManifest(dir)
		resp.ManifestApply = applyRes
		if err != nil {
			resp.Error = err.Error()
			return resp, err
		}
	}

	for _, promotion := range req.PhonePromotions {
		if strings.TrimSpace(promotion.Slug) == "" || strings.TrimSpace(promotion.Target) == "" {
			continue
		}
		phoneDir, err := PhoneProjectDir(promotion.Slug)
		if err != nil {
			resp.Error = err.Error()
			return resp, err
		}
		engine := NewSwitchEngine()
		state, err := engine.Plan(phoneDir, promotion.Target, promotion.DryRun)
		if err != nil {
			resp.Error = err.Error()
			return resp, err
		}
		if err := engine.Persist(state); err != nil {
			resp.Error = err.Error()
			return resp, err
		}
		result := map[string]interface{}{
			"slug":   promotion.Slug,
			"target": promotion.Target,
			"state":  state,
		}
		if promotion.Run {
			if err := engine.Run(state); err != nil {
				result["error"] = err.Error()
				resp.PhoneSwitches = append(resp.PhoneSwitches, result)
				resp.Error = err.Error()
				return resp, err
			}
		}
		resp.PhoneSwitches = append(resp.PhoneSwitches, result)
	}

	resp.OK = true
	if summary, err := BuildProjectRuntimeSummary(ctx, s, dir); err == nil {
		resp.Summary = summary
	} else {
		resp.Summary = buildProjectRuntimeSummaryFromManifest(ctx, s, dir, merged)
	}
	return resp, nil
}

type ProjectRuntimeWorkspaceSummary struct {
	Root     string             `json:"root"`
	Manifest *WorkspaceManifest `json:"manifest,omitempty"`
}

type ProjectRuntimeResolvedRole struct {
	Role                 string       `json:"role"`
	Mode                 string       `json:"mode,omitempty"`
	RequiredCapabilities []string     `json:"requiredCapabilities,omitempty"`
	Machine              *MachineInfo `json:"machine,omitempty"`
	Reason               string       `json:"reason,omitempty"`
	CandidateCount       int          `json:"candidateCount"`
}

type ProjectRuntimeResolvedAssignment struct {
	Name                 string       `json:"name"`
	Role                 string       `json:"role"`
	RequiredCapabilities []string     `json:"requiredCapabilities,omitempty"`
	Machine              *MachineInfo `json:"machine,omitempty"`
	Reason               string       `json:"reason,omitempty"`
}

type ProjectRuntimeProviderRequirement struct {
	Provider      string   `json:"provider"`
	Label         string   `json:"label,omitempty"`
	AuthType      string   `json:"authType,omitempty"`
	Fields        []string `json:"fields,omitempty"`
	CredentialRef string   `json:"credentialRef,omitempty"`
	RequiredBy    []string `json:"requiredBy,omitempty"`
	Connected     bool     `json:"connected"`
	AuthSource    string   `json:"authSource,omitempty"`
	Warning       string   `json:"warning,omitempty"`
}

type ProjectRuntimeExportPlan struct {
	Name                 string       `json:"name"`
	Source               string       `json:"source"`
	Kind                 string       `json:"kind,omitempty"`
	Provider             string       `json:"provider,omitempty"`
	Target               string       `json:"target,omitempty"`
	App                  string       `json:"app,omitempty"`
	ProjectSlug          string       `json:"projectSlug,omitempty"`
	CredentialRef        string       `json:"credentialRef,omitempty"`
	MachineRole          string       `json:"machineRole,omitempty"`
	RequiredCapabilities []string     `json:"requiredCapabilities,omitempty"`
	Machine              *MachineInfo `json:"machine,omitempty"`
	Reason               string       `json:"reason,omitempty"`
	ProviderReady        bool         `json:"providerReady"`
	ProviderAuthSource   string       `json:"providerAuthSource,omitempty"`
	Warning              string       `json:"warning,omitempty"`
}

type ProjectRuntimeSummary struct {
	ProjectDir           string                              `json:"projectDir"`
	Manifest             *ProjectManifest                    `json:"manifest,omitempty"`
	Workspace            *ProjectRuntimeWorkspaceSummary     `json:"workspace,omitempty"`
	Machines             []MachineInfo                       `json:"machines,omitempty"`
	ResolvedRoles        []ProjectRuntimeResolvedRole        `json:"resolvedRoles,omitempty"`
	ResolvedAssignments  []ProjectRuntimeResolvedAssignment  `json:"resolvedAssignments,omitempty"`
	ProviderRequirements []ProjectRuntimeProviderRequirement `json:"providerRequirements,omitempty"`
	ExportPlans          []ProjectRuntimeExportPlan          `json:"exportPlans,omitempty"`
	Warnings             []string                            `json:"warnings,omitempty"`
}

func BuildProjectRuntimeSummary(ctx context.Context, s *HTTPServer, dir string) (*ProjectRuntimeSummary, error) {
	m, err := LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	return buildProjectRuntimeSummaryFromManifest(ctx, s, dir, m), nil
}

func buildProjectRuntimeSummaryFromManifest(ctx context.Context, s *HTTPServer, dir string, m *ProjectManifest) *ProjectRuntimeSummary {
	summary := &ProjectRuntimeSummary{
		ProjectDir: dir,
		Manifest:   m,
		Machines:   listAllMachines(ctx),
	}

	if root, wm := loadNearestWorkspaceManifest(dir); wm != nil {
		summary.Workspace = &ProjectRuntimeWorkspaceSummary{
			Root:     root,
			Manifest: wm,
		}
	}

	primaryDeviceID := ""
	if s != nil {
		if resolved, err := resolvePrimaryDeviceID(ctx, s); err == nil {
			primaryDeviceID = strings.TrimSpace(resolved)
		}
	}

	defaultRole := "primary"
	allowManagedCloud := false
	if summary.Workspace != nil {
		if role := strings.TrimSpace(summary.Workspace.Manifest.Workspace.Placement.DefaultExecutionRole); role != "" {
			defaultRole = role
		}
		allowManagedCloud = summary.Workspace.Manifest.Workspace.Placement.ManagedCloudFallback
	}
	if m.Placement != nil {
		if m.Placement.Policy.AllowManagedCloud {
			allowManagedCloud = true
		}
	}

	roleDefs := map[string]ManifestMachineRole{}
	roleDefs[defaultRole] = ManifestMachineRole{}
	if m.Placement != nil {
		for name, role := range m.Placement.Roles {
			roleDefs[name] = role
		}
		for _, role := range m.Placement.Assignments {
			if _, ok := roleDefs[role]; !ok {
				roleDefs[role] = ManifestMachineRole{}
			}
		}
	}

	roleNames := make([]string, 0, len(roleDefs))
	for name := range roleDefs {
		roleNames = append(roleNames, name)
	}
	sort.Strings(roleNames)

	resolvedByRole := map[string]ProjectRuntimeResolvedRole{}
	for _, roleName := range roleNames {
		resolved := resolveProjectRuntimeRole(summary.Machines, roleName, roleDefs[roleName], primaryDeviceID, allowManagedCloud)
		resolvedByRole[roleName] = resolved
		summary.ResolvedRoles = append(summary.ResolvedRoles, resolved)
		if resolved.Machine == nil {
			summary.Warnings = append(summary.Warnings, fmt.Sprintf("no machine resolved for role %q", roleName))
		}
	}

	if m.Placement != nil && len(m.Placement.Assignments) > 0 {
		assignmentNames := make([]string, 0, len(m.Placement.Assignments))
		for name := range m.Placement.Assignments {
			assignmentNames = append(assignmentNames, name)
		}
		sort.Strings(assignmentNames)
		for _, name := range assignmentNames {
			roleName := strings.TrimSpace(m.Placement.Assignments[name])
			role := resolvedByRole[roleName]
			summary.ResolvedAssignments = append(summary.ResolvedAssignments, ProjectRuntimeResolvedAssignment{
				Name:                 name,
				Role:                 roleName,
				RequiredCapabilities: append([]string(nil), role.RequiredCapabilities...),
				Machine:              cloneResolvedMachine(role.Machine),
				Reason:               role.Reason,
			})
		}
	}

	if m.Runtime != nil {
		appendImplicitRuntimeAssignments(summary, resolvedByRole, defaultRole)
		summary.ExportPlans = buildProjectRuntimeExportPlans(summary, resolvedByRole, defaultRole)
	}
	summary.ProviderRequirements = buildProjectRuntimeProviderRequirements(summary)
	for _, plan := range summary.ExportPlans {
		if strings.TrimSpace(plan.Warning) != "" {
			summary.Warnings = append(summary.Warnings, plan.Warning)
		}
	}

	return summary
}

func loadNearestWorkspaceManifest(dir string) (string, *WorkspaceManifest) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	cur := abs
	for {
		if _, err := os.Stat(filepath.Join(cur, WorkspaceManifestPath)); err == nil {
			if wm, err := LoadWorkspaceManifest(cur); err == nil {
				return cur, wm
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

func appendImplicitRuntimeAssignments(summary *ProjectRuntimeSummary, resolvedByRole map[string]ProjectRuntimeResolvedRole, defaultRole string) {
	if summary == nil || summary.Manifest == nil || summary.Manifest.Runtime == nil {
		return
	}
	seen := map[string]bool{}
	for _, assignment := range summary.ResolvedAssignments {
		seen[assignment.Name] = true
	}
	appendOne := func(name string, roleName string) {
		if seen[name] {
			return
		}
		role := resolvedByRole[roleName]
		summary.ResolvedAssignments = append(summary.ResolvedAssignments, ProjectRuntimeResolvedAssignment{
			Name:                 name,
			Role:                 roleName,
			RequiredCapabilities: append([]string(nil), role.RequiredCapabilities...),
			Machine:              cloneResolvedMachine(role.Machine),
			Reason:               role.Reason,
		})
		seen[name] = true
	}
	if summary.Manifest.Runtime.Frontend != nil {
		appendOne("frontend", defaultRole)
	}
	if summary.Manifest.Runtime.Backend != nil {
		appendOne("backend", defaultRole)
	}
	if summary.Manifest.Runtime.Mobile != nil {
		appendOne("mobile", defaultRole)
	}
	for _, job := range summary.Manifest.Jobs {
		roleName := strings.TrimSpace(job.MachineRole)
		if roleName == "" {
			roleName = defaultRole
		}
		appendOne("job:"+jobDisplayName(job), roleName)
	}
}

func buildProjectRuntimeExportPlans(summary *ProjectRuntimeSummary, resolvedByRole map[string]ProjectRuntimeResolvedRole, defaultRole string) []ProjectRuntimeExportPlan {
	if summary == nil || summary.Manifest == nil || summary.Manifest.Runtime == nil {
		return nil
	}
	plans := []ProjectRuntimeExportPlan{}
	appendPlan := func(name string, source string, kind string, app string, projectSlug string, credentialRef string, explicitRole string, exports []ManifestRuntimeExport) {
		roleName := strings.TrimSpace(explicitRole)
		if roleName == "" {
			roleName = projectRuntimeAssignmentRole(summary.Manifest, name)
		}
		if roleName == "" {
			roleName = defaultRole
		}
		role := resolvedByRole[roleName]
		if len(exports) == 0 {
			provider := projectRuntimeProviderName(kind, "")
			connected, authSource := projectRuntimeProviderStatus(provider)
			plans = append(plans, ProjectRuntimeExportPlan{
				Name:                 name,
				Source:               source,
				Kind:                 kind,
				Provider:             provider,
				App:                  strings.TrimSpace(app),
				ProjectSlug:          strings.TrimSpace(projectSlug),
				CredentialRef:        strings.TrimSpace(credentialRef),
				MachineRole:          roleName,
				RequiredCapabilities: append([]string(nil), role.RequiredCapabilities...),
				Machine:              cloneResolvedMachine(role.Machine),
				Reason:               role.Reason,
				ProviderReady:        connected || provider == "",
				ProviderAuthSource:   authSource,
				Warning:              projectRuntimeExportWarning(name, provider, strings.TrimSpace(credentialRef), connected, authSource),
			})
			return
		}
		for i, export := range exports {
			provider := projectRuntimeProviderName(export.Kind, export.Target)
			connected, authSource := projectRuntimeProviderStatus(provider)
			planName := name
			if len(exports) > 1 {
				planName = fmt.Sprintf("%s[%d]", name, i+1)
			}
			planCredentialRef := strings.TrimSpace(export.CredentialRef)
			if planCredentialRef == "" {
				planCredentialRef = strings.TrimSpace(credentialRef)
			}
			plans = append(plans, ProjectRuntimeExportPlan{
				Name:                 planName,
				Source:               source,
				Kind:                 strings.TrimSpace(export.Kind),
				Provider:             provider,
				Target:               strings.TrimSpace(export.Target),
				App:                  firstNonEmpty(strings.TrimSpace(export.App), strings.TrimSpace(app)),
				ProjectSlug:          firstNonEmpty(strings.TrimSpace(export.ProjectSlug), strings.TrimSpace(projectSlug)),
				CredentialRef:        planCredentialRef,
				MachineRole:          roleName,
				RequiredCapabilities: append([]string(nil), role.RequiredCapabilities...),
				Machine:              cloneResolvedMachine(role.Machine),
				Reason:               role.Reason,
				ProviderReady:        connected || provider == "",
				ProviderAuthSource:   authSource,
				Warning:              projectRuntimeExportWarning(planName, provider, planCredentialRef, connected, authSource),
			})
		}
	}

	if rt := summary.Manifest.Runtime.Frontend; rt != nil {
		appendPlan("frontend", "runtime.frontend", rt.Kind, rt.App, "", rt.CredentialRef, "", rt.Exports)
	}
	if rt := summary.Manifest.Runtime.Backend; rt != nil {
		appendPlan("backend", "runtime.backend", rt.Kind, rt.App, rt.ProjectSlug, rt.CredentialRef, "", rt.Exports)
	}
	if rt := summary.Manifest.Runtime.Mobile; rt != nil {
		appendPlan("mobile", "runtime.mobile", "react-native", rt.App, "", "", "", rt.Exports)
		if rt.Sandbox != nil {
			appendPlan("mobile-sandbox", "runtime.mobile.sandbox", "mobile-sandbox", rt.App, rt.Sandbox.ProjectSlug, "", "cron", rt.Sandbox.Exports)
		}
	}
	return plans
}

func buildProjectRuntimeProviderRequirements(summary *ProjectRuntimeSummary) []ProjectRuntimeProviderRequirement {
	if summary == nil {
		return nil
	}
	order := []string{}
	byProvider := map[string]*ProjectRuntimeProviderRequirement{}
	for _, plan := range summary.ExportPlans {
		provider := strings.TrimSpace(plan.Provider)
		if provider == "" {
			continue
		}
		req := byProvider[provider]
		if req == nil {
			req = &ProjectRuntimeProviderRequirement{
				Provider: provider,
			}
			if meta := projectRuntimeProviderMeta(provider); meta != nil {
				req.Label = meta.Label
				req.AuthType = meta.AuthType
				req.Fields = append([]string(nil), meta.Fields...)
			}
			req.Connected, req.AuthSource = projectRuntimeProviderStatus(provider)
			byProvider[provider] = req
			order = append(order, provider)
		}
		if req.CredentialRef == "" && strings.TrimSpace(plan.CredentialRef) != "" {
			req.CredentialRef = strings.TrimSpace(plan.CredentialRef)
		}
		req.RequiredBy = append(req.RequiredBy, plan.Name)
	}

	out := make([]ProjectRuntimeProviderRequirement, 0, len(order))
	for _, provider := range order {
		req := byProvider[provider]
		sort.Strings(req.RequiredBy)
		req.RequiredBy = uniqTrimmedStrings(req.RequiredBy)
		if !req.Connected {
			switch {
			case req.CredentialRef != "":
				req.Warning = fmt.Sprintf("%s credentials are not connected on this machine; manifest references %q", req.Provider, req.CredentialRef)
			default:
				req.Warning = fmt.Sprintf("%s credentials are not connected on this machine", req.Provider)
			}
		}
		out = append(out, *req)
	}
	return out
}

func projectRuntimeAssignmentRole(m *ProjectManifest, assignment string) string {
	if m == nil || m.Placement == nil {
		return ""
	}
	return strings.TrimSpace(m.Placement.Assignments[assignment])
}

func jobDisplayName(job ManifestJob) string {
	if strings.TrimSpace(job.ID) != "" {
		return job.ID
	}
	if strings.TrimSpace(job.Kind) != "" {
		return job.Kind
	}
	return "unnamed"
}

func resolveProjectRuntimeRole(machines []MachineInfo, roleName string, role ManifestMachineRole, primaryDeviceID string, allowManagedCloud bool) ProjectRuntimeResolvedRole {
	reqs := uniqLowerStrings(role.Capabilities)
	mode := strings.TrimSpace(role.Mode)
	if mode == "" {
		mode = "owned"
	}
	candidates := make([]MachineInfo, 0, len(machines))
	for _, machine := range machines {
		if projectRuntimeMachineMatches(machine, mode, reqs) {
			candidates = append(candidates, machine)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return projectRuntimeMachineScore(candidates[i], roleName, primaryDeviceID, mode) >
			projectRuntimeMachineScore(candidates[j], roleName, primaryDeviceID, mode)
	})
	resolved := ProjectRuntimeResolvedRole{
		Role:                 roleName,
		Mode:                 mode,
		RequiredCapabilities: reqs,
		CandidateCount:       len(candidates),
	}
	if len(candidates) > 0 {
		resolved.Machine = cloneResolvedMachine(&candidates[0])
		resolved.Reason = projectRuntimeReason(candidates[0], primaryDeviceID, mode)
		return resolved
	}
	if allowManagedCloud {
		for _, machine := range machines {
			if !machine.IsOnline || machine.IsShared {
				continue
			}
			if !projectRuntimeMachineSupports(machine, reqs) {
				continue
			}
			if projectRuntimeLooksManaged(machine) {
				resolved.Machine = cloneResolvedMachine(&machine)
				resolved.Reason = "fell back to an online managed/cloud-capable machine"
				return resolved
			}
		}
	}
	resolved.Reason = "no online machine matched the role constraints"
	return resolved
}

func projectRuntimeMachineMatches(machine MachineInfo, mode string, reqs []string) bool {
	if !machine.IsOnline {
		return false
	}
	switch mode {
	case "owned":
		if machine.IsShared {
			return false
		}
	case "owned-or-cloud", "always-on", "optional":
		if machine.IsShared {
			return false
		}
	}
	return projectRuntimeMachineSupports(machine, reqs)
}

func projectRuntimeMachineSupports(machine MachineInfo, reqs []string) bool {
	for _, req := range reqs {
		switch req {
		case "ios-build", "ios", "testflight":
			if !machineSupportsIOS(machine.Capabilities) {
				return false
			}
		case "android-build", "android", "playstore":
			if !machineSupportsAndroid(machine.Capabilities) {
				return false
			}
		case "local-llm", "gpu":
			if machine.Capabilities == nil || !machine.Capabilities.SupportsLocalLLM {
				return false
			}
		case "docker":
			if machine.Capabilities == nil || !machine.Capabilities.SupportsDocker {
				return false
			}
		}
	}
	return true
}

func projectRuntimeMachineScore(machine MachineInfo, roleName string, primaryDeviceID string, mode string) int {
	score := 0
	if machine.DeviceID == primaryDeviceID && primaryDeviceID != "" {
		score += 40
	}
	if !machine.IsShared {
		score += 20
	}
	if machine.Capabilities != nil && !machine.Capabilities.LowPower {
		score += 10
	}
	if machine.Capabilities != nil && machine.Capabilities.MaxTaskSlots > 1 {
		score += machine.Capabilities.MaxTaskSlots
	}
	if mode == "always-on" {
		if projectRuntimeLooksManaged(machine) {
			score += 15
		}
		if machine.IsLocal {
			score -= 5
		}
	}
	if roleName == "gpu" && machine.Capabilities != nil && machine.Capabilities.SupportsLocalLLM {
		score += 20
	}
	if roleName == "build-mac" && machineSupportsIOS(machine.Capabilities) {
		score += 20
	}
	return score
}

func projectRuntimeLooksManaged(machine MachineInfo) bool {
	provider := strings.ToLower(strings.TrimSpace(machine.Provider))
	if provider == "" {
		return false
	}
	return provider != "local" && provider != "local-mac"
}

func projectRuntimeReason(machine MachineInfo, primaryDeviceID string, mode string) string {
	parts := []string{}
	if machine.DeviceID == primaryDeviceID && primaryDeviceID != "" {
		parts = append(parts, "matches the user's primary device")
	}
	if mode == "always-on" && projectRuntimeLooksManaged(machine) {
		parts = append(parts, "looks like an always-on remote machine")
	}
	if machine.Capabilities != nil && !machine.Capabilities.LowPower {
		parts = append(parts, "has non-low-power capability profile")
	}
	if len(parts) == 0 {
		parts = append(parts, "best available online match")
	}
	return strings.Join(parts, "; ")
}

func cloneResolvedMachine(machine *MachineInfo) *MachineInfo {
	if machine == nil {
		return nil
	}
	copy := *machine
	return &copy
}

func uniqLowerStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqTrimmedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func projectRuntimeProviderName(kind string, target string) string {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(kind, target)))
	switch value {
	case "cloudflare", "cloudflare-pages", "cloudflare-workers", "dns-cloudflare":
		return string(ProviderCloudflare)
	case "convex", "convex-cloud":
		return string(ProviderConvex)
	case "yaver", "yaver-cloud", "managed-cloud":
		return string(ProviderYaver)
	default:
		return ""
	}
}

func projectRuntimeProviderMeta(provider string) *AccountProviderMeta {
	for _, meta := range AccountProviders() {
		if string(meta.ID) == provider {
			copy := meta
			return &copy
		}
	}
	return nil
}

func projectRuntimeProviderStatus(provider string) (bool, string) {
	switch strings.TrimSpace(provider) {
	case "":
		return true, ""
	case string(ProviderCloudflare), string(ProviderSupabase), string(ProviderNeon), string(ProviderVercel),
		string(ProviderHetzner), string(ProviderRailway), string(ProviderFly), string(ProviderRender),
		string(ProviderAWS), string(ProviderGCP), string(ProviderDigitalOcean), string(ProviderTurso):
		rec, err := globalAccountsManager.Get(AccountProvider(provider))
		if err != nil || rec == nil {
			return false, ""
		}
		return true, "yaver-account"
	case string(ProviderYaver):
		rec, err := globalAccountsManager.Get(ProviderYaver)
		if err == nil && rec != nil {
			return true, "yaver-account"
		}
		if cfg, err := LoadConfig(); err == nil && cfg != nil && strings.TrimSpace(cfg.AuthToken) != "" {
			return true, "yaver-auth"
		}
		return false, ""
	case string(ProviderConvex):
		rec, err := globalAccountsManager.Get(ProviderConvex)
		if err == nil && rec != nil {
			return true, "yaver-account"
		}
		if strings.TrimSpace(os.Getenv("CONVEX_DEPLOY_KEY")) != "" {
			return true, "env:CONVEX_DEPLOY_KEY"
		}
		home, _ := os.UserHomeDir()
		if home != "" {
			if _, err := os.Stat(filepath.Join(home, ".convex", "config.json")); err == nil {
				return true, "~/.convex/config.json"
			}
		}
		return false, ""
	default:
		return false, ""
	}
}

func projectRuntimeExportWarning(name string, provider string, credentialRef string, connected bool, authSource string) string {
	if strings.TrimSpace(provider) == "" || connected {
		return ""
	}
	switch {
	case strings.TrimSpace(credentialRef) != "":
		return fmt.Sprintf("export %q needs %s credentials on the selected machine; manifest references %q", name, provider, credentialRef)
	case strings.TrimSpace(authSource) != "":
		return ""
	default:
		return fmt.Sprintf("export %q needs %s credentials on the selected machine", name, provider)
	}
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleProjectRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	summary, err := BuildProjectRuntimeSummary(r.Context(), s, s.dirParam(r))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func mcpProjectRuntime(dir string) interface{} {
	summary, err := BuildProjectRuntimeSummary(context.Background(), nil, dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return summary
}

func mcpProjectRuntimeApply(dir string, req ProjectRuntimeApplyRequest) interface{} {
	resp, err := ApplyProjectRuntimeMutation(context.Background(), nil, dir, req)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "result": resp}
	}
	return resp
}

func (s *HTTPServer) handleProjectRuntimeApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req ProjectRuntimeApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	resp, err := ApplyProjectRuntimeMutation(r.Context(), s, s.dirParam(r), req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": resp})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

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
