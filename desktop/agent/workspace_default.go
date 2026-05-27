package main

// workspace_default.go — single source of truth for "where do new
// clones / scaffolds / init_project results land by default."
//
// Decision: $HOME/Workspace (capital W).
//
// Rationale:
//   - macOS users (incl. kivanc) already use ~/Workspace/<repo>; this
//     matches their muscle memory + the existing project-discovery
//     scanner in convex_state_sync.go::discoverProjectDirs which
//     already looks at ~/Workspace + ~/Projects + ~/Code + ~/src.
//   - Linux users: same path works. ~/Workspace is a common-enough
//     convention that auto-creating it doesn't clash with XDG.
//   - Windows: %USERPROFILE%\Workspace via os.UserHomeDir() — same
//     pattern, same behavior.
//   - Managed-cloud boxes (Hetzner / Linode / etc.): agent runs as
//     either root or the `yaver` user depending on provisioner.
//     os.UserHomeDir() returns /root or /home/yaver respectively;
//     we land at /root/Workspace or /home/yaver/Workspace. Either
//     way it's a stable, user-visible path the user can ssh into.
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
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultWorkspaceDirName is the relative directory name under $HOME
// where Yaver lands new clones / scaffolds by default. Capital W to
// match the macOS/dev convention (Finder shows ~/Workspace, not
// ~/workspace).
const DefaultWorkspaceDirName = "Workspace"

// DefaultWorkspaceDir returns the absolute path where new clones,
// init_project scaffolds, and any other "where do I put this new
// repo" default lands. Auto-creates the dir on first call.
//
// Path: $HOME/Workspace
//
// Errors only when both:
//   1. os.UserHomeDir() fails (rare — usually only in stripped
//      Docker containers without HOME set), AND
//   2. /workspace doesn't exist as a fallback.
//
// Callers that can tolerate fallback should use ResolveWorkspaceParent
// instead — that helper accepts a user override and falls back to
// CWD when DefaultWorkspaceDir is unavailable.
func DefaultWorkspaceDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Managed-cloud boxes sometimes ship without HOME — the
		// provisioner can pre-create /workspace as a fallback. If
		// it exists, use it; otherwise fail loudly.
		if _, statErr := os.Stat("/workspace"); statErr == nil {
			return "/workspace", nil
		}
		if err == nil {
			err = errors.New("HOME is empty and /workspace does not exist")
		}
		return "", fmt.Errorf("resolve default workspace: %w", err)
	}
	dir := filepath.Join(home, DefaultWorkspaceDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return dir, nil
}

// ResolveWorkspaceParent picks the right parent directory for a new
// clone / scaffold, with this precedence:
//
//   1. `provided` if non-empty and non-whitespace — user / API
//      explicitly set it, honor verbatim.
//   2. $HOME/Workspace via DefaultWorkspaceDir() — auto-created.
//   3. os.Getwd() — last-resort fallback if HOME resolution dies.
//      Logged-warning case; usually only hit in degenerate containers.
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
	return "/tmp" // absolute last resort
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
