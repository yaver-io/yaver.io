package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SaveConfig must not silently wipe an on-disk auth_token when the
// in-memory cfg has an empty one. Caused real user harm: a `cfg =
// &Config{}` fallback after a transient LoadConfig miss saved an
// empty token over a valid session, forcing an unexpected re-auth.
func TestSaveConfig_PreservesAuthTokenWhenInMemoryEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := setupYaverDir(dir); err != nil {
		t.Fatal(err)
	}

	// Seed a real config with a non-empty token.
	original := &Config{
		AuthToken:     "valid-existing-token",
		DeviceID:      "device-abc",
		ConvexSiteURL: "https://convex.example",
	}
	if err := SaveConfig(original); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// Pretend a buggy caller built a fresh Config{} and tried to save
	// it (this is the historical wipe pattern).
	bug := &Config{
		DeviceID:      "device-abc",
		ConvexSiteURL: "https://convex.example",
	}
	if err := SaveConfig(bug); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if strings.TrimSpace(got.AuthToken) != "valid-existing-token" {
		t.Fatalf("auth token wiped: got %q, want preserved", got.AuthToken)
	}
}

// SaveConfigClearingAuth is the explicit opt-in for sign-out paths.
// It must not be guarded — clearing has to actually clear.
func TestSaveConfigClearingAuth_DoesClear(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := setupYaverDir(dir); err != nil {
		t.Fatal(err)
	}

	if err := SaveConfig(&Config{AuthToken: "tok", DeviceID: "d"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.AuthToken = ""
	if err := SaveConfigClearingAuth(cfg); err != nil {
		t.Fatalf("clearing save: %v", err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if strings.TrimSpace(got.AuthToken) != "" {
		t.Fatalf("expected token cleared, got %q", got.AuthToken)
	}
	if strings.TrimSpace(got.DeviceID) != "d" {
		t.Fatalf("expected DeviceID preserved, got %q", got.DeviceID)
	}
}

func setupYaverDir(home string) error {
	cfgPath, err := ConfigPath()
	if err != nil {
		return err
	}
	return os.MkdirAll(filepath.Dir(cfgPath), 0o700)
}
