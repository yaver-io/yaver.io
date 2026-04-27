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

type autoMachineDecision struct {
	Machine string
	Reason  string
}

func resolveAutoOpsMachine(octx OpsContext, req OpsRequest) autoMachineDecision {
	if octx.Server == nil {
		return autoMachineDecision{
			Machine: "local",
			Reason:  "no server context available; defaulted to local execution",
		}
	}
	switch strings.TrimSpace(req.Verb) {
	case "reload":
		return resolveAutoReloadMachine(octx, req.Payload)
	case "deploy":
		return resolveAutoDeployMachine(octx, req.Payload)
	default:
		if resolved, err := resolvePrimaryDeviceID(octx.Ctx, octx.Server); err == nil && strings.TrimSpace(resolved) != "" {
			return autoMachineDecision{
				Machine: strings.TrimSpace(resolved),
				Reason:  "matched the user's primary device",
			}
		}
		return autoMachineDecision{
			Machine: "local",
			Reason:  "no primary device configured; defaulted to local execution",
		}
	}
}

func resolveAutoReloadMachine(octx OpsContext, payload json.RawMessage) autoMachineDecision {
	var p opsReloadPayload
	_ = json.Unmarshal(payload, &p)
	requestedWorkDir := resolveOpsPlacementWorkDir("reload", payload)
	primaryDeviceID, _ := resolvePrimaryDeviceID(octx.Ctx, octx.Server)

	if localStatus := localDevServerStatus(octx.Server); localStatus != nil && localStatus.Running {
		if requestedWorkDir == "" || workDirsMatch(localStatus.WorkDir, requestedWorkDir) {
			return autoMachineDecision{
				Machine: "local",
				Reason:  "the local machine already hosts the matching active dev server",
			}
		}
	}

	bestRunning := autoMachineDecision{}
	for _, machine := range listAllMachines(octx.Ctx) {
		if machine.IsLocal || !machine.IsOnline || strings.TrimSpace(machine.DeviceID) == "" {
			continue
		}
		status, err := remoteDevServerStatus(octx.Ctx, machine.DeviceID)
		if err != nil || status == nil || !status.Running {
			continue
		}
		if requestedWorkDir != "" && !workDirsMatch(status.WorkDir, requestedWorkDir) {
			continue
		}
		if machine.DeviceID == strings.TrimSpace(primaryDeviceID) {
			return autoMachineDecision{
				Machine: machine.DeviceID,
				Reason:  "the primary device already hosts the matching active dev server",
			}
		}
		if bestRunning.Machine == "" {
			bestRunning = autoMachineDecision{
				Machine: machine.DeviceID,
				Reason:  "a remote machine already hosts the matching active dev server",
			}
		}
	}
	if bestRunning.Machine != "" {
		return bestRunning
	}

	if requestedWorkDir != "" {
		if summary, err := BuildProjectRuntimeSummary(octx.Ctx, octx.Server, requestedWorkDir); err == nil && summary != nil {
			if machine, reason := machineFromRuntimeAssignments(summary, "mobile", "frontend", "backend"); machine != nil {
				return autoMachineDecision{
					Machine: resolvedMachineID(machine),
					Reason:  reason,
				}
			}
		}
	}

	if strings.TrimSpace(primaryDeviceID) != "" {
		return autoMachineDecision{
			Machine: strings.TrimSpace(primaryDeviceID),
			Reason:  "fell back to the user's primary device",
		}
	}
	return autoMachineDecision{
		Machine: "local",
		Reason:  "no active dev server or placement hint found; defaulted to local execution",
	}
}

func resolveAutoDeployMachine(octx OpsContext, payload json.RawMessage) autoMachineDecision {
	var p opsDeployPayload
	_ = json.Unmarshal(payload, &p)
	workDir := resolveOpsPlacementWorkDir("deploy", payload)
	primaryDeviceID, _ := resolvePrimaryDeviceID(octx.Ctx, octx.Server)

	if workDir != "" {
		if summary, err := BuildProjectRuntimeSummary(octx.Ctx, octx.Server, workDir); err == nil && summary != nil {
			if machine, reason := machineFromRuntimeAssignments(summary, deployAssignmentHints(p.Target)...); machine != nil {
				return autoMachineDecision{
					Machine: resolvedMachineID(machine),
					Reason:  reason,
				}
			}
			if machine, reason := machineFromExportPlans(summary, p.Target); machine != nil {
				return autoMachineDecision{
					Machine: resolvedMachineID(machine),
					Reason:  reason,
				}
			}
		}
	}

	reqs := deployCapabilityRequirements(p.Target)
	if len(reqs) > 0 {
		role := resolveProjectRuntimeRole(listAllMachines(octx.Ctx), "deploy-auto", ManifestMachineRole{
			Mode:         "owned",
			Capabilities: reqs,
		}, strings.TrimSpace(primaryDeviceID), targetAllowsManagedCloud(p.Target))
		if role.Machine != nil {
			return autoMachineDecision{
				Machine: resolvedMachineID(role.Machine),
				Reason:  "selected the best machine that matches deploy capability requirements: " + role.Reason,
			}
		}
	}

	if strings.TrimSpace(primaryDeviceID) != "" {
		return autoMachineDecision{
			Machine: strings.TrimSpace(primaryDeviceID),
			Reason:  "fell back to the user's primary device",
		}
	}
	return autoMachineDecision{
		Machine: "local",
		Reason:  "no project-aware deploy placement matched; defaulted to local execution",
	}
}

