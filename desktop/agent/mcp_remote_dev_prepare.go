package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type remoteDevPrepareRequest struct {
	TargetDeviceID        string `json:"targetDeviceId,omitempty"`
	RepoURL               string `json:"repoUrl,omitempty"`
	Branch                string `json:"branch,omitempty"`
	Dir                   string `json:"dir,omitempty"`
	InstallMissing        bool   `json:"installMissing,omitempty"`
	IncludeGitCredentials bool   `json:"includeGitCredentials,omitempty"`
	ConfigureCode         bool   `json:"configureCode,omitempty"`
	PrepareMobile         bool   `json:"prepareMobile,omitempty"`
	MobileDirectory       string `json:"mobileDirectory,omitempty"`
	Verify                bool   `json:"verify,omitempty"`
	DryRun                bool   `json:"dryRun,omitempty"`
}

func runRemoteDevPrepareMCP(s *HTTPServer, raw json.RawMessage) map[string]any {
	req := parseRemoteDevPrepareRequest(raw)
	ctx := context.Background()
	if strings.TrimSpace(req.TargetDeviceID) == "" {
		deviceID, err := resolvePrimaryDeviceIDForMCP()
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		req.TargetDeviceID = deviceID
	}
	repo := DevEnvironmentCloneRepo{URL: req.RepoURL, Branch: req.Branch, Dir: req.Dir}
	if strings.TrimSpace(repo.URL) == "" {
		defaults := defaultDevEnvCloneReposFromCwd()
		if len(defaults) == 0 {
			return map[string]any{"ok": false, "error": "repoUrl is required because the current directory has no inferable git remote"}
		}
		repo = defaults[0]
		if strings.TrimSpace(req.Branch) != "" {
			repo.Branch = req.Branch
		}
		if strings.TrimSpace(req.Dir) != "" {
			repo.Dir = req.Dir
		}
	}
	cloneReq := DevEnvironmentCloneRequest{
		TargetDeviceID:        req.TargetDeviceID,
		Repos:                 []DevEnvironmentCloneRepo{repo},
		InstallMissing:        req.InstallMissing,
		IncludeGitCredentials: req.IncludeGitCredentials,
		SkipConfigs:           true,
		ConfigureCode:         req.ConfigureCode,
		Verify:                req.Verify,
		DryRun:                req.DryRun,
	}
	plan := buildDevEnvironmentClonePlan(ctx, s, cloneReq)
	out := map[string]any{
		"ok":             plan.OK,
		"targetDeviceId": req.TargetDeviceID,
		"plan":           plan,
		"steps":          []map[string]any{},
	}
	addStep := func(id, status string, data any) {
		out["steps"] = append(out["steps"].([]map[string]any), map[string]any{"id": id, "status": status, "data": data})
	}
	if !plan.OK {
		out["error"] = "remote dev prepare plan is not runnable"
		return out
	}
	if req.InstallMissing || req.IncludeGitCredentials {
		if plan.SourceProfile == nil {
			out["ok"] = false
			out["error"] = "source profile missing"
			return out
		}
		toolchain, err := applyDevEnvProfileToTarget(ctx, s, cloneReq, *plan.SourceProfile)
		if err != nil {
			out["ok"] = false
			out["error"] = fmt.Sprintf("toolchain/profile apply failed: %v", err)
			addStep("toolchain_apply", "error", err.Error())
			return out
		}
		addStep("toolchain_apply", "ok", toolchain)
	}
	cloneOut, err := cloneDevEnvRepo(ctx, s, req.TargetDeviceID, repo, req.DryRun)
	if err != nil {
		out["ok"] = false
		out["error"] = fmt.Sprintf("repo clone failed: %v", err)
		addStep("repo_clone", "error", err.Error())
		return out
	}
	addStep("repo_clone", "ok", cloneOut)
	repoPath, _ := cloneOut["path"].(string)
	if repoPath == "" {
		repoPath = strings.TrimSpace(repo.Dir)
	}
	out["repoPath"] = repoPath
	if req.ConfigureCode && strings.TrimSpace(repoPath) != "" {
		codeOut, err := setDevEnvCodeRepo(ctx, s, req.TargetDeviceID, repoPath, req.DryRun)
		if err != nil {
			addStep("code_config", "warning", err.Error())
			out["codeWarning"] = err.Error()
		} else {
			addStep("code_config", "ok", codeOut)
		}
	}
	if req.PrepareMobile {
		mobileDir := strings.TrimSpace(firstNonEmpty(req.MobileDirectory, repoPath))
		if mobileDir == "" {
			addStep("mobile_prepare", "warning", "mobileDirectory could not be inferred")
		} else if req.DryRun {
			addStep("mobile_prepare", "skipped", map[string]any{"dryRun": true, "directory": mobileDir})
		} else {
			prepOut, err := proxyToDeviceJSON(ctx, "remote_dev_prepare", req.TargetDeviceID, http.MethodPost, "/mobile/project/prepare", map[string]any{"directory": mobileDir})
			if err != nil {
				out["mobileWarning"] = err.Error()
				addStep("mobile_prepare", "warning", err.Error())
			} else {
				addStep("mobile_prepare", "ok", prepOut)
				out["mobileStatus"] = prepOut
			}
		}
	}
	if req.Verify {
		verifyOut, err := verifyDevEnvTarget(ctx, s, req.TargetDeviceID)
		if err != nil {
			out["verifyWarning"] = err.Error()
			addStep("verify", "warning", err.Error())
		} else {
			addStep("verify", "ok", verifyOut)
			out["verification"] = verifyOut
		}
	}
	out["nextActions"] = remoteDevPrepareNextActions(req.TargetDeviceID, repoPath)
	return out
}

