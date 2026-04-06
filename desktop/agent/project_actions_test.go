package main

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot finds the yaver.io repo root by walking up from the test file's directory
// looking for CLAUDE.md (or README.md as fallback).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Skipf("cannot get working directory: %v", err)
	}
	// Walk up from desktop/agent/ to find repo root
	for {
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "desktop", "agent")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("cannot find yaver.io repo root — skipping")
		}
		dir = parent
	}
}

func TestDetectProjectActions_YaverRepo(t *testing.T) {
	root := repoRoot(t)
	actions := DetectProjectActions(root)
	if len(actions) == 0 {
		t.Fatal("expected actions for yaver.io repo")
	}

	// Should find: mobile (expo), web (nextjs), backend (convex), relay (go/docker), desktop (go)
	types := map[string]bool{}
	for _, a := range actions {
		types[a.Framework+"/"+a.Platform] = true
		t.Logf("  %s [%s] %s → %s", a.Icon, a.Type, a.Label, a.Target)
	}

	if !types["expo/"] {
		t.Error("expected expo action")
	}
	if !types["nextjs/vercel"] {
		t.Error("expected nextjs/vercel action")
	}
	if !types["/convex"] {
		t.Error("expected convex action")
	}
}

func TestDetectProjectActions_AcmeStore(t *testing.T) {
	root := repoRoot(t)
	acmeStore := filepath.Join(root, "demo", "AcmeStore")
	if _, err := os.Stat(acmeStore); os.IsNotExist(err) {
		t.Skip("demo/AcmeStore not present — skipping")
	}
	actions := DetectProjectActions(acmeStore)
	if len(actions) == 0 {
		t.Fatal("expected actions for AcmeStore")
	}
	hasHotReload := false
	for _, a := range actions {
		if a.Type == "dev-server" && a.Framework == "expo" {
			hasHotReload = true
		}
		t.Logf("  %s [%s] %s", a.Icon, a.Type, a.Label)
	}
	if !hasHotReload {
		t.Error("expected expo hot reload action")
	}
}
