package main

import "strings"

// Ask-intent auto-detection for console surfaces.
//
// In a yaver console (the `yaver code` / attach terminal, the web console,
// mobile code-mode) a typed line normally becomes a *work* task — the runner
// acts on it. But when the user types a natural-language QUESTION ("how do I
// test STT/TTS?", "where does auth get wired?") and none of the words are a
// yaver command / verb / tool-call, the intent is to understand, not to
// change anything. detectAskIntent() spots that case so the console can route
// it through ask mode (askModePreamble: deep grounded analysis, file:line
// cites, explain-first with a confirm gate) instead of a work run.
//
// The classifier is deliberately HIGH PRECISION, like the soft-question
// fallback (agent_question_fallback.go): a false positive sends a genuine
// work instruction down the read-only explain path, which is annoying, so we
// only flip to ask when there is a clear question signal AND the line is not
// a command. Imperative work prose ("add a dark-mode toggle", "refactor auth")
// stays a work task — it is not a question.

// consoleSourcesForAskDetection are the free-typing console surfaces where a
// plain prose question should auto-route to ask mode. Structured surfaces
// (mobile feedback, vibing, guest) are intentionally excluded — their input
// is already shaped by the UI, not free console text.
var consoleSourcesForAskDetection = map[string]bool{
	terminalLocalTaskSource:  true,
	terminalRemoteTaskSource: true,
	"cli":                    true,
	"console":                true,
	"connect":                true,
	"attach":                 true,
	"mobile-code":            true,
}

func isConsoleAskSource(source string) bool {
	return consoleSourcesForAskDetection[strings.TrimSpace(source)]
}

// askQuestionStarters are first words that, on an otherwise non-command line,
// signal a question rather than an instruction.
var askQuestionStarters = map[string]bool{
	"how": true, "what": true, "whats": true, "what's": true,
	"why": true, "where": true, "when": true, "who": true, "which": true,
	"can": true, "could": true, "should": true, "would": true,
	"does": true, "do": true, "is": true, "are": true,
	"explain": true, "describe": true,
}

// askQuestionPhrases are multi-word lead-ins that signal a question even when
// the first token alone is ambiguous.
var askQuestionPhrases = []string{
	"how do i", "how would i", "how can i", "how to", "how does",
	"what is", "what's the", "what are", "what does", "what happens",
	"where is", "where do", "where does", "why is", "why does", "why do",
	"is there", "are there", "can i", "tell me", "walk me through",
}

// detectAskIntent reports whether a console line is a natural-language
// question (an "ask case") rather than a command / tool-call / work
// instruction. It is conservative: when in doubt, it returns false and the
// caller treats the line as a normal task.
func detectAskIntent(input string) bool {
	line := strings.TrimSpace(input)
	if line == "" {
		return false
	}

	// Explicit command sigils — never an ask.
	switch line[0] {
	case '/', '$', '!', '-':
		return false
	}

	fields := strings.Fields(line)
	// Single token is a name / verb / command, not a question.
	if len(fields) < 2 {
		return false
	}

	// If the line references any yaver verb / tool name, it's a command the
	// user is driving, not a question about the repo.
	if lineMentionsYaverVerb(fields) {
		return false
	}

	lower := strings.ToLower(line)

	// Ends with a question mark → question.
	if strings.HasSuffix(strings.TrimRight(lower, " \t"), "?") {
		return true
	}

	// Multi-word lead-in phrase → question.
	for _, p := range askQuestionPhrases {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}

	// First word is a question word → question (e.g. "how do i test stt tts").
	first := strings.ToLower(fields[0])
	if askQuestionStarters[first] {
		return true
	}

	return false
}

// askBreadthSignals are phrases that mark a question as broad / architectural
// / cross-cutting — the kind that benefits from a multi-agent graph
// (investigate → answer → verify) rather than a single read-only pass.
var askBreadthSignals = []string{
	"architecture", "architect", "end to end",
	"end-to-end", "across", "data flow", "control flow", "lifecycle",
	"wired", "wire up", "wired up", "pipeline", "interact", "overall",
	"entire", "whole system", "trace", "why does", "why do",
	"what happens when", "difference between", "compare", "relationship",
	"big picture", "from scratch", "step by step", "all the ways",
}

// detectAskBreadth reports whether an ask-case question is broad enough to
// warrant escalating from a single read-only agent to a multi-agent graph
// (investigate → answer → verify). Narrow lookups ("where is X defined?")
// stay single-agent; sweeping ones ("how does auth work end to end?") escalate.
// Conservative: defaults to false so most questions take the cheap path.
func detectAskBreadth(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return false
	}
	for _, sig := range askBreadthSignals {
		if strings.Contains(q, sig) {
			return true
		}
	}
	// Long, multi-clause questions tend to be architectural even without a
	// keyword hit.
	if len(strings.Fields(q)) >= 16 {
		return true
	}
	return false
}

// lineMentionsYaverVerb reports whether any token is a known yaver command /
// ops verb / MCP tool name (with or without an underscore-to-hyphen swap), so
// command-driving lines like "run cloud_deploy now" are not mistaken for
// questions. The catalog is sourced from the live ops verb list when
// available, falling back to a small set of unmistakable command verbs.
func lineMentionsYaverVerb(fields []string) bool {
	for _, raw := range fields {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" {
			continue
		}
		if knownYaverVerbSet()[t] {
			return true
		}
		// underscore/hyphen normalization (cloud-deploy ~ cloud_deploy)
		if knownYaverVerbSet()[strings.ReplaceAll(t, "-", "_")] {
			return true
		}
	}
	return false
}

// knownYaverVerbSet is the lazily-built lookup of command/verb tokens that
// disqualify a line from being an "ask". Kept small + unmistakable: these are
// tokens that, when present, mean the user is invoking yaver, not asking
// about it. (A broad MCP-tool dump would over-match common English words like
// "ping" / "say" / "news", so we curate.)
var knownYaverVerbCache map[string]bool

func knownYaverVerbSet() map[string]bool {
	if knownYaverVerbCache != nil {
		return knownYaverVerbCache
	}
	set := map[string]bool{}
	for _, v := range []string{
		"yaver", "ops", "create_task", "yaver_ask", "exec_command",
		"cloud_deploy", "cloud_destroy", "deploy_run", "git_push",
		"build_ios", "build_android", "native_build", "wire_push",
		"wireless_push", "convex_deploy", "cf_deploy", "switch_run",
		"vault", "serve",
	} {
		set[v] = true
	}
	knownYaverVerbCache = set
	return set
}
