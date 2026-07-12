package main

// watch_risk.go — the pure, dependency-free logic behind the standalone
// smartwatch turn endpoint (watch_http.go): risk gating, one-sentence
// readback summarization, and complication-intent expansion.
//
// This is the Go mirror of the phone-side TS modules so a watch that is
// signed in WITHOUT a paired phone (standalone LAN / relay mode, see
// docs/yaver-smartwatch-voice-terminal.md §3) gets the SAME safety
// behavior the phone bridge gives in the paired case:
//
//   - carVoiceConfirm.ts   → riskAssessment / watchNeedsConfirm
//   - carVoiceCoding.ts     → summarizeForWatch / isReadCodeRequest
//
// Kept as plain functions with no HTTP/agent coupling so watch_risk_test.go
// can exercise every branch without a server. The wire-facing handler in
// watch_http.go is the only place these meet net/http.

import (
	"regexp"
	"strings"
)

// watchReadbackMaxChars is the hard ceiling on any sentence we hand a
// wrist. Tighter than the car's 200 — a watch face shows ~1-2 short lines.
const watchReadbackMaxChars = 120

type watchPromptPlan struct {
	Mode   string
	Prompt string
}

func buildWatchPrompt(transcript string) watchPromptPlan {
	clean := strings.TrimSpace(transcript)
	mode := classifyWatchPrompt(clean)
	shared := strings.Join([]string{
		"Surface: Apple Watch / smartwatch voice command.",
		"The user is invoking Yaver through STT on a tiny wrist screen and will hear the reply through TTS.",
		"Treat the transcript as an instruction to act, not as normal chat, unless it is clearly a question or idea.",
		"Input may be short, noisy, under-specified, or missing app names; infer from the active Yaver device, current repo, selected project, and recent session when that is safer than asking.",
		"Do not ask for long clarification on the watch; ask only for credentials, irreversible/destructive authorization, payment, CAPTCHA/consent, or truly ambiguous target selection.",
		"Optimize for an async wrist workflow: start useful work, keep detailed reasoning/logs/code on phone/desktop, and make the first/final watch reply one short spoken sentence.",
		"Never put code, diffs, secrets, tokens, file dumps, or long logs in the watch summary.",
	}, " ")
	var instruction string
	switch mode {
	case "implementation":
		instruction = "Treat this as permission to work on the remote runtime after the watch risk gate has passed. Resolve ambiguous app/repo references from current Yaver context before asking; if multiple plausible targets remain, prepare a concise plan and hand the choice to phone. For code changes, make focused edits, run the smallest useful check, leave detailed results in the task output, and summarize the outcome in one sentence for the watch."
	case "browser-automation":
		instruction = "Treat this as a remote browser/runtime automation request. Use visible or auditable browser automation where possible. Stop for login, payment, CAPTCHA, consent, destructive actions, or private data exposure; hand off the exact next step to phone. For read-only browsing, run the session and summarize the result in one watch-safe sentence."
	case "remote-runtime-question":
		instruction = "Treat this as a question for the remote runtime. Inspect the relevant app/session/log/status if available. Answer with a one-sentence watch summary first and put detailed findings on the phone/desktop task output."
	default:
		mode = "idea-capture"
		instruction = "Treat this as idea capture unless the user explicitly asked to implement now. Turn it into a concise product note: app/context guess, user problem, possible feature, acceptance criteria, and next implementation step. Do not edit code unless the transcript explicitly asks to build/change/fix/add."
	}
	return watchPromptPlan{
		Mode:   mode,
		Prompt: shared + "\n\n" + instruction + "\n\nWatch transcript: " + clean,
	}
}

func classifyWatchPrompt(transcript string) string {
	t := " " + strings.ToLower(strings.TrimSpace(transcript)) + " "
	if regexp.MustCompile(`\b(implement|build|make|code|edit|change|add|fix|wire|create|ship|deploy|redeploy|deployment|release|rollout|push|force|reset|delete|remove|destroy|prod|production)\b`).MatchString(t) {
		return "implementation"
	}
	if regexp.MustCompile(`\b(browser|website|site|page|login|search|scrape|click|open|chrome|safari|playwright|selenium)\b`).MatchString(t) {
		return "browser-automation"
	}
	if regexp.MustCompile(`\b(question|ask|check|status|what|why|how|can you|runtime|session|summari[sz]e)\b`).MatchString(t) {
		return "remote-runtime-question"
	}
	if regexp.MustCompile(`\b(idea|remember|note|thought|feature idea|maybe|should we|what if)\b`).MatchString(t) {
		return "idea-capture"
	}
	return "idea-capture"
}

// ── Risk gate ────────────────────────────────────────────────────────
// Coarse on purpose: the point is "stop and ask before something
// destructive/irreversible", not a policy engine. Mirrors RISK_PATTERNS in
// carVoiceConfirm.ts. Word-boundaried so "redeploy"/"deployment" match
// deploy but "deltas" doesn't match "delete".

var watchRiskPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"force", regexp.MustCompile(`\bforce[ -]?push(es|ed|ing)?\b|\b--force\b|\bforce\b`)},
	{"push", regexp.MustCompile(`\b(git )?push(es|ed|ing)?\b`)},
	{"deploy", regexp.MustCompile(`\b(re)?deploy(s|ed|ing|ment)?\b|\bship (it|to)\b|\brelease\b|\brollout\b`)},
	{"delete", regexp.MustCompile(`\b(delete|remove|destroy|drop|wipe|rm)\b|\brm -rf\b`)},
	{"reset", regexp.MustCompile(`\b(reset|revert|rollback|roll back|hard reset)\b`)},
	{"prod", regexp.MustCompile(`\b(prod|production|live|mainnet)\b`)},
}

// watchRiskKinds returns the matched risk categories for a transcript, in
// pattern order, de-duplicated.
func watchRiskKinds(transcript string) []string {
	t := strings.ToLower(transcript)
	var kinds []string
	for _, p := range watchRiskPatterns {
		if p.re.MatchString(t) && !watchContainsStr(kinds, p.kind) {
			kinds = append(kinds, p.kind)
		}
	}
	return kinds
}

// watchNeedsConfirm reports whether a transcript must be explicitly
// confirmed before dispatch.
func watchNeedsConfirm(transcript string) bool {
	return len(watchRiskKinds(transcript)) > 0
}

// watchConfirmPrompt is the one-sentence prompt the wrist shows + speaks
// before a risky dispatch. Names the action so the user knows exactly what
// a tap authorizes.
func watchConfirmPrompt(transcript string) string {
	kinds := watchRiskKinds(transcript)
	if len(kinds) == 0 {
		return ""
	}
	human := map[string]string{
		"deploy": "deploy", "push": "push", "delete": "delete",
		"force": "force-push", "reset": "reset", "prod": "production",
	}
	labels := make([]string, 0, len(kinds))
	for _, k := range kinds {
		labels = append(labels, human[k])
	}
	var label string
	if len(labels) == 1 {
		label = labels[0]
	} else {
		label = strings.Join(labels[:len(labels)-1], ", ") + " / " + labels[len(labels)-1]
	}
	return "That looks like a " + label + " command — confirm to run it."
}

// ── Read-code guard ──────────────────────────────────────────────────
// Mirrors isReadCodeRequest in carVoiceCoding.ts. We never read code, a
// diff, or file contents aloud / onto a watch face — that's a phone job.

var (
	watchReadVerbs    = regexp.MustCompile(`\b(read|show|tell me|what'?s in|recite|dictate)\b`)
	watchReadSubjects = regexp.MustCompile(`\b(the )?(diff|code|file|function|patch|changes|contents?|source|stack ?trace|log|output)\b`)
)

func watchIsReadCodeRequest(transcript string) bool {
	t := strings.ToLower(transcript)
	return watchReadVerbs.MatchString(t) && watchReadSubjects.MatchString(t)
}

// ── Complication intents ─────────────────────────────────────────────
// A watch-face complication sends a fixed {"kind":"intent","intent":...}
// instead of a transcript. Expand the small fixed set into the transcript
// the task pipeline already understands. Unknown intents return "".

func watchIntentToTranscript(intent string) string {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "run-tests", "tests", "test":
		return "run the tests on the primary device and tell me if they pass"
	case "deploy":
		return "deploy" // routed through the risk gate like any deploy
	case "status":
		return "give me a one-line status of the current work"
	default:
		return ""
	}
}

// ── One-sentence readback ────────────────────────────────────────────
// Mirrors summarizeForReadback in carVoiceCoding.ts. Status-keyed lead +
// at most the first clean status-shaped clause of the body; never code;
// clamped to watchReadbackMaxChars.

func summarizeForWatch(status, body string) string {
	var lead string
	switch strings.ToLower(status) {
	case "completed", "finished":
		lead = "Done."
	case "failed":
		lead = "That failed."
	case "stopped":
		lead = "I stopped it."
	case "review":
		lead = "It needs your review."
	default:
		lead = "Finished."
	}
	clause := watchFirstStatusClause(body)
	sentence := lead
	if clause != "" {
		sentence = lead + " " + clause
	}
	return watchClampSentence(sentence)
}

// watchFirstStatusClause pulls the first short, status-shaped clause out of
// an agent result, refusing anything that smells like code or a path dump.
func watchFirstStatusClause(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var firstLine string
	for _, l := range strings.Split(body, "\n") {
		if strings.TrimSpace(l) != "" {
			firstLine = strings.TrimSpace(l)
			break
		}
	}
	if firstLine == "" {
		return ""
	}
	// Refuse code/markup/path-dump shaped lines.
	if regexp.MustCompile("[{}<>;=]|```|\\b(function|const|class|def|import|return)\\b|/\\w+/").MatchString(firstLine) {
		return ""
	}
	// First sentence only.
	clause := firstLine
	if m := regexp.MustCompile(`^(.{1,120}?[.!?])(\s|$)`).FindStringSubmatch(firstLine); m != nil {
		clause = m[1]
	}
	// Strip markdown emphasis/heading markers.
	clause = regexp.MustCompile("[#*`_~]").ReplaceAllString(clause, "")
	return strings.TrimSpace(clause)
}

func watchClampSentence(s string) string {
	s = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
	if len(s) <= watchReadbackMaxChars {
		return s
	}
	return strings.TrimRight(s[:watchReadbackMaxChars-1], " ") + "…"
}

func watchContainsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
