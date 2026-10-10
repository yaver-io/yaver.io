package main

// installVersionedAgentBinary_test.go — Snowball proof for the 2026-08-09
// auto-update poisoning incident.
//
// The incident: auto-update replaced the binary behind ~/.yaver/bin/current
// IN PLACE while the agent ran from it. macOS 26's code-signing monitor then
// SIGKILLed every later exec of that path ("Code Signature Invalid", exit
// 137) even though the bytes were valid — the box's own restarts and every
// runner MCP handshake ("connection closed") died until `current` was
// repointed at a fresh dir.
//
// The guard: installVersionedAgentBinary must (1) never touch the running
// binary's file, (2) exec-probe the NEW path before committing, and
// (3) atomically repoint `current` at the new versioned dir. These tests
// prove all three, including the failure path (a binary the kernel would
// reject must abort the update with `current` untouched).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary writes a tiny executable that behaves like `yaver version` and
// completes one MCP initialize request.
// good=true → both operations succeed (a usable coding-surface binary).
// good=false → exits 1 (the shape of a kernel-rejected/poisoned binary).
func fakeBinary(t *testing.T, path, version string, good bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	body := "#!/bin/sh\n"
	if good {
		body += "case \"${1:-}\" in\n"
		body += "  version) echo \"yaver " + version + "\" ;;\n"
		body += "  mcp) read -r request; echo '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"serverInfo\":{\"name\":\"yaver\",\"version\":\"" + version + "\"}}}' ;;\n"
		body += "  *) exit 2 ;;\n"
		body += "esac\n"
	} else {
		body += "exit 1\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary %s: %v", path, err)
	}
}

// setupHome creates a fake HOME with an existing versioned install:
//
//	$HOME/.yaver/bin/1.99.100/darwin-arm64/yaver   (the "running" binary)
//	$HOME/.yaver/bin/current -> 1.99.100            (the stale current link)
//
// Returns the dir, the running binary path, and a cleanup.
func setupHome(t *testing.T) (home, runningBin string) {
	t.Helper()
	home = t.TempDir()
	platform := runtime.GOOS + "-" + runtime.GOARCH
	oldDir := filepath.Join(home, ".yaver", "bin", "1.99.100")
	runningBin = filepath.Join(oldDir, platform, "yaver")
	fakeBinary(t, runningBin, "1.99.100", true)
	if err := os.Symlink(oldDir, filepath.Join(home, ".yaver", "bin", "current")); err != nil {
		t.Fatalf("symlink current: %v", err)
	}
	// installVersionedAgentBinary resolves the home via os.UserHomeDir(),
	// which honours $HOME on unix — redirect it into the sandbox.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows fallback, harmless elsewhere
	return home, runningBin
}

