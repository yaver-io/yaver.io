package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type OpsExecutionPlan struct {
	OK               bool                     `json:"ok"`
	Verb             string                   `json:"verb"`
	RequestedMachine string                   `json:"requestedMachine"`
	ResolvedMachine  string                   `json:"resolvedMachine"`
	SelectionReason  string                   `json:"selectionReason,omitempty"`
	RemoteExecution  bool                     `json:"remoteExecution"`
	Access           OpsExecutionAccessPlan   `json:"access"`
	Project          *OpsExecutionProjectPlan `json:"project,omitempty"`
	Warnings         []string                 `json:"warnings,omitempty"`
}

type OpsExecutionAccessPlan struct {
	Caller                 string   `json:"caller"`
	GuestUserID            string   `json:"guestUserId,omitempty"`
	GuestScope             string   `json:"guestScope,omitempty"`
	HostShare              bool     `json:"hostShare,omitempty"`
	HostShareSessionID     string   `json:"hostShareSessionId,omitempty"`
	HostShareToolingPreset string   `json:"hostShareToolingPreset,omitempty"`
	AllowedProjects        []string `json:"allowedProjects,omitempty"`
	AllowedRunners         []string `json:"allowedRunners,omitempty"`
	ResourcePreset         string   `json:"resourcePreset,omitempty"`
	PriorityMode           string   `json:"priorityMode,omitempty"`
	AllowInfra             bool     `json:"allowInfra,omitempty"`
	RequireIsolation       bool     `json:"requireIsolation,omitempty"`
	AllowDesktopControl    bool     `json:"allowDesktopControl,omitempty"`
	AllowBrowserControl    bool     `json:"allowBrowserControl,omitempty"`
	AllowTunnelForward     bool     `json:"allowTunnelForward,omitempty"`
	UseHostAPIKeys         bool     `json:"useHostApiKeys,omitempty"`
	AllowGuestProvidedAK   bool     `json:"allowGuestProvidedApiKeys,omitempty"`
	UseHostAgentTools      bool     `json:"useHostAgentTools,omitempty"`
	UseHostInfra           bool     `json:"useHostInfra,omitempty"`
	CPULimitPercent        *int     `json:"cpuLimitPercent,omitempty"`
	RAMLimitMB             *int     `json:"ramLimitMb,omitempty"`
}

type OpsExecutionProjectPlan struct {
	WorkDir              string                              `json:"workDir"`
	RequestedProject     string                              `json:"requestedProject,omitempty"`
	Name                 string                              `json:"name,omitempty"`
	GitBranch            string                              `json:"gitBranch,omitempty"`
	GitRemote            string                              `json:"gitRemote,omitempty"`
	Framework            string                              `json:"framework,omitempty"`
	Tags                 []string                            `json:"tags,omitempty"`
	Discovery            projectDiscoverySnapshot            `json:"discovery"`
	WorkspaceRoot        string                              `json:"workspaceRoot,omitempty"`
	WorkspaceName        string                              `json:"workspaceName,omitempty"`
	WorkspacePrimary     string                              `json:"workspacePrimaryDevice,omitempty"`
	WorkspaceVaultMode   string                              `json:"workspaceVaultMode,omitempty"`
	ProjectRemote        *ProjectRemote                      `json:"projectRemote,omitempty"`
	RuntimeAssignments   []ProjectRuntimeResolvedAssignment  `json:"runtimeAssignments,omitempty"`
	ExportPlans          []ProjectRuntimeExportPlan          `json:"exportPlans,omitempty"`
	ProviderRequirements []ProjectRuntimeProviderRequirement `json:"providerRequirements,omitempty"`
	RuntimeWarnings      []string                            `json:"runtimeWarnings,omitempty"`
}

