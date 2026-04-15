package main

// autodev_ideas.go — when an autodev run drives a `remained.md`
// checklist to zero, this module spawns a quick Claude call that
// reads recent commits + open TODOs in the repo and appends a fresh
// batch of small unchecked items so the loop keeps going overnight
// instead of exiting early.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// autodevRefillIdeas appends fresh checklist items to p.RemainedFile
// by asking Claude to propose 5 small concrete improvements based on
// the current repo state. Best-effort: if claude isn't installed the
// caller falls back to ending the run.
func autodevRefillIdeas(p autodevPlan) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("`claude` CLI not on PATH")
	}

	wd, _ := os.Getwd()
	prompt := fmt.Sprintf(`The autodev loop has finished every item in this checklist:

    %s

Read the project at %s — recent git log, open TODO/FIXME comments, half-finished code, obvious UX gaps — and append 5 NEW unchecked checklist items to that same file (preserve everything already there).

Format strictly:
- One item per line, starting with "- [ ] " (markdown task syntax).
- Each item: a small, concrete, single-PR-sized improvement (≤1 day of work).
- No headings, no commentary, no code fences, no preamble. Append the lines and stop.

Do not edit any other file. Do not commit.`, p.RemainedFile, wd)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--permission-mode", "acceptEdits",
		"--add-dir", wd,
	)
	cmd.Dir = wd
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = os.Stderr
	// Tee stdout to the user's terminal so the refill is visible in
	// the live stream just like a normal kick.
	cmd.Stdout = io.MultiWriter(os.Stderr) // discard captured copy; we re-read the file

	fmt.Fprintf(os.Stderr, "[autodev] refilling %s via claude…\n", p.RemainedFile)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("claude refill: %w", err)
	}
	return nil
}
