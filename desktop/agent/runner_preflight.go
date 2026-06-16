package main

// runner_preflight.go — proactive runner auth pre-flight.
//
// THE PROBLEM (the "codex expired" friction): today a runner's auth state is
// only discovered when a task FAILS — watchProcess pattern-matches a 401 in the
// output and flips AuthConfigured off AFTER the fact (runner_auth.go). For a
// voice command from a car that is the worst possible moment: the user asks for
// something, waits, and gets "it failed" because the runner's subscription token
// quietly expired.
//
// RunnerPreflight checks the runner BEFORE dispatch so the surface can say
// "your codex login expired — re-authenticate" up front, instead of launching a
// doomed task. It cannot silently refresh a subscription OAuth token (claude /
// codex tokens are re-auth-only), so "proactive" here means: detect early +
// hand back an actionable CTA the voice/UI speaks, rather than a mid-task crash.

import "strings"

// RunnerPreflightResult is the verdict for one runner before dispatch.
type RunnerPreflightResult struct {
	Runner      string `json:"runner"`
	Fresh       bool   `json:"fresh"`                 // ready to dispatch
	NeedsReauth bool   `json:"needsReauth,omitempty"` // auth missing/rejected
	Reason      string `json:"reason,omitempty"`
	Action      string `json:"action,omitempty"` // the command/CTA that fixes it
	// Spoken is a short, TTS-friendly line for the voice surface.
	Spoken string `json:"spoken,omitempty"`
}

// RunnerPreflightByID checks a runner by id (a minimal RunnerConfig is enough —
// DetectRunnerRuntimeStatus only switches on the normalized id). An unknown/empty
// id is treated as fresh (the TaskManager resolves the default runner itself; we
// don't block a path we can't assess).
func RunnerPreflightByID(runnerID, workDir string) RunnerPreflightResult {
	id := normalizeRunnerID(runnerID)
	if id == "" || !runnerHasAuthModel(id) {
		// Unknown / no-auth runners have nothing to pre-flight — the TaskManager
		// handles them; we don't block a path we can't assess.
		return RunnerPreflightResult{Runner: id, Fresh: true}
	}
	status := DetectRunnerRuntimeStatus(RunnerConfig{RunnerID: id}, workDir)
	if status.AuthConfigured {
		return RunnerPreflightResult{Runner: id, Fresh: true}
	}
	reason := gatewayFirstNonEmpty(status.Warning, status.Error, "not signed in")
	action := runnerReauthCommand(id)
	return RunnerPreflightResult{
		Runner:      id,
		Fresh:       false,
		NeedsReauth: true,
		Reason:      reason,
		Action:      action,
		Spoken:      "Your " + id + " login has expired. Re-authenticate with " + action + " to continue.",
	}
}

// runnerHasAuthModel reports whether a runner authenticates (so a missing/expired
// credential is a real pre-flight failure). Runners without an auth model are not
// pre-flighted.
func runnerHasAuthModel(id string) bool {
	switch normalizeRunnerID(id) {
	case "codex", "claude", "glm", "opencode":
		return true
	}
	return false
}

// runnerReauthCommand returns the command that re-establishes a runner's auth.
func runnerReauthCommand(id string) string {
	switch normalizeRunnerID(id) {
	case "codex":
		return "codex login"
	case "claude":
		return "claude setup-token"
	case "glm":
		return "yaver runner auth glm"
	case "opencode":
		return "opencode auth login"
	default:
		return "yaver runner auth " + id
	}
}

// runnerPreflightSpoken renders the preflight result as a one-line TTS string,
// or "" when fresh (nothing to say).
func runnerPreflightSpoken(r RunnerPreflightResult) string {
	if r.Fresh {
		return ""
	}
	return strings.TrimSpace(r.Spoken)
}
