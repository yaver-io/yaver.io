package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

type EnvironmentProjectSummary struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
}

type EnvironmentRunnerSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Command        string `json:"command"`
	Installed      bool   `json:"installed"`
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
}

type EnvironmentSyncSummary struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type ToolchainGitCredentialSummary struct {
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
	HasToken bool   `json:"hasToken"`
}

type EnvironmentProfile struct {
	GeneratedAt        string                          `json:"generatedAt"`
	SourceDeviceID     string                          `json:"sourceDeviceId,omitempty"`
	Hostname           string                          `json:"hostname,omitempty"`
	Platform           string                          `json:"platform"`
	Arch               string                          `json:"arch"`
	WorkDir            string                          `json:"workDir,omitempty"`
	DiscoveredProjects []EnvironmentProjectSummary     `json:"discoveredProjects,omitempty"`
	Binaries           []DetectedBinary                `json:"binaries,omitempty"`
	ToolchainTargets   []string                        `json:"toolchainTargets,omitempty"`
	Configs            []DetectedDevConfig             `json:"configs,omitempty"`
	Runners            []EnvironmentRunnerSummary      `json:"runners,omitempty"`
	SyncKinds          []EnvironmentSyncSummary        `json:"syncKinds,omitempty"`
	GitCredentials     []ToolchainGitCredentialSummary `json:"gitCredentials,omitempty"`
}

type EnvironmentProfileApplyResult struct {
	OK                bool     `json:"ok"`
	Status            string   `json:"status"`
	SourcePlatform    string   `json:"sourcePlatform,omitempty"`
	TargetPlatform    string   `json:"targetPlatform"`
	InstallPlan       []string `json:"installPlan,omitempty"`
	Installed         []string `json:"installed,omitempty"`
	AlreadyPresent    []string `json:"alreadyPresent,omitempty"`
	ImportedSyncKinds []string `json:"importedSyncKinds,omitempty"`
	ManualSteps       []string `json:"manualSteps,omitempty"`
	ProjectHints      []string `json:"projectHints,omitempty"`
	Notes             []string `json:"notes,omitempty"`
	RemovalPlan       []string `json:"removalPlan,omitempty"`
	Removed           []string `json:"removed,omitempty"`
	ImportedGitHosts  []string `json:"importedGitHosts,omitempty"`
	RemovedGitHosts   []string `json:"removedGitHosts,omitempty"`
}

func currentGitCredentialSummaries() []ToolchainGitCredentialSummary {
	creds, err := loadGitCredentials()
	if err != nil || len(creds) == 0 {
		return nil
	}
	out := make([]ToolchainGitCredentialSummary, 0, len(creds))
	for _, cred := range creds {
		out = append(out, ToolchainGitCredentialSummary{
			Host:     cred.Host,
			Username: cred.Username,
			HasToken: strings.TrimSpace(cred.Token) != "",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Host) < strings.ToLower(out[j].Host)
	})
	return out
}

func buildEnvironmentProfile(s *HTTPServer) EnvironmentProfile {
	hostname, _ := os.Hostname()
	projects := listDiscoveredProjects()
	if len(projects) == 0 && s != nil && s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" {
		projects = []projectInfo{{Path: s.taskMgr.workDir}}
	}
	projectSummaries := make([]EnvironmentProjectSummary, 0, len(projects))
	for _, project := range projects {
		projectSummaries = append(projectSummaries, EnvironmentProjectSummary{
			Path:   project.Path,
			Branch: project.Branch,
		})
	}

	runnerIDs := make([]string, 0, len(builtinRunners))
	for id := range builtinRunners {
		runnerIDs = append(runnerIDs, id)
	}
	sort.Strings(runnerIDs)
	runners := make([]EnvironmentRunnerSummary, 0, len(runnerIDs))
	workDir := ""
	if s != nil && s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}
	sourceDeviceID := ""
	if s != nil {
		sourceDeviceID = s.deviceID
	}
	for _, id := range runnerIDs {
		runner := builtinRunners[id]
		installed := DiscoverBinary(runner.Command) != ""
		status := RunnerRuntimeStatus{Ready: installed}
		if installed {
			status = DetectRunnerRuntimeStatus(runner, workDir)
		}
		runners = append(runners, EnvironmentRunnerSummary{
			ID:             runner.RunnerID,
			Name:           runner.Name,
			Command:        runner.Command,
			Installed:      installed,
			Ready:          installed && status.Ready,
			AuthConfigured: status.AuthConfigured,
			AuthSource:     status.AuthSource,
			Warning:        status.Warning,
			Error:          status.Error,
		})
	}

	syncKinds := make([]EnvironmentSyncSummary, 0, len(syncKindAllowList))
	kinds := make([]string, 0, len(syncKindAllowList))
	for kind := range syncKindAllowList {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		store, err := OpenSyncStore(kind)
		if err != nil {
			continue
		}
		syncKinds = append(syncKinds, EnvironmentSyncSummary{
			Kind:  kind,
			Count: len(store.List()),
		})
	}

	binaries := DiscoverInstalledBinaries()
	return EnvironmentProfile{
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
		SourceDeviceID:     sourceDeviceID,
		Hostname:           hostname,
		Platform:           runtime.GOOS,
		Arch:               runtime.GOARCH,
		WorkDir:            workDir,
		DiscoveredProjects: projectSummaries,
		Binaries:           binaries,
		ToolchainTargets:   toolchainTargetsFromBinaries(binaries),
		Configs:            discoverDevConfigs(),
		Runners:            runners,
		SyncKinds:          syncKinds,
		GitCredentials:     currentGitCredentialSummaries(),
	}
}

