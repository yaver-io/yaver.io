package main

// beta_managed_git.go — wire a beta tenant's project to a PER-TENANT
// managed-git bare repo INSIDE the tenant partition.
//
// managed_git.go's bare-repo root is managedGitReposRoot() =
// ~/.yaver/managed-git/repos — single-tenant (owner-home, slug-keyed). A
// beta tenant runs as yv-<id> with no access to the owner's home, and two
// tenants would collide on slug. So beta uses a per-tenant root under the
// tenant partition instead: <tenantDir>/managed-git/repos/<repoID>.git.
//
// The win (see beta-invisible-infra-share-design.md "Finding 1"): the
// tenant pushes to a LOCAL bare repo (a filesystem path) → NO credentials
// ever touch the tenant. The owner's GitHub/GitLab creds enter only via
// the owner-side mirror step (managed_git.go's ManagedGitMirrorToProvider),
// which runs outside any tenant context. We do NOT edit managed_git.go
// here — only reuse its public mirror function from the owner path.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// betaTenantRepoRoot — per-tenant managed-git bare-repo root, under the
// tenant partition (NOT the owner's ~/.yaver). Created 0700 + tenant-owned
// by the on-box provisioner.
func betaTenantRepoRoot(tenantDir string) string {
	return filepath.Join(tenantDir, "managed-git", "repos")
}

// betaTenantPhoneRoot — the per-tenant phone-projects root, under the
// tenant partition. The serverless-normie-cloud receive path
// (ImportPhoneProject) should land a beta tenant's app HERE instead of the
// shared ~/.yaver/phone-projects, satisfying the handoff requirement
// "tenant isolation stronger than a shared owner home". The receive
// handler passes this as PhoneImportOptions.BaseRoot for a beta caller (see
// the patch spec in beta-invisible-infra-share-design.md). Returns "" for a
// non-beta user, so the caller falls back to the default shared root.
func betaTenantPhoneRoot(userID string) string {
	id := betaSanitizeRef(userID)
	if id == "anon" {
		return ""
	}
	return filepath.Join(betaTenantRoot, id, "phone-projects")
}

// betaTenantBarePath — bare repo path for a project within a tenant.
func betaTenantBarePath(tenantDir, repoID string) string {
	return filepath.Join(betaTenantRepoRoot(tenantDir), betaSanitizeRef(repoID)+".git")
}

// BetaEnsureTenantRepo inits a per-tenant bare repo (if absent) and points
// the tenant project's origin at it — a LOCAL path, so tenant pushes need
// no credentials. Returns the bare path. Every git call here carries a nil
// env: there is no credential anywhere on this path.
func BetaEnsureTenantRepo(runner betaGitRunner, tenantProjectDir, tenantDir, repoID string) (barePath string, err error) {
	barePath = betaTenantBarePath(tenantDir, repoID)
	if _, err = runner.git("", nil, "init", "--bare", barePath); err != nil {
		return "", fmt.Errorf("init bare: %w", err)
	}
	if _, err = runner.git(tenantProjectDir, nil, "init", "-b", "main"); err != nil {
		// older git without -b: fall back, then force the branch name
		_, _ = runner.git(tenantProjectDir, nil, "init")
		_, _ = runner.git(tenantProjectDir, nil, "checkout", "-B", "main")
	}
	_, _ = runner.git(tenantProjectDir, nil, "remote", "remove", "origin")
	if _, err = runner.git(tenantProjectDir, nil, "remote", "add", "origin", barePath); err != nil {
		return "", fmt.Errorf("add origin: %w", err)
	}
	return barePath, nil
}

// BetaTenantCheckpoint commits the tenant working tree and pushes to the
// LOCAL bare origin — NO credentials (origin is a filesystem path). This is
// the beta "commit & push" primitive that the /beta/push endpoint runs as
// the tenant user. Returns the commit sha. An empty commit (nothing
// changed) is tolerated.
func BetaTenantCheckpoint(runner betaGitRunner, tenantProjectDir, message string) (sha string, err error) {
	if strings.TrimSpace(message) == "" {
		message = "yaver beta: checkpoint"
	}
	if _, err = runner.git(tenantProjectDir, nil, "add", "-A"); err != nil {
		return "", fmt.Errorf("add: %w", err)
	}
	if out, cerr := runner.git(tenantProjectDir, nil,
		"-c", "user.name=Yaver Beta", "-c", "user.email=beta@yaver.io",
		"commit", "-m", message); cerr != nil && !strings.Contains(out, "nothing to commit") {
		return "", fmt.Errorf("commit: %w (%s)", cerr, out)
	}
	if out, perr := runner.git(tenantProjectDir, nil, "push", "-u", "origin", "HEAD:main"); perr != nil {
		return "", fmt.Errorf("push local bare: %w (%s)", perr, out)
	}
	raw, rerr := runner.git(tenantProjectDir, nil, "rev-parse", "HEAD")
	if rerr != nil {
		return "", fmt.Errorf("rev-parse: %w", rerr)
	}
	return strings.TrimSpace(raw), nil
}
