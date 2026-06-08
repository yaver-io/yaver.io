package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workspace_dir.go — the single "$HOME/Workspace" project-home contract
// (docs §4b). Projects always live in $HOME/Workspace/<repo> for whoever
// owns the runtime — Mac, fresh Linux box, container, proot, cloud box —
// mirroring the human layout (/Users/<you>/Workspace with repos as
// siblings). Each tenant is its own OS user, so its $HOME/Workspace is
// automatically per-tenant, non-overlapping, non-root, and removable.

// WorkspaceRoot returns the canonical project home for the CURRENT user:
// $YAVER_WORKSPACE_DIR if set, else $HOME/Workspace. Never /root/Workspace
// implicitly — if running as root the caller should have already refused
// (operator) or be a deliberate personal-box root.
func WorkspaceRoot() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_WORKSPACE_DIR")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		// Last-resort fallback; should never hit on a real box.
		home = "."
	}
	return filepath.Join(home, "Workspace")
}

// EnsureWorkspaceRoot creates $HOME/Workspace (0755 — it's the user's own
// project home, same perms as a hand-made ~/Workspace) and returns it.
func EnsureWorkspaceRoot() (string, error) {
	root := WorkspaceRoot()
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", fmt.Errorf("create workspace root %s: %w", root, err)
	}
	return root, nil
}

// tenantUserName maps a tenant's Yaver userId to a short, filesystem- and
// OS-user-safe slug "yv-<12 hex/alnum>". Deterministic so the same tenant
// always resolves to the same home/user.
func tenantUserName(tenantUserID string) string {
	id := strings.TrimSpace(tenantUserID)
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, id)
	clean = strings.ToLower(clean)
	if len(clean) > 12 {
		clean = clean[:12]
	}
	if clean == "" {
		clean = "anon"
	}
	return "yv-" + clean
}

// tenantWorkspaceDir returns the per-tenant project home, non-overlapping
// with every other tenant. If the per-tenant OS user exists (the primary
// model — /home/yv-<id>), its $HOME/Workspace is used; otherwise we fall
// back to a per-tenant subdir under the agent user's workspace root
// (.tenants/yv-<id>/Workspace) so the contract still holds before OS-user
// provisioning lands. Always mode 0700 (tenant-private).
func tenantWorkspaceDir(tenantUserID string) string {
	name := tenantUserName(tenantUserID)
	osHome := filepath.Join("/home", name)
	if fi, err := os.Stat(osHome); err == nil && fi.IsDir() {
		return filepath.Join(osHome, "Workspace")
	}
	return filepath.Join(WorkspaceRoot(), ".tenants", name, "Workspace")
}

// EnsureTenantWorkspace creates a tenant's Workspace dir (0700) and returns
// it. Two tenants never share a dir, so creating/removing one never touches
// another.
func EnsureTenantWorkspace(tenantUserID string) (string, error) {
	dir := tenantWorkspaceDir(tenantUserID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create tenant workspace %s: %w", dir, err)
	}
	return dir, nil
}
