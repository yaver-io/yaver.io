package main

// runner_signature.go — sanity-check that a binary resolved by name (e.g.
// "codex" found via PATH or login-shell lookup) is actually the runner we
// think it is, not a same-named shim. This guards the feedback SDK and
// the tasks-tab runner picker against false-positive "Installed" claims
// that turn into "no coding agent signed in" / 4xx errors at Send time.
//
// Why we need this: collectRunnerAuthStatusRows + handleRunners decide
// Installed=true purely from the binary being found on disk. On a host
// where the user has a `~/code/` directory (cloned source) or VS Code's
// `code` wrapper, a shell function named `codex`, or any other binary
// of the same name, the agent used to claim "OpenAI Codex installed"
// even though running it would fail. Mobile then surfaced a contradictory
// state — picker shows the runner, feedback panel says "no coding agent
// signed in".
//
// Verification runs `<path> --version` with a short timeout and matches
// the first line against a per-runner signature substring (case-insensitive).
// Results cache for 5 min so the /runner-auth/status poll loop doesn't
// fork a subprocess every request.

import (
	"context"
	osexec "os/exec"
	"strings"
	"sync"
	"time"
)

// runnerSignatures lists substrings the `<bin> --version` output is
// expected to contain (lowercased) for each first-class runner. We keep
// this generous — the codex / claude / opencode CLIs evolve their version
// banner copy across releases, and we'd rather accept a slightly different
// banner than reject a real install.
var runnerSignatures = map[string][]string{
	"claude":   {"claude", "anthropic"},
	"codex":    {"codex"},
	"glm":      {"claude", "anthropic"},
	"opencode": {"opencode"},
}

type runnerSignatureEntry struct {
	ok      bool
	version string
	at      time.Time
}

var (
	runnerSignatureCache sync.Map // map[string]runnerSignatureEntry — keyed by runnerID + "\x00" + path
	runnerSignatureTTL   = 5 * time.Minute
)

const runnerSignatureProbeTimeout = 1500 * time.Millisecond

// verifyRunnerBinarySignature runs `<path> --version` and confirms the
// output looks like the runner we asked for. Returns (true, "<first line
// of version output>") on a clean match, (false, "") when the probe
// fails (binary not executable, timed out, exit non-zero, output empty,
// or output doesn't contain the expected signature). An unknown runnerID
// (one not in runnerSignatures) is treated as trusted — we only know
// how to verify the runners we ship signatures for.
func verifyRunnerBinarySignature(runnerID, path string) (bool, string) {
	runnerID = normalizeRunnerID(runnerID)
	path = strings.TrimSpace(path)
	if path == "" {
		return false, ""
	}
	expected, known := runnerSignatures[runnerID]
	if !known {
		return true, ""
	}
	cacheKey := runnerID + "\x00" + path
	if v, ok := runnerSignatureCache.Load(cacheKey); ok {
		entry, _ := v.(runnerSignatureEntry)
		if time.Since(entry.at) < runnerSignatureTTL {
			return entry.ok, entry.version
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerSignatureProbeTimeout)
	defer cancel()
	out, err := osexec.CommandContext(ctx, path, "--version").CombinedOutput()
	ok := false
	version := ""
	if err == nil {
		first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		lower := strings.ToLower(first)
		for _, want := range expected {
			if strings.Contains(lower, want) {
				ok = true
				if len(first) > 80 {
					first = first[:80]
				}
				version = first
				break
			}
		}
		// Fallback for runners whose --version banner has no anchor
		// substring — opencode 1.x prints just "1.4.0" with no name.
		// If the output is non-empty AND looks like a bare version
		// (digits + dots, optionally with a trailing pre-release), trust
		// it. Foreign binaries (e.g. "Visual Studio Code 1.94.2") still
		// fail because they have words in front of the version, not
		// just a version on its own line.
		if !ok && first != "" && looksLikeBareVersion(first) {
			ok = true
			if len(first) > 80 {
				first = first[:80]
			}
			version = first
		}
	}
	runnerSignatureCache.Store(cacheKey, runnerSignatureEntry{ok: ok, version: version, at: time.Now()})
	return ok, version
}

// looksLikeBareVersion returns true when the trimmed line contains
// only a semver-ish token and optionally surrounding whitespace —
// e.g. "1.4.0", "v1.4.0", "1.4.0-rc.2". Used as a fallback signature
// for runners (opencode) that print only a version with no banner.
func looksLikeBareVersion(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		s = s[1:]
	}
	// Split off any pre-release / build suffix (`-rc.2`, `+build.5`).
	for _, sep := range []string{"-", "+", " "} {
		if i := strings.Index(s, sep); i >= 0 {
			s = s[:i]
		}
	}
	if s == "" {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// runnerSignatureCacheClear is a hook for tests to wipe the cache.
func runnerSignatureCacheClear() {
	runnerSignatureCache.Range(func(k, _ any) bool {
		runnerSignatureCache.Delete(k)
		return true
	})
}
