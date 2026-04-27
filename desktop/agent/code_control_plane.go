package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type CodeRepoRow struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type CodeAttachResult struct {
	Device      *DeviceInfo            `json:"device,omitempty"`
	Code        *CodeConfigSummary     `json:"code,omitempty"`
	Runners     string                 `json:"runners,omitempty"`
	OnlineHosts []DeviceInfo           `json:"onlineHosts,omitempty"`
	Context     map[string]interface{} `json:"context,omitempty"`
}

type CodeDeployResult struct {
	App         string                   `json:"app"`
	Targets     []string                 `json:"targets"`
	Device      string                   `json:"device,omitempty"`
	RepoPath    string                   `json:"repoPath,omitempty"`
	CIProvider  string                   `json:"ciProvider,omitempty"`
	Distributed bool                     `json:"distributed,omitempty"`
	Runs        []map[string]interface{} `json:"runs,omitempty"`
}

type CodeDeployRequest struct {
	App        string   `json:"app,omitempty"`
	Surface    string   `json:"surface,omitempty"`
	Targets    []string `json:"targets,omitempty"`
	RepoQuery  string   `json:"repoQuery,omitempty"`
	RepoPath   string   `json:"repoPath,omitempty"`
	Machine    string   `json:"machine,omitempty"` // "", local, auto, or device id/name
	Distribute bool     `json:"distribute,omitempty"`
	CIProvider string   `json:"ciProvider,omitempty"` // github | gitlab
	CIRepo     string   `json:"ciRepo,omitempty"`
	Workflow   string   `json:"workflow,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Tag        string   `json:"tag,omitempty"`
	File       string   `json:"file,omitempty"`
}

func buildCodeStatusPayload() (map[string]interface{}, error) {
	summary, err := buildCodeConfigSummary()
	if err != nil {
		return nil, err
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"code": summary,
	}
	if cfg.AuthToken != "" {
		if devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); err == nil {
			payload["onlineHosts"] = filterOnlineDevices(devices)
		}
	}
	if normalizeRunnerID(profile.Runner) == "opencode" {
		if oc, err := codeGetOpenCodeConfig(codeAttachedDevice(profile)); err == nil {
			payload["openCode"] = oc
		}
	}
	if globalAgentGraphMgr != nil {
		runs := globalAgentGraphMgr.ListRuns()
		if len(runs) > 5 {
			runs = runs[:5]
		}
		payload["graphs"] = encodeJSONClone(runs)
	}
	return payload, nil
}

func applyCodeAttach(target, username string) (*CodeAttachResult, error) {
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	online := filterOnlineDevices(devices)
	if len(online) == 0 {
		return nil, fmt.Errorf("no online devices found")
	}
	target = strings.TrimSpace(target)
	var device *DeviceInfo
	if target == "" {
		if len(online) == 1 {
			device = &online[0]
		} else {
			return nil, fmt.Errorf("multiple online devices; pass device_id or device name")
		}
	} else {
		device, err = resolveCodeAttachDevice(cfg, target, username)
		if err != nil {
			return nil, err
		}
	}
	profile.WorkMode = codeWorkModeAttached
	profile.AttachedDeviceID = device.DeviceID
	profile.AttachedDeviceName = device.Name
	if !profile.RepoRemote {
		profile.RepoPath = ""
	}
	if err := saveCodeConfig(cfg); err != nil {
		return nil, err
	}
	summary, err := buildCodeConfigSummary()
	if err != nil {
		return nil, err
	}
	out := &CodeAttachResult{
		Device:      device,
		Code:        summary,
		OnlineHosts: online,
	}
	if runners, err := codeRunnerSummaryForDevice(device.DeviceID); err == nil {
		out.Runners = runners
	}
	if ctx, err := codeCurrentContext(device.DeviceID); err == nil {
		out.Context = ctx
	}
	return out, nil
}

func applyCodeDetach() (*CodeConfigSummary, error) {
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	profile.WorkMode = codeWorkModeLocal
	profile.AttachedDeviceID = ""
	profile.AttachedDeviceName = ""
	if profile.RepoRemote {
		profile.RepoPath = ""
		profile.RepoRemote = false
	}
	if err := saveCodeConfig(cfg); err != nil {
		return nil, err
	}
	return buildCodeConfigSummary()
}

func listCodeReposStructured() ([]CodeRepoRow, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	deviceID := codeAttachedDevice(profile)
	out := []CodeRepoRow{}
	if deviceID == "" {
		projects := listDiscoveredProjects()
		for _, p := range projects {
			out = append(out, CodeRepoRow{Name: filepath.Base(p.Path), Path: p.Path})
		}
		sort.Slice(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		return out, nil
	}
	projects, err := codeListRemoteProjects(deviceID)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		out = append(out, CodeRepoRow{Name: p.Name, Path: p.Path})
	}
	return out, nil
}

func setCodeRepoStructured(query string) (map[string]interface{}, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("repo query is required")
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	deviceID := codeAttachedDevice(profile)
	var repoPath string
	var repoName string
	if deviceID == "" {
		ref, err := resolveProjectRef(query, query)
		if err != nil {
			if match, ferr := findProject(query); ferr == nil {
				repoPath = match
			} else {
				return nil, err
			}
		} else {
			repoPath = ref.Path
			repoName = ref.Name
		}
		if err := codeSetLocalWorkDir(repoPath); err != nil {
			return nil, err
		}
		profile.RepoPath = repoPath
		profile.RepoRemote = false
	} else {
		projects, err := codeListRemoteProjects(deviceID)
		if err != nil {
			return nil, err
		}
		match, err := matchCodeProject(projects, query)
		if err != nil {
			return nil, err
		}
		repoPath = match.Path
		repoName = match.Name
		if err := codeSetRemoteWorkDir(deviceID, repoPath); err != nil {
			return nil, err
		}
		profile.RepoPath = repoPath
		profile.RepoRemote = true
	}
	if err := saveCodeConfig(cfg); err != nil {
		return nil, err
	}
	summary, err := buildCodeConfigSummary()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"ok":       true,
		"repoPath": repoPath,
		"repoName": repoName,
		"code":     summary,
	}, nil
}

func runCodeDevActionStructured(action string) (map[string]interface{}, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	deviceID := codeAttachedDevice(profile)
	action = strings.ToLower(strings.TrimSpace(action))
	if deviceID == "" {
		switch action {
		case "reload":
			_, err = localAgentRequest("POST", "/dev/reload", nil)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"ok": true, "action": action, "device": "local"}, nil
		case "status":
			out, err := localAgentRequest("GET", "/dev/status", nil)
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"ok": true, "action": action, "status": out, "device": "local"}, nil
		default:
			return nil, fmt.Errorf("unsupported dev action %q", action)
		}
	}
	switch action {
	case "reload":
		if err := remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/dev/reload", nil, nil); err != nil {
			return nil, err
		}
		return map[string]interface{}{"ok": true, "action": action, "device": deviceID}, nil
	case "status":
		var out map[string]any
		if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/dev/status", nil, &out); err != nil {
			return nil, err
		}
		return map[string]interface{}{"ok": true, "action": action, "status": out, "device": deviceID}, nil
	default:
		return nil, fmt.Errorf("unsupported dev action %q", action)
	}
}

func runCodeDeployStructured(surface string) (*CodeDeployResult, error) {
	return runCodeDeployRequestStructured(CodeDeployRequest{Surface: surface})
}

func runCodeDeployRequestStructured(req CodeDeployRequest) (*CodeDeployResult, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	targets, err := resolveCodeDeployTargets(req)
	if err != nil {
		return nil, err
	}
	repoPath, err := resolveCodeDeployRepoPath(codeAttachedDevice(profile), profile, req)
	if err != nil {
		return nil, err
	}
	app, err := resolveCodeDeployApp(codeAttachedDevice(profile), profile, req, repoPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.CIProvider) != "" {
		return runCodeCIDeployStructured(req, app, repoPath, targets)
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	requestedMachine := strings.TrimSpace(req.Machine)
	if requestedMachine == "" {
		requestedMachine = codeAttachedDevice(profile)
	}
	result := &CodeDeployResult{
		App:         app,
		Targets:     targets,
		Device:      requestedMachine,
		RepoPath:    repoPath,
		Distributed: req.Distribute,
	}
	distribute := req.Distribute || strings.EqualFold(strings.TrimSpace(req.Machine), "auto")
	if distribute && len(targets) > 1 {
		for _, target := range targets {
			machine, reason, err := resolveCodeDeployMachine(target, repoPath, req.Machine, codeAttachedDevice(profile))
			if err != nil {
				return nil, err
			}
			exitCode := shipToAgent(cfg, app, []string{target}, "", repoPath, 0, normalizeDeployShipMachine(machine))
			result.Runs = append(result.Runs, map[string]interface{}{
				"target":          target,
				"machine":         firstNonEmpty(machine, "local"),
				"selectionReason": reason,
				"exitCode":        exitCode,
			})
			if exitCode != 0 {
				return nil, fmt.Errorf("deploy target %s failed with exit %d", target, exitCode)
			}
		}
		return result, nil
	}
	machine, reason, err := resolveCodeDeployMachine(firstNonEmpty(targets[0], ""), repoPath, req.Machine, codeAttachedDevice(profile))
	if err != nil {
		return nil, err
	}
	exitCode := shipToAgent(cfg, app, targets, "", repoPath, 0, normalizeDeployShipMachine(machine))
	result.Device = firstNonEmpty(machine, codeAttachedDevice(profile))
	result.Runs = []map[string]interface{}{{
		"targets":         targets,
		"machine":         firstNonEmpty(machine, "local"),
		"selectionReason": reason,
		"exitCode":        exitCode,
	}}
	if exitCode != 0 {
		return nil, fmt.Errorf("deploy failed with exit %d", exitCode)
	}
	return result, nil
}

func runCodeBuildStructured(target string) (map[string]interface{}, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	workDir, err := codeResolvedWorkDir(profile)
	if err != nil {
		return nil, err
	}
	return codeOpsStructured(codeAttachedDevice(profile), "build", map[string]interface{}{
		"workDir": workDir,
		"target":  strings.TrimSpace(target),
	})
}

func runCodeVaultStructured(op, project, name, value, category, notes string, includeGlobals *bool) (map[string]interface{}, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"op":    strings.TrimSpace(op),
		"scope": "vault",
	}
	if v := normalizeCodeVaultProject(project, profile); v != "" {
		payload["project"] = v
	}
	if v := strings.TrimSpace(name); v != "" {
		payload["name"] = v
	}
	if v := strings.TrimSpace(value); v != "" {
		payload["value"] = v
	}
	if v := strings.TrimSpace(category); v != "" {
		payload["category"] = v
	}
	if v := strings.TrimSpace(notes); v != "" {
		payload["notes"] = v
	}
	if includeGlobals != nil {
		payload["include_globals"] = *includeGlobals
	}
	return codeOpsStructured(codeAttachedDevice(profile), "secrets", payload)
}

func runCodeGitStructured(action string, opts map[string]interface{}) (map[string]interface{}, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, err
	}
	deviceID := codeAttachedDevice(profile)
	workDir, err := codeResolvedWorkDir(profile)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("workDir", workDir)
	action = strings.ToLower(strings.TrimSpace(action))
	path := ""
	method := http.MethodGet
	var body interface{}
	switch action {
	case "status":
		path = "/git/status?" + q.Encode()
	case "log":
		if limit := intFromAny(opts["limit"]); limit > 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		path = "/git/log?" + q.Encode()
	case "diff":
		if file := strings.TrimSpace(stringFromAny(opts["file"])); file != "" {
			q.Set("file", file)
		}
		path = "/git/diff?" + q.Encode()
	case "branches":
		path = "/git/branches?" + q.Encode()
	case "stash":
		method = http.MethodPost
		path = "/git/stash?" + q.Encode()
		body = map[string]interface{}{}
	case "stash-pop":
		method = http.MethodPost
		path = "/git/stash-pop?" + q.Encode()
		body = map[string]interface{}{}
	case "checkout":
		branch := strings.TrimSpace(stringFromAny(opts["branch"]))
		if branch == "" {
			return nil, fmt.Errorf("branch is required")
		}
		method = http.MethodPost
		path = "/git/checkout?" + q.Encode()
		body = map[string]interface{}{"branch": branch}
	case "commit":
		message := strings.TrimSpace(stringFromAny(opts["message"]))
		if message == "" {
			return nil, fmt.Errorf("message is required")
		}
		method = http.MethodPost
		path = "/git/commit?" + q.Encode()
		body = map[string]interface{}{"message": message, "files": stringSliceFromAny(opts["files"])}
	case "push":
		method = http.MethodPost
		path = "/git/push?" + q.Encode()
		body = map[string]interface{}{}
	case "pull":
		method = http.MethodPost
		path = "/git/pull?" + q.Encode()
		body = map[string]interface{}{}
	case "revert":
		hash := strings.TrimSpace(stringFromAny(opts["hash"]))
		if hash == "" {
			return nil, fmt.Errorf("hash is required")
		}
		method = http.MethodPost
		path = "/git/revert?" + q.Encode()
		body = map[string]interface{}{"hash": hash}
	default:
		return nil, fmt.Errorf("unsupported git action %q", action)
	}
	var out map[string]interface{}
	if err := codeAgentJSON(deviceID, method, path, body, &out); err != nil {
		return nil, err
	}
	return map[string]interface{}{"ok": true, "action": action, "result": out, "device": firstNonEmpty(deviceID, "local"), "workDir": workDir}, nil
}

func buildCodeStatusTextPayload() map[string]interface{} {
	out, _ := buildCodeStatusPayload()
	return out
}

func encodeJSONClone(v interface{}) interface{} {
	raw, _ := json.Marshal(v)
	var out interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func codeOpsStructured(deviceID, verb string, payload map[string]interface{}) (map[string]interface{}, error) {
	var out map[string]interface{}
	body := map[string]interface{}{
		"machine": "local",
		"verb":    verb,
		"payload": payload,
	}
	if err := codeAgentJSON(deviceID, http.MethodPost, "/ops", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func codeAgentJSON(deviceID, method, path string, body any, out any) error {
	if strings.TrimSpace(deviceID) != "" {
		return remoteAgentJSONForDevice(context.Background(), deviceID, method, path, body, out)
	}
	return codeLocalJSON(method, path, body, out)
}

func codeLocalJSON(method, path string, body any, out any) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		return fmt.Errorf("not signed in — run 'yaver auth'")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, localAgentBaseURL()+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("local %s %s failed: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func codeResolvedWorkDir(profile *CodeCLIConfig) (string, error) {
	if profile == nil {
		return "", fmt.Errorf("code profile unavailable")
	}
	if ctx, err := codeCurrentContext(codeAttachedDevice(profile)); err == nil {
		if workDir, _ := ctx["workDir"].(string); strings.TrimSpace(workDir) != "" {
			return strings.TrimSpace(workDir), nil
		}
	}
	if v := strings.TrimSpace(profile.RepoPath); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no active repo/workdir selected for yaver code")
}

func normalizeCodeVaultProject(project string, profile *CodeCLIConfig) string {
	project = strings.TrimSpace(project)
	switch project {
	case "":
		return ""
	case "current":
		if app, err := codeCurrentAppName(codeAttachedDevice(profile), profile); err == nil && strings.TrimSpace(app) != "" {
			return strings.TrimSpace(app)
		}
		return ""
	default:
		return project
	}
}

func stringFromAny(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(v)
	}
}

func intFromAny(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	default:
		return 0
	}
}

func stringSliceFromAny(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return append([]string{}, t...)
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s := strings.TrimSpace(stringFromAny(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func resolveCodeDeployTargets(req CodeDeployRequest) ([]string, error) {
	if len(req.Targets) > 0 {
		return cleanProjectList(req.Targets), nil
	}
	surface := strings.ToLower(strings.TrimSpace(req.Surface))
	switch surface {
	case "mobile":
		return []string{"testflight", "playstore"}, nil
	case "backend":
		return []string{"convex"}, nil
	case "frontend":
		return []string{"cloudflare"}, nil
	case "all":
		return []string{"testflight", "playstore", "convex", "cloudflare"}, nil
	case "":
		return nil, fmt.Errorf("surface or targets is required")
	default:
		return []string{surface}, nil
	}
}

func resolveCodeDeployRepoPath(deviceID string, profile *CodeCLIConfig, req CodeDeployRequest) (string, error) {
	if v := strings.TrimSpace(req.RepoPath); v != "" {
		return v, nil
	}
	if q := strings.TrimSpace(req.RepoQuery); q != "" {
		if deviceID == "" {
			if ref, err := resolveProjectRef(q, q); err == nil && strings.TrimSpace(ref.Path) != "" {
				return strings.TrimSpace(ref.Path), nil
			}
			if match, err := findProject(q); err == nil && strings.TrimSpace(match) != "" {
				return strings.TrimSpace(match), nil
			}
			return "", fmt.Errorf("repo query %q not found locally", q)
		}
		projects, err := codeListRemoteProjects(deviceID)
		if err != nil {
			return "", err
		}
		match, err := matchCodeProject(projects, q)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(match.Path), nil
	}
	return codeResolvedWorkDir(profile)
}

func resolveCodeDeployApp(deviceID string, profile *CodeCLIConfig, req CodeDeployRequest, repoPath string) (string, error) {
	if v := strings.TrimSpace(req.App); v != "" {
		return v, nil
	}
	tmp := *profile
	tmp.RepoPath = repoPath
	return codeCurrentAppName(deviceID, &tmp)
}

func resolveCodeDeployMachine(target, workDir, requestedMachine, attachedDevice string) (string, string, error) {
	requestedMachine = strings.TrimSpace(requestedMachine)
	switch requestedMachine {
	case "", "current":
		if strings.TrimSpace(attachedDevice) != "" {
			return strings.TrimSpace(attachedDevice), "used the currently attached machine", nil
		}
		return "local", "used the local machine", nil
	case "local":
		return "local", "forced local deployment", nil
	case "auto":
		plan, err := codeLocalOpsPlan("deploy", map[string]interface{}{
			"machine": "auto",
			"verb":    "deploy",
			"payload": map[string]interface{}{"target": target, "workDir": workDir},
		})
		if err != nil {
			return "", "", err
		}
		resolved, _ := plan["resolvedMachine"].(string)
		reason, _ := plan["selectionReason"].(string)
		if strings.TrimSpace(resolved) == "" {
			resolved = "local"
		}
		return strings.TrimSpace(resolved), firstNonEmpty(strings.TrimSpace(reason), "auto-selected deploy machine"), nil
	default:
		return requestedMachine, "forced requested machine", nil
	}
}

func normalizeDeployShipMachine(machine string) string {
	machine = strings.TrimSpace(machine)
	if machine == "" || strings.EqualFold(machine, "local") {
		return ""
	}
	return machine
}

func runCodeCIDeployStructured(req CodeDeployRequest, app, repoPath string, targets []string) (*CodeDeployResult, error) {
	provider := strings.ToLower(strings.TrimSpace(req.CIProvider))
	if provider == "" {
		return nil, fmt.Errorf("ci provider is required")
	}
	branch := firstNonEmpty(strings.TrimSpace(req.Branch), "main")
	repo := strings.TrimSpace(req.CIRepo)
	if repo == "" {
		_, detected := detectRepoFromGit(repoPath)
		repo = strings.TrimSpace(detected)
	}
	if repo == "" {
		return nil, fmt.Errorf("could not detect CI repo from %s; pass ciRepo explicitly", repoPath)
	}
	switch provider {
	case "github":
		token := getVaultToken("github-token")
		if token == "" {
			return nil, fmt.Errorf("github-token not found in vault")
		}
		if strings.TrimSpace(req.File) != "" && strings.TrimSpace(req.Tag) != "" {
			if err := uploadGitHubRelease(token, repo, strings.TrimSpace(req.Tag), strings.TrimSpace(req.File)); err != nil {
				return nil, err
			}
		} else {
			if strings.TrimSpace(req.Workflow) == "" {
				return nil, fmt.Errorf("workflow is required for github ci deploy")
			}
			if err := triggerGitHubWorkflow(token, repo, strings.TrimSpace(req.Workflow), branch, nil); err != nil {
				return nil, err
			}
		}
	case "gitlab":
		token := getVaultToken("gitlab-token")
		if token == "" {
			return nil, fmt.Errorf("gitlab-token not found in vault")
		}
		if err := triggerGitLabPipeline(token, repo, branch, nil); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported ci provider %q", provider)
	}
	return &CodeDeployResult{
		App:        app,
		Targets:    targets,
		RepoPath:   repoPath,
		CIProvider: provider,
		Runs: []map[string]interface{}{{
			"provider": provider,
			"repo":     repo,
			"workflow": strings.TrimSpace(req.Workflow),
			"branch":   branch,
			"tag":      strings.TrimSpace(req.Tag),
			"file":     strings.TrimSpace(req.File),
			"targets":  targets,
		}},
	}, nil
}

func codeLocalOpsPlan(verb string, body map[string]interface{}) (map[string]interface{}, error) {
	if body == nil {
		body = map[string]interface{}{}
	}
	if _, ok := body["verb"]; !ok {
		body["verb"] = verb
	}
	var out map[string]interface{}
	if err := codeLocalJSON(http.MethodPost, "/ops/plan", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