func parseRemoteDevPrepareRequest(raw json.RawMessage) remoteDevPrepareRequest {
	req := remoteDevPrepareRequest{ConfigureCode: true, Verify: true}
	if len(raw) == 0 {
		return req
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return req
	}
	_ = json.Unmarshal(raw, &req)
	if _, ok := args["configureCode"]; !ok {
		req.ConfigureCode = true
	}
	if _, ok := args["verify"]; !ok {
		req.Verify = true
	}
	req.TargetDeviceID = strings.TrimSpace(req.TargetDeviceID)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Branch = strings.TrimSpace(req.Branch)
	req.Dir = strings.TrimSpace(req.Dir)
	req.MobileDirectory = strings.TrimSpace(req.MobileDirectory)
	return req
}

func remoteDevPrepareNextActions(deviceID, repoPath string) []map[string]string {
	actions := []map[string]string{
		{"tool": "code_status", "why": "Confirm the remote code-control workdir and runner state.", "example": fmt.Sprintf(`{"device_id":%q}`, deviceID)},
		{"tool": "exec_command", "why": "Run quick checks on the remote machine without using local CPU.", "example": fmt.Sprintf(`{"device_id":%q,"work_dir":%q,"command":"git status --short && npm --version","timeout":120}`, deviceID, repoPath)},
		{"tool": "mobile_project_prepare", "why": "Install dependencies on the remote clone before iOS/Android Hermes testing.", "example": fmt.Sprintf(`{"device_id":%q,"directory":%q}`, deviceID, repoPath)},
		{"tool": "mobile_project_build", "why": "Build a Hermes bundle on the remote machine for Yaver mobile reload.", "example": fmt.Sprintf(`{"device_id":%q,"directory":%q,"platform":"android"}`, deviceID, repoPath)},
		{"tool": "browser_open + browser_navigate", "why": "Use Yaver's browser automation for web smoke tests on the dev-machine browser.", "example": "Start the dev server on the remote clone, then navigate to its preview URL."},
	}
	return actions
}
