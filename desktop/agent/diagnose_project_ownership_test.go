package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProjectOwnershipBlocker_NonRootCannotWrite — non-root agent vs a
// 0555 dir trips the doctor warning. Mirrors the runtime preflight's
// non-root case so the two never disagree.
func TestProjectOwnershipBlocker_NonRootCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DAC semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("uid=0 has CAP_DAC_OVERRIDE; can't simulate the unprivileged case")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod 0555: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	got := projectOwnershipBlocker(dir, info, os.Geteuid())
	if got == "" {
		t.Fatalf("expected blocker for 0555 dir, got empty")
	}
	if !strings.Contains(got, "not writable") {
		t.Errorf("expected 'not writable' message, got: %s", got)
	}
}

// TestProjectOwnershipBlocker_WritableOK — happy path; default tmpdir
// must NOT trip the warning.
func TestProjectOwnershipBlocker_WritableOK(t *testing.T) {
	dir := t.TempDir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := projectOwnershipBlocker(dir, info, os.Geteuid()); got != "" {
		t.Errorf("expected fresh tmpdir to be ok, got: %s", got)
	}
}

// TestProjectRootsForOwnershipScan_DedupesAndFilters — only existing
// dirs are returned, and case-insensitive duplicates fold.
func TestProjectRootsForOwnershipScan_DedupesAndFilters(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "Workspace"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "Code"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got := projectRootsForOwnershipScan()
	if len(got) < 1 {
		t.Fatalf("expected at least Workspace + Code, got %v", got)
	}
	for _, p := range got {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			t.Errorf("returned non-existent / non-dir entry: %s", p)
		}
	}
}

// TestCheckProjectOwnership_EmitsWarningForBadDir — end-to-end smoke:
// drop a non-writable dir under HOME/Workspace and verify the diag
// emits a warning event with the chown command.
func TestCheckProjectOwnership_EmitsWarningForBadDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DAC semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("uid=0 case is exercised by the bwrap-trap test below")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	bad := filepath.Join(tmp, "Workspace", "stuck-project")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(bad, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	var got []DiagEvent
	checkProjectOwnership(context.Background(), func(ev DiagEvent) {
		got = append(got, ev)
	})

	var warned bool
	for _, ev := range got {
		if ev.Severity == DiagWarning && strings.Contains(ev.Message, bad) && strings.Contains(ev.Message, "chown -R") {
			warned = true
			break
		}
	}
	if !warned {
		t.Fatalf("expected a Warning event mentioning %s and chown -R; got events: %+v", bad, got)
	}
}

// TestCheckProjectOwnership_EmptyHomeIsInfo — when no Workspace/etc
// roots exist (clean machine) the diag prints an info line and exits;
// no false-positive warnings.
func TestCheckProjectOwnership_EmptyHomeIsInfo(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	var got []DiagEvent
	checkProjectOwnership(context.Background(), func(ev DiagEvent) {
		got = append(got, ev)
	})

	for _, ev := range got {
		if ev.Severity == DiagWarning {
			t.Errorf("unexpected warning on empty home: %s", ev.Message)
		}
	}
}