// TestInstallVersionedNeverTouchesRunningBinary is the Snowball break-it
// proof: after a successful update, the file the agent was running from must
// be BYTE-IDENTICAL to what it was before. Pre-fix (os.Rename over
// os.Executable()), this test fails — the running file was clobbered.
func TestInstallVersionedNeverTouchesRunningBinary(t *testing.T) {
	home, runningBin := setupHome(t)
	before, err := os.ReadFile(runningBin)
	if err != nil {
		t.Fatalf("read running binary before: %v", err)
	}

	// A freshly downloaded tarball lands at a .update temp path (as the real
	// downloader writes), then installVersionedAgentBinary places it.
	tmpPath := filepath.Join(home, "yaver.update")
	fakeBinary(t, tmpPath, "1.99.999", true)

	installPath, _, err := installVersionedAgentBinary(runningBin, tmpPath, "1.99.999")
	if err != nil {
		t.Fatalf("installVersionedAgentBinary: %v", err)
	}
	if installPath == runningBin {
		t.Fatal("install path must be a NEW versioned dir, never the running binary path")
	}

	after, err := os.ReadFile(runningBin)
	if err != nil {
		t.Fatalf("read running binary after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("RUNNING BINARY WAS MODIFIED — this is the exact poisoning bug. before=%q after=%q", before, after)
	}

	// `current` must now point at the new version dir.
	cur, err := os.Readlink(filepath.Join(home, ".yaver", "bin", "current"))
	if err != nil {
		t.Fatalf("readlink current: %v", err)
	}
	if !strings.Contains(cur, "1.99.999") {
		t.Fatalf("current not repointed to new version: %q", cur)
	}
	// And the new install path must resolve through it.
	resolved, rerr := filepath.EvalSymlinks(filepath.Join(home, ".yaver", "bin", "current", runtime.GOOS+"-"+runtime.GOARCH, "yaver"))
	if rerr != nil {
		t.Fatalf("eval current link: %v", rerr)
	}
	installResolved, ierr := filepath.EvalSymlinks(installPath)
	if ierr != nil {
		t.Fatalf("eval install path: %v", ierr)
	}
	if filepath.Clean(resolved) != filepath.Clean(installResolved) {
		t.Fatalf("current should resolve to the new binary: resolved=%q want=%q", resolved, installResolved)
	}
}

// TestInstallVersionedProbeFailureAbortsAndRollsBack proves the exec probe
// gates the commit: a binary the OS would reject (exit 1) must abort the
// update with `current` still pointing at the OLD version.
func TestInstallVersionedProbeFailureAbortsAndRollsBack(t *testing.T) {
	home, _ := setupHome(t)
	oldDir := filepath.Join(home, ".yaver", "bin", "1.99.100")
	curBefore, err := os.Readlink(filepath.Join(home, ".yaver", "bin", "current"))
	if err != nil {
		t.Fatalf("readlink current before: %v", err)
	}
	if curBefore != oldDir {
		t.Fatalf("precondition: current should point at 1.99.100, got %q", curBefore)
	}

	tmpPath := filepath.Join(home, "yaver.update")
	fakeBinary(t, tmpPath, "1.99.999", false) // poisoned shape: fails to exec

	installPath, _, err := installVersionedAgentBinary(filepath.Join(oldDir, runtime.GOOS+"-"+runtime.GOARCH, "yaver"), tmpPath, "1.99.999")
	if err == nil {
		t.Fatal("expected probe failure to abort the update")
	}
	if !strings.Contains(err.Error(), "exec probe failed") {
		t.Fatalf("error should name the probe: %v", err)
	}
	if _, statErr := os.Stat(installPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed install must be removed, still exists: %v", statErr)
	}
	curAfter, err := os.Readlink(filepath.Join(home, ".yaver", "bin", "current"))
	if err != nil {
		t.Fatalf("readlink current after: %v", err)
	}
	if curAfter != oldDir {
		t.Fatalf("ROLLBACK FAILED: current moved to %q on a failed install; must stay at %q", curAfter, oldDir)
	}
}

// TestPlatformSegmentFromExePath guards the versioned-dir naming the whole
// layout depends on (~/.yaver/bin/<version>/<platform>/yaver).
func TestPlatformSegmentFromExePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	platform := runtime.GOOS + "-" + runtime.GOARCH
	exe := filepath.Join(home, ".yaver", "bin", "1.99.409", platform, "yaver")
	if got := platformSegmentFromExePath(exe); got != platform {
		t.Fatalf("platform segment: got %q want %q", got, platform)
	}
	if got := platformSegmentFromExePath("/usr/local/bin/yaver"); got != "" {
		t.Fatalf("non-.yaver path should yield empty platform, got %q", got)
	}
}

// TestProbeAgentBinary requires the expected version string, not just a zero
// exit — a binary that launches but is the wrong build must fail the probe.
func TestProbeAgentBinary(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	fakeBinary(t, good, "1.99.999", true)
	if !probeAgentBinary(good, "1.99.999") {
		t.Fatal("good binary with matching version must pass the probe")
	}
	if probeAgentBinary(good, "1.99.998") {
		t.Fatal("good binary with WRONG version must fail the probe")
	}
	bad := filepath.Join(dir, "bad")
	fakeBinary(t, bad, "1.99.999", false)
	if probeAgentBinary(bad, "1.99.999") {
		t.Fatal("binary that fails to exec must fail the probe")
	}

	versionOnly := filepath.Join(dir, "version-only")
	if err := os.WriteFile(versionOnly, []byte("#!/bin/sh\nif [ \"${1:-}\" = version ]; then echo 'yaver 1.99.999'; exit 0; fi\nexit 137\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if probeAgentBinary(versionOnly, "1.99.999") {
		t.Fatal("version-only success must not hide a broken MCP operation")
	}
}
