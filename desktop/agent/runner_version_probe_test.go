package main

import (
	"testing"
)

// The probe must trust the ANSWER, not the exit.
//
// Reproduces autorun-1784281562998711000 on the mini, which died on
// `opencode found but not working: signal: killed (output: 1.14.41)` at exactly
// the 10s probe deadline. opencode had printed its version and simply not exited;
// exec.CommandContext SIGKILLed it on deadline and the probe declared a working
// binary broken, taking a whole autorun with it.
//
// looksLikeRunnerVersion is the predicate that decides "did it answer?", so it
// carries the rule: real output with a digit is an answer; a banner, a stack
// trace, or silence is not.

func TestLooksLikeRunnerVersionAcceptsRealVersions(t *testing.T) {
	// The literal output from the mini failure, plus the shapes the four
	// supported runners actually print.
	for _, out := range []string{
		"1.14.41",
		"  1.14.41\n",
		"opencode 1.14.41",
		"codex-cli 0.123.0",
		"claude 2.0.1 (build 4)",
	} {
		if !looksLikeRunnerVersion([]byte(out)) {
			t.Errorf("a runner that printed %q ANSWERED; the probe must treat it as ready", out)
		}
	}
}

func TestLooksLikeRunnerVersionRejectsNonAnswers(t *testing.T) {
	// "It wrote something before we killed it" is not health. A hung binary that
	// emitted a banner or a panic must still be reported broken, or the fallback
	// never fires and the run dies later and more confusingly.
	for _, out := range []string{
		"",
		"   ",
		"\n\n",
		"panic: runtime error",              // crashed, no version
		"Welcome to the interactive shell!", // TUI banner, hung before answering
	} {
		if looksLikeRunnerVersion([]byte(out)) {
			t.Errorf("output %q is not a version answer; the probe must not call it ready", out)
		}
	}
}
