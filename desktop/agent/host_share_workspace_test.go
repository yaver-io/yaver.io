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

// newTestWorkspaceMgr builds a manager rooted in a temp dir so the
// removable-allocation tests never touch the real ~/.yaver config dir.
func newTestWorkspaceMgr(t *testing.T) *HostShareWorkspaceManager {
	t.Helper()
	base := filepath.Join(t.TempDir(), "host-share", "workspaces")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	return &HostShareWorkspaceManager{baseDir: base}
}

func TestDeleteWorkspaceRemovesTree(t *testing.T) {
	m := newTestWorkspaceMgr(t)
	ws, err := m.EnsureWorkspace("sess-A")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	secret := filepath.Join(ws.RepoDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("tenant data"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(ws.RootDir); err != nil {
		t.Fatalf("root should exist pre-delete: %v", err)
	}
	if err := m.DeleteWorkspace("sess-A"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(ws.RootDir); !os.IsNotExist(err) {
		t.Fatalf("workspace tree must be gone after DeleteWorkspace, got err=%v", err)
	}
	// Idempotent.
	if err := m.DeleteWorkspace("sess-A"); err != nil {
		t.Fatalf("delete (idempotent) should not error: %v", err)
	}
}

func TestReapExceptKeepsActiveWipesRest(t *testing.T) {
	m := newTestWorkspaceMgr(t)
	for _, id := range []string{"keep-1", "kill-1", "kill-2"} {
		if _, err := m.EnsureWorkspace(id); err != nil {
			t.Fatalf("ensure %s: %v", id, err)
		}
	}
	keep := map[string]bool{m.SanitizeSessionID("keep-1"): true}
	removed, err := m.ReapExcept(keep)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected 2 removed, got %d (%v)", len(removed), removed)
	}
	if ws, _ := m.GetWorkspace("keep-1"); ws == nil {
		t.Fatalf("keep-1 must survive reap")
	}
	for _, id := range []string{"kill-1", "kill-2"} {
		root := filepath.Join(m.baseDir, m.SanitizeSessionID(id))
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("%s must be wiped, got err=%v", id, err)
		}
	}
}

func TestReapExceptEmptyKeepWipesAll(t *testing.T) {
	m := newTestWorkspaceMgr(t)
	for _, id := range []string{"a", "b"} {
		if _, err := m.EnsureWorkspace(id); err != nil {
			t.Fatalf("ensure %s: %v", id, err)
		}
	}
	removed, err := m.ReapExcept(map[string]bool{})
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("expected all wiped, got %d", len(removed))
	}
	names, _ := m.listSessionDirsLocked()
	if len(names) != 0 {
		t.Fatalf("base dir should be empty, found %v", names)
	}
}

// killHostShareSession must match only the requested host-share id and
// leave plain (non-host-share) terminals untouched.
func TestKillHostShareSessionSelectsByID(t *testing.T) {
	s := &HTTPServer{}
	mk := func(hsID string) *terminalSession {
		// closed:true so close() short-circuits without a real pty/proc.
		return &terminalSession{id: hsID + "-t", hostShareID: hsID, srv: s, closed: true}
	}
	tX := mk("sess-X")
	tY := mk("sess-Y")
	tPlain := &terminalSession{id: "plain-t", srv: s, closed: true}
	s.terminalSessions.Store(tX.id, tX)
	s.terminalSessions.Store(tY.id, tY)
	s.terminalSessions.Store(tPlain.id, tPlain)

	if n := s.killHostShareSession("sess-X"); n != 1 {
		t.Fatalf("expected 1 victim for sess-X, got %d", n)
	}
	if n := s.killHostShareSession("nope"); n != 0 {
		t.Fatalf("expected 0 victims for unknown id, got %d", n)
	}
}
