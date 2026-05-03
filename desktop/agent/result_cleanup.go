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

	// Strip every "tokens used N" footer (mid-stream too — see
	// tokensUsedFooterRE comment).
	out = tokensUsedFooterRE.ReplaceAllString(out, "\n\n")

	out = dedupeCodexEchoes(out)

	return strings.TrimSpace(out)
}

// dedupeCodexEchoes collapses the redundant blocks codex 0.123.0 prints
// for a single command. For "Run ls" against /root the raw stream has
// the same listing three times: once as the exec output rows
// (after `succeeded in Xms:`), then twice as identical fenced markdown
// blocks (a codex bug where the final answer is emitted twice). We keep
// one structured copy and reduce the exec announcement to a `$ <cmd>`
// header so users still see what was run.
//
// Mirrors the JS dedupeCodexEchoes in mobile/app/(tabs)/tasks.tsx —
// keep both in sync.
func dedupeCodexEchoes(s string) string {
	// (1) Replace `exec\n<cmd>\n succeeded in Xms:\n<rows>` with a
	// `**$ <cmd>**` line. Stops at the next blank line or `\ncodex\n`
	// section marker. The rows are almost always echoed inside the
	// fenced block of codex's final answer.
	s = codexExecBlockRE.ReplaceAllStringFunc(s, func(match string) string {
		sub := codexExecBlockRE.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		cmd := strings.TrimSpace(sub[1])
		terminator := sub[2]
		return "\n**$ " + cmd + "**\n" + terminator
	})
	// (2) Strip lone `codex` section markers left over from
	// ANSI-coloured `[codex]` headers — they add no signal once the
	// body text follows.
	s = codexSectionMarkerRE.ReplaceAllString(s, "$1")
	// (3) Collapse two consecutive identical fenced code blocks
	// (codex's duplicate-message bug). Backreferences need RE2 — Go's
	// regexp engine doesn't support `\1`, so we do this manually.
	s = collapseConsecutiveFences(s)
	// (4) Collapse a "<lead-in>:\n\n```fenced```" pair that repeats
	// verbatim (e.g. "Here is the ls output … ```…``` Here is the ls
	// output … ```…```").
	s = collapseRepeatedLeadInFence(s)
	return s
}

var (
	// Match `exec\n<cmd>\n succeeded in Xms:\n<rows>` and capture the
	// terminator (the next blank line, the `\ncodex\n` section header,
	// or end-of-string). RE2 has no lookahead, so we capture-and-restore
	// the terminator in the replacement function above.
	codexExecBlockRE = regexp.MustCompile(
		`(?s)\n?exec\n([^\n]+?)(?:\s+in\s+[^\n]+)?\n\s*succeeded in [\d.]+\s*m?s:\n.*?(\ncodex\n|\n\n|\z)`,
	)
	codexSectionMarkerRE = regexp.MustCompile(`(^|\n)codex\n`)
	fencedBlockRE        = regexp.MustCompile("(?s)```[^\\n]*\\n.*?\\n```")
)

// collapseConsecutiveFences finds adjacent identical fenced code blocks
// (separated only by whitespace) and drops every duplicate after the
// first. Implemented manually because Go's RE2 lacks backreferences.
func collapseConsecutiveFences(s string) string {
	matches := fencedBlockRE.FindAllStringIndex(s, -1)
	if len(matches) < 2 {
		return s
	}
	var b strings.Builder
	last := 0
	skip := make(map[int]bool)
	for i := 0; i < len(matches)-1; i++ {
		if skip[i] {
			continue
		}
		a := matches[i]
		nb := matches[i+1]
		gap := s[a[1]:nb[0]]
		if strings.TrimSpace(gap) != "" {
			continue
		}
		if s[a[0]:a[1]] != s[nb[0]:nb[1]] {
			continue
		}
		// Emit content up to end of first fence, drop the gap and the
		// duplicate fence.
		b.WriteString(s[last:a[1]])
		last = nb[1]
		skip[i+1] = true
	}
	b.WriteString(s[last:])
	return b.String()
}

// collapseRepeatedLeadInFence collapses adjacent "<line>:\n\n```fenced```"
// pairs whose preamble + fence are byte-identical.
func collapseRepeatedLeadInFence(s string) string {
	// Iterate fenced blocks; for each pair with matching preambles, drop
	// the second pair entirely.
	fences := fencedBlockRE.FindAllStringIndex(s, -1)
	if len(fences) < 2 {
		return s
	}
	type pair struct{ start, end int }
	var pairs []pair
	for _, f := range fences {
		// Walk back to find preceding "<line>:\n\n" preamble.
		preStart := f[0]
		for preStart > 0 && s[preStart-1] == '\n' {
			preStart--
		}
		// Walk to start of the line (find previous newline before any
		// non-newline char).
		lineStart := preStart
		for lineStart > 0 && s[lineStart-1] != '\n' {
			lineStart--
		}
		// The preamble is s[lineStart:preStart] — must end with ":".
		preamble := s[lineStart:preStart]
		if !strings.HasSuffix(strings.TrimSpace(preamble), ":") {
			pairs = append(pairs, pair{f[0], f[1]})
			continue
		}
		pairs = append(pairs, pair{lineStart, f[1]})
	}
	var b strings.Builder
	last := 0
	skip := make(map[int]bool)
	for i := 0; i < len(pairs)-1; i++ {
		if skip[i] {
			continue
		}
		a := pairs[i]
		nb := pairs[i+1]
		gap := s[a.end:nb.start]
		if strings.TrimSpace(gap) != "" {
			continue
		}
		if s[a.start:a.end] != s[nb.start:nb.end] {
			continue
		}
		b.WriteString(s[last:a.end])
		last = nb.end
		skip[i+1] = true
	}
	b.WriteString(s[last:])
	return b.String()
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

// tokensUsedFooterRE strips every "tokens used N" / "tokens used\nN"
// footer Codex emits — not just the trailing one. Codex 0.123.0
// frequently prints its final answer twice with this footer wedged
// between the two copies. If the mid-stream footer survives, the two
// answer blocks aren't adjacent and dedupeCodexEchoes can't collapse
// them, so the listing renders twice on the phone. Case-insensitive;
// tolerates surrounding whitespace and the optional newline between
// label and number.
var tokensUsedFooterRE = regexp.MustCompile(`(?i)\n*\s*tokens used\s*\n?\s*[\d,]+\s*`)

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
