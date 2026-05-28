package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type DevEnvironmentCloneTarget struct {
	Mode                  string `json:"mode"` // existing-device | ssh | managed-cloud
	DeviceID              string `json:"deviceId,omitempty"`
	SSHHost               string `json:"sshHost,omitempty"`
	SSHUser               string `json:"sshUser,omitempty"`
	ManagedCloudMachineID string `json:"managedCloudMachineId,omitempty"`
}

type DevEnvironmentCloneRepo struct {
	URL            string `json:"url"`
	Branch         string `json:"branch,omitempty"`
	Dir            string `json:"dir,omitempty"`
	AutoInit       bool   `json:"autoInit,omitempty"`
	AutoInitRunner string `json:"autoInitRunner,omitempty"`
}

type DevEnvironmentCloneRequest struct {
	SourceDeviceID         string                    `json:"sourceDeviceId,omitempty"`
	TargetDeviceID         string                    `json:"targetDeviceId,omitempty"`
	Target                 DevEnvironmentCloneTarget `json:"target,omitempty"`
	Repos                  []DevEnvironmentCloneRepo `json:"repos,omitempty"`
	IncludeDiscoveredRepos bool                      `json:"includeDiscoveredRepos,omitempty"`
	InstallMissing         bool                      `json:"installMissing,omitempty"`
	IncludeGitCredentials  bool                      `json:"includeGitCredentials,omitempty"`
	SkipConfigs            bool                      `json:"skipConfigs,omitempty"`
	ConfigKeys             []string                  `json:"configKeys,omitempty"`
	SyncKinds              []string                  `json:"syncKinds,omitempty"`
	ConfigureCode          bool                      `json:"configureCode,omitempty"`
	RunnerIDs              []string                  `json:"runnerIds,omitempty"`
	Verify                 bool                      `json:"verify,omitempty"`
	DryRun                 bool                      `json:"dryRun,omitempty"`
}