func buildOpsExecutionPlan(octx OpsContext, req OpsRequest) OpsExecutionPlan {
	plan := OpsExecutionPlan{
		OK:               true,
		Verb:             strings.TrimSpace(req.Verb),
		RequestedMachine: strings.TrimSpace(req.Machine),
		Access:           buildOpsExecutionAccessPlan(octx),
	}
	if plan.RequestedMachine == "" {
		plan.RequestedMachine = "local"
	}

	resolved := plan.RequestedMachine
	decision := autoMachineDecision{}
	switch resolved {
	case "", "local":
		resolved = "local"
	case "auto":
		decision = resolveAutoOpsMachine(octx, req)
		resolved = decision.Machine
	case "primary":
		if octx.Server == nil {
			plan.Warnings = append(plan.Warnings, "primary alias could not be resolved without a server context")
		} else if deviceID, err := resolvePrimaryDeviceID(octx.Ctx, octx.Server); err != nil {
			plan.Warnings = append(plan.Warnings, "primary alias resolution failed: "+err.Error())
		} else if strings.TrimSpace(deviceID) != "" {
			resolved = strings.TrimSpace(deviceID)
			decision = autoMachineDecision{
				Machine: resolved,
				Reason:  "matched the user's primary device",
			}
		} else {
			plan.Warnings = append(plan.Warnings, "no primary device configured")
		}
	}
	if strings.TrimSpace(resolved) == "" {
		resolved = "local"
	}
	plan.ResolvedMachine = resolved
	plan.SelectionReason = strings.TrimSpace(decision.Reason)
	plan.RemoteExecution = resolved != "local"
	if plan.SelectionReason == "" && resolved == "local" {
		plan.SelectionReason = "local execution"
	}

	if project := buildOpsExecutionProjectPlan(octx, req); project != nil {
		plan.Project = project
		if len(project.RuntimeWarnings) > 0 {
			plan.Warnings = append(plan.Warnings, project.RuntimeWarnings...)
		}
		if len(plan.Access.AllowedProjects) > 0 && !opsExecutionAllowedProject(plan.Access.AllowedProjects, project.Name, project.WorkDir) {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("project %q is outside the caller's allowed project list", project.Name))
		}
	}
	if len(plan.Access.AllowedRunners) > 0 {
		plan.Warnings = append(plan.Warnings, "caller is runner-restricted; downstream execution should respect allowedRunners")
	}
	return plan
}

func buildOpsExecutionAccessPlan(octx OpsContext) OpsExecutionAccessPlan {
	plan := OpsExecutionAccessPlan{Caller: strings.TrimSpace(octx.Caller)}
	headers := octx.RequestHeaders
	if headers == nil {
		headers = http.Header{}
	}
	guestUserID := strings.TrimSpace(octx.ActorUserID)
	if guestUserID == "" {
		guestUserID = strings.TrimSpace(headers.Get("X-Yaver-GuestUserID"))
	}
	plan.GuestUserID = guestUserID
	plan.GuestScope = strings.TrimSpace(headers.Get("X-Yaver-GuestScope"))
	if plan.GuestScope == "" && guestUserID != "" && octx.Server != nil && octx.Server.guestConfigMgr != nil {
		plan.GuestScope = octx.Server.guestConfigMgr.GetScope(guestUserID)
	}
	plan.HostShare = strings.EqualFold(strings.TrimSpace(headers.Get("X-Yaver-HostShare")), "true")
	plan.HostShareSessionID = strings.TrimSpace(headers.Get("X-Yaver-HostShareSessionID"))
	plan.HostShareToolingPreset = strings.TrimSpace(headers.Get("X-Yaver-HostShareToolingPreset"))
	plan.AllowedProjects = cleanProjectList(strings.Split(strings.TrimSpace(headers.Get("X-Yaver-GuestAllowedProjects")), ","))
	if len(plan.AllowedProjects) == 0 {
		plan.AllowedProjects = hostShareAllowedProjectsFromHeader(&http.Request{Header: headers})
	}
	plan.AllowedRunners = cleanProjectList(strings.Split(strings.TrimSpace(headers.Get("X-Yaver-HostShareAllowedRunners")), ","))
	plan.ResourcePreset = strings.TrimSpace(headers.Get("X-Yaver-HostShareResourcePreset"))
	plan.AllowInfra = parseBoolHeader(headers, "X-Yaver-HostShareAllowInfra")
	plan.AllowTunnelForward = parseBoolHeader(headers, "X-Yaver-HostShareAllowTunnel")
	plan.UseHostAgentTools = parseBoolHeader(headers, "X-Yaver-HostShareUseHostAgentTools")
	plan.UseHostInfra = parseBoolHeader(headers, "X-Yaver-HostShareUseHostInfra")
	if guestUserID != "" && octx.Server != nil && octx.Server.guestConfigMgr != nil {
		if cfg := octx.Server.guestConfigMgr.GetConfig(guestUserID); cfg != nil {
			if len(plan.AllowedProjects) == 0 {
				plan.AllowedProjects = cleanProjectList(cfg.AllowedProjects)
			}
			if len(plan.AllowedRunners) == 0 {
				plan.AllowedRunners = append([]string(nil), cfg.AllowedRunners...)
			}
			if plan.ResourcePreset == "" {
				plan.ResourcePreset = strings.TrimSpace(cfg.ResourcePreset)
			}
			plan.PriorityMode = strings.TrimSpace(cfg.PriorityMode)
			plan.AllowDesktopControl = guestAllowDesktopControl(cfg)
			plan.AllowBrowserControl = guestAllowBrowserControl(cfg)
			plan.AllowTunnelForward = guestAllowTunnelForward(cfg)
			if cfg.RequireIsolation != nil {
				plan.RequireIsolation = *cfg.RequireIsolation
			}
			if cfg.UseHostAPIKeys != nil {
				plan.UseHostAPIKeys = *cfg.UseHostAPIKeys
			}
			if cfg.AllowGuestProvidedAPIKeys != nil {
				plan.AllowGuestProvidedAK = *cfg.AllowGuestProvidedAPIKeys
			}
			plan.CPULimitPercent = cfg.CPULimitPercent
			plan.RAMLimitMB = cfg.RAMLimitMB
		}
	}
	return plan
}

