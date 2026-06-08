package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultWorkspaceDirHonorsEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom-ws")
	t.Setenv("YAVER_WORKSPACE_DIR", dir)
	got, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("DefaultWorkspaceDir: %v", err)
	}
	if got != dir {
		t.Fatalf("env override: got %q want %q", got, dir)
	}
}

func TestDefaultWorkspaceDirDefaultsToHomeWorkspace(t *testing.T) {
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultWorkspaceDir()
	if err != nil {
		t.Fatalf("DefaultWorkspaceDir: %v", err)
	}
	if got != filepath.Join(home, "Workspace") {
		t.Fatalf("default: got %q want %q", got, filepath.Join(home, "Workspace"))
	}
}

func TestTenantUserNameSafeAndDeterministic(t *testing.T) {
	a := tenantUserName("jh761gbn7z5f3xtxae2dzebn4n8898xe")
	b := tenantUserName("jh761gbn7z5f3xtxae2dzebn4n8898xe")
	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "yv-") {
		t.Fatalf("missing yv- prefix: %q", a)
	}
	// Only safe chars (yv- + lowercase alnum).
	for _, r := range strings.TrimPrefix(a, "yv-") {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !ok {
			t.Fatalf("unsafe char %q in %q", r, a)
		}
	}
	// Different tenants → different names → non-overlapping dirs.
	if tenantUserName("aaaa") == tenantUserName("bbbb") {
		t.Fatalf("distinct tenants collided")
	}
}

func TestTenantWorkspaceDirNonOverlapping(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YAVER_WORKSPACE_DIR", ws)
	d1 := tenantWorkspaceDir("tenant-one")
	d2 := tenantWorkspaceDir("tenant-two")
	if d1 == d2 {
		t.Fatalf("tenant dirs overlap: %q", d1)
	}
	// Fall-back form lives under the workspace root's .tenants and ends in Workspace.
	if !strings.HasPrefix(d1, filepath.Join(ws, ".tenants")) || !strings.HasSuffix(d1, "Workspace") {
		t.Fatalf("unexpected tenant dir shape: %q", d1)
	}
}