type DevEnvironmentCloneStep struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Status  string         `json:"status"` // pending | running | ok | skipped | warning | error
	Detail  string         `json:"detail,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Started string         `json:"started,omitempty"`
	Ended   string         `json:"ended,omitempty"`
}

type DevEnvironmentClonePlan struct {
	OK               bool                      `json:"ok"`
	SourceDeviceID   string                    `json:"sourceDeviceId,omitempty"`
	TargetDeviceID   string                    `json:"targetDeviceId,omitempty"`
	TargetMode       string                    `json:"targetMode"`
	Repos            []DevEnvironmentCloneRepo `json:"repos,omitempty"`
	ToolchainTargets []string                  `json:"toolchainTargets,omitempty"`
	Configs          []DetectedDevConfig       `json:"configs,omitempty"`
	Steps            []DevEnvironmentCloneStep `json:"steps"`
	Warnings         []string                  `json:"warnings,omitempty"`
	ManualSteps      []string                  `json:"manualSteps,omitempty"`
	SourceProfile    *EnvironmentProfile       `json:"sourceProfile,omitempty"`
	CapabilityHints  []string                  `json:"capabilityHints,omitempty"`
}

type DevEnvironmentCloneJob struct {
	ID        string                     `json:"id"`
	Status    string                     `json:"status"` // queued | running | completed | failed
	Request   DevEnvironmentCloneRequest `json:"request"`
	Plan      DevEnvironmentClonePlan    `json:"plan"`
	Steps     []DevEnvironmentCloneStep  `json:"steps"`
	Error     string                     `json:"error,omitempty"`
	CreatedAt string                     `json:"createdAt"`
	UpdatedAt string                     `json:"updatedAt"`
}

var devEnvironmentCloneJobs = struct {
	sync.Mutex
	next int64
	m    map[string]*DevEnvironmentCloneJob
}{m: map[string]*DevEnvironmentCloneJob{}}

const (
	devEnvCloneJobMax = 64
	devEnvCloneJobTTL = time.Hour
)

func sweepDevEnvCloneJobsLocked() {
	cutoff := time.Now().UTC().Add(-devEnvCloneJobTTL)
	for id, job := range devEnvironmentCloneJobs.m {
		if job.Status != "completed" && job.Status != "failed" {
			continue
		}
		t, err := time.Parse(time.RFC3339, job.UpdatedAt)
		if err == nil && t.After(cutoff) {
			continue
		}
		delete(devEnvironmentCloneJobs.m, id)
	}
	if len(devEnvironmentCloneJobs.m) <= devEnvCloneJobMax {
		return
	}
	type kv struct {
		id      string
		updated time.Time
		active  bool
	}
	pairs := make([]kv, 0, len(devEnvironmentCloneJobs.m))
	for id, job := range devEnvironmentCloneJobs.m {
		t, _ := time.Parse(time.RFC3339, job.UpdatedAt)
		active := job.Status != "completed" && job.Status != "failed"
		pairs = append(pairs, kv{id: id, updated: t, active: active})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].active != pairs[j].active {
			return pairs[i].active // keep active first
		}
		return pairs[i].updated.After(pairs[j].updated)
	})
	for _, p := range pairs[devEnvCloneJobMax:] {
		if p.active {
			continue
		}
		delete(devEnvironmentCloneJobs.m, p.id)
	}
}

func sanitizeRepoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func normalizeDevEnvCloneRequest(req DevEnvironmentCloneRequest) DevEnvironmentCloneRequest {
	req.SourceDeviceID = strings.TrimSpace(req.SourceDeviceID)
	req.TargetDeviceID = strings.TrimSpace(firstNonEmpty(req.TargetDeviceID, req.Target.DeviceID))
	req.Target.DeviceID = strings.TrimSpace(firstNonEmpty(req.Target.DeviceID, req.TargetDeviceID))
	req.Target.Mode = strings.ToLower(strings.TrimSpace(req.Target.Mode))
	req.Target.SSHHost = strings.TrimSpace(req.Target.SSHHost)
	req.Target.SSHUser = strings.TrimSpace(req.Target.SSHUser)
	req.Target.ManagedCloudMachineID = strings.TrimSpace(req.Target.ManagedCloudMachineID)
	if req.Target.Mode == "" {
		switch {
		case req.TargetDeviceID != "":
			req.Target.Mode = "existing-device"
		case req.Target.SSHHost != "":
			req.Target.Mode = "ssh"
		case req.Target.ManagedCloudMachineID != "":
			req.Target.Mode = "managed-cloud"
		default:
			req.Target.Mode = "existing-device"
		}
	}
	if req.Target.Mode == "existing" || req.Target.Mode == "device" {
		req.Target.Mode = "existing-device"
	}
	if req.Target.Mode == "ssh" && req.Target.SSHUser == "" {
		req.Target.SSHUser = "root"
	}
	req.SyncKinds = uniqueNonEmptyStrings(req.SyncKinds)
	req.RunnerIDs = uniqueNonEmptyStrings(req.RunnerIDs)
	req.ConfigKeys = uniqueNonEmptyStrings(req.ConfigKeys)
	for i := range req.Repos {
		req.Repos[i].URL = strings.TrimSpace(req.Repos[i].URL)
		req.Repos[i].Branch = strings.TrimSpace(req.Repos[i].Branch)
		req.Repos[i].Dir = strings.TrimSpace(req.Repos[i].Dir)
		req.Repos[i].AutoInitRunner = strings.TrimSpace(req.Repos[i].AutoInitRunner)
	}
	return req
}

func buildDevEnvironmentClonePlan(ctx context.Context, s *HTTPServer, req DevEnvironmentCloneRequest) DevEnvironmentClonePlan {
	req = normalizeDevEnvCloneRequest(req)
	plan := DevEnvironmentClonePlan{
		OK:             true,
		SourceDeviceID: req.SourceDeviceID,
		TargetDeviceID: req.TargetDeviceID,
		TargetMode:     req.Target.Mode,
	}
	addStep := func(id, title, status, detail string) {
		plan.Steps = append(plan.Steps, DevEnvironmentCloneStep{ID: id, Title: title, Status: status, Detail: detail})
	}

	switch req.Target.Mode {
	case "existing-device":
		if req.TargetDeviceID == "" {
			plan.OK = false
			addStep("target", "Resolve target device", "error", "targetDeviceId is required for existing-device clones")
		} else {
			addStep("target", "Resolve target device", "pending", req.TargetDeviceID)
		}
	case "ssh":
		if req.Target.SSHHost == "" {
			plan.OK = false
			addStep("target", "Resolve SSH target", "error", "target.sshHost is required")
		} else {
			addStep("remote_setup", "Bootstrap SSH host", "pending", req.Target.SSHUser+"@"+req.Target.SSHHost)
			if req.TargetDeviceID == "" {
				plan.ManualSteps = append(plan.ManualSteps, "After SSH setup completes, run yaver auth/serve on the host or wait for the provisioned agent to appear, then rerun with targetDeviceId.")
				addStep("target_device", "Wait for Yaver device id", "warning", "SSH setup can install Yaver, but clone phases need a registered target device id")
			}
		}
	case "managed-cloud":
		addStep("managed_cloud", "Provision managed cloud", "pending", firstNonEmpty(req.Target.ManagedCloudMachineID, "existing billing flow"))
		if req.TargetDeviceID == "" {
			plan.ManualSteps = append(plan.ManualSteps, "Complete managed-cloud provisioning and rerun after the cloud machine reports a Yaver device id.")
			addStep("target_device", "Wait for cloud device id", "warning", "clone phases need targetDeviceId")
		}
	default:
		plan.OK = false
		addStep("target", "Resolve target", "error", "unsupported target mode: "+req.Target.Mode)
	}

	profile, err := sourceEnvironmentProfile(ctx, s, req.SourceDeviceID)
	if err != nil {
		plan.OK = false
		addStep("source_profile", "Read source environment profile", "error", err.Error())
	} else {
		plan.SourceProfile = profile
		plan.ToolchainTargets = profile.ToolchainTargets
		if len(plan.ToolchainTargets) == 0 {
			plan.ToolchainTargets = toolchainTargetsFromBinaries(profile.Binaries)
		}
		plan.Configs = profile.Configs
		addStep("source_profile", "Read source environment profile", "pending", profile.Platform+"/"+profile.Arch)
		plan.CapabilityHints = capabilityHintsFromEnvironmentProfile(*profile)
	}

	repos := append([]DevEnvironmentCloneRepo{}, req.Repos...)
	if req.IncludeDiscoveredRepos && profile != nil {
		inferred, warnings := inferReposFromSourceProfile(req.SourceDeviceID, *profile)
		repos = append(repos, inferred...)
		plan.Warnings = append(plan.Warnings, warnings...)
	}
	repos = uniqueCloneRepos(repos)
	plan.Repos = repos
	if len(repos) == 0 {
		addStep("repos", "Clone repositories", "skipped", "no repos requested or inferable")
	} else {
		addStep("repos", "Clone repositories", "pending", fmt.Sprintf("%d repo(s)", len(repos)))
	}

	if req.InstallMissing || len(req.SyncKinds) > 0 || req.IncludeGitCredentials {
		detail := "installMissing=" + fmt.Sprint(req.InstallMissing)
		if req.InstallMissing && len(plan.ToolchainTargets) > 0 {
			detail = fmt.Sprintf("installMissing=true; prioritized targets=%s", strings.Join(plan.ToolchainTargets, ", "))
		}
		addStep("toolchain_apply", "Apply toolchain/profile", "pending", detail)
	} else {
		addStep("toolchain_apply", "Apply toolchain/profile", "skipped", "no toolchain/profile apply requested")
	}
	if req.ConfigureCode {
		addStep("code_config", "Configure yaver code target", "pending", "set active repo on target when clone succeeds")
	}
	if !req.SkipConfigs {
		if len(plan.Configs) == 0 {
			addStep("configs", "Apply developer configs", "skipped", "no allowlisted configs detected")
		} else {
			addStep("configs", "Apply developer configs", "pending", fmt.Sprintf("%d config item(s), allowlisted and secret-filtered", len(plan.Configs)))
		}
	}
	if req.Verify {
		addStep("verify", "Verify runners and capabilities", "pending", "runner status + agent capabilities")
	}
	if runtime.GOOS != "" && profile != nil && profile.Platform != "" && profile.Platform != runtime.GOOS {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("cross-platform source profile: source=%s target-controller=%s; target host may differ again", profile.Platform, runtime.GOOS))
	}
	sort.Strings(plan.CapabilityHints)
	return plan
}

func sourceEnvironmentProfile(ctx context.Context, s *HTTPServer, sourceDeviceID string) (*EnvironmentProfile, error) {
	if strings.TrimSpace(sourceDeviceID) == "" || strings.TrimSpace(sourceDeviceID) == localDeviceID() {
		profile := buildEnvironmentProfile(s)
		return &profile, nil
	}
	return profileFromPeer(ctx, sourceDeviceID)
}

func capabilityHintsFromEnvironmentProfile(profile EnvironmentProfile) []string {
	hints := []string{}
	for _, b := range profile.Binaries {
		switch strings.ToLower(strings.TrimSpace(b.Name)) {
		case "yaver", "cloudflared", "wrangler", "convex", "vercel", "git", "gh", "glab", "node", "npm", "npx", "pnpm", "bun", "deno", "go", "python3", "uv", "tmux", "ffmpeg", "docker", "xcodebuild", "xcrun", "gradle", "java", "adb", "brew", "apt-get", "snap":
			hints = append(hints, strings.ToLower(strings.TrimSpace(b.Name)))
		}
	}
	for _, r := range profile.Runners {
		if r.Installed || r.Ready {
			hints = append(hints, "runner:"+normalizeRunnerID(r.ID))
		}
	}
	return uniqueNonEmptyStrings(hints)
}

func inferReposFromSourceProfile(sourceDeviceID string, profile EnvironmentProfile) ([]DevEnvironmentCloneRepo, []string) {
	if strings.TrimSpace(sourceDeviceID) != "" && strings.TrimSpace(sourceDeviceID) != localDeviceID() {
		if len(profile.DiscoveredProjects) > 0 {
			return nil, []string{"repo URL inference from remote source profiles is not implemented yet; pass repos explicitly"}
		}
		return nil, nil
	}
	var repos []DevEnvironmentCloneRepo
	var warnings []string
	for _, p := range profile.DiscoveredProjects {
		path := strings.TrimSpace(p.Path)
		if path == "" {
			continue
		}
		provider, repo := detectRepoFromGit(path)
		url := cloneURLForDetectedRepo(provider, repo)
		if url == "" {
			warnings = append(warnings, "could not infer git remote for "+path)
			continue
		}
		repos = append(repos, DevEnvironmentCloneRepo{URL: url, Branch: strings.TrimSpace(p.Branch)})
	}
	return repos, warnings
}

func cloneURLForDetectedRepo(provider CIProvider, repo string) string {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if repo == "" {
		return ""
	}
	switch provider {
	case CIGitHub:
		return "https://github.com/" + repo + ".git"
	case CIGitLab:
		return "https://gitlab.com/" + repo + ".git"
	default:
		return ""
	}
}

func uniqueCloneRepos(in []DevEnvironmentCloneRepo) []DevEnvironmentCloneRepo {
	seen := map[string]bool{}
	out := make([]DevEnvironmentCloneRepo, 0, len(in))
	for _, r := range in {
		r.URL = strings.TrimSpace(r.URL)
		if r.URL == "" {
			continue
		}
		key := strings.ToLower(r.URL) + "\x00" + strings.ToLower(strings.TrimSpace(r.Branch))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func startDevEnvironmentCloneJob(s *HTTPServer, req DevEnvironmentCloneRequest, plan DevEnvironmentClonePlan) *DevEnvironmentCloneJob {
	req = normalizeDevEnvCloneRequest(req)
	now := time.Now().UTC().Format(time.RFC3339)
	devEnvironmentCloneJobs.Lock()
	devEnvironmentCloneJobs.next++
	id := fmt.Sprintf("dev-env-clone-%d", devEnvironmentCloneJobs.next)
	job := &DevEnvironmentCloneJob{ID: id, Status: "queued", Request: req, Plan: plan, CreatedAt: now, UpdatedAt: now}
	devEnvironmentCloneJobs.m[id] = job
	sweepDevEnvCloneJobsLocked()
	devEnvironmentCloneJobs.Unlock()
	go runDevEnvironmentCloneJob(s, job)
	return job
}

func getDevEnvironmentCloneJob(id string) (*DevEnvironmentCloneJob, bool) {
	devEnvironmentCloneJobs.Lock()
	defer devEnvironmentCloneJobs.Unlock()
	job, ok := devEnvironmentCloneJobs.m[strings.TrimSpace(id)]
	if !ok {
		return nil, false
	}
	copy := *job
	copy.Steps = append([]DevEnvironmentCloneStep(nil), job.Steps...)
	for i := range copy.Steps {
		copy.Steps[i].Detail = scrubCredentialURLs(copy.Steps[i].Detail)
	}
	copy.Error = scrubCredentialURLs(copy.Error)
	copy.Request.Repos = append([]DevEnvironmentCloneRepo(nil), copy.Request.Repos...)
	for i := range copy.Request.Repos {
		copy.Request.Repos[i].URL = sanitizeRepoURL(copy.Request.Repos[i].URL)
	}
	copy.Plan.Repos = append([]DevEnvironmentCloneRepo(nil), copy.Plan.Repos...)
	for i := range copy.Plan.Repos {
		copy.Plan.Repos[i].URL = sanitizeRepoURL(copy.Plan.Repos[i].URL)
	}
	return &copy, true
}

var credentialURLPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s@]+@`)

