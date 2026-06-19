package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setupMachineOnboardingTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_VAULT_PASSPHRASE", "test-vault-passphrase")
	t.Setenv("YAVER_VAULT_SKIP_KEYCHAIN", "1")
	t.Setenv("YAVER_VAULT_USER_ID", "test-user")
	cfg := &Config{AuthToken: "test-auth-token"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
}

func TestGitLabVaultEntryOptionalPrefersHostSpecificKey(t *testing.T) {
	setupMachineOnboardingTestEnv(t)

	if _, err := setGitLabVaultEntry("code.example.com", "glpat-test", "custom host"); err != nil {
		t.Fatalf("setGitLabVaultEntry: %v", err)
	}

	entry, key, err := loadGitLabVaultEntryOptional("code.example.com")
	if err != nil {
		t.Fatalf("loadGitLabVaultEntryOptional: %v", err)
	}
	if entry == nil || entry.Value != "glpat-test" {
		t.Fatalf("expected custom GitLab token, got %#v", entry)
	}
	if key != "gitlab-token.code.example.com" {
		t.Fatalf("unexpected vault key %q", key)
	}
}

func TestCollectMachineOnboardingStatusUsesHostScopedGitLabVault(t *testing.T) {
	setupMachineOnboardingTestEnv(t)

	if err := upsertGitCredential("code.example.com", "alice", "clone-token"); err != nil {
		t.Fatalf("upsertGitCredential: %v", err)
	}
	if err := upsertGitProvider("code.example.com", "gitlab", "alice", "", "clone-token"); err != nil {
		t.Fatalf("upsertGitProvider: %v", err)
	}
	if _, err := setGitLabVaultEntry("code.example.com", "ci-token", "custom host"); err != nil {
		t.Fatalf("setGitLabVaultEntry: %v", err)
	}

	status := collectMachineOnboardingStatus()
	var gitlab *machineOnboardingProviderStatus
	for i := range status.Providers {
		if status.Providers[i].ID == "gitlab" {
			gitlab = &status.Providers[i]
			break
		}
	}
	if gitlab == nil {
		t.Fatal("missing gitlab status")
	}
	if gitlab.Host != "code.example.com" {
		t.Fatalf("expected custom host, got %q", gitlab.Host)
	}
	if !gitlab.CIReady || gitlab.CISource != "vault:gitlab-token.code.example.com" {
		t.Fatalf("expected host-scoped CI token, got ready=%v source=%q", gitlab.CIReady, gitlab.CISource)
	}
	if !gitlab.Ready {
		t.Fatal("expected gitlab status to be fully ready")
	}
}

func TestApplyMachineOnboardingRemoveLocalRemovesAllGitLabVaultKeysWithoutHost(t *testing.T) {
	setupMachineOnboardingTestEnv(t)

	if err := upsertGitCredential("gitlab.com", "alice", "clone-token"); err != nil {
		t.Fatalf("upsertGitCredential gitlab.com: %v", err)
	}
	if err := upsertGitCredential("code.example.com", "alice", "clone-token"); err != nil {
		t.Fatalf("upsertGitCredential custom host: %v", err)
	}
	if err := upsertGitProvider("gitlab.com", "gitlab", "alice", "", "clone-token"); err != nil {
		t.Fatalf("upsertGitProvider gitlab.com: %v", err)
	}
	if err := upsertGitProvider("code.example.com", "gitlab", "alice", "", "clone-token"); err != nil {
		t.Fatalf("upsertGitProvider custom host: %v", err)
	}
	if _, err := setGitLabVaultEntry("gitlab.com", "ci-default", "default host"); err != nil {
		t.Fatalf("setGitLabVaultEntry gitlab.com: %v", err)
	}
	if _, err := setGitLabVaultEntry("code.example.com", "ci-custom", "custom host"); err != nil {
		t.Fatalf("setGitLabVaultEntry custom host: %v", err)
	}

	result, err := applyMachineOnboardingRemoveLocal(machineOnboardingRemoveRequest{
		Providers:     []string{"gitlab"},
		RemoveClone:   boolPtr(true),
		RemoveCIToken: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("applyMachineOnboardingRemoveLocal: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("expected ok result, got %#v", result)
	}
	if cred := findCredentialForHost("gitlab.com"); cred != nil {
		t.Fatalf("expected gitlab.com credential removed, got %#v", cred)
	}
	if cred := findCredentialForHost("code.example.com"); cred != nil {
		t.Fatalf("expected custom credential removed, got %#v", cred)
	}
	if provider := findProvider("gitlab.com"); provider != nil {
		t.Fatalf("expected gitlab.com provider removed, got %#v", provider)
	}
	if provider := findProvider("code.example.com"); provider != nil {
		t.Fatalf("expected custom provider removed, got %#v", provider)
	}
	keys, err := listGitLabVaultKeysOptional()
	if err != nil {
		t.Fatalf("listGitLabVaultKeysOptional: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected all GitLab vault keys removed, got %v", keys)
	}
}

func TestGitLabVaultKeySanitizesHost(t *testing.T) {
	setupMachineOnboardingTestEnv(t)

	key := gitLabVaultKey("GitLab.Custom Host:8443")
	if key != "gitlab-token.gitlab.custom-host-8443" {
		t.Fatalf("unexpected sanitized key %q", key)
	}
	path, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "config.json")); err != nil {
		t.Fatalf("expected config.json to exist: %v", err)
	}
}
