package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// mobileWorkspaceAction is an invocable route, not advisory prose. Mobile
// Workspace renders this as the next tap when a remote-box capability is not
// ready. Secrets are never returned by this endpoint.
type mobileWorkspaceAction struct {
	Label  string `json:"label"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Stream string `json:"stream,omitempty"`
}

type mobileWorkspaceGate struct {
	ID         string                 `json:"id"`
	Code       string                 `json:"code"`
	Label      string                 `json:"label"`
	Ready      bool                   `json:"ready"`
	Configured bool                   `json:"configured"`
	Detail     string                 `json:"detail,omitempty"`
	Model      string                 `json:"model,omitempty"`
	Provider   string                 `json:"provider,omitempty"`
	Action     *mobileWorkspaceAction `json:"action,omitempty"`
}

type mobileWorkspaceStatus struct {
	OK           bool                   `json:"ok"`
	Ready        bool                   `json:"ready"`
	Stack        map[string]string      `json:"stack"`
	Device       mobileWorkspaceGate    `json:"device"`
	Runners      []mobileWorkspaceGate  `json:"runners"`
	OpenCode     mobileWorkspaceGate    `json:"openCode"`
	GitProviders []mobileWorkspaceGate  `json:"gitProviders"`
	Backend      mobileWorkspaceGate    `json:"backend"`
	Layout       *WorkspaceLayoutStatus `json:"layout,omitempty"`
}

func runnerWorkspaceGate(row runnerAuthStatusRow) mobileWorkspaceGate {
	id := normalizeRunnerAuthName(row.ID)
	if id == "" {
		id = strings.ToLower(strings.TrimSpace(row.ID))
	}
	label := row.Name
	if strings.TrimSpace(label) == "" {
		label = id
	}
	gate := mobileWorkspaceGate{
		ID: id, Label: label, Ready: row.Installed && row.Ready && row.AuthConfigured && row.AuthVerified,
		Configured: row.Installed && row.AuthConfigured,
		Detail:     firstNonEmpty(strings.TrimSpace(row.Error), strings.TrimSpace(row.Warning), strings.TrimSpace(row.Detail)),
	}
	switch {
	case !row.Installed:
		gate.Code = "mobile_workspace.runner.not_installed"
		gate.Detail = "Not installed on this remote box"
		installPath := "/install/" + id
		gate.Action = &mobileWorkspaceAction{Label: "Install " + label, Method: http.MethodPost, Path: installPath, Stream: installStreamPathForEndpoint(installPath)}
	case !row.AuthConfigured:
		gate.Code = "mobile_workspace.runner.auth_required"
		if id == "opencode" {
			gate.Detail = "OpenCode needs a provider and model on this remote box"
			gate.Action = &mobileWorkspaceAction{Label: "Configure OpenCode", Method: http.MethodPatch, Path: "/runner/opencode/config"}
		} else {
			gate.Detail = label + " needs sign-in on this remote box"
			gate.Action = &mobileWorkspaceAction{Label: "Sign in to " + label, Method: http.MethodPost, Path: "/runner-auth/browser/start"}
		}
	case !row.AuthVerified:
		gate.Code = "mobile_workspace.runner.verification_required"
		gate.Detail = "Credentials are present but have not completed a provider operation yet"
		gate.Action = &mobileWorkspaceAction{Label: "Test " + label, Method: http.MethodPost, Path: "/agent/runners/test"}
	case !row.Ready:
		gate.Code = "mobile_workspace.runner.unavailable"
		gate.Action = &mobileWorkspaceAction{Label: "Test " + label, Method: http.MethodPost, Path: "/agent/runners/test"}
	default:
		gate.Code = "mobile_workspace.runner.ready"
		gate.Detail = firstNonEmpty(strings.TrimSpace(row.AuthSource), "Operational on this remote box")
	}
	return gate
}

func buildMobileWorkspaceStatus(runners []runnerAuthStatusRow, onboarding machineOnboardingStatus, openCode OpenCodeConfigSummary) mobileWorkspaceStatus {
	status := mobileWorkspaceStatus{
		OK:      true,
		Stack:   map[string]string{"framework": "react-native", "language": "typescript", "backend": "yaver-serverless", "database": "sqlite", "runtime": "remote-box"},
		Device:  mobileWorkspaceGate{ID: "remote-box", Code: "mobile_workspace.device.ready", Label: "Remote device", Ready: true, Configured: true, Detail: "Agent answered this operational readiness probe"},
		Backend: mobileWorkspaceGate{ID: "yaver-serverless", Code: "mobile_workspace.backend.ready", Label: "Yaver Serverless · SQLite", Ready: true, Configured: true, Detail: "Built into the Yaver agent; no Docker required"},
	}

	opencodeAuthConfigured := false
	opencodeVerified := false
	anyRunnerReady := false
	for _, row := range runners {
		id := normalizeRunnerAuthName(row.ID)
		if id != "claude" && id != "codex" && id != "opencode" {
			continue
		}
		gate := runnerWorkspaceGate(row)
		status.Runners = append(status.Runners, gate)
		anyRunnerReady = anyRunnerReady || gate.Ready
		if id == "opencode" {
			opencodeAuthConfigured = row.Installed && row.AuthConfigured
			opencodeVerified = row.Installed && row.Ready && row.AuthConfigured && row.AuthVerified
		}
	}

	model := firstNonEmpty(strings.TrimSpace(openCode.BuildModel), strings.TrimSpace(openCode.Model), "deepseek/deepseek-v4-flash")
	provider := ""
	if slash := strings.Index(model, "/"); slash > 0 {
		provider = model[:slash]
	}
	status.OpenCode = mobileWorkspaceGate{
		ID: "opencode-provider", Label: "OpenCode provider", Model: model, Provider: provider,
		Ready: opencodeVerified, Configured: opencodeAuthConfigured,
	}
	if opencodeVerified {
		status.OpenCode.Code = "mobile_workspace.opencode.provider_ready"
		status.OpenCode.Detail = "Provider credentials completed a real model operation"
		status.OpenCode.Action = &mobileWorkspaceAction{Label: "Test OpenCode", Method: http.MethodPost, Path: "/agent/runners/test"}
	} else if opencodeAuthConfigured {
		status.OpenCode.Code = "mobile_workspace.opencode.provider_verification_required"
		status.OpenCode.Detail = "Provider credentials are present; run a real model generation probe"
		status.OpenCode.Action = &mobileWorkspaceAction{Label: "Test OpenCode", Method: http.MethodPost, Path: "/agent/runners/test"}
	} else {
		status.OpenCode.Code = "mobile_workspace.opencode.provider_required"
		status.OpenCode.Detail = "Connect DeepSeek or another OpenCode provider on this remote box"
		status.OpenCode.Action = &mobileWorkspaceAction{Label: "Configure OpenCode", Method: http.MethodPatch, Path: "/runner/opencode/config"}
	}

	status.GitProviders = append(status.GitProviders, mobileWorkspaceGate{
		ID: "yaver-git", Code: "mobile_workspace.git.ready", Label: "Yaver Git", Ready: true, Configured: true, Detail: "Built in",
	})
	for _, providerStatus := range onboarding.Providers {
		if providerStatus.ID != "github" && providerStatus.ID != "gitlab" {
			continue
		}
		gate := mobileWorkspaceGate{
			ID: providerStatus.ID, Label: providerStatus.Name, Ready: providerStatus.Ready,
			Configured: providerStatus.Configured, Detail: firstNonEmpty(providerStatus.Detail, providerStatus.Warning),
		}
		if providerStatus.Ready {
			gate.Code = "mobile_workspace.git.ready"
		} else if providerStatus.Configured {
			gate.Code = "mobile_workspace.git.configured"
			gate.Action = &mobileWorkspaceAction{Label: "Repair " + providerStatus.Name, Method: http.MethodPost, Path: "/git/provider/oauth/start"}
		} else {
			gate.Code = "mobile_workspace.git.not_configured"
			gate.Detail = "Not configured on this remote box"
			gate.Action = &mobileWorkspaceAction{Label: "Configure " + providerStatus.Name, Method: http.MethodPost, Path: "/git/provider/oauth/start"}
		}
		status.GitProviders = append(status.GitProviders, gate)
	}
	status.Ready = status.Device.Ready && status.Backend.Ready && anyRunnerReady
	return status
}

func applyMobileWorkspaceOpenCodeConfigFailure(status *mobileWorkspaceStatus, err error) {
	if status == nil || err == nil {
		return
	}
	status.OpenCode.Ready = false
	status.OpenCode.Configured = false
	status.OpenCode.Code = "mobile_workspace.opencode.config_invalid"
	status.OpenCode.Detail = "OpenCode configuration could not be read: " + err.Error()
	status.OpenCode.Action = &mobileWorkspaceAction{Label: "Repair OpenCode", Method: http.MethodPatch, Path: "/runner/opencode/config"}
}

type gitProviderOperationalProbe struct {
	ID     string
	User   string
	Detail string
	Ready  bool
	Absent bool
}

// probeGitProviderOperation attempts the same read-only provider operation the
// project wizard depends on before it later asks gh/glab to create a repo.
// `auth status` and token inventory are proxies; `api user` proves the CLI can
// actually reach the provider with its current credential on this box.
func probeGitProviderOperation(id string) gitProviderOperationalProbe {
	cli := "gh"
	args := []string{"api", "user", "--jq", ".login"}
	if id == "gitlab" {
		cli = "glab"
		args = []string{"api", "user"}
	}
	if _, err := exec.LookPath(cli); err != nil {
		return gitProviderOperationalProbe{ID: id, Absent: true, Detail: cli + " CLI is not installed on this box"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, args...).Output()
	if err != nil {
		if ctx.Err() != nil {
			return gitProviderOperationalProbe{ID: id, Detail: cli + " provider query timed out"}
		}
		return gitProviderOperationalProbe{ID: id, Detail: cli + " could not complete a read-only provider query; reconnect it on this box"}
	}
	user := strings.TrimSpace(string(out))
	if id == "gitlab" {
		var payload struct {
			Username string `json:"username"`
		}
		if json.Unmarshal(out, &payload) == nil {
			user = strings.TrimSpace(payload.Username)
		}
	}
	detail := "Verified with `" + cli + " api user`"
	if user != "" {
		detail += " as " + user
	}
	return gitProviderOperationalProbe{ID: id, User: user, Detail: detail, Ready: true}
}

func applyGitProviderOperationalProbes(status *mobileWorkspaceStatus) {
	if status == nil {
		return
	}
	results := make(chan gitProviderOperationalProbe, 2)
	for _, id := range []string{"github", "gitlab"} {
		go func(provider string) { results <- probeGitProviderOperation(provider) }(id)
	}
	byID := map[string]gitProviderOperationalProbe{}
	for range 2 {
		result := <-results
		byID[result.ID] = result
	}
	applyGitProviderOperationalProbeResults(status, byID)
}

func applyGitProviderOperationalProbeResults(status *mobileWorkspaceStatus, byID map[string]gitProviderOperationalProbe) {
	if status == nil {
		return
	}
	for i := range status.GitProviders {
		gate := &status.GitProviders[i]
		if gate.ID == "yaver-git" {
			if _, err := exec.LookPath("git"); err != nil {
				gate.Ready = false
				gate.Configured = false
				gate.Code = "mobile_workspace.git.not_installed"
				gate.Detail = "Git is not installed on this box"
			} else {
				gate.Detail = "Built in · Git executable verified on this box"
			}
			continue
		}
		result, ok := byID[gate.ID]
		if !ok {
			continue
		}
		gate.Ready = result.Ready
		gate.Configured = gate.Configured || result.Ready
		gate.Detail = result.Detail
		if result.Ready {
			gate.Code = "mobile_workspace.git.ready"
			gate.Action = nil
		} else if result.Absent {
			gate.Code = "mobile_workspace.git.cli_not_installed"
			cli := "gh"
			if gate.ID == "gitlab" {
				cli = "glab"
			}
			gate.Action = &mobileWorkspaceAction{Label: "Install " + cli, Method: http.MethodPost, Path: "/install/" + cli, Stream: installStreamPathForEndpoint("/install/" + cli)}
		} else {
			gate.Code = "mobile_workspace.git.operation_failed"
			gate.Action = &mobileWorkspaceAction{Label: "Connect " + gate.Label, Method: http.MethodPost, Path: "/git/provider/oauth/start"}
		}
	}
}

func (s *HTTPServer) handleMobileWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	runners, err := collectRunnerAuthStatusRows()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "mobile workspace runner probe: "+err.Error())
		return
	}
	openCode, err := loadOpenCodeConfigSummary()
	status := buildMobileWorkspaceStatus(runners, collectMachineOnboardingStatus(), openCode)
	applyMobileWorkspaceOpenCodeConfigFailure(&status, err)
	applyGitProviderOperationalProbes(&status)
	if layout, layoutErr := CollectWorkspaceLayoutStatus(); layoutErr == nil {
		status.Layout = &layout
	}
	jsonReply(w, http.StatusOK, status)
}
