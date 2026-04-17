package main

// wipe_cmd_test.go — unit coverage for `yaver wipe`. Calls runWipe
// directly against a fake HOME tree so we never touch the dev's real
// ~/.yaver.

import (
	"os"
	"path/filepath"
	"testing"
)

// seedFakeHome builds a ~/.yaver layout that matches what a real
// agent produces, inside a tempdir. Returns the tempdir itself.
func seedFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, ".yaver")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Non-wipeable by default — auth stays unless --including-auth.
	mustWrite(t, filepath.Join(base, "config.json"), `{"authToken":"abc"}`)
	mustWrite(t, filepath.Join(base, "device.key"), "key-bytes")
	// Wipe targets.
	mustWrite(t, filepath.Join(base, "vault.enc"), "encrypted")
	mustWrite(t, filepath.Join(base, "vault.enc.bak"), "encrypted")
	if err := os.MkdirAll(filepath.Join(base, "apikeys"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "apikeys", "registry.json"), `{"keys":{}}`)
	if err := os.MkdirAll(filepath.Join(base, "tasks"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "tasks", "x.json"), `{}`)
	if err := os.MkdirAll(filepath.Join(base, "blobs", "bkt"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(base, "blobs", "bkt", "a.txt"), "hi")
	mustWrite(t, filepath.Join(base, "agent.log"), "log")
	return home
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func TestWipeVaultOnly(t *testing.T) {
	home := seedFakeHome(t)
	t.Setenv("HOME", home)

	runWipe([]string{"vault", "--yes"})

	base := filepath.Join(home, ".yaver")
	// vault gone.
	if exists(t, filepath.Join(base, "vault.enc")) {
		t.Error("vault.enc should be gone")
	}
	if exists(t, filepath.Join(base, "vault.enc.bak")) {
		t.Error("vault.enc.bak should be gone")
	}
	// Auth + other targets intact.
	for _, p := range []string{"config.json", "device.key", "agent.log", "apikeys/registry.json", "tasks/x.json", "blobs/bkt/a.txt"} {
		if !exists(t, filepath.Join(base, p)) {
			t.Errorf("%s got unexpectedly wiped", p)
		}
	}
}

func TestWipeAllKeepsAuth(t *testing.T) {
	home := seedFakeHome(t)
	t.Setenv("HOME", home)

	runWipe([]string{"all", "--yes"})

	base := filepath.Join(home, ".yaver")
	// config + device.key still there.
	if !exists(t, filepath.Join(base, "config.json")) {
		t.Error("config.json should survive `wipe all`")
	}
	if !exists(t, filepath.Join(base, "device.key")) {
		t.Error("device.key should survive `wipe all`")
	}
	// Everything else wiped.
	for _, p := range []string{"vault.enc", "apikeys/registry.json", "tasks/x.json", "blobs/bkt/a.txt", "agent.log"} {
		if exists(t, filepath.Join(base, p)) {
			t.Errorf("%s should have been wiped by `wipe all`", p)
		}
	}
}

func TestWipeIncludingAuthWipesEverything(t *testing.T) {
	home := seedFakeHome(t)
	t.Setenv("HOME", home)

	runWipe([]string{"all", "--yes", "--including-auth"})

	base := filepath.Join(home, ".yaver")
	entries, err := os.ReadDir(base)
	if err != nil {
		// Directory itself may not exist after a full wipe, which is
		// fine too.
		return
	}
	if len(entries) > 0 {
		t.Errorf("~/.yaver should be empty after wipe --including-auth, got %d entries", len(entries))
	}
}
