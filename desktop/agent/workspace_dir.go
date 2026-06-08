package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workspace_dir.go — PER-TENANT extension of the "$HOME/Workspace" project
// home contract (docs §4b). The base contract + single source of truth is
// DefaultWorkspaceDir() in workspace_default.go ($YAVER_WORKSPACE_DIR or
// $HOME/Workspace, auto-created). This file adds the per-tenant variants the
// operator fleet needs: each tenant is its own OS user, so its
// $HOME/Workspace is automatically per-tenant, non-overlapping, non-root,
// and removable.

// tenantUserName maps a tenant's Yaver userId to a short, filesystem- and
// OS-user-safe slug "yv-<≤12 alnum>". Deterministic: the same tenant always
// resolves to the same home/user.
func tenantUserName(tenantUserID string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, strings.TrimSpace(tenantUserID))
	clean = strings.ToLower(clean)
	if len(clean) > 12 {
		clean = clean[:12]
	}
	if clean == "" {
		clean = "anon"
	}
	return "yv-" + clean
}

// tenantWorkspaceDir returns a tenant's project home, non-overlapping with
// every other tenant. Primary model: the per-tenant OS user's own
// $HOME/Workspace (/home/yv-<id>/Workspace) when that user exists. Until
// OS-user provisioning lands, falls back to a per-tenant subdir under the
// agent user's workspace root (.tenants/yv-<id>/Workspace), 0700.
func tenantWorkspaceDir(tenantUserID string) string {
	name := tenantUserName(tenantUserID)
	osHome := filepath.Join("/home", name)
	if fi, err := os.Stat(osHome); err == nil && fi.IsDir() {
		return filepath.Join(osHome, "Workspace")
	}
	base := ResolveWorkspaceParent("") // $HOME/Workspace (or override)
	return filepath.Join(base, ".tenants", name, "Workspace")
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
