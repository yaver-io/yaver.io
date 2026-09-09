package main

// workspace_default.go — single source of truth for Yaver-managed repository
// and development-worktree placement.
//
// Decision:
//
//   $HOME/Workspace/repos/<repository>       pristine default-branch checkout
//   $HOME/Workspace/worktrees/<development> isolated development checkout
//
// Explicit paths are never rewritten. A self-hosted user can keep any existing
// hierarchy; this convention applies only when Yaver chooses a destination.
//
// Rationale:
//   - Existing installs under ~/Workspace remain discoverable; managed clones
//     now use its repos child and managed coding trees use worktrees.
//   - Linux users: same path works. ~/Workspace is a common-enough
//     convention that auto-creating it doesn't clash with XDG.
//   - Windows: %USERPROFILE%\Workspace via os.UserHomeDir() — same
//     pattern, same behavior.
//   - Managed-cloud boxes: os.UserHomeDir resolves the provisioned runtime
//     account without assuming its username or filesystem layout.
//   - Self-hosted (user's own Linux PC): ~/Workspace = their home.
//
// Anti-rationale (paths we deliberately do NOT pick):
//   - ~/.yaver/workspace — hidden, not discoverable to the user
//   - /opt/yaver — needs root, breaks the no-root contract (NO_ROOT.md)
//   - /tmp/yaver — wiped on reboot, lost work
//   - $PWD — non-deterministic, depends on where `yaver serve` was
//     launched from (often / or $HOME or wherever systemd starts it)
//
// Auto-creation: the helper mkdirs the path on first call. Idempotent.
// Permission 0755 — owner rwx, group + others rx, standard for a
// project dir the user will read from + git will clone into.

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultWorkspaceDirName is the relative directory name under $HOME
// where Yaver lands new clones / scaffolds by default. Capital W to
// match the macOS/dev convention (Finder shows ~/Workspace, not
// ~/workspace).
const DefaultWorkspaceDirName = "Workspace"

const (
	DefaultWorkspaceReposDirName     = "repos"
	DefaultWorkspaceWorktreesDirName = "worktrees"
)

// DefaultWorkspaceDir returns the absolute path where new clones,
// init_project scaffolds, and any other "where do I put this new
// repo" default lands. Auto-creates the dir on first call.
//
// Path: $HOME/Workspace
//
// Errors when os.UserHomeDir() is unavailable. ResolveWorkspaceParent handles
// that rare stripped-container case with runtime CWD/temp fallbacks.
//
// Callers that can tolerate fallback should use ResolveWorkspaceParent
// instead — that helper accepts a user override and falls back to
// CWD when DefaultWorkspaceDir is unavailable.
func DefaultWorkspaceDir() (string, error) {
	// Explicit override wins (operator nodes / tests pin the project home).
	if v := trimSpace(os.Getenv("YAVER_WORKSPACE_DIR")); v != "" {
		if err := os.MkdirAll(v, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", v, err)
		}
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err == nil {
			err = fmt.Errorf("HOME is empty")
		}
		return "", fmt.Errorf("resolve default workspace: %w", err)
	}
	dir := filepath.Join(home, DefaultWorkspaceDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

// DefaultWorkspaceReposDir returns the parent used for repositories Yaver
// clones or scaffolds when the caller did not choose a directory. Keeping this
// separate from worktrees makes it possible to keep the checkout on its clean,
// current default branch while every coding session gets an isolated tree.
func DefaultWorkspaceReposDir() (string, error) {
	if v := trimSpace(os.Getenv("YAVER_REPOS_DIR")); v != "" {
		return ensureWorkspaceDirectory(v)
	}
	root, err := DefaultWorkspaceDir()
	if err != nil {
		return "", err
	}
	return ensureWorkspaceDirectory(filepath.Join(root, DefaultWorkspaceReposDirName))
}

// DefaultWorkspaceWorktreesDir returns the parent used for Yaver-managed
// development trees. The operator override is intentionally independent from
// YAVER_WORKSPACE_DIR so large worktrees can live on another filesystem.
func DefaultWorkspaceWorktreesDir() (string, error) {
	if v := trimSpace(os.Getenv("YAVER_WORKTREES_DIR")); v != "" {
		return ensureWorkspaceDirectory(v)
	}
	root, err := DefaultWorkspaceDir()
	if err != nil {
		return "", err
	}
	return ensureWorkspaceDirectory(filepath.Join(root, DefaultWorkspaceWorktreesDirName))
}

func ensureWorkspaceDirectory(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace directory %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", abs, err)
	}
	return abs, nil
}

// ResolveRepositoryParent honors a user/API-selected path verbatim. Only an
// omitted path selects the managed Workspace/repos convention.
func ResolveRepositoryParent(provided string) string {
	if p := trimSpace(provided); p != "" {
		return p
	}
	if dir, err := DefaultWorkspaceReposDir(); err == nil {
		return dir
	}
	return ResolveWorkspaceParent("")
}

// ResolveWorktreeParent is the development-tree counterpart of
// ResolveRepositoryParent. Explicit custom layouts remain supported.
func ResolveWorktreeParent(provided string) string {
	if p := trimSpace(provided); p != "" {
		return p
	}
	if dir, err := DefaultWorkspaceWorktreesDir(); err == nil {
		return dir
	}
	return ResolveWorkspaceParent("")
}

// ResolveWorkspaceParent picks the workspace root. New repositories should
// use ResolveRepositoryParent; new coding trees use ResolveWorktreeParent.
//
//  1. `provided` if non-empty and non-whitespace — user / API
//     explicitly set it, honor verbatim.
//  2. $HOME/Workspace via DefaultWorkspaceDir() — auto-created.
//  3. os.Getwd() — last-resort fallback if HOME resolution dies.
//     Logged-warning case; usually only hit in degenerate containers.
//
// Returns the absolute path that callers should use as the parent.
// The named repo dir (`<parent>/<repo-name>`) is the caller's job.
func ResolveWorkspaceParent(provided string) string {
	if p := trimSpace(provided); p != "" {
		return p
	}
	if dir, err := DefaultWorkspaceDir(); err == nil {
		return dir
	}
	// Last-ditch: cwd. Shouldn't be reached in normal install flows
	// because $HOME is always set on every supported platform (macOS,
	// Linux, Windows), and the dir is mkdir-p'd. If we do land here,
	// the user gets the historical pre-2026-05-28 behavior — at least
	// they can override via --dir.
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return os.TempDir() // runtime-resolved absolute last resort
}

func trimSpace(s string) string {
	out := s
	for len(out) > 0 && (out[0] == ' ' || out[0] == '\t' || out[0] == '\n') {
		out = out[1:]
	}
	for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t' || out[len(out)-1] == '\n') {
		out = out[:len(out)-1]
	}
	return out
}
