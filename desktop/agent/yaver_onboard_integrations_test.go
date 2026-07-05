package main

import (
	"strings"
	"testing"
)

func TestYaverInitialIntegrationChecklistCoversCoreProviders(t *testing.T) {
	items := yaverInitialIntegrationChecklist(&Config{}, machineOnboardingStatus{})
	var labels []string
	for _, item := range items {
		labels = append(labels, item.Label)
		if item.Enables == "" {
			t.Fatalf("%s missing enabled verbs", item.Label)
		}
	}
	joined := strings.Join(labels, "\n")
	for _, want := range []string{"Google", "Microsoft", "Zoom", "GitHub", "GitLab"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s integration in checklist: %s", want, joined)
		}
	}
}

func TestYaverInitialIntegrationChecklistReadiness(t *testing.T) {
	status := machineOnboardingStatus{Providers: []machineOnboardingProviderStatus{
		{ID: "github", Configured: true},
		{ID: "gitlab", CloneReady: true},
	}}
	cfg := &Config{Email: &EmailConfig{GoogleRefreshToken: "refresh", Provider: "office365", AzureTenantID: "tenant"}}
	items := yaverInitialIntegrationChecklist(cfg, status)
	for _, item := range items {
		if !item.Done {
			t.Fatalf("expected %q ready, got not done", item.Label)
		}
	}
}

func TestYaverOnboardChecklistIncludesIntegrationSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAVER_VAULT_SKIP_KEYCHAIN", "1")
	out := yaverOnboardChecklist()
	for _, want := range []string{"Integrations for MCP / car / watch / TV", "Google connected", "GitHub connected", "GitLab connected"} {
		if !strings.Contains(out, want) {
			t.Fatalf("checklist missing %q:\n%s", want, out)
		}
	}
}