func localDevServerStatus(s *HTTPServer) *DevServerStatus {
	if s == nil || s.devServerMgr == nil {
		return nil
	}
	return s.devServerMgr.Status()
}

func remoteDevServerStatus(ctx context.Context, deviceID string) (*DevServerStatus, error) {
	resp, err := proxyToDeviceJSON(ctx, "ops:reload:auto-status", deviceID, http.MethodGet, "/dev/status", nil)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	var status DevServerStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func cleanComparableWorkDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

func workDirsMatch(left, right string) bool {
	left = cleanComparableWorkDir(left)
	right = cleanComparableWorkDir(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if filepath.Base(left) == filepath.Base(right) {
		return true
	}
	return false
}

func machineFromRuntimeAssignments(summary *ProjectRuntimeSummary, names ...string) (*MachineInfo, string) {
	if summary == nil {
		return nil, ""
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, assignment := range summary.ResolvedAssignments {
			if !strings.EqualFold(strings.TrimSpace(assignment.Name), name) || assignment.Machine == nil {
				continue
			}
			return assignment.Machine, fmt.Sprintf("project runtime assignment %q resolved to this machine (%s)", assignment.Name, assignment.Reason)
		}
	}
	return nil, ""
}

func machineFromExportPlans(summary *ProjectRuntimeSummary, target string) (*MachineInfo, string) {
	if summary == nil {
		return nil, ""
	}
	target = strings.ToLower(strings.TrimSpace(target))
	provider := projectRuntimeProviderName(target, target)
	for _, plan := range summary.ExportPlans {
		if plan.Machine == nil {
			continue
		}
		switch target {
		case "testflight", "playstore", "play":
			if strings.Contains(strings.ToLower(plan.Name), "mobile") {
				return plan.Machine, fmt.Sprintf("project export plan %q resolved to this machine (%s)", plan.Name, plan.Reason)
			}
		default:
			if provider != "" && strings.EqualFold(strings.TrimSpace(plan.Provider), provider) {
				return plan.Machine, fmt.Sprintf("project export plan %q for provider %q resolved to this machine (%s)", plan.Name, plan.Provider, plan.Reason)
			}
		}
	}
	return nil, ""
}

func resolvedMachineID(machine *MachineInfo) string {
	if machine == nil {
		return "local"
	}
	if machine.IsLocal || strings.TrimSpace(machine.DeviceID) == "" || strings.EqualFold(strings.TrimSpace(machine.DeviceID), "local") {
		return "local"
	}
	return strings.TrimSpace(machine.DeviceID)
}

func deployCapabilityRequirements(target string) []string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "testflight":
		return []string{"ios", "testflight"}
	case "playstore", "play":
		return []string{"android", "playstore"}
	case "cloud", "yaver-cloud", "platform", "docker":
		return []string{"docker"}
	default:
		return nil
	}
}

func targetAllowsManagedCloud(target string) bool {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "cloud", "yaver-cloud", "platform", "cloudflare", "cf", "workers", "pages", "vercel", "fly", "fly.io", "netlify", "railway", "firebase", "convex":
		return true
	default:
		return false
	}
}

func deployAssignmentHints(target string) []string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "testflight", "playstore", "play":
		return []string{"mobile", "backend", "frontend"}
	case "convex":
		return []string{"backend", "frontend", "mobile"}
	default:
		return []string{"frontend", "backend", "mobile"}
	}
}

func annotateOpsResultMachine(result OpsResult, machine autoMachineDecision) OpsResult {
	if !result.OK || machine.Machine == "" {
		return result
	}
	meta := map[string]interface{}{
		"selectedMachine": machine.Machine,
		"selectionReason": machine.Reason,
	}
	switch initial := result.Initial.(type) {
	case nil:
		result.Initial = meta
	case map[string]interface{}:
		for k, v := range meta {
			if _, exists := initial[k]; !exists {
				initial[k] = v
			}
		}
		result.Initial = initial
	}
	return result
}

func workDirFromEnv() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func resolveOpsPlacementWorkDir(verb string, payload json.RawMessage) string {
	workDir := cleanComparableWorkDir(inferOpsExecutionWorkDir(OpsRequest{
		Verb:    verb,
		Payload: payload,
	}))
	if workDir != "" {
		return workDir
	}
	projectHint := strings.TrimSpace(inferOpsExecutionProjectHint(OpsRequest{
		Verb:    verb,
		Payload: payload,
	}))
	if projectHint == "" {
		return ""
	}
	if resolved, err := resolveProjectRef(projectHint, ""); err == nil {
		return cleanComparableWorkDir(resolved.Path)
	}
	return ""
}
