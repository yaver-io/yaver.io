package main

// Yaver-action sentinel dispatch: lets a coding-agent runner (claude /
// codex / opencode) signal the orchestrator mid-stream by emitting a
// `<<yaver-action: <verb> <args>>>` line on its own. The runner.go pump
// loop intercepts the sentinel before it lands in the task's stored
// output, parses it, and fires the corresponding side-effect on the
// agent (e.g. broadcast `open_app` to a paired phone for `reload`).
//
// This is what powers the "user types `reload sfmg` in the Tasks chat
// → Hot Reload tab opens on the phone with sfmg already building"
// experience without forcing them to leave the chat.
//
// Why a sentinel and not a tool call: claude-code/codex/opencode all
// emit free-form stdout. They don't share an MCP/tool-call protocol the
// orchestrator can subscribe to uniformly. A printable line that's
// trivially both parseable and human-readable in raw logs is the
// cheapest cross-runner channel — and it survives prompt caching so
// the LLM's response text remains identical across re-runs.

import (
	"log"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// activeBlackboxMgr is the global pointer the runner pump reaches for
// when it sees a sentinel. main.go sets it after the BlackBoxManager
// boots; if the pump runs before init (or in a unit test where blackbox
// isn't wired) the dispatch is a silent no-op. Atomic so registration
// vs concurrent pump goroutines don't race.
var activeBlackboxMgr atomic.Pointer[BlackBoxManager]

// SetActiveBlackboxMgr registers the manager that DispatchYaverAction
// will broadcast through. Idempotent — main.go calls it once after the
// agent finishes wiring the HTTP server.
func SetActiveBlackboxMgr(mgr *BlackBoxManager) {
	activeBlackboxMgr.Store(mgr)
}

// yaverActionSentinelRE matches a sentinel line. Matches:
//
//	<<yaver-action: reload sfmg>>
//	<<yaver-action: reload carrotbet >>
//	  <<yaver-action: reload TodoKt>>     (allow leading whitespace)
//
// Args are everything after the verb up to the closing `>>`, trimmed.
// Verb is one word; further verbs (e.g. `serve`, `stop`) can plug in
// without changing the regex.
var yaverActionSentinelRE = regexp.MustCompile(`(?i)^\s*<<yaver-action:\s*([a-z][a-z0-9_-]*)(?:\s+([^>]+?))?\s*>>\s*$`)

// ParseYaverActionSentinel returns (verb, args, true) if the line is a
// sentinel, otherwise ("", "", false). The args string is whatever the
// runner placed between the verb and `>>`, trimmed but not split — the
// dispatcher decides how to interpret it.
func ParseYaverActionSentinel(line string) (string, string, bool) {
	m := yaverActionSentinelRE.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), strings.TrimSpace(m[2]), true
}

// DispatchYaverAction handles a sentinel that ParseYaverActionSentinel
// already validated. Side effects only — never blocks the runner pump
// for longer than the broadcast (BroadcastCommand is non-blocking, it
// drops on slow listeners).
//
// taskID is logged for traceability so a confused user can grep logs
// for which task fired the open_app on their phone.
func DispatchYaverAction(verb, args, taskID string) {
	mgr := activeBlackboxMgr.Load()
	if mgr == nil {
		log.Printf("[yaver-action] task %s emitted <<yaver-action: %s %s>> but no BlackBoxManager registered — ignoring",
			taskID, verb, args)
		return
	}
	switch verb {
	case "reload", "open", "serve":
		// All three verbs map to the same mobile flow today: the phone
		// navigates to Hot Reload and triggers `/dev/build-native`.
		// `serve` is a hint that the user wants the dev server up
		// (which open_app does anyway as part of its handshake).
		slug := strings.TrimSpace(args)
		if slug == "" {
			log.Printf("[yaver-action] task %s emitted <<yaver-action: %s>> with empty slug — ignoring", taskID, verb)
			return
		}
		// Strip stray quotes the LLM might wrap the slug in.
		slug = strings.Trim(slug, "\"' `")
		cmd := BlackBoxCommand{
			Command: "open_app",
			Data: map[string]interface{}{
				"app":    slug,
				"reason": "yaver-action:" + verb,
				"taskId": taskID,
				"sentAt": time.Now().UnixMilli(),
			},
		}
		mgr.BroadcastCommand(cmd)
		log.Printf("[yaver-action] task %s broadcast open_app{app=%s} via <<yaver-action: %s>>", taskID, slug, verb)
	default:
		log.Printf("[yaver-action] task %s emitted unknown verb %q — ignoring", taskID, verb)
	}
}

// YaverActionSystemPrompt is the instruction we splice into every
// runner's system context so the LLM knows it can emit sentinels. Kept
// short (sub-100 tokens) so the cost across millions of tasks stays
// negligible. The wording is deliberately permissive — "if you detect"
// — so the LLM doesn't false-fire on tangentially-related prompts.
//
// The instruction omits less-load-bearing examples on purpose; keeping
// it terse makes it less likely to be paraphrased away by aggressive
// system-prompt summarisation in claude-code's context manager.
const YaverActionSystemPrompt = `Yaver orchestration: this conversation is running inside Yaver, which can talk directly to the user's paired mobile phone. If the user asks to reload, open, or serve a project on their phone (e.g. "reload sfmg", "open carrotbet on phone"), emit exactly one line in your reply:

<<yaver-action: reload <slug>>>

Replace <slug> with the project name the user actually said. Do NOT emit the sentinel for unrelated requests, and do NOT invent slugs. The orchestrator parses this line and triggers the phone-side reload — you do not need to start the dev server yourself.`
