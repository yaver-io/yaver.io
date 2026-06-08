package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRootHonorsEnv(t *testing.T) {
	t.Setenv("YAVER_WORKSPACE_DIR", "/custom/ws")
	if got := WorkspaceRoot(); got != "/custom/ws" {
		t.Fatalf("WorkspaceRoot env override: got %q", got)
	}
}

func TestWorkspaceRootDefaultsToHomeWorkspace(t *testing.T) {
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	t.Setenv("HOME", "/home/someuser")
	got := WorkspaceRoot()
	if got != "/home/someuser/Workspace" {
		t.Fatalf("WorkspaceRoot default: got %q want /home/someuser/Workspace", got)
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
	t.Setenv("YAVER_WORKSPACE_DIR", "/ws")
	d1 := tenantWorkspaceDir("tenant-one")
	d2 := tenantWorkspaceDir("tenant-two")
	if d1 == d2 {
		t.Fatalf("tenant dirs overlap: %q", d1)
	}
	// Fall-back form lives under the workspace root's .tenants and ends in Workspace.
	if !strings.HasPrefix(d1, filepath.Join("/ws", ".tenants")) || !strings.HasSuffix(d1, "Workspace") {
		t.Fatalf("unexpected tenant dir shape: %q", d1)
	}
}
