package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiagnoseTaskFailureClaudeRevokedOAuth(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	task := &Task{
		ID:       "task_1",
		Status:   TaskStatusFailed,
		RunnerID: "claude",
		Model:    "claude-sonnet-4",
		Output:   "Failed to authenticate. API Error: 401 OAuth access token has been revoked.",
	}

	got := diagnoseTaskFailure(task, now)
	if got == nil {
		t.Fatal("diagnosis is nil")
	}
	if got.Kind != "runner_auth" {
		t.Fatalf("kind = %q, want runner_auth", got.Kind)
	}
	if got.Code != "runner.claude.oauth_revoked" {
		t.Fatalf("code = %q, want runner.claude.oauth_revoked", got.Code)
	}
	if got.Fix == nil || got.Fix.Type != "runner_browser_auth" || got.Fix.RunnerID != "claude" || !got.Fix.TestAfter {
		t.Fatalf("fix route missing browser auth: %+v", got.Fix)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "revoked") {
		t.Fatalf("reason does not name revocation: %q", got.Reason)
	}
}

func TestDiagnoseTaskFailureUsesKnownRunnerForGeneric401(t *testing.T) {
	got := diagnoseTaskFailure(&Task{
		ID:       "task_2",
		Status:   TaskStatusFailed,
		RunnerID: "codex",
		Model:    "gpt-5.4",
		Output:   "API Error: 401 Unauthorized",
	}, time.Now())
	if got == nil {
		t.Fatal("diagnosis is nil")
	}
	if got.Code != "runner.codex.auth_required" {
		t.Fatalf("code = %q, want runner.codex.auth_required", got.Code)
	}
	if got.Fix == nil || got.Fix.RunnerID != "codex" {
		t.Fatalf("fix route = %+v, want codex runner auth", got.Fix)
	}
}

func TestDiagnoseOpenCode401UsesProviderConfigNeverBrowserAuth(t *testing.T) {
	got := diagnoseTaskFailure(&Task{
		ID:       "task_opencode_key",
		Status:   TaskStatusFailed,
		RunnerID: "opencode",
		Model:    "deepseek/deepseek-flash",
		Output:   "API Error: 401 Unauthorized",
	}, time.Now())
	if got == nil {
		t.Fatal("diagnosis is nil")
	}
	if got.Code != "runner.opencode.provider_key_rejected" {
		t.Fatalf("code = %q, want provider-key rejection", got.Code)
	}
	if got.Fix == nil || got.Fix.Type != "runner_provider_config" || got.Fix.RunnerID != "opencode" {
		t.Fatalf("fix route = %+v, want OpenCode provider config", got.Fix)
	}
	if strings.Contains(strings.ToLower(got.Remedy), "sign-in flow") || !strings.Contains(strings.ToLower(got.Remedy), "browser sign-in does not apply") {
		t.Fatalf("OpenCode remedy routed toward browser auth: %q", got.Remedy)
	}
}

func TestCodexTaskNeverInheritsClaudeAuthFailureFromItsTranscript(t *testing.T) {
	got := diagnoseTaskFailure(&Task{
		ID:       "task_cross_runner",
		Status:   TaskStatusFailed,
		RunnerID: "codex",
		Model:    "gpt-5.4",
		Output:   strings.Repeat("ordinary Codex work\n", 30) + "Claude Code answered `Please run /login`\n",
	}, time.Now())
	if got == nil {
		t.Fatal("diagnosis is nil")
	}
	if got.Kind == "runner_auth" || got.RunnerID == "claude" {
		t.Fatalf("codex task inherited foreign Claude auth failure: %+v", got)
	}
}

func TestDiagnoseRunnerFailureTextClassifiesModelEntitlement(t *testing.T) {
	got := diagnoseRunnerFailureText(
		"codex",
		"gpt-5.6-sol",
		"subprocess",
		`ERROR: {"status":400,"error":{"message":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}}`,
		time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
	)
	if got == nil {
		t.Fatal("diagnosis is nil")
	}
	if got.Code != "runner.model.not_supported" {
		t.Fatalf("code = %q, want runner.model.not_supported", got.Code)
	}
	if got.Model != "gpt-5.6-sol" {
		t.Fatalf("model = %q, want gpt-5.6-sol", got.Model)
	}
	if got.Fix == nil || got.Fix.Type != "runner_test" || got.Fix.RunnerID != "codex" {
		t.Fatalf("fix route = %+v, want runner_test for codex", got.Fix)
	}
}

func TestRunnerTestResultCarriesStructuredFailure(t *testing.T) {
	info := runnerTestResult{
		OK:     false,
		Runner: "codex",
		Probe:  "subprocess",
		Model:  "gpt-5.6-sol",
		Failure: &TaskFailureDiagnosis{
			Kind:       "runner_model",
			Code:       "runner.model.not_supported",
			Title:      "Selected model is rejected by the account",
			Reason:     "Codex reached the provider, but the account cannot use the configured model.",
			Remedy:     "Switch to a model your subscription supports, or sign in with the account that owns that model entitlement.",
			RunnerID:   "codex",
			Model:      "gpt-5.6-sol",
			Probe:      "subprocess",
			DetectedAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
			Fix:        &TaskFailureFix{Type: "runner_test", RunnerID: "codex", TestAfter: true},
		},
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{`"failure"`, `"code":"runner.model.not_supported"`, `"runnerId":"codex"`, `"model":"gpt-5.6-sol"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("runnerTestResult JSON missing %s: %s", want, body)
		}
	}
}

func TestTaskInfoCarriesStructuredFailure(t *testing.T) {
	info := TaskInfo{
		ID:     "task_3",
		Status: TaskStatusFailed,
		Failure: &TaskFailureDiagnosis{
			Kind:       "runner_auth",
			Code:       "runner.claude.oauth_revoked",
			Title:      "Runner OAuth grant was revoked",
			Reason:     "Claude Code's OAuth access token has been revoked.",
			Remedy:     "Start the runner sign-in flow from this task, then run Test before retrying.",
			RunnerID:   "claude",
			Model:      "claude-sonnet-4",
			Probe:      "subprocess",
			DetectedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			Fix:        &TaskFailureFix{Type: "runner_browser_auth", RunnerID: "claude", TestAfter: true},
		},
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{`"failure"`, `"code":"runner.claude.oauth_revoked"`, `"type":"runner_browser_auth"`, `"testAfter":true`} {
		if !strings.Contains(body, want) {
			t.Fatalf("TaskInfo JSON missing %s: %s", want, body)
		}
	}
}
