package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMobileOverviewRunnerIsEmbeddedAndHeadless(t *testing.T) {
	script := string(mobileUIOverviewScript)
	for _, want := range []string{
		"devices['iPhone 15 Pro']",
		"headless: true",
		"physicalAccessRequired: false",
		"manifest.json",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("embedded mobile overview runner is missing %q", want)
		}
	}
	for _, forbidden := range []string{"/Users/", "/home/", "headless: false"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("embedded mobile overview runner contains host-specific or headed fallback %q", forbidden)
		}
	}
}

func TestMobileOverviewArtifactCleanupIsScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".yaver", "artifacts", "mobile-overview")
	oldRun := filepath.Join(root, "run-old")
	recentRun := filepath.Join(root, "run-recent")
	outside := filepath.Join(home, "keep-source")
	for _, dir := range []string{oldRun, recentRun, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldRun, old, old); err != nil {
		t.Fatal(err)
	}

	created, err := newMobileOverviewArtifactDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created, root+string(os.PathSeparator)) {
		t.Fatalf("artifact dir escaped owned root: %s", created)
	}
	if _, err := os.Stat(oldRun); !os.IsNotExist(err) {
		t.Fatalf("old owned run was not reaped: %v", err)
	}
	for _, kept := range []string{recentRun, outside} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("cleanup touched %s: %v", kept, err)
		}
	}
}