func scrubCredentialURLs(s string) string {
	if s == "" {
		return s
	}
	return credentialURLPattern.ReplaceAllString(s, "${1}[redacted]@")
}

func updateDevEnvironmentCloneJob(job *DevEnvironmentCloneJob, status, errText string) {
	devEnvironmentCloneJobs.Lock()
	defer devEnvironmentCloneJobs.Unlock()
	stored := devEnvironmentCloneJobs.m[job.ID]
	if stored == nil {
		return
	}
	stored.Status = status
	stored.Error = errText
	stored.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

func appendDevEnvironmentCloneStep(job *DevEnvironmentCloneJob, step DevEnvironmentCloneStep) {
	now := time.Now().UTC().Format(time.RFC3339)
	if step.Started == "" {
		step.Started = now
	}
	if step.Status == "ok" || step.Status == "error" || step.Status == "warning" || step.Status == "skipped" {
		step.Ended = now
	}
	devEnvironmentCloneJobs.Lock()
	defer devEnvironmentCloneJobs.Unlock()
	stored := devEnvironmentCloneJobs.m[job.ID]
	if stored == nil {
		return
	}
	stored.Steps = append(stored.Steps, step)
	stored.UpdatedAt = now
}

func runDevEnvironmentCloneJob(s *HTTPServer, job *DevEnvironmentCloneJob) {
	updateDevEnvironmentCloneJob(job, "running", "")
	ctx := context.Background()
	plan := job.Plan
	if !plan.OK {
		updateDevEnvironmentCloneJob(job, "failed", "clone plan is not runnable")
		return
	}
	req := normalizeDevEnvCloneRequest(job.Request)

	if req.Target.Mode == "ssh" {
		step := DevEnvironmentCloneStep{ID: "remote_setup", Title: "Bootstrap SSH host", Status: "running", Detail: req.Target.SSHUser + "@" + req.Target.SSHHost}
		appendDevEnvironmentCloneStep(job, step)
		if !req.DryRun {
			mgr := NewRemoteManager()
			out, err := mgr.Setup(req.Target.SSHHost, req.Target.SSHUser)
			if err != nil {
				appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "remote_setup", Title: "Bootstrap SSH host", Status: "error", Detail: err.Error()})
				updateDevEnvironmentCloneJob(job, "failed", err.Error())
				return
			}
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "remote_setup", Title: "Bootstrap SSH host", Status: "ok", Detail: truncateDevEnvDetail(out)})
		} else {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "remote_setup", Title: "Bootstrap SSH host", Status: "skipped", Detail: "dry run"})
		}
		if req.TargetDeviceID == "" {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "target_device", Title: "Wait for Yaver device id", Status: "warning", Detail: "rerun with targetDeviceId after the SSH host registers"})
			updateDevEnvironmentCloneJob(job, "completed", "")
			return
		}
	}
	if req.Target.Mode == "managed-cloud" && req.TargetDeviceID == "" {
		appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "target_device", Title: "Wait for cloud device id", Status: "warning", Detail: "managed cloud provisioning must produce targetDeviceId before clone can continue"})
		updateDevEnvironmentCloneJob(job, "completed", "")
		return
	}

	if plan.SourceProfile != nil && (req.InstallMissing || len(req.SyncKinds) > 0 || req.IncludeGitCredentials) {
		result, err := applyDevEnvProfileToTarget(ctx, s, req, *plan.SourceProfile)
		if err != nil {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "toolchain_apply", Title: "Apply toolchain/profile", Status: "error", Detail: err.Error()})
			updateDevEnvironmentCloneJob(job, "failed", err.Error())
			return
		}
		appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "toolchain_apply", Title: "Apply toolchain/profile", Status: "ok", Data: result})
	}

	var firstRepoPath string
	for _, repo := range plan.Repos {
		out, err := cloneDevEnvRepo(ctx, s, req.TargetDeviceID, repo, req.DryRun)
		if err != nil {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "repo_clone", Title: "Clone " + sanitizeRepoURL(repo.URL), Status: "error", Detail: err.Error()})
			updateDevEnvironmentCloneJob(job, "failed", err.Error())
			return
		}
		if firstRepoPath == "" {
			if p, _ := out["path"].(string); p != "" {
				firstRepoPath = p
			}
		}
		appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "repo_clone", Title: "Clone " + sanitizeRepoURL(repo.URL), Status: "ok", Data: out})
	}

	if req.ConfigureCode && firstRepoPath != "" {
		out, err := setDevEnvCodeRepo(ctx, s, req.TargetDeviceID, firstRepoPath, req.DryRun)
		if err != nil {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "code_config", Title: "Configure yaver code target", Status: "warning", Detail: err.Error()})
		} else {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "code_config", Title: "Configure yaver code target", Status: "ok", Data: out})
		}
	}

	if !req.SkipConfigs && len(plan.Configs) > 0 {
		out, err := applyDevEnvConfigsToTarget(ctx, s, req)
		if err != nil {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "configs", Title: "Apply developer configs", Status: "warning", Detail: err.Error()})
		} else {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "configs", Title: "Apply developer configs", Status: "ok", Data: out})
		}
	}

	if req.Verify {
		out, err := verifyDevEnvTarget(ctx, s, req.TargetDeviceID)
		if err != nil {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "verify", Title: "Verify runners and capabilities", Status: "warning", Detail: err.Error()})
		} else {
			appendDevEnvironmentCloneStep(job, DevEnvironmentCloneStep{ID: "verify", Title: "Verify runners and capabilities", Status: "ok", Data: out})
		}
	}
	updateDevEnvironmentCloneJob(job, "completed", "")
}

