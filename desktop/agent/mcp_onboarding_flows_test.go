package main

import "testing"

func TestYaverIntegrationOnboardingGuideCoversCoreProviders(t *testing.T) {
	guide := yaverIntegrationOnboardingGuide("managed-cloud")
	providers, ok := guide["providers"].([]map[string]interface{})
	if !ok {
		t.Fatalf("providers type = %T", guide["providers"])
	}
	seen := map[string]bool{}
	for _, p := range providers {
		if id, _ := p["id"].(string); id != "" {
			seen[id] = true
		}
	}
	for _, id := range []string{"google", "microsoft", "zoom", "github", "gitlab"} {
		if !seen[id] {
			t.Fatalf("missing provider %s in integration guide", id)
		}
	}
}
