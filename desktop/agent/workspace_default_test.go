package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultWorkspaceDir_HomeSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("DefaultWorkspaceDir: %v", err)
	}
	want := filepath.Join(home, "Workspace")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestDefaultWorkspaceDir_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// First call creates
	dir1, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Drop a sentinel file inside
	sentinel := filepath.Join(dir1, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second call must NOT clobber
	dir2, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if dir1 != dir2 {
		t.Errorf("dirs differ: %q vs %q", dir1, dir2)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("idempotent call wiped existing contents: %v", err)
	}
}

func TestResolveWorkspaceParent_Provided(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	custom := filepath.Join(t.TempDir(), "my-custom-path")
	if got := ResolveWorkspaceParent(custom); got != custom {
		t.Errorf("expected provided path verbatim, got %q", got)
	}
	// Whitespace trim
	if got := ResolveWorkspaceParent("  " + custom + "  "); got != custom {
		t.Errorf("whitespace not trimmed: got %q", got)
	}
}

func TestResolveWorkspaceParent_DefaultsToWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := ResolveWorkspaceParent("")
	want := filepath.Join(home, "Workspace")
	if got != want {
		t.Errorf("default mismatch: got %q, want %q", got, want)
	}
}

func TestResolveWorkspaceParent_NoHomeFallsBackToCwd(t *testing.T) {
	// Force HOME empty AND no /workspace
	t.Setenv("HOME", "")
	// We can't easily test the /workspace branch without root, so
	// just verify the function returns SOMETHING absolute and
	// doesn't panic.
	got := ResolveWorkspaceParent("")
	if got == "" {
		t.Error("ResolveWorkspaceParent returned empty string")
	}
	if !strings.HasPrefix(got, "/") {
		t.Errorf("expected absolute path fallback, got %q", got)
	}
}

func TestDefaultWorkspaceDirName_IsCapitalW(t *testing.T) {
	// Lock the spelling: kivanc's macOS Finder shows "Workspace"
	// (capital W). Changing this to lowercase would silently break
	// his existing layout — fail loudly if anyone changes it without
	// updating downstream callers.
	if DefaultWorkspaceDirName != "Workspace" {
		t.Errorf("DefaultWorkspaceDirName changed: %q — verify all callers + docs", DefaultWorkspaceDirName)
	}
}