func authorizeOpsExecution(_ OpsContext, req OpsRequest, plan OpsExecutionPlan) *OpsResult {
	switch plan.Access.Caller {
	case "guest":
		return authorizeGuestOpsExecution(req, plan)
	case "host-share":
		return authorizeHostShareOpsExecution(req, plan)
	default:
		return nil
	}
}

func authorizeGuestOpsExecution(req OpsRequest, plan OpsExecutionPlan) *OpsResult {
	scope := guestScopeOrDefault(plan.Access.GuestScope)
	verb := strings.TrimSpace(req.Verb)
	if scope == GuestScopeDeploy {
		switch verb {
		case "info", "status", "deploy":
		default:
			return &OpsResult{OK: false, Code: "unauthorized", Error: fmt.Sprintf("guest deploy scope cannot run ops verb %q", verb)}
		}
	}
	if len(plan.Access.AllowedProjects) > 0 {
		if plan.Project == nil || !opsExecutionAllowedProject(plan.Access.AllowedProjects, plan.Project.Name, plan.Project.WorkDir) {
			return &OpsResult{OK: false, Code: "unauthorized", Error: "project is outside the guest's allowed project list"}
		}
	}
	return nil
}

func authorizeHostShareOpsExecution(req OpsRequest, plan OpsExecutionPlan) *OpsResult {
	verb := strings.TrimSpace(req.Verb)
	switch verb {
	case "info", "status", "deploy", "reload":
	default:
		return &OpsResult{OK: false, Code: "unauthorized", Error: fmt.Sprintf("host-share session cannot run ops verb %q", verb)}
	}
	if len(plan.Access.AllowedProjects) > 0 && verb != "info" && verb != "status" {
		if plan.Project == nil || !opsExecutionAllowedProject(plan.Access.AllowedProjects, plan.Project.Name, plan.Project.WorkDir) {
			return &OpsResult{OK: false, Code: "unauthorized", Error: "project is outside the host-share allowed project list"}
		}
	}
	if verb == "deploy" && !plan.Access.AllowInfra {
		return &OpsResult{OK: false, Code: "unauthorized", Error: "host-share session does not allow infra actions"}
	}
	if verb == "reload" && !plan.Access.UseHostAgentTools {
		return &OpsResult{OK: false, Code: "unauthorized", Error: "host-share session does not expose host agent tools"}
	}
	return nil
}

