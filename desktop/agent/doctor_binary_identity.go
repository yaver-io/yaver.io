package main

// doctor_binary_identity.go — is the agent you are DEBUGGING the agent that is
// RUNNING?
//
// ── The incident this encodes (2026-07-24) ───────────────────────────────────
//
// A whole session was spent fixing browser-lane and discovery bugs, deploying
// by hot-swapping a locally-built binary over ~/.yaver/bin/<version>/…/yaver on
// two machines. Three separate traps, each of which silently invalidated a
// verification:
//
//  1. The agent runs ~/.yaver/bin/CURRENT/… — a SYMLINK. Swapping the binary at
//     the versioned path changes nothing if `current` points elsewhere, and it
//     has gone stale before (1.99.190 while PATH said 1.99.285).
//  2. Auto-update re-downloads the PUBLISHED build for that version number and
//     overwrites a hot-swap. The fix evaporates on a jittered 6-12h timer with
//     no log anyone reads.
//  3. A hot-swap keeps the OLD version STRING (built with -X main.version=…), so
//     /info, the device list and every surface report a version whose bytes are
//     not what npm shipped. Two boxes can both say "1.99.349" and run different
//     code.
//
// Net effect: "I deployed the fix and it still fails" was, more than once,
// "the process never ran the fix". That is the same false-green class as every
// other bug this session — the inventory says yes, the operation says no.
//
// So the agent reports its own identity: which binary the RUNNING process was
// launched from, whether that resolves through a symlink, and whether it
// matches the version it claims. A surface can then say "this box is running an
// unpublished build" instead of letting someone verify against a ghost.

import (
	"os"
	"path/filepath"
	"strings"
)

// BinaryIdentity describes the executable actually backing this process.
type BinaryIdentity struct {
	// ExecPath is what the OS says launched us.
	ExecPath string `json:"execPath"`
	// ResolvedPath is ExecPath with every symlink followed. When these differ,
	// the launcher went through something like ~/.yaver/bin/current.
	ResolvedPath string `json:"resolvedPath"`
	// ViaSymlink is true when the running path is not the real one — the trap
	// that makes "I swapped the binary" and "the new binary is running"
	// different statements.
	ViaSymlink bool `json:"viaSymlink"`
	// ReportedVersion is what this build claims (main.version).
	ReportedVersion string `json:"reportedVersion"`
	// VersionDirOnPath is the version segment of the resolved path, when the
	// binary lives under ~/.yaver/bin/<version>/. Empty when it does not.
	VersionDirOnPath string `json:"versionDirOnPath,omitempty"`
	// VersionMismatch is true when the path says one version and the build
	// claims another — the signature of a hot-swap or a stale `current`.
	VersionMismatch bool `json:"versionMismatch"`
	// Warning is human-readable and non-empty ONLY when something is off, so a
	// caller can surface it verbatim.
	Warning string `json:"warning,omitempty"`
}

// DescribeBinaryIdentity answers "what is actually running here?".
func DescribeBinaryIdentity() BinaryIdentity {
	id := BinaryIdentity{ReportedVersion: version}

	exe, err := os.Executable()
	if err != nil {
		id.Warning = "could not determine the running executable path: " + err.Error()
		return id
	}
	id.ExecPath = exe

	resolved, rerr := filepath.EvalSymlinks(exe)
	if rerr != nil {
		resolved = exe
	}
	id.ResolvedPath = resolved
	id.ViaSymlink = resolved != exe

	id.VersionDirOnPath = versionSegmentFromYaverBinPath(resolved)
	if id.VersionDirOnPath == "" {
		id.VersionDirOnPath = versionSegmentFromYaverBinPath(exe)
	}
	if id.VersionDirOnPath != "" && id.VersionDirOnPath != id.ReportedVersion {
		id.VersionMismatch = true
	}

	switch {
	case id.VersionMismatch:
		id.Warning = "this process reports version " + id.ReportedVersion +
			" but was launched from a directory for " + id.VersionDirOnPath +
			" — the running bytes are not the published build for either. " +
			"A hot-swapped or stale binary will be replaced by auto-update; publish a real release instead of trusting this."
	case id.ViaSymlink:
		// Not wrong by itself — it is how the launcher is meant to work — but
		// worth stating, because swapping the versioned path while `current`
		// points elsewhere is a silent no-op.
		id.Warning = "running via a symlink (" + id.ExecPath + " -> " + id.ResolvedPath +
			"); replacing a binary at a versioned path only takes effect if this link points at it."
	}
	return id
}

// versionSegmentFromYaverBinPath pulls "<version>" out of a path shaped like
// .../.yaver/bin/<version>/<platform>/yaver. Returns "" for any other layout,
// so a source build or a packaged install is never mislabelled.
func versionSegmentFromYaverBinPath(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "bin" && i > 0 && parts[i-1] == ".yaver" {
			seg := parts[i+1]
			// "current" is the symlink itself, not a version.
			if seg == "" || seg == "current" {
				return ""
			}
			return seg
		}
	}
	return ""
}