func devConfigBundleFromSource(ctx context.Context, s *HTTPServer, sourceDeviceID string, keys []string) (DevConfigBundle, error) {
	body := map[string]any{"keys": keys}
	if strings.TrimSpace(sourceDeviceID) != "" && strings.TrimSpace(sourceDeviceID) != localDeviceID() {
		out, err := proxyToDeviceJSON(ctx, "dev_environment_clone", sourceDeviceID, http.MethodPost, "/agent/dev-configs/bundle", body)
		if err != nil {
			return DevConfigBundle{}, err
		}
		raw, _ := json.Marshal(out["bundle"])
		var bundle DevConfigBundle
		if err := json.Unmarshal(raw, &bundle); err != nil {
			return DevConfigBundle{}, err
		}
		return bundle, nil
	}
	return buildDevConfigBundle(keys), nil
}

func applyDevEnvConfigsToTarget(ctx context.Context, s *HTTPServer, req DevEnvironmentCloneRequest) (map[string]any, error) {
	bundle, err := devConfigBundleFromSource(ctx, s, req.SourceDeviceID, req.ConfigKeys)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"bundle": bundle, "dryRun": req.DryRun}
	if strings.TrimSpace(req.TargetDeviceID) != "" && strings.TrimSpace(req.TargetDeviceID) != localDeviceID() {
		return proxyToDeviceJSON(ctx, "dev_environment_clone", req.TargetDeviceID, http.MethodPost, "/agent/dev-configs/apply", body)
	}
	return callDevEnvLocalJSON(s.handleDevConfigApply, http.MethodPost, "/agent/dev-configs/apply", body)
}