func toolchainTargetsFromBinaries(binaries []DetectedBinary) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(binaries))
	for _, binary := range binaries {
		target := strings.TrimSpace(binary.InstallTarget)
		if target == "" {
			if mapped, ok := profileInstallTarget(binary.Name); ok {
				target = mapped
			}
		}
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return toolchainTargetPriority(out[i]) < toolchainTargetPriority(out[j])
	})
	return out
}

func toolchainTargetPriority(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "yaver":
		return 10
	case "cloudflared", "wrangler":
		return 20
	case "convex":
		return 30
	case "vercel":
		return 40
	case "git", "gh", "glab":
		return 50
	case "node":
		return 70
	case "go", "python3", "python", "uv", "cargo", "rustc", "ruby", "php", "dart", "flutter", "java", "gradle":
		return 80
	case "docker", "tailscale", "kubectl", "terraform":
		return 100
	case "android-sdk", "xcodegen", "xcodebuild", "xcrun", "pod", "fastlane":
		return 110
	case "postgresql-client", "sqlite3", "redis-tools":
		return 120
	case "tmux", "ffmpeg", "rg", "fd", "bat", "jq", "fzf":
		return 130
	default:
		return 500
	}
}

func profileInstallTarget(name string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "yaver":
		return "yaver", true
	case "git":
		return "git", true
	case "gh":
		return "gh", true
	case "glab":
		return "glab", true
	case "uv":
		return "uv", true
	case "docker":
		return "docker", true
	case "docker-compose":
		return "docker", true
	case "tailscale":
		return "tailscale", true
	case "cloudflared":
		return "cloudflared", true
	case "wrangler":
		return "wrangler", true
	case "convex":
		return "convex", true
	case "vercel":
		return "vercel", true
	case "supabase":
		return "supabase", true
	case "sqlite3":
		return "sqlite3", true
	case "psql":
		return "postgresql-client", true
	case "redis-cli":
		return "redis-tools", true
	case "rg", "ripgrep":
		return "rg", true
	case "fd", "fdfind":
		return "fd", true
	case "bat", "batcat":
		return "bat", true
	case "jq":
		return "jq", true
	case "fzf":
		return "fzf", true
	case "node", "npm", "npx", "pnpm", "yarn", "bun", "deno":
		return "node", true
	case "opencode":
		return "opencode", true
	case "claude", "claude-code":
		return "claude", true
	case "codex":
		return "codex", true
	case "glm", "zai", "z.ai":
		// GLM rides the claude binary (provider env selects z.ai).
		return "claude", true
	case "chrome", "google-chrome", "google-chrome-stable":
		return "chrome", true
	case "chromium", "chromium-browser":
		return "chromium", true
	case "firefox":
		return "firefox", true
	case "adb", "emulator", "android-sdk":
		return "android-sdk", true
	case "go", "cargo", "rustc", "rustup", "python3", "python", "pip3", "pip", "pipx", "ruby", "gem", "bundle", "composer", "php", "dart", "flutter", "java", "javac", "gradle", "mvn", "kubectl", "terraform", "aws", "gcloud", "az", "netlify", "firebase", "railway", "flyctl", "xcodebuild", "xcrun", "pod", "fastlane", "make", "cmake", "ninja", "gcc", "g++", "clang", "clang++", "swift":
		return strings.ToLower(strings.TrimSpace(name)), true
	case "tmux":
		return "tmux", true
	case "ffmpeg":
		return "ffmpeg", true
	}
	return "", false
}

