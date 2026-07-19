package main

// autorun_blocked_prompt.go — recognise a runner TUI sitting on a modal prompt
// BEFORE autorun types a task instruction into it.
//
// THE FAILURE THIS EXISTS FOR. autorunTmuxKick sends the instruction with
// send-keys and then polls the pane. send-keys does not care what is on screen.
// When the TUI is showing a modal — a trust dialog, an update nag, a model
// migration — the instruction is typed INTO THE MODAL, consumed as menu input,
// and never reaches the composer. The loop then polls a pane that never goes
// busy, waits out its timeout, and reports the runner "made no changes". The
// run converges as a clean no-op. Nothing in the log says "blocked".
//
// Every incident below is that same shape, and each one cost hours:
//
//   - 2026-07-17 claude's folder-trust dialog on a fresh worktree.
//     --dangerously-skip-permissions does NOT skip it. Pre-accepted in
//     ensureClaudeFolderTrusted.
//   - 2026-07-18 codex's update nag ("Update available"). Pre-dismissed in
//     ensureCodexUpdatePromptDismissed via ~/.codex/version.json.
//   - 2026-07-19 codex's MODEL MIGRATION: "GPT-5.4 is no longer available …
//     Choose how you'd like Codex to proceed." A run sat on this for 1h43m
//     while its peer completed the same task. This one CANNOT be pre-answered
//     the way the other two were: the config key codex writes is
//     [notice.model_migrations] "<old-model>" = "<new-model>", and both names
//     are invented by the vendor at deprecation time. There is no value to
//     pre-seed before you know them. Verified empirically by answering the
//     live prompt and diffing ~/.codex/config.toml.
//
// So pre-answering is necessary but can never be sufficient — the NEXT vendor
// prompt is by definition one nobody has seen. The durable fix is not another
// pre-answer: it is refusing to type into a modal, and failing with the text
// that is on the screen so the operator can answer it in one look.
//
// Detection is deliberately CONTENT-based and conservative. A false positive
// stalls a healthy run, so every signature requires a modal-shaped
// confirmation cue, not just a suggestive word.

import (
	"fmt"
	"strings"
)

// autorunBlockingPrompt describes one recognised modal.
type autorunBlockingPrompt struct {
	// Name is the short id used in logs and errors.
	Name string
	// All of these must be present (case-insensitive) for a match. Requiring
	// several cues is what keeps ordinary runner prose from tripping this.
	Needles []string
	// Remedy names the specific fix, per the "carry the why into the error
	// text" rule — never "check your configuration".
	Remedy string
}

// autorunBlockingPrompts is the signature table. Ordered most-specific first so
// the reported name is the useful one when cues overlap.
var autorunBlockingPrompts = []autorunBlockingPrompt{
	{
		Name:    "codex-model-migration",
		Needles: []string{"no longer available", "codex", "proceed"},
		Remedy: "Codex is waiting on a model-migration choice. Attach and answer it once " +
			"(`tmux attach -t <session>`, pick the new model, Enter) — codex then records it " +
			"under [notice.model_migrations] in ~/.codex/config.toml and will not ask again.",
	},
	{
		Name:    "claude-folder-trust",
		Needles: []string{"do you trust", "folder"},
		Remedy: "Claude's folder-trust dialog is up; --dangerously-skip-permissions does not skip it. " +
			"ensureClaudeFolderTrusted should have pre-accepted this workDir in ~/.claude.json — " +
			"check that it ran and that the path it wrote matches this worktree exactly.",
	},
	{
		Name:    "codex-folder-trust",
		Needles: []string{"allow codex to work", "directory"},
		Remedy: "Codex's directory-trust prompt is up. ensureCodexFolderTrusted appends a " +
			"[projects.\"<path>\"] trust_level = \"trusted\" block to ~/.codex/config.toml — " +
			"verify it ran for this exact absolute path.",
	},
	{
		Name:    "runner-update-nag",
		Needles: []string{"update available", "press enter"},
		Remedy: "The runner is showing an update nag. For codex, ensureCodexUpdatePromptDismissed " +
			"writes dismissed_version into ~/.codex/version.json — confirm it matches the version " +
			"being advertised.",
	},
	{
		Name:    "runner-usage-limit",
		Needles: []string{"usage limit", "reset"},
		Remedy: "The runner's subscription usage limit is exhausted. This needs a human or a wait — " +
			"autorun must not burn iterations against it. Park the run until the quota resets.",
	},
}

// autorunConfirmCues are the modal-shaped cues. A signature match only counts
// when the pane ALSO looks like something is waiting for a keypress, which is
// what separates a real dialog from a runner merely discussing one.
var autorunConfirmCues = []string{
	"press enter to confirm",
	"enter to confirm",
	"use ↑/↓ to move",
	"❯ 1.",
	"› 1.",
	"> 1.",
	"(y/n)",
	"[y/n]",
	"1. yes",
}

// autorunDetectBlockingPrompt reports the modal a runner pane is sitting on, or
// nil when the pane looks like an ordinary working session.
//
// Only the TAIL of the pane is considered. A dialog the runner already answered
// scrolls up and stays in the buffer forever; matching on it would block every
// subsequent turn of a perfectly healthy session.
func autorunDetectBlockingPrompt(pane string) *autorunBlockingPrompt {
	tail := autorunPaneTail(pane, autorunBlockedPromptTailLines)
	if strings.TrimSpace(tail) == "" {
		return nil
	}
	lower := strings.ToLower(tail)

	// The confirmation cue must be at the very BOTTOM, in its own tight window.
	// An ACTIVE modal always renders its menu last — that is what "waiting for
	// a keypress" looks like. Once answered, the runner prints below it and the
	// cue scrolls out of this window while the dialog text is still in
	// scrollback. Searching for the cue over the wide window instead would
	// report every later turn of a healthy session as blocked.
	cueWindow := strings.ToLower(autorunPaneTail(pane, autorunBlockedPromptCueLines))
	if !autorunContainsAny(cueWindow, autorunConfirmCues) {
		return nil
	}
	for i := range autorunBlockingPrompts {
		p := &autorunBlockingPrompts[i]
		matched := true
		for _, needle := range p.Needles {
			if !strings.Contains(lower, strings.ToLower(needle)) {
				matched = false
				break
			}
		}
		if matched {
			return p
		}
	}
	return nil
}

// autorunBlockedPromptTailLines is how much of the pane counts as "on screen
// now". Generous enough to hold a multi-line dialog plus its menu, small enough
// that an answered dialog from earlier in the turn has scrolled out of range.
const autorunBlockedPromptTailLines = 25

// autorunBlockedPromptCueLines is the window the "waiting for a keypress" cue
// must fall inside. Tight on purpose: a live menu is the last thing drawn, so
// anything further up is history. Trailing blank lines are trimmed before the
// tail is taken, so a dialog padded with whitespace still lands inside it.
const autorunBlockedPromptCueLines = 6

func autorunPaneTail(pane string, n int) string {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func autorunContainsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// autorunBlockedPromptError builds the operator-facing failure. It quotes the
// pane tail verbatim: the whole point is that the operator reads the actual
// question instead of going to look for it.
func autorunBlockedPromptError(session string, p *autorunBlockingPrompt, pane string) error {
	return fmt.Errorf(
		"runner TUI session %s is blocked on a modal prompt (%s) — refusing to type the task into it.\n"+
			"REMEDY: %s\n"+
			"--- pane tail ---\n%s\n--- end pane ---",
		session, p.Name, p.Remedy, autorunPaneTail(pane, autorunBlockedPromptTailLines))
}