func applyDevEnvProfileToTarget(ctx context.Context, s *HTTPServer, req DevEnvironmentCloneRequest, profile EnvironmentProfile) (map[string]any, error) {
	var gitCredentials []GitCredential
	if req.IncludeGitCredentials {
		if strings.TrimSpace(req.SourceDeviceID) == "" || strings.TrimSpace(req.SourceDeviceID) == localDeviceID() {
			creds, err := loadGitCredentials()
			if err != nil {
				return nil, err
			}
			gitCredentials = creds
		} else {
			creds, err := toolchainGitCredentialsFromPeer(ctx, req.SourceDeviceID)
			if err != nil {
				return nil, err
			}
			gitCredentials = creds
		}
	}
	body := map[string]any{
		"profile":               profile,
		"installMissing":        req.InstallMissing,
		"syncKinds":             req.SyncKinds,
		"includeGitCredentials": req.IncludeGitCredentials,
		"gitCredentials":        gitCredentials,
		"dryRun":                req.DryRun,
	}
	if req.TargetDeviceID == "" || req.TargetDeviceID == localDeviceID() {
		syncPayload := map[string][]SyncItem{}
		if len(req.SyncKinds) > 0 && strings.TrimSpace(req.SourceDeviceID) != "" && strings.TrimSpace(req.SourceDeviceID) != localDeviceID() {
			for _, kind := range req.SyncKinds {
				kind = strings.TrimSpace(kind)
				if !syncKindAllowList[kind] {
					continue
				}
				items, err := syncItemsFromPeer(ctx, req.SourceDeviceID, kind)
				if err != nil {
					return nil, fmt.Errorf("fetch source sync %s: %w", kind, err)
				}
				syncPayload[kind] = items
			}
		}
		result := applyEnvironmentProfile(ctx, s.convexURL, profile, req.InstallMissing, syncPayload, gitCredentials, false, req.DryRun)
		raw, _ := json.Marshal(result)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out, nil
	}
	return proxyToDeviceJSON(ctx, "dev_environment_clone", req.TargetDeviceID, http.MethodPost, "/agent/toolchain-sync/apply", body)
}