func installTargetSupported(name, convexURL string) bool {
	if _, ok := lookupIntegration(name); ok {
		return true
	}
	for _, candidate := range PackageRegistry(convexURL) {
		if candidate.Name == name {
			return ResolveInstallStep(candidate, AvailablePackageManagersSet()) != nil
		}
	}
	return false
}

func runInstallTarget(ctx context.Context, convexURL, tool string) error {
	stream := newLogStream("toolchain-sync-install:" + tool)
	if plan, ok := lookupIntegration(tool); ok {
		if checkInstalled(tool) == "✓" {
			return nil
		}
		if plan.runFunc != nil {
			return plan.runFunc(ctx, func(string) {})
		}
		switch runtime.GOOS {
		case "darwin":
			for _, command := range plan.macOS {
				if err := runRegistryInstall(ctx, tool, &PackageRegistryStep{PackageManager: "shell", Command: command}, stream); err != nil {
					return err
				}
			}
			return nil
		case "linux":
			for _, step := range plan.linux {
				if DiscoverBinary(step.manager) != "" {
					return runRegistryInstall(ctx, tool, &PackageRegistryStep{PackageManager: step.manager, Command: step.cmd}, stream)
				}
			}
			return fmt.Errorf("no compatible package manager for %s", tool)
		default:
			return fmt.Errorf("unsupported target OS: %s", runtime.GOOS)
		}
	}
	for _, candidate := range PackageRegistry(convexURL) {
		if candidate.Name != tool {
			continue
		}
		step := ResolveInstallStep(candidate, AvailablePackageManagersSet())
		if step == nil {
			return fmt.Errorf("no compatible install step for %s", tool)
		}
		return runRegistryInstall(ctx, tool, step, stream)
	}
	return fmt.Errorf("unknown install target: %s", tool)
}

