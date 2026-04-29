package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindMobileProjectByBundleID_ResolvesExpoConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"sfmg","dependencies":{"expo":"~52.0.0"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte("{\n  \"expo\": {\n    \"name\": \"sfmg\",\n    \"ios\": {\n      \"bundleIdentifier\": \"com.sfmg.app\"\n    }\n  }\n}\n"), 0o644); err != nil {
		t.Fatalf("write app.json: %v", err)
	}

	mobileProjectCache.mu.Lock()
	prevProjects := mobileProjectCache.projects
	prevScannedAt := mobileProjectCache.scannedAt
	mobileProjectCache.projects = []MobileProject{{
		Name:      "sfmg",
		Path:      dir,
		Framework: "expo",
	}}
	mobileProjectCache.scannedAt = time.Now()
	mobileProjectCache.mu.Unlock()
	defer func() {
		mobileProjectCache.mu.Lock()
		mobileProjectCache.projects = prevProjects
		mobileProjectCache.scannedAt = prevScannedAt
		mobileProjectCache.mu.Unlock()
	}()

	got := findMobileProjectByBundleID("com.sfmg.app")
	if got == nil {
		t.Fatal("findMobileProjectByBundleID returned nil")
	}
	if got.Path != dir {
		t.Fatalf("findMobileProjectByBundleID path = %q, want %q", got.Path, dir)
	}
	if got.Name != "sfmg" {
		t.Fatalf("findMobileProjectByBundleID name = %q, want sfmg", got.Name)
	}
}