func cloneDevEnvRepo(ctx context.Context, s *HTTPServer, targetDeviceID string, repo DevEnvironmentCloneRepo, dryRun bool) (map[string]any, error) {
	body := map[string]any{
		"url":            repo.URL,
		"branch":         repo.Branch,
		"dir":            repo.Dir,
		"autoInit":       repo.AutoInit,
		"autoInitRunner": repo.AutoInitRunner,
	}
	if dryRun {
		return map[string]any{"ok": true, "dryRun": true, "url": repo.URL, "dir": repo.Dir, "branch": repo.Branch}, nil
	}
	if strings.TrimSpace(targetDeviceID) != "" && strings.TrimSpace(targetDeviceID) != localDeviceID() {
		return proxyToDeviceJSON(ctx, "dev_environment_clone", targetDeviceID, http.MethodPost, "/repos/clone", body)
	}
	return callDevEnvLocalJSON(s.handleRepoCloneWithMetadata, http.MethodPost, "/repos/clone", body)
}

func setDevEnvCodeRepo(ctx context.Context, s *HTTPServer, targetDeviceID, repoPath string, dryRun bool) (map[string]any, error) {
	body := map[string]any{"query": repoPath}
	if dryRun {
		return map[string]any{"ok": true, "dryRun": true, "query": repoPath}, nil
	}
	if strings.TrimSpace(targetDeviceID) != "" && strings.TrimSpace(targetDeviceID) != localDeviceID() {
		return proxyToDeviceJSON(ctx, "dev_environment_clone", targetDeviceID, http.MethodPost, "/code/repo", body)
	}
	return callDevEnvLocalJSON(s.handleCodeRepo, http.MethodPost, "/code/repo", body)
}

