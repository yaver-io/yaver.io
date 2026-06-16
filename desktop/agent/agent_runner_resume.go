package main

// agent_runner_resume.go — runner-agnostic session resume. Lets a follow-up
// (continue_task) or a recurring scheduled run pick up the prior conversation
// instead of starting cold, for every runner — not just claude.
//
// Two pure, unit-tested helpers keep the fragile per-CLI argv shapes in one
// place: resumeTransform (build the resume argv) and parseRawSessionID
// (recover a session id from a raw runner's output). Verified against
// claude 2.1.178, codex 0.135.0, opencode 1.4.0.

import (
	"regexp"
	"strings"
)

// resumeTransform rewrites a runner's freshly-built spawn args so it resumes a
// prior conversation. Returns (args, true) when the runner can resume with the
// given info, or (baseArgs, false) when it can't — the caller then spawns
// fresh.
//
// Per-runner contract:
//   - claude / glm: append `--resume <id>` (needs a captured session id);
//     `--no-session-persistence` is stripped since a non-persisted session
//     can't be resumed. (glm is the claude binary against z.ai.)
//   - opencode: append `--continue` — resumes the most recent session in the
//     working dir, no id needed. Robust for the sequential follow-up /
//     recurring-schedule case.
//   - codex: `exec resume <id>` is a distinct subcommand that does NOT accept
//     `--full-auto`, so the argv is rebuilt from scratch and the equivalent
//     sandbox/approval is restored via the GLOBAL `--sandbox` /
//     `--ask-for-approval` flags. Needs a captured id — never reconstructed
//     blind.
//   - any other runner with ResumeArgs: append the template (needs an id).
func resumeTransform(runner RunnerConfig, baseArgs []string, prompt, workDir, sessionID string) ([]string, bool) {
	switch normalizeRunnerID(runner.RunnerID) {
	case "claude", "glm":
		if sessionID == "" {
			return baseArgs, false
		}
		out := make([]string, 0, len(baseArgs)+2)
		for _, a := range baseArgs {
			if a == "--no-session-persistence" {
				continue
			}
			out = append(out, a)
		}
		out = append(out, "--resume", sessionID)
		return out, true

	case "opencode":
		// --continue resumes the last session in cwd; id-independent.
		return append(append([]string{}, baseArgs...), "--continue"), true

	case "codex":
		if sessionID == "" {
			return baseArgs, false
		}
		// codex --sandbox workspace-write --ask-for-approval on-failure
		// [-C <dir>] exec resume <id> <prompt>. The sandbox/approval globals
		// replicate `exec --full-auto`, which `exec resume` rejects.
		out := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-failure"}
		if strings.TrimSpace(workDir) != "" {
			out = append(out, "-C", workDir)
		}
		out = append(out, "exec", "resume", sessionID, prompt)
		return out, true

	default:
		if sessionID == "" || len(runner.ResumeArgs) == 0 {
			return baseArgs, false
		}
		out := append([]string{}, baseArgs...)
		for _, ra := range runner.ResumeArgs {
			out = append(out, strings.ReplaceAll(ra, "{sessionId}", sessionID))
		}
		return out, true
	}
}

// rawSessionID patterns recover a session id from a raw (non-stream-json)
// output chunk. Best-effort: a miss just means we fall back to id-independent
// resume (opencode --continue) or carry-memo (codex).
var (
	codexSessionIDPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)session[ _]?id["']?\s*[:=]\s*["']?([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`),
		regexp.MustCompile(`(?i)/sessions/[^\s"']*?([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`),
	}
	opencodeSessionIDPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\b(ses_[A-Za-z0-9]{10,})`),
		regexp.MustCompile(`opencode\.ai/s/([A-Za-z0-9]{6,})`),
	}
)

// parseRawSessionID returns a session id found in a raw output chunk for codex
// or opencode, or "" if none. claude/glm capture their id from stream-json and
// never reach here.
func parseRawSessionID(runnerID, text string) string {
	if text == "" {
		return ""
	}
	var pats []*regexp.Regexp
	switch normalizeRunnerID(runnerID) {
	case "codex":
		pats = codexSessionIDPatterns
	case "opencode":
		pats = opencodeSessionIDPatterns
	default:
		return ""
	}
	for _, re := range pats {
		if m := re.FindStringSubmatch(text); len(m) >= 2 {
			return m[1]
		}
	}
	return ""
}
