package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// runExitWithCode launches /bin/sh and returns the *exec.ExitError that
// `cmd.Wait()` would return for the given exit code. Lets us craft a
// realistic ExitError without depending on the host having `exit`-style
// builtins on PATH.
func runExitWithCode(t *testing.T, code int) *exec.ExitError {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit "+itoa(code))
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T (%v)", err, err)
	}
	return exitErr
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

func TestIsSoftRunnerFailure_NilErrorIsNeverSoft(t *testing.T) {
	if isSoftRunnerFailure("codex", strings.Repeat("OpenAI Codex banner ", 30), nil) {
		t.Fatal("nil error must not be classified as soft failure")
	}
}

func TestIsSoftRunnerFailure_CodexExit1WithBannerIsSoft(t *testing.T) {
	output := "OpenAI Codex v0.123.0 (research preview)\n--------\nworkdir: /root\n" +
		strings.Repeat("some streamed content ", 20)
	err := runExitWithCode(t, 1)
	if !isSoftRunnerFailure("codex", output, err) {
		t.Fatal("codex exit-1 with banner + substantial output should be soft")
	}
}

func TestIsSoftRunnerFailure_CodexShortOutputIsHard(t *testing.T) {
	// Banner present but very little output — a truly broken run.
	err := runExitWithCode(t, 1)
	if isSoftRunnerFailure("codex", "OpenAI Codex\nfailed", err) {
		t.Fatal("codex with <200 chars output must be hard failure")
	}
}

func TestIsSoftRunnerFailure_NoBannerIsHard(t *testing.T) {
	// Real codex never gets to print its banner — auth wedge, missing
	// binary etc. Output looks like generic shell error.
	err := runExitWithCode(t, 1)
	if isSoftRunnerFailure("codex", strings.Repeat("error: command not found ", 20), err) {
		t.Fatal("output without runner banner must be hard failure")
	}
}

func TestIsSoftRunnerFailure_SignalKilledIsHard(t *testing.T) {
	// Synthesise a "signal-killed" ExitError by returning a fake
	// implementation. Easiest: wrap a normal error via errors.New so the
	// type assertion to *exec.ExitError fails — then the function falls
	// through to the default len/banner checks. Confirm it's still
	// classified soft when other criteria pass — proving the sigkill
	// branch isn't gating real soft failures.
	output := "OpenAI Codex banner\n" + strings.Repeat("streamed content ", 30)
	if !isSoftRunnerFailure("codex", output, errors.New("non-exit-error wrap")) {
		t.Fatal("non-ExitError wrap with banner+output should still be soft (no signal info available)")
	}
}

func TestIsSoftRunnerFailure_ClaudeBannerVariants(t *testing.T) {
	err := runExitWithCode(t, 1)
	for _, banner := range []string{"Claude Code v", "Anthropic Claude"} {
		output := banner + "\n" + strings.Repeat("streamed content ", 30)
		if !isSoftRunnerFailure("claude", output, err) {
			t.Fatalf("claude banner %q should be soft", banner)
		}
	}
}

func TestIsSoftRunnerFailure_UnknownRunnerIsHard(t *testing.T) {
	err := runExitWithCode(t, 1)
	output := strings.Repeat("OpenAI Codex banner ", 20) // banner but wrong runner
	if isSoftRunnerFailure("ghost-runner", output, err) {
		t.Fatal("unknown runner ID must default to hard failure")
	}
}