func verifyDevEnvTarget(ctx context.Context, s *HTTPServer, targetDeviceID string) (map[string]any, error) {
	out := map[string]any{}
	if strings.TrimSpace(targetDeviceID) != "" && strings.TrimSpace(targetDeviceID) != localDeviceID() {
		caps, err := proxyToDeviceJSON(ctx, "dev_environment_clone", targetDeviceID, http.MethodGet, "/agent/capabilities", nil)
		if err != nil {
			return nil, err
		}
		runners, err := proxyToDeviceJSON(ctx, "dev_environment_clone", targetDeviceID, http.MethodGet, "/runner-auth/status", nil)
		if err != nil {
			out["runnerWarning"] = err.Error()
		} else {
			out["runnerAuth"] = runners
		}
		out["capabilities"] = caps
		return out, nil
	}
	out["capabilities"] = map[string]any{"ok": true, "machine": selfMachine(ctx)}
	if rows, err := collectRunnerAuthStatusRows(); err == nil {
		out["runnerAuth"] = map[string]any{"ok": true, "runners": rows}
	} else {
		out["runnerWarning"] = err.Error()
	}
	return out, nil
}

func callDevEnvLocalJSON(handler http.HandlerFunc, method, path string, body map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	resp := rec.Result()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		msg := fmt.Sprint(out["error"])
		if msg == "" || msg == "<nil>" {
			msg = resp.Status
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

func truncateDevEnvDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 600 {
		return s
	}
	return s[:600] + "..."
}

func defaultDevEnvCloneReposFromCwd() []DevEnvironmentCloneRepo {
	cwd := ResolveMCPCwd()
	if cwd == "" {
		return nil
	}
	provider, repo := detectRepoFromGit(cwd)
	url := cloneURLForDetectedRepo(provider, repo)
	if url == "" {
		return nil
	}
	branch := ""
	if out, err := gitOutput(cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	return []DevEnvironmentCloneRepo{{URL: url, Branch: branch}}
}
