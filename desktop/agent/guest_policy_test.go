package main

import (
	"strings"
	"testing"
)

func TestGuestResourcePresetInference(t *testing.T) {
	trueVal := true

	cfg := &GuestConfig{ResourcePreset: "desktop-control-with-host-keys"}
	if got := guestResourcePreset(cfg); got != "desktop-control-with-host-keys" {
		t.Fatalf("expected explicit preset to win, got %q", got)
	}
	if !guestUseHostAPIKeys(cfg) {
		t.Fatal("desktop-control-with-host-keys should imply host keys")
	}
	if !guestAllowDesktopControl(cfg) {
		t.Fatal("desktop-control-with-host-keys should imply desktop control")
	}

	cfg = &GuestConfig{UseHostAPIKeys: &trueVal}
	if got := guestResourcePreset(cfg); got != "machine-with-host-keys" {
		t.Fatalf("expected inferred machine-with-host-keys, got %q", got)
	}

	cfg = &GuestConfig{AllowDesktopControl: &trueVal}
	if got := guestResourcePreset(cfg); got != "desktop-control" {
		t.Fatalf("expected inferred desktop-control, got %q", got)
	}
	if !guestAllowBrowserControl(cfg) {
		t.Fatal("desktop control should imply browser control by default")
	}
}

func TestGuestPromptPrefixIncludesResourcePolicies(t *testing.T) {
	trueVal := true
	prefix := guestPromptPrefix("/repo/app", &GuestConfig{
		ResourcePreset:     "desktop-control",
		AllowTunnelForward: &trueVal,
	})
	for _, needle := range []string{
		"Share preset for this guest: desktop-control",
		"Desktop-control sessions may be created only when the host explicitly initiates or approves them.",
		"Browser automation is approved only for the host-approved session scope",
		"Tunnel forwarding is approved only for the exact host-approved endpoints needed for the task.",
	} {
		if !strings.Contains(prefix, needle) {
			t.Fatalf("expected prompt to include %q, got:\n%s", needle, prefix)
		}
	}
}

func TestCollectAPIKeysForGuestBlocksHostKeysByDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "host-openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "host-anthropic-key")

	task := &Task{
		GuestUserID:         "guest-1",
		GuestUseHostAPIKeys: false,
	}

	keys := CollectAPIKeysForTask(task)
	if len(keys) != 0 {
		t.Fatalf("expected no host keys for guest task, got %#v", keys)
	}
}

func TestTaskEnvStripsSharedSecretEnvForGuests(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "host-openai-key")
	t.Setenv("GH_TOKEN", "host-gh-token")

	env := taskEnv(&Task{
		GuestUserID:         "guest-1",
		GuestUseHostAPIKeys: false,
	})

	for _, entry := range env {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") || strings.HasPrefix(entry, "GH_TOKEN=") {
			t.Fatalf("guest environment leaked host secret: %s", entry)
		}
	}
}

func TestTaskEnvKeepsHostKeysWhenExplicitlyAllowed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "host-openai-key")

	env := taskEnv(&Task{
		GuestUserID:         "guest-1",
		GuestUseHostAPIKeys: true,
	})

	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected guest environment to include host key when explicitly allowed")
	}
}

func TestTaskEnvAddsVaultBackedHostKeysForOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "OPENAI_API_KEY", Category: "api-key", Value: "vault-openai-key"}); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	setRuntimeVaultStore(vs)
	defer setRuntimeVaultStore(nil)

	env := taskEnv(&Task{})
	for _, entry := range env {
		if entry == "OPENAI_API_KEY=vault-openai-key" {
			return
		}
	}
	t.Fatal("expected owner environment to include vault-backed OPENAI_API_KEY")
}
