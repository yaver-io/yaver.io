package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlayUploadUsesProjectHelperAndExactArtifact(t *testing.T) {
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	aab := filepath.Join(dir, "customer.aab")
	if err := os.WriteFile(aab, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(scripts, "run-playstore-upload.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$AAB_PATH\" = \""+aab+"\" ] && [ \"$PLAY_PACKAGE_NAME\" = \"com.acme.app\" ]\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := uploadToPlayStoreWithEnv(aab, dir, "third-party", map[string]string{
		"PLAY_STORE_KEY_FILE": "/project/acme-play.json",
		"PLAY_PACKAGE_NAME":   "com.acme.app",
	})
	if err != nil {
		t.Fatalf("project-scoped upload helper failed: %v", err)
	}
}

func TestPlayUploadNamedProjectDoesNotInheritProcessCredential(t *testing.T) {
	dir := t.TempDir()
	aab := filepath.Join(dir, "customer.aab")
	if err := os.WriteFile(aab, []byte("bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLAY_STORE_KEY_FILE", "/operator/yaver-play.json")
	err := uploadToPlayStoreWithEnv(aab, dir, "third-party", nil)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("named project must fail closed instead of inheriting process credentials, got %v", err)
	}
}
