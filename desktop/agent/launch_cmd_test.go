package main

import "testing"

func TestLaunchProviderSelectionIsAutomaticByDefault(t *testing.T) {
	t.Setenv("YAVER_OPERATOR_PROVIDER_OVERRIDE", "")
	t.Setenv("YAVER_LAUNCH_PROVIDER_OVERRIDE", "")

	if !isExplicitCloudProvider("aws") || !isExplicitCloudProvider("gcp") || !isExplicitCloudProvider("azure") || !isExplicitCloudProvider("hetzner") {
		t.Fatal("all concrete cloud providers must be classified as explicit provider overrides")
	}
	if isExplicitCloudProvider("cloud") {
		t.Fatal("cloud is the automatic end-user launch path, not a provider override")
	}
	if operatorProviderOverrideEnabled() {
		t.Fatal("operator provider override must be disabled by default")
	}
}

func TestLaunchProviderOverrideRequiresOperatorEnv(t *testing.T) {
	t.Setenv("YAVER_OPERATOR_PROVIDER_OVERRIDE", "1")
	t.Setenv("YAVER_LAUNCH_PROVIDER_OVERRIDE", "")

	if !operatorProviderOverrideEnabled() {
		t.Fatal("operator override env should enable concrete provider launch for development")
	}
}
