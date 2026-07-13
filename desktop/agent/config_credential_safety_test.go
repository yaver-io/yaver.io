package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A failed READ must never cause a destructive WRITE.
//
// Regression (2026-07-13): a managed box came up unable to resolve its config,
// treated that as "no config", minted a fresh device_id and saved — overwriting
// a file that held a live auth_token, relay_password and convex_site_url. The
// box lost its sign-in AND its identity: it re-registered as a brand-new device
// while the owner's primary pointer still named the old one, so a perfectly
// healthy machine reported "no device responded". It had to be re-authorized by
// hand.
func TestSaveConfig_NeverDestroysCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // windows
	cfgDir := filepath.Join(dir, configDirName)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.json")

	// A fully provisioned box.
	if err := SaveConfig(&Config{
		AuthToken:     "live-token",
		DeviceID:      "cloud-mn71me24",
		RelayPassword: "live-relay-pw",
		ConvexSiteURL: "https://example.convex.site",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("a blank save must not wipe credentials", func(t *testing.T) {
		// Exactly what the bootstrap path did: it thought there was no config.
		if err := SaveConfig(&Config{DeviceID: "a-fresh-random-uuid"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if got.AuthToken != "live-token" {
			t.Errorf("auth_token destroyed: got %q, want it preserved", got.AuthToken)
		}
		if got.RelayPassword != "live-relay-pw" {
			t.Errorf("relay_password destroyed: got %q", got.RelayPassword)
		}
		if got.ConvexSiteURL != "https://example.convex.site" {
			t.Errorf("convex_site_url destroyed: got %q", got.ConvexSiteURL)
		}
	})

	t.Run("an unreadable config must not be overwritten", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := SaveConfig(&Config{DeviceID: "another-uuid"})
		if err == nil {
			t.Fatal("SaveConfig overwrote a config it could not read — that is how credentials get destroyed")
		}
		// The original bytes must still be on disk, untouched.
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if string(raw) != "{ this is not json" {
			t.Errorf("refused write still clobbered the file: %q", raw)
		}
	})

	t.Run("a deliberate wipe still works", func(t *testing.T) {
		if err := os.WriteFile(path, []byte(`{"auth_token":"live-token"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveConfigClearingAuth(&Config{}); err != nil {
			t.Fatalf("logout must still be able to clear credentials: %v", err)
		}
		got, err := LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		if got.AuthToken != "" {
			t.Errorf("logout failed to clear the token: %q", got.AuthToken)
		}
	})
}
