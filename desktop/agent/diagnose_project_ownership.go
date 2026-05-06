package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkProjectOwnership scans the well-known dev-machine project roots
// (~/Workspace, ~/Projects, ~/repos, …) and warns about any top-level
// child whose ownership would trip codex's bwrap sandbox or the host's
// own DAC at task time. The user gets the chown command up front
// instead of having to vibe-and-fail to discover it.
//
// Heuristic per child dir:
//   - euid != 0: the dir is unwritable when current uid isn't owner
//     and group/other write isn't granted. Plain DAC, applies to every
//     runner.
//   - euid == 0: codex bwrap drops CAP_DAC_OVERRIDE before running, so
//     a non-root-owned dir without world-write breaks codex even
//     though host-side root could write fine. Use the same predicate
//     as the runtime preflight (codexBwrapWillFail).
//
// We don't recurse — the symptom is at the project root, and walking
// every nested file would balloon the diagnose runtime on machines
// with deep monorepos. False negatives on edge cases (e.g. only a
// nested subdir is mis-owned) are acceptable: the runtime preflight
// at task-create time is the safety net, this check is the heads-up.
func checkProjectOwnership(ctx context.Context, emit DiagEmit) {
	roots := projectRootsForOwnershipScan()
	if len(roots) == 0 {
		emit(DiagEvent{Type: "finding", Check: "project-ownership", Severity: DiagInfo,
			Message: "no Workspace/Projects/repos dirs under HOME — nothing to scan"})
		return
	}
	euid := os.Geteuid()
	egid := os.Getegid()
	totalChecked := 0
	totalFlagged := 0
	for _, root := range roots {
		if ctx.Err() != nil {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // root not accessible to us; skip silently
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			child := filepath.Join(root, entry.Name())
			info, err := os.Stat(child)
			if err != nil || !info.IsDir() {
				continue
			}
			totalChecked++
			blocker := projectOwnershipBlocker(child, info, euid)
			if blocker == "" {
				continue
			}
			totalFlagged++
			owner := workDirOwnerLabel(info)
			if owner == "" {
				owner = "unknown"
			}
			emit(DiagEvent{
				Type:     "finding",
				Check:    "project-ownership",
				Severity: DiagWarning,
				Message:  fmt.Sprintf("%s — %s (owner: %s, agent: uid=%d gid=%d). Run: sudo chown -R %d:%d %s", child, blocker, owner, euid, egid, euid, egid, child),
			})
		}
	}
	if totalChecked > 0 && totalFlagged == 0 {
		emit(DiagEvent{Type: "finding", Check: "project-ownership", Severity: DiagOK,
			Message: fmt.Sprintf("scanned %d project dirs across %s — all owned correctly for the agent (uid=%d)", totalChecked, strings.Join(roots, ", "), euid)})
	}
}

// projectRootsForOwnershipScan returns the existing well-known project
// roots under the agent's HOME. We deliberately keep the list short —
// the goal is to catch the common case (user clones into ~/Workspace)
// not to enumerate every path on disk.
func projectRootsForOwnershipScan() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, "Workspace"),
		filepath.Join(home, "workspace"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "projects"),
		filepath.Join(home, "repos"),
		filepath.Join(home, "src"),
		filepath.Join(home, "Code"),
		filepath.Join(home, "code"),
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue // case-insensitive FS dedupe
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}

// projectOwnershipBlocker classifies why this dir would break a runner.
// Returns "" when the dir is fine, otherwise a one-line reason. We
// reuse the codexBwrapWillFail predicate for the root case — same
// rule as the runtime preflight, so doctor and runtime never disagree.
func projectOwnershipBlocker(child string, info os.FileInfo, euid int) string {
	if info == nil || !info.IsDir() {
		return ""
	}
	if euid != 0 {
		// Non-root: real DAC probe. If we can create a tempfile we're
		// fine. Don't second-guess via bit math — group memberships,
		// ACLs, and capability inheritance can flip the answer.
		probe, err := os.CreateTemp(child, ".yaver-doctor-probe-*")
		if err != nil {
			return "not writable by current user"
		}
		probe.Close()
		_ = os.Remove(probe.Name())
		return ""
	}
	// Root: host probe lies (CAP_DAC_OVERRIDE), so use the predicate
	// the runtime preflight uses — codex bwrap drops the cap and will
	// fail at task time even though host root could write fine.
	if codexBwrapWillFail(info) {
		return "codex bwrap will fail (root + non-root-owned dir, not world-writable)"
	}
	return ""
}
