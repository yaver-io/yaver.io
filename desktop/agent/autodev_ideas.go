package main

// autodev_ideas.go — when an autodev run drives a `remained.md`
// checklist to zero, this module spawns a quick Claude call that
// reads recent commits + open TODOs in the repo and appends a fresh
// batch of small unchecked items so the loop keeps going overnight
// instead of exiting early.
//
// We DON'T trust Claude to write the markdown checklist directly —
// it loves to drift into bold-numbered-list style or add commentary.
// Instead we ask for a strict JSON array of titles and write the
// "- [ ] {title}" lines ourselves. Robust regardless of model mood.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const autodevRefillBatchSize = 5

// ifFocus returns s if a non-empty focus prompt was provided, else "".
// Tiny helper that keeps the refill prompt readable when the user
// hasn't given a roof theme.
func ifFocus(focus, s string) string {
	if strings.TrimSpace(focus) == "" {
		return ""
	}
	return s
}

// autodevRefillIdeas appends fresh checklist items to p.RemainedFile.
// Runner-agnostic via RunAIGenerator (claude / codex / aider /
// ollama). Best-effort: returns an error only when nothing usable
// was produced — the caller decides whether to keep going or end
// the run.
func autodevRefillIdeas(p autodevPlan) error {
	wd, _ := os.Getwd()

	// If the user gave the run a focus prompt (--prompt), thread it
	// into the refill so generated ideas stay within their roof
	// theme ("focus on onboarding", "improve checkout flow",
	// "polish settings UX"). Otherwise leave it open-ended.
	focus := strings.TrimSpace(p.Prompt)
	// applyAutodevDefaults synthesises a long instructional prompt
	// from the remained.md template when the user hasn't given an
	// explicit --prompt. That template is autodev plumbing, not a
	// user-chosen roof theme — don't echo it back as a focus area.
	if strings.Contains(focus, "remained.md") || strings.Contains(focus, "- [ ]") || strings.Contains(focus, "- [x]") {
		focus = ""
	}
	focusBlock := ""
	if focus != "" {
		focusBlock = fmt.Sprintf("\nROOF THEME (every item must serve this goal):\n%s\n", focus)
	}

	prompt := fmt.Sprintf(`You are picking the next %d small features / improvements for an overnight autonomous coding loop.

Project root: %s
Existing checklist file (do NOT edit it directly): %s
%s
Read recent git log (git log --oneline -20), open TODO / FIXME / HACK comments, half-finished components, missing tests, broken UX, accessibility gaps, dead code, slow endpoints, and missing features visible in the code itself. Pick the %d best small items%s.

Each item must be:
- single-PR-sized: implementable + testable in under one day
- concrete and specific (file or feature mentioned)
- non-trivial (no whitespace edits, no rename-only)%s

Output ONLY a JSON array of strings — one short imperative title per item, no other text, no code fences, no markdown. Example:

["Wire share button to Share.share() in DealCard.tsx","Translate hardcoded TR strings in PortfolioEmpty.tsx via i18n","Persist tweets to Convex (currently lost on reinstall)"]

Do not write any file. Do not commit. Just print the JSON array and stop.`,
		autodevRefillBatchSize, wd, p.RemainedFile, focusBlock,
		autodevRefillBatchSize,
		ifFocus(focus, " that advance the ROOF THEME above"),
		ifFocus(focus, "\n- aligned with the ROOF THEME (skip anything off-topic, even if it looks valuable)"),
	)

	fmt.Fprintf(os.Stderr, "[autodev] refilling %s…\n", p.RemainedFile)
	body, err := RunAIGenerator(AIGeneratorSpec{
		WorkDir: wd,
		Prompt:  prompt,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("refill: %w", err)
	}

	titles, err := extractRefillTitles(body)
	if err != nil {
		return err
	}
	if len(titles) == 0 {
		return fmt.Errorf("no items extracted from claude output")
	}

	// Append "- [ ] {title}" lines to the checklist. Add a leading
	// blank line if the existing file doesn't end with one, so the
	// new block doesn't merge into the previous text.
	var prefix string
	if existing, err := os.ReadFile(p.RemainedFile); err == nil && len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n\n")) {
		if !bytes.HasSuffix(existing, []byte("\n")) {
			prefix = "\n\n"
		} else {
			prefix = "\n"
		}
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		sb.WriteString("- [ ] ")
		sb.WriteString(t)
		sb.WriteByte('\n')
	}

	f, err := os.OpenFile(p.RemainedFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open remained.md for append: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(sb.String()); err != nil {
		return fmt.Errorf("write remained.md: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[autodev] appended %d new checklist items\n", len(titles))
	return nil
}

// extractRefillTitles finds the last JSON string-array in Claude's
// stdout. We scan from the end so any prose preamble is ignored.
func extractRefillTitles(out string) ([]string, error) {
	// Strip common code-fence wrappers Claude sometimes adds despite
	// being told not to.
	out = strings.ReplaceAll(out, "```json", "```")
	for {
		idx := strings.LastIndex(out, "[")
		if idx < 0 {
			return nil, fmt.Errorf("no JSON array in output")
		}
		// Try increasingly larger candidates ending after each ']'.
		end := strings.Index(out[idx:], "]")
		if end < 0 {
			out = out[:idx]
			continue
		}
		candidate := out[idx : idx+end+1]
		var arr []string
		if err := json.Unmarshal([]byte(candidate), &arr); err == nil && len(arr) > 0 {
			return arr, nil
		}
		// Couldn't parse this one — strip it and look further back.
		out = out[:idx]
	}
}
