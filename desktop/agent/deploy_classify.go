package main

// deploy_classify.go — regex-based error classification for
// /deploy/ship runs. The goal is "exit 1" → "what actually went
// wrong" without making the user grep through the build log.
//
// Rules of thumb:
//
//   - Classification is a hint, never a source of truth. The raw
//     exit code and full output log stay available for the
//     user-who-doesn't-trust-the-label.
//   - `already_uploaded` is the one class that rewrites OK from
//     false→true: TestFlight's "Redundant Binary Upload" is what
//     Apple says when you re-upload an existing build; it is not a
//     failure and we should not surface it as one.
//   - Patterns are matched case-insensitively unless the upstream
//     tool is picky. Keep patterns narrow — a false positive that
//     lights up `auth_error` on a genuine build failure would be
//     worse than no label.

import (
	"regexp"
	"strings"
)

// DeployErrorClass is a compact label for why a run ended the way
// it did. Empty means "not yet classified" (still running) or
// "successful run, nothing to classify".
type DeployErrorClass string

const (
	DeployErrUnknown         DeployErrorClass = ""
	DeployErrVaultLocked     DeployErrorClass = "vault_locked"
	DeployErrToolchainMiss   DeployErrorClass = "toolchain_missing"
	DeployErrAuthError       DeployErrorClass = "auth_error"
	DeployErrAlreadyUploaded DeployErrorClass = "already_uploaded"
	DeployErrTimeout         DeployErrorClass = "timeout"
	DeployErrPreflight       DeployErrorClass = "preflight_failed"
	DeployErrNetwork         DeployErrorClass = "network_error"
	DeployErrDiskFull        DeployErrorClass = "disk_full"
	DeployErrSignRing        DeployErrorClass = "signing_error"
	DeployErrBuildFailed     DeployErrorClass = "build_failed"
	DeployErrInternal        DeployErrorClass = "internal_error"
)

// classifyRule pairs a narrow regex with the class to emit if it matches.
// Order matters: the first match wins, so rules are ordered from most
// specific → most generic.
type classifyRule struct {
	Pattern *regexp.Regexp
	Class   DeployErrorClass
}

// deployClassifyRules are tried in order.
var deployClassifyRules = []classifyRule{
	// TestFlight "you already shipped this". Not a failure.
	{regexp.MustCompile(`(?i)Redundant Binary Upload`), DeployErrAlreadyUploaded},
	{regexp.MustCompile(`(?i)The bundle version must be higher than`), DeployErrAlreadyUploaded},

	// Vault / passphrase issues.
	{regexp.MustCompile(`(?i)wrong passphrase or corrupted vault`), DeployErrVaultLocked},
	{regexp.MustCompile(`(?i)vault entry \S+ not found`), DeployErrVaultLocked},

	// Preflight (toolchain doctor) refused to let the deploy start.
	{regexp.MustCompile(`(?i)Preflight failed — re-run with`), DeployErrPreflight},

	// Apple / Play signing.
	{regexp.MustCompile(`(?i)Code Sign(ing)? error`), DeployErrSignRing},
	{regexp.MustCompile(`(?i)No signing certificate`), DeployErrSignRing},
	{regexp.MustCompile(`(?i)Keystore was tampered with, or password was incorrect`), DeployErrSignRing},
	{regexp.MustCompile(`(?i)signed JAR file not found`), DeployErrSignRing},

	// Auth — Apple, Play, Cloudflare, Convex, npm.
	{regexp.MustCompile(`(?i)Authentication failed|401 Unauthorized|403 Forbidden`), DeployErrAuthError},
	{regexp.MustCompile(`(?i)invalid.{0,10}token|bad.{0,10}credentials`), DeployErrAuthError},
	{regexp.MustCompile(`(?i)E401|E403`), DeployErrAuthError},

	// Toolchain missing.
	{regexp.MustCompile(`(?i)command not found`), DeployErrToolchainMiss},
	{regexp.MustCompile(`(?i)No such file or directory.*\.(sh|yaml|plist|gradle)`), DeployErrToolchainMiss},
	{regexp.MustCompile(`(?i)xcodebuild: error: Unable to find a destination`), DeployErrToolchainMiss},

	// Network.
	{regexp.MustCompile(`(?i)could not resolve host|name or service not known|dial tcp .+ connection refused`), DeployErrNetwork},
	{regexp.MustCompile(`(?i)TLS handshake timeout|i/o timeout`), DeployErrNetwork},

	// Disk.
	{regexp.MustCompile(`(?i)No space left on device`), DeployErrDiskFull},

	// Timeout signalled by our own context.
	{regexp.MustCompile(`(?i)context deadline exceeded|signal: killed`), DeployErrTimeout},
}

// ClassifyDeployOutput inspects the subprocess tail (can also include
// stderr) and exit code and returns a (class, treatAsOK) pair.
//
//	treatAsOK=true is the escape hatch for "looks like failure at the
//	exit-code level, but is actually success" — today: Apple's
//	Redundant Binary Upload. Callers should honour it and rewrite
//	DeployRun.OK/ExitCode accordingly.
func ClassifyDeployOutput(tail string, exitCode int, timedOut bool) (DeployErrorClass, bool) {
	if timedOut {
		return DeployErrTimeout, false
	}
	if exitCode == 0 && tail == "" {
		return DeployErrUnknown, true
	}
	tail = strings.TrimSpace(tail)
	for _, rule := range deployClassifyRules {
		if rule.Pattern.MatchString(tail) {
			treatAsOK := rule.Class == DeployErrAlreadyUploaded
			return rule.Class, treatAsOK
		}
	}
	if exitCode == 0 {
		return DeployErrUnknown, true
	}
	return DeployErrBuildFailed, false
}
