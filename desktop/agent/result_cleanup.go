package main

import (
	"regexp"
	"strings"
)

// stripPromptEcho removes the noisy preamble that wraps a runner's
// actual answer when it streams stdout verbatim. Two layers, applied
// in order:
//
//  1. Slice after the LAST occurrence of any agent-injected
//     system-context-block end marker. Codex (and any other runner
//     that echoes stdin) repeats the entire `[Yaver Agent Context]` /
//     `[Yaver wrapper capabilities]` / `[AUTOPILOT MODE …]` blocks we
//     append to the prompt. Each marker is the last sentence of one
//     of the `yaver*Context()` raw strings in task_context.go — keep
//     this list in sync if those strings change.
//
//  2. Strip Codex CLI scaffolding around the answer:
//       - leading "Reading additional input from stdin…" hint
//       - "OpenAI Codex vX.Y.Z" banner + the `workdir/model/provider/
//         approval/sandbox/reasoning` config dump that follows it
//         (until the first blank line after the banner block)
//       - trailing "tokens used N" footer
//
// Mirrors the mobile-side `stripPromptEcho` in mobile/app/(tabs)/
// tasks.tsx — we apply the same logic on both ends so:
//   - persisted Task.ResultText (web/MCP/desktop reads) is clean
//   - mobile still works correctly when serving an older agent that
//     hasn't rolled out the cleanup yet
//
// The raw stream is preserved in task.Output so logs/debug still see
// exactly what the runner emitted.
func stripPromptEcho(content string) string {
	if content == "" {
		return content
	}
	out := stripANSI(content)

	// Layer 1: slice after the LAST system-context end marker.
	bestIdx := -1
	for _, marker := range systemContextEndMarkers {
		idx := strings.LastIndex(out, marker)
		if idx >= 0 && idx+len(marker) > bestIdx {
			bestIdx = idx + len(marker)
		}
	}
	if bestIdx > 0 {
		out = out[bestIdx:]
	}

	// Layer 2: Codex CLI scaffolding.
	//
	// Order matters: strip the leading "Reading additional input from
	// stdin…" line FIRST so the banner regex can anchor at start-of-
	// content. With the prefix in place the banner sits on line 2 and
	// our `loc[0] == 0` guard would skip the match.
	out = readingStdinPrefixRE.ReplaceAllString(out, "")

	// Drop everything from the start up to and including the first
	// blank line that follows an "OpenAI Codex vX.Y.Z" banner line.
	// We anchor to start-of-content (loc[0] == 0) so an answer that
	// happens to mention "OpenAI Codex" later isn't mangled.
	if loc := codexBannerBlockRE.FindStringIndex(out); loc != nil && loc[0] == 0 {
		out = out[loc[1]:]
	}

	// Strip trailing "tokens used N" footer.
	out = tokensUsedFooterRE.ReplaceAllString(out, "")

	return strings.TrimSpace(out)
}

// systemContextEndMarkers — last sentence of each agent-injected
// system-context block. KEEP IN SYNC with task_context.go:
//
//	yaverDevServerContext         → "Kill any stale expo/metro processes before retrying."
//	yaverWrapperCapabilityContext → "or related Yaver preview tools instead of asking them to guess."
//	autopilotContext              → "pick up where you left off."
var systemContextEndMarkers = []string{
	"Kill any stale expo/metro processes before retrying.",
	"or related Yaver preview tools instead of asking them to guess.",
	"pick up where you left off.",
}

// codexBannerBlockRE matches the Codex CLI's banner + config dump at
// the very top of the stream. Anchored at start (^); we only strip it
// when it's the leading content. The non-greedy `[\s\S]*?` plus the
// `\n\s*\n` terminator means this stops at the first blank line after
// the banner, leaving the rest of the answer intact.
var codexBannerBlockRE = regexp.MustCompile(`(?m)^[^\n]*?OpenAI Codex v[^\n]*\n(?:[\s\S]*?\n)?\s*\n`)

var readingStdinPrefixRE = regexp.MustCompile(`(?m)^\s*Reading additional input from stdin[.…]*\s*\n?`)

// tokensUsedFooterRE strips the "tokens used N" or "tokens used\nN"
// footer Codex prints after the answer. Case-insensitive; tolerates
// the surrounding whitespace and the optional newline between label
// and number.
var tokensUsedFooterRE = regexp.MustCompile(`(?i)\n*\s*tokens used\s*\n?\s*[\d,]+\s*$`)

// stripANSI removes the most common ANSI/CSI/OSC escape sequences a
// CLI runner can leak into its stdout when it thinks it's writing to
// a real terminal. Same regex shape as the mobile-side stripAnsi in
// tasks.tsx, plus a fallback for the `[1m…[0m` / `[0m` runs that
// survive when ESC was already filtered upstream.
var (
	ansiEscapeRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07`)
	bareCSI_RE   = regexp.MustCompile(`\[\d+(?:;\d+)*m`)
)

func stripANSI(s string) string {
	if s == "" {
		return s
	}
	return bareCSI_RE.ReplaceAllString(ansiEscapeRE.ReplaceAllString(s, ""), "")
}