func syncItemsFromPeer(ctx context.Context, deviceID, kind string) ([]SyncItem, error) {
	status, raw, err := proxyToDevice(ctx, "toolchain-sync", deviceID, http.MethodGet, "/sync/"+kind+"?since=0", nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("peer sync fetch %s: http %d", kind, status)
	}
	var payload struct {
		Items []SyncItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Items, nil
}

func profileFromPeer(ctx context.Context, deviceID string) (*EnvironmentProfile, error) {
	status, raw, err := proxyToDevice(ctx, "toolchain-sync", deviceID, http.MethodGet, "/agent/toolchain-sync/profile", nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("peer profile fetch: http %d", status)
	}
	var payload struct {
		OK      bool               `json:"ok"`
		Profile EnvironmentProfile `json:"profile"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return &payload.Profile, nil
}

func toolchainGitCredentialsFromPeer(ctx context.Context, deviceID string) ([]GitCredential, error) {
	status, raw, err := proxyToDevice(ctx, "toolchain-sync", deviceID, http.MethodGet, "/agent/toolchain-sync/git-credentials", nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("peer git credentials fetch: http %d", status)
	}
	var payload struct {
		OK          bool            `json:"ok"`
		Credentials []GitCredential `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Credentials, nil
}

func importToolchainGitCredentials(creds []GitCredential, removeMissing bool, dryRun bool) (imported []string, removed []string, notes []string) {
	current, err := loadGitCredentials()
	if err != nil {
		notes = append(notes, fmt.Sprintf("Load current git credentials failed: %v", err))
		return
	}
	currentByHost := map[string]GitCredential{}
	for _, cred := range current {
		currentByHost[strings.ToLower(strings.TrimSpace(cred.Host))] = cred
	}
	next := make([]GitCredential, 0, len(creds))
	seen := map[string]bool{}
	for _, cred := range creds {
		host := strings.TrimSpace(cred.Host)
		if host == "" || strings.TrimSpace(cred.Token) == "" {
			continue
		}
		key := strings.ToLower(host)
		seen[key] = true
		next = append(next, GitCredential{
			Host:     host,
			Username: strings.TrimSpace(cred.Username),
			Token:    strings.TrimSpace(cred.Token),
		})
		imported = append(imported, host)
	}
	if removeMissing {
		for key, cred := range currentByHost {
			if !seen[key] {
				removed = append(removed, cred.Host)
			}
		}
	}
	sort.Strings(imported)
	sort.Strings(removed)
	if dryRun {
		return
	}
	if !removeMissing {
		for key, existing := range currentByHost {
			if seen[key] {
				continue
			}
			next = append(next, existing)
		}
	}
	sort.Slice(next, func(i, j int) bool {
		return strings.ToLower(next[i].Host) < strings.ToLower(next[j].Host)
	})
	if err := saveGitCredentials(next); err != nil {
		notes = append(notes, fmt.Sprintf("Save git credentials failed: %v", err))
	}
	return
}

func applyEnvironmentProfile(ctx context.Context, convexURL string, profile EnvironmentProfile, installMissing bool, syncPayload map[string][]SyncItem, gitCredentials []GitCredential, removeMissing bool, dryRun bool) EnvironmentProfileApplyResult {
	result := EnvironmentProfileApplyResult{
		OK:             true,
		Status:         "ok",
		SourcePlatform: profile.Platform,
		TargetPlatform: runtime.GOOS,
	}

	if profile.Platform != "" && profile.Platform != runtime.GOOS {
		result.Notes = append(result.Notes, fmt.Sprintf("Cross-platform clone: source=%s target=%s. Linux-safe/common tools only.", profile.Platform, runtime.GOOS))
	}

	sourceToolTargets := map[string]bool{}
	installTargets := map[string]bool{}
	for _, binary := range profile.Binaries {
		if target, ok := profileInstallTarget(binary.Name); ok {
			installTargets[target] = true
			sourceToolTargets[target] = true
		}
	}
	for _, runner := range profile.Runners {
		if target, ok := profileInstallTarget(runner.ID); ok {
			installTargets[target] = true
			sourceToolTargets[target] = true
			// Install is automated; auth is not (subscription OAuth only).
			// Point at the mirror path instead of a dead-end manual step.
			if runner.Installed || runner.Ready {
				switch normalizeRunnerID(runner.ID) {
				case "claude", "codex", "opencode":
					result.Notes = append(result.Notes, fmt.Sprintf(
						"%s auth does not travel with the toolchain — mirror it from a signed-in machine (runner_auth_mirror / credentials_import) or run the device-auth flow on the target.",
						normalizeRunnerID(runner.ID)))
				}
			}
			continue
		}
	}

	if removeMissing {
		targetProfile := buildEnvironmentProfile(nil)
		targetOnly := map[string]bool{}
		for _, binary := range targetProfile.Binaries {
			if target, ok := profileInstallTarget(binary.Name); ok && !sourceToolTargets[target] {
				targetOnly[target] = true
			}
		}
		for _, runner := range targetProfile.Runners {
			if !(runner.Installed || runner.Ready) {
				continue
			}
			if target, ok := profileInstallTarget(runner.ID); ok && !sourceToolTargets[target] {
				targetOnly[target] = true
			}
		}
		extraTargets := make([]string, 0, len(targetOnly))
		for target := range targetOnly {
			extraTargets = append(extraTargets, target)
		}
		sort.Strings(extraTargets)
		for _, target := range extraTargets {
			result.RemovalPlan = append(result.RemovalPlan, target)
		}
		if len(extraTargets) > 0 {
			result.Notes = append(result.Notes, "Toolchain sync does not auto-uninstall OS packages yet; removalPlan lists target-only tools for manual cleanup.")
		}
	}

	targets := make([]string, 0, len(installTargets))
	for target := range installTargets {
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool {
		pi := toolchainTargetPriority(targets[i])
		pj := toolchainTargetPriority(targets[j])
		if pi != pj {
			return pi < pj
		}
		return targets[i] < targets[j]
	})
	for _, target := range targets {
		if checkInstalled(target) == "✓" {
			result.AlreadyPresent = append(result.AlreadyPresent, target)
			continue
		}
		if !installTargetSupported(target, convexURL) {
			result.ManualSteps = append(result.ManualSteps, fmt.Sprintf("%s is present on source but not installable automatically on this target.", target))
			continue
		}
		if dryRun || !installMissing {
			result.InstallPlan = append(result.InstallPlan, target)
			continue
		}
		if err := runInstallTarget(ctx, convexURL, target); err != nil {
			result.Status = "partial"
			result.Notes = append(result.Notes, fmt.Sprintf("Install %s failed: %v", target, err))
			continue
		}
		result.Installed = append(result.Installed, target)
	}

	for kind, items := range syncPayload {
		if len(items) == 0 {
			continue
		}
		if dryRun {
			result.ImportedSyncKinds = append(result.ImportedSyncKinds, kind)
			continue
		}
		store, err := OpenSyncStore(kind)
		if err != nil {
			result.Status = "partial"
			result.Notes = append(result.Notes, fmt.Sprintf("Open sync store %s failed: %v", kind, err))
			continue
		}
		store.Merge(items)
		result.ImportedSyncKinds = append(result.ImportedSyncKinds, kind)
	}

	if len(gitCredentials) > 0 || removeMissing {
		importedHosts, removedHosts, gitNotes := importToolchainGitCredentials(gitCredentials, removeMissing, dryRun)
		result.ImportedGitHosts = importedHosts
		result.RemovedGitHosts = removedHosts
		for _, note := range gitNotes {
			result.Status = "partial"
			result.Notes = append(result.Notes, note)
		}
		if dryRun {
			result.Removed = append(result.Removed, removedHosts...)
		}
	}

	for _, project := range profile.DiscoveredProjects {
		if strings.TrimSpace(project.Path) == "" {
			continue
		}
		result.ProjectHints = append(result.ProjectHints, project.Path)
	}

	if len(result.Installed) == 0 && len(result.ImportedSyncKinds) == 0 && len(result.InstallPlan) == 0 && len(result.ImportedGitHosts) == 0 && len(result.RemovedGitHosts) == 0 {
		result.Notes = append(result.Notes, "No automatic changes were needed from the supplied profile.")
	}
	return result
}

func (s *HTTPServer) handleEnvironmentProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":      true,
		"profile": buildEnvironmentProfile(s),
	})
}

