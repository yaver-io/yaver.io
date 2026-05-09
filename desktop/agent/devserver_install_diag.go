package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
)

// installDiag captures the tail of install (npm/yarn/pnpm/bun) stdout +
// stderr for diagnostic surfacing when the install fails. The previous
// behavior was to return only the bare exec error ("npm install failed:
// exit status 254") which made every install failure look identical to
// the dashboard / mobile cards / autodev loop — even though the actual
// cause (EINTEGRITY, ENOENT for a file: dep, peer-dep conflict, network)
// is right there in the stderr we already tee through extraOut.
//
// We keep the tail bounded so a noisy npm log can't blow up the agent
// memory footprint, and we read in 1KB chunks so even a single huge
// line gets truncated gracefully.
type installDiag struct {
	mu     sync.Mutex
	buf    bytes.Buffer // last installDiagMaxBytes worth of output
	maxLen int
}

const installDiagMaxBytes = 8192

func newInstallDiag() *installDiag {
	return &installDiag{maxLen: installDiagMaxBytes}
}

// Write implements io.Writer. The latest bytes win — when the buffer
// would exceed maxLen, drop the oldest.
func (d *installDiag) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.buf.Write(p)
	if d.buf.Len() > d.maxLen {
		// Compact: keep only the trailing maxLen bytes.
		tail := d.buf.Bytes()[d.buf.Len()-d.maxLen:]
		next := make([]byte, len(tail))
		copy(next, tail)
		d.buf.Reset()
		d.buf.Write(next)
	}
	return len(p), nil
}

// Tail returns the captured tail for inclusion in error messages.
func (d *installDiag) Tail() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.buf.String()
}

// classifyInstallFailure inspects a captured install tail and returns a
// short, structured user-facing message that names the actual cause and,
// when known, the exact action to take. Returns "" when nothing
// classifies — caller falls back to the generic exit-status message.
//
// Tested against real failures observed in the wild (carrotbet hitting a
// stale package-lock.json integrity hash on yaver-test-ephemeral; missing
// `file:`-tarball deps; EACCES on Linux without sudo; peer-dep conflicts).
func classifyInstallFailure(tail string) string {
	t := strings.ToLower(tail)
	switch {
	case strings.Contains(t, "code eintegrity") || strings.Contains(t, "integrity checksum failed"):
		return "package-lock.json has a stale integrity hash for a file: dependency. " +
			"Re-pack the referenced tarball OR strip the `integrity` field from that " +
			"entry in package-lock.json and retry. (`npm i --force` does NOT bypass this — " +
			"npm always verifies file: tarball integrity.)"
	case strings.Contains(t, "code enoent") && strings.Contains(t, "yaver-feedback") && strings.Contains(t, ".tgz"):
		return "a `file:` dependency points at a yaver-feedback SDK tarball that doesn't exist on this machine. " +
			"Either pack the SDK at the expected version (`cd <yaver.io>/sdk/feedback/<flavor> && npm pack`) " +
			"or update the dependency to the published npm version."
	case strings.Contains(t, "code enoent") && strings.Contains(t, ".tgz"):
		return "a `file:` dependency tarball is missing on this machine. " +
			"Verify the relative path from the package.json resolves to an existing .tgz."
	case strings.Contains(t, "eresolve") || strings.Contains(t, "could not resolve dependency"):
		return "peer-dependency conflict. Re-run with --legacy-peer-deps (the agent already does this for npm) " +
			"or upgrade the conflicting package — npm's tail above lists the exact pair."
	case strings.Contains(t, "eacces") || strings.Contains(t, "permission denied"):
		return "permission denied while installing. Check that the workspace dir and ~/.npm are writable by the agent user."
	case strings.Contains(t, "etarget") || strings.Contains(t, "no matching version"):
		return "a dependency version range can't be satisfied from the registry. The tail above names the package."
	case strings.Contains(t, "enotfound") || strings.Contains(t, "etimedout") || strings.Contains(t, "econnreset"):
		return "network error reaching the npm registry. Check connectivity / proxy settings on the host."
	case strings.Contains(t, "code elspr") || strings.Contains(t, "lockfile") && strings.Contains(t, "out of sync"):
		return "package-lock.json is out of sync with package.json. Run `npm install` (without --ci) once to refresh the lockfile."
	}
	return ""
}

// installFailureMessage builds the final error string used by the agent's
// HTTP responses + SSE events when an install fails. Format:
//
//	<package-manager> install failed (exit status N).
//	cause: <classifier-output, when matched>
//	last lines:
//	<bounded tail>
//
// This stays one structured envelope so callers can either render the
// whole thing or split on `\ncause: `/`\nlast lines:` headers.
func installFailureMessage(packageManager string, runErr error, tail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s install failed: %v", packageManager, runErr)
	if cause := classifyInstallFailure(tail); cause != "" {
		fmt.Fprintf(&b, "\ncause: %s", cause)
	}
	if tail != "" {
		// Trim to the last ~30 lines so the message fits in dashboard
		// cards without scrolling past the actionable cause line.
		lines := strings.Split(strings.TrimRight(tail, "\n"), "\n")
		if len(lines) > 30 {
			lines = lines[len(lines)-30:]
		}
		fmt.Fprintf(&b, "\nlast lines:\n%s", strings.Join(lines, "\n"))
	}
	return b.String()
}
