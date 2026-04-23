package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostShareWorkspaceBootstrapFromDir(t *testing.T) {
	cfgDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	t.Setenv("HOME", cfgDir)
	if oldHome != "" {
		t.Setenv("USERPROFILE", cfgDir)
	}

	source := filepath.Join(cfgDir, "source")
	if err := os.MkdirAll(filepath.Join(source, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "subdir", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(source, ".yaver"), 0o755); err != nil {
		t.Fatalf("mkdir hidden dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, ".yaver", "secret.txt"), []byte("skip"), 0o644); err != nil {
		t.Fatalf("write hidden file: %v", err)
	}

	mgr, err := NewHostShareWorkspaceManager()
	if err != nil {
		t.Fatalf("NewHostShareWorkspaceManager: %v", err)
	}
	ws, err := mgr.BootstrapFromDir("sess_123", source)
	if err != nil {
		t.Fatalf("BootstrapFromDir: %v", err)
	}
	if ws.State != "ready" {
		t.Fatalf("state = %q, want ready", ws.State)
	}
	if _, err := os.Stat(filepath.Join(ws.RepoDir, "README.md")); err != nil {
		t.Fatalf("expected README in workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.RepoDir, "subdir", "main.go")); err != nil {
		t.Fatalf("expected nested file in workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.RepoDir, ".yaver", "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected .yaver to be skipped, got err=%v", err)
	}
}