func buildOpsExecutionProjectPlan(octx OpsContext, req OpsRequest) *OpsExecutionProjectPlan {
	workDir := inferOpsExecutionWorkDir(req)
	requestedProject := inferOpsExecutionProjectHint(req)
	if (workDir == "" || requestedProject != "") && requestedProject != "" {
		if resolved, err := resolveProjectRef(requestedProject, workDir); err == nil {
			workDir = resolved.Path
		}
	}
	if workDir == "" && octx.Server != nil && octx.Server.taskMgr != nil {
		workDir = strings.TrimSpace(octx.Server.taskMgr.workDir)
	}
	if workDir == "" {
		if wd, err := os.Getwd(); err == nil {
			workDir = wd
		}
	}
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil
	}
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		return &OpsExecutionProjectPlan{
			WorkDir:          workDir,
			RequestedProject: requestedProject,
			Discovery:        currentProjectDiscoverySnapshot(),
		}
	}

	project := &OpsExecutionProjectPlan{
		WorkDir:          workDir,
		RequestedProject: requestedProject,
		Discovery:        currentProjectDiscoverySnapshot(),
	}
	info := DetectProjectInfo(workDir)
	project.Name = info.Name
	project.GitBranch = info.GitBranch
	project.GitRemote = info.GitRemote
	project.Framework = info.Framework
	project.Tags = DetectProjectTags(workDir)
	if project.GitRemote == "" {
		if binding := findProjectRemote(project.Name); binding != nil {
			project.ProjectRemote = binding
			project.GitRemote = binding.RemoteURL
		}
	} else if binding := findProjectRemote(project.Name); binding != nil {
		project.ProjectRemote = binding
	}

	if root, wm := loadNearestWorkspaceManifest(workDir); wm != nil {
		project.WorkspaceRoot = root
		project.WorkspaceName = strings.TrimSpace(wm.Name)
		project.WorkspacePrimary = strings.TrimSpace(wm.Workspace.PrimaryDevice)
		project.WorkspaceVaultMode = strings.TrimSpace(wm.Workspace.Vault)
	}
	if summary, err := BuildProjectRuntimeSummary(context.Background(), octx.Server, workDir); err == nil && summary != nil {
		project.RuntimeAssignments = append(project.RuntimeAssignments, summary.ResolvedAssignments...)
		project.ExportPlans = append(project.ExportPlans, summary.ExportPlans...)
		project.ProviderRequirements = append(project.ProviderRequirements, summary.ProviderRequirements...)
		project.RuntimeWarnings = append(project.RuntimeWarnings, summary.Warnings...)
	}
	return project
}

func inferOpsExecutionWorkDir(req OpsRequest) string {
	switch strings.TrimSpace(req.Verb) {
	case "reload":
		var p opsReloadPayload
		if json.Unmarshal(req.Payload, &p) == nil && strings.TrimSpace(p.WorkDir) != "" {
			return p.WorkDir
		}
	case "deploy":
		var p opsDeployPayload
		if json.Unmarshal(req.Payload, &p) == nil && strings.TrimSpace(p.WorkDir) != "" {
			return p.WorkDir
		}
	}
	var payload map[string]interface{}
	if json.Unmarshal(req.Payload, &payload) != nil {
		return ""
	}
	for _, key := range []string{"workDir", "directory", "dir", "cwd", "path"} {
		if v := strings.TrimSpace(fmt.Sprint(payload[key])); v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}

func inferOpsExecutionProjectHint(req OpsRequest) string {
	var payload map[string]interface{}
	if json.Unmarshal(req.Payload, &payload) != nil {
		return ""
	}
	for _, key := range []string{"project", "projectName", "name", "slug", "app"} {
		if v := strings.TrimSpace(fmt.Sprint(payload[key])); v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}

func opsExecutionAllowedProject(allowed []string, name, workDir string) bool {
	if len(allowed) == 0 {
		return true
	}
	base := strings.TrimSpace(filepath.Base(workDir))
	for _, candidate := range []string{strings.TrimSpace(name), base, strings.TrimSpace(workDir)} {
		if candidate == "" {
			continue
		}
		for _, allowedProject := range allowed {
			if strings.EqualFold(strings.TrimSpace(allowedProject), candidate) {
				return true
			}
		}
	}
	return false
}

func parseBoolHeader(headers http.Header, key string) bool {
	switch strings.ToLower(strings.TrimSpace(headers.Get(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
