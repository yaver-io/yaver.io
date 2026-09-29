package main

import (
	"errors"
	"strings"
	"testing"
)

func TestMobileWorkspaceStatusCarriesFirstClassRoutes(t *testing.T) {
	status := buildMobileWorkspaceStatus(
		[]runnerAuthStatusRow{
			{ID: "opencode", Name: "OpenCode", Installed: true, Ready: true, AuthConfigured: true, AuthVerified: true, AuthSource: "opencode auth store"},
			{ID: "codex", Name: "Codex", Installed: true},
			{ID: "claude", Name: "Claude Code", Installed: false},
		},
		machineOnboardingStatus{Providers: []machineOnboardingProviderStatus{
			{ID: "github", Name: "GitHub", Configured: true, Ready: true},
			{ID: "gitlab", Name: "GitLab"},
		}},
		OpenCodeConfigSummary{Model: "deepseek/deepseek-flash"},
	)
	if !status.Ready || status.OpenCode.Model != "deepseek/deepseek-flash" || !status.OpenCode.Ready {
		t.Fatalf("unexpected ready status: %+v", status)
	}
	if status.Runners[1].Action == nil || status.Runners[1].Action.Path != "/runner-auth/browser/start" {
		t.Fatalf("Codex auth route missing: %+v", status.Runners[1])
	}
	if status.Runners[2].Action == nil || status.Runners[2].Action.Path != "/install/claude" || status.Runners[2].Action.Stream != "/streams/install:claude" {
		t.Fatalf("Claude install route missing: %+v", status.Runners[2])
	}
	if len(status.GitProviders) != 3 || status.GitProviders[2].Action == nil || status.GitProviders[2].Action.Path != "/git/provider/oauth/start" {
		t.Fatalf("GitLab configure route missing: %+v", status.GitProviders)
	}
}

func TestMobileWorkspaceStatusKeepsFixRouteWhenOpenCodeConfigIsInvalid(t *testing.T) {
	status := buildMobileWorkspaceStatus(nil, machineOnboardingStatus{}, OpenCodeConfigSummary{})
	applyMobileWorkspaceOpenCodeConfigFailure(&status, errors.New("invalid JSON"))
	if status.OpenCode.Ready || status.OpenCode.Code != "mobile_workspace.opencode.config_invalid" {
		t.Fatalf("invalid config produced a false green: %+v", status.OpenCode)
	}
	if status.OpenCode.Action == nil || status.OpenCode.Action.Path != "/runner/opencode/config" {
		t.Fatalf("invalid config lost its repair route: %+v", status.OpenCode)
	}
}

func TestMobileWorkspaceStatusDoesNotTreatInstalledOpenCodeAsConfigured(t *testing.T) {
	status := buildMobileWorkspaceStatus(
		[]runnerAuthStatusRow{{ID: "opencode", Name: "OpenCode", Installed: true, Ready: true, AuthConfigured: false}},
		machineOnboardingStatus{},
		OpenCodeConfigSummary{},
	)
	if status.Ready || status.OpenCode.Ready || status.OpenCode.Action == nil {
		t.Fatalf("installed-only OpenCode produced a false green: %+v", status)
	}
	if got := status.OpenCode.Code; got != "mobile_workspace.opencode.provider_required" {
		t.Fatalf("code = %q", got)
	}
}

func TestMobileWorkspaceGitReadinessUsesOperationsNotInventory(t *testing.T) {
	status := buildMobileWorkspaceStatus(nil, machineOnboardingStatus{Providers: []machineOnboardingProviderStatus{
		{ID: "github", Name: "GitHub", Configured: true, Ready: true},
		{ID: "gitlab", Name: "GitLab", Configured: true, Ready: true},
	}}, OpenCodeConfigSummary{})
	applyGitProviderOperationalProbeResults(&status, map[string]gitProviderOperationalProbe{
		"github": {ID: "github", Ready: true, User: "octocat", Detail: "Verified with `gh api user` as octocat"},
		"gitlab": {ID: "gitlab", Ready: false, Detail: "glab could not complete a read-only provider query"},
	})
	var github, gitlab mobileWorkspaceGate
	for _, gate := range status.GitProviders {
		switch gate.ID {
		case "github":
			github = gate
		case "gitlab":
			gitlab = gate
		}
	}
	if !github.Ready || !strings.Contains(github.Detail, "gh api user") {
		t.Fatalf("GitHub operation did not produce verified readiness: %+v", github)
	}
	if gitlab.Ready || gitlab.Code != "mobile_workspace.git.operation_failed" || gitlab.Action == nil {
		t.Fatalf("failed glab operation left a false green or no repair route: %+v", gitlab)
	}
}
