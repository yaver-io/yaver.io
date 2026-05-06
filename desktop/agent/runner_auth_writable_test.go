package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCheckWorkDirWritable_CodexNonWritable — codex's bwrap drops
// CAP_DAC_OVERRIDE so a 0555 project dir is hard-failed mid-task with
// `bwrap: Can't create file at <dir>/.codex: Permission denied`. We
// catch it pre-spawn and surface a user-readable error instead.
func TestCheckWorkDirWritable_CodexNonWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("codex sandbox path is Linux/Darwin only")
	}
	if os.Geteuid() == 0 {
		// Root has CAP_DAC_OVERRIDE outside the bwrap sandbox, so a 0555
		// dir is still writable to it. The bug only repros for non-root.
		t.Skip("uid=0 bypasses dir permissions; the bug only repros for unprivileged users")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod 0555: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	codex := builtinRunners["codex"]
	err := checkRunnerWorkDirWritable(codex, dir)
	if err == nil {
		t.Fatalf("expected non-writable error for 0555 dir, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"codex sandbox cannot write",
		dir,                    // surfaces the offending path
		"chown -R",             // surfaces the fix command
		"CAP_DAC_OVERRIDE",     // explains why root in bwrap can't write either
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; got: %s", want, msg)
		}
	}
}

// TestCheckWorkDirWritable_CodexWritableOK — happy path; a normal 0755
// tmp dir owned by the test process must NOT trip the preflight.
func TestCheckWorkDirWritable_CodexWritableOK(t *testing.T) {
	dir := t.TempDir()
	if err := checkRunnerWorkDirWritable(builtinRunners["codex"], dir); err != nil {
		t.Fatalf("expected writable tmp dir to pass; got: %v", err)
	}
}

// TestCheckWorkDirWritable_OtherRunnersUnaffected — claude / opencode
// don't bwrap their writes, so they don't need this preflight. Adding
// it for them would surface false-positive failures on read-only review
// dirs that those runners can still legitimately read+grep through.
func TestCheckWorkDirWritable_OtherRunnersUnaffected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file-mode semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("uid=0 bypasses dir permissions; can't simulate the failure")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod 0555: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	for _, id := range []string{"claude", "opencode"} {
		if err := checkRunnerWorkDirWritable(builtinRunners[id], dir); err != nil {
			t.Errorf("runner %s should not get a writable preflight; got: %v", id, err)
		}
	}
}

// TestCheckWorkDirWritable_EmptyDirSkipped — when the caller didn't
// pin a workDir we let the agent's cwd resolution run; no preflight
// fires. Otherwise we'd block legitimate "no-project" mobile chat
// tasks that don't intend to edit anything.
func TestCheckWorkDirWritable_EmptyDirSkipped(t *testing.T) {
	if err := checkRunnerWorkDirWritable(builtinRunners["codex"], ""); err != nil {
		t.Fatalf("empty workDir should be a no-op; got: %v", err)
	}
	if err := checkRunnerWorkDirWritable(builtinRunners["codex"], "   "); err != nil {
		t.Fatalf("whitespace-only workDir should be a no-op; got: %v", err)
	}
}

// TestCheckWorkDirWritable_NonexistentDirSkipped — a typo / stale path
// is not THIS preflight's concern; downstream cmd.Dir resolution
// surfaces a clearer "no such file or directory" anyway.
func TestCheckWorkDirWritable_NonexistentDirSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := checkRunnerWorkDirWritable(builtinRunners["codex"], dir); err != nil {
		t.Fatalf("nonexistent dir should be skipped, downstream surfaces the real error; got: %v", err)
	}
}

// TestCheckWorkDirWritable_RootCapDacOverrideTrap — when the agent
// runs as root, the host-level write probe lies because root has
// CAP_DAC_OVERRIDE. The probe says "yes, writable", but bwrap (which
// strips the cap) will then fail mid-task on the same dir. The
// secondary owner-based check has to flag it. We can only assert this
// when the test process IS uid 0 — otherwise we're in the non-root
// branch already covered by the earlier test.
func TestCheckWorkDirWritable_RootCapDacOverrideTrap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("codex bwrap path is Linux-only")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires uid=0 to exercise the CAP_DAC_OVERRIDE branch")
	}
	dir := t.TempDir()
	// Make the dir look "owned by someone else" — chown to nobody (uid
	// 65534 on Linux). The dir mode stays 0700 (default tmp), so it's
	// neither group- nor world-writable: bwrap with stripped caps would
	// fail to create files inside.
	if err := os.Chown(dir, 65534, 65534); err != nil {
		t.Skipf("chown to nobody failed (likely sandboxed test runner): %v", err)
	}
	t.Cleanup(func() { _ = os.Chown(dir, 0, 0) })

	err := checkRunnerWorkDirWritable(builtinRunners["codex"], dir)
	if err == nil {
		t.Fatalf("expected CAP_DAC_OVERRIDE-trap error for root agent vs nobody-owned dir, got nil")
	}
	if !strings.Contains(err.Error(), "CAP_DAC_OVERRIDE") {
		t.Errorf("error should explain the cap-drop trap; got: %s", err.Error())
	}
}
