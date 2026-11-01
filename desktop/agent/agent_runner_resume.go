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
//   - claude: append `--resume <id>` (needs a captured session id);
//     `--no-session-persistence` is stripped since a non-persisted session
//     can't be resumed.
//   - opencode: append `--session <id>` — resumes the exact task session.
//     `--continue` is deliberately forbidden: it means "most recent in this
//     directory", so two tasks in one repo can cross-wire conversations.
//   - codex: `exec resume <id>` is a distinct subcommand that does NOT accept
//     `--full-auto`, so the argv is rebuilt from scratch and the equivalent
//     sandbox/approval is restored via the GLOBAL `--sandbox` /
//     `--ask-for-approval` flags. Needs a captured id — never reconstructed
//     blind.
//   - any other runner with ResumeArgs: append the template (needs an id).
func resumeTransform(runner RunnerConfig, baseArgs []string, prompt, workDir, sessionID string) ([]string, bool) {
	// Single oracle, shared with the prompt composer — see
	// resumeCanCarryContext. The argv we build and the decision to re-arm the
	// Yaver preamble MUST agree, or a cold process gets a bare follow-up and
	// no briefing.
	if !resumeCanCarryContext(runner, sessionID) {
		return baseArgs, false
	}
	switch normalizeRunnerID(runner.RunnerID) {
	case "opencode", "remoteless":
		return append(append([]string{}, baseArgs...), "--session", sessionID), true

	case "codex":
		// codex --dangerously-bypass-approvals-and-sandbox
		// [-C <dir>] exec resume <id> <prompt>. The sandbox/approval globals
		// replicate `exec --full-auto`, which `exec resume` rejects.
		out := []string{"--dangerously-bypass-approvals-and-sandbox"}
		if strings.TrimSpace(workDir) != "" {
			out = append(out, "-C", workDir)
		}
		out = append(out, "exec", "resume")
		// Preserve Codex's clean assistant-message boundary on follow-ups.
		// `exec resume --help` exposes -o/--output-last-message at the resume
		// subcommand level, so it belongs after `resume` and before the id.
		for i, arg := range baseArgs {
			if arg == "--output-last-message" && i+1 < len(baseArgs) {
				out = append(out, arg, baseArgs[i+1])
				break
			}
		}
		out = append(out, sessionID, prompt)
		return out, true

	default:
		out := append([]string{}, baseArgs...)
		for _, ra := range runner.ResumeArgs {
			out = append(out, strings.ReplaceAll(ra, "{sessionId}", sessionID))
		}
		return out, true
	}
}

// applyResumeRunnerSelection carries the task's typed `/model` choice into the
// operation that actually runs the next turn. Persisting Task.Model alone is a
// false green: all three first-class CLIs resume an existing conversation in a
// new process, and that process otherwise keeps the old session model.
//
// Verified against the real local CLIs (2026-08-31):
//   - codex 0.147.0: `codex exec resume -m … -c model_reasoning_effort=…`
//   - claude 2.1.233: `claude --resume … --model …`
//   - opencode 1.18.25: `opencode run --session … --model provider/model`
func applyResumeRunnerSelection(runnerID string, args []string, model, reasoningEffort string) []string {
	out := append([]string(nil), args...)
	model = strings.TrimSpace(model)
	if model != "" {
		replaced := false
		for i := 0; i+1 < len(out); i++ {
			if out[i] == "--model" || out[i] == "-m" {
				out[i+1] = model
				replaced = true
				break
			}
		}
		if !replaced {
			switch normalizeRunnerID(runnerID) {
			case "codex":
				out = insertRunnerFlagAfter(out, "resume", "--model", model)
			case "opencode", "remoteless":
				out = insertRunnerFlagAfter(out, "run", "--model", model)
			default:
				// Claude accepts model as a command-level option. Prepending keeps
				// it outside the `-p <prompt>` value and before `--resume`.
				out = append([]string{"--model", model}, out...)
			}
		}
	}

	if normalizeRunnerID(runnerID) == "codex" {
		if effort := normalizeCodexReasoningEffort(reasoningEffort); effort != "" {
			value := "model_reasoning_effort=\"" + effort + "\""
			replaced := false
			for i := 0; i+1 < len(out); i++ {
				if (out[i] == "--config" || out[i] == "-c") && strings.HasPrefix(out[i+1], "model_reasoning_effort=") {
					out[i+1] = value
					replaced = true
					break
				}
			}
			if !replaced {
				out = insertRunnerFlagAfter(out, "resume", "--config", value)
			}
		}
	}
	return out
}

// resumeCanCarryContext answers the one question that decides both the resume
// argv AND whether a follow-up must re-arm the Yaver preamble: will the process
// we are about to spawn still hold the earlier turns of this conversation?
//
// It is deliberately a property of the RUNNER + what we captured, not of the
// UI. A phone typing a second message looks identical either way; the
// difference is whether `claude --resume <id>` has an id to resume, and only
// this layer knows that. When it returns false the next process starts COLD —
// the same state as a crash restart, a runner switch, or a fork — and a cold
// runner that never read the preamble does not know it is inside Yaver.
//
// Per runner:
//   - claude / codex: resume is id-addressed. No captured id, no context.
//   - opencode: resume is also id-addressed (`run --session <id>`). The prior
//     `--continue` fallback could attach to another task's newest session when
//     two tasks shared a work directory, so no id now means no continuation.
//   - any other runner: needs both a captured id and a ResumeArgs template.
func resumeCanCarryContext(runner RunnerConfig, sessionID string) bool {
	switch normalizeRunnerID(runner.RunnerID) {
	// claude was removed in chat-only phase 3 (see docs/CHAT_ONLY.md). Leaving
	// it in this list made resumeTransform's default arm append the (absent)
	// ResumeArgs template and still report ok=true, i.e. claim a resume it
	// could not perform.
	case "codex", "opencode", "remoteless":
		return strings.TrimSpace(sessionID) != ""
	default:
		return strings.TrimSpace(sessionID) != "" && len(runner.ResumeArgs) > 0
	}
}

// rawSessionID patterns recover a session id from a raw (non-stream-json)
// output chunk. A miss means a future continuation is refused rather than
// guessed; "most recent" is not a task identity when runs share a directory.
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
// or opencode, or "" if none. claude captures its id from stream-json and
// never reach here.
func parseRawSessionID(runnerID, text string) string {
	if text == "" {
		return ""
	}
	var pats []*regexp.Regexp
	switch normalizeRunnerID(runnerID) {
	case "codex":
		pats = codexSessionIDPatterns
	case "opencode", "remoteless":
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