func (s *HTTPServer) handleToolchainGitCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	// SECURITY (audit 2026-07-13): plaintext git PATs are a local secret and must
	// never cross machines. A same-user remote worker (relay-bridged / proxied /
	// non-loopback) must not be able to pull them — same boundary as ops secret
	// verbs. Local owner tooling on 127.0.0.1 keeps access.
	if opsCallIsRemote(r) {
		jsonError(w, http.StatusForbidden, "git credentials are local-only and cannot be read from another device")
		return
	}
	creds, err := loadGitCredentials()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load git credentials: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":          true,
		"credentials": creds,
	})
}

func (s *HTTPServer) handleEnvironmentProfileApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Profile               *EnvironmentProfile   `json:"profile"`
		SourceDeviceID        string                `json:"sourceDeviceId"`
		InstallMissing        bool                  `json:"installMissing"`
		SyncKinds             []string              `json:"syncKinds"`
		SyncPayload           map[string][]SyncItem `json:"syncPayload"`
		IncludeGitCredentials bool                  `json:"includeGitCredentials"`
		GitCredentials        []GitCredential       `json:"gitCredentials"`
		RemoveMissing         bool                  `json:"removeMissing"`
		DryRun                bool                  `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	profile := body.Profile
	syncPayload := body.SyncPayload
	gitCredentials := body.GitCredentials
	if syncPayload == nil {
		syncPayload = map[string][]SyncItem{}
	}
	if strings.TrimSpace(body.SourceDeviceID) != "" {
		remoteProfile, err := profileFromPeer(r.Context(), body.SourceDeviceID)
		if err != nil {
			jsonError(w, http.StatusBadGateway, "fetch source profile: "+err.Error())
			return
		}
		profile = remoteProfile
		for _, kind := range body.SyncKinds {
			kind = strings.TrimSpace(kind)
			if !syncKindAllowList[kind] {
				continue
			}
			items, err := syncItemsFromPeer(r.Context(), body.SourceDeviceID, kind)
			if err != nil {
				jsonError(w, http.StatusBadGateway, "fetch source sync "+kind+": "+err.Error())
				return
			}
			syncPayload[kind] = items
		}
		if body.IncludeGitCredentials {
			creds, err := toolchainGitCredentialsFromPeer(r.Context(), body.SourceDeviceID)
			if err != nil {
				jsonError(w, http.StatusBadGateway, "fetch source git credentials: "+err.Error())
				return
			}
			gitCredentials = creds
		}
	}
	if profile == nil {
		jsonError(w, http.StatusBadRequest, "profile or sourceDeviceId required")
		return
	}
	result := applyEnvironmentProfile(r.Context(), s.convexURL, *profile, body.InstallMissing, syncPayload, gitCredentials, body.RemoveMissing, body.DryRun)
	jsonReply(w, http.StatusOK, result)
}
