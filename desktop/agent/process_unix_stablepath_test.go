//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// layoutWithCurrentSymlink builds the exact on-disk shape every installed box
// has: a versioned directory holding the real binary, and a `current` symlink
// pointing at that directory.
//
//	<home>/.yaver/bin/1.99.386/linux-arm64/yaver   (real file)
//	<home>/.yaver/bin/current -> <home>/.yaver/bin/1.99.386
func layoutWithCurrentSymlink(t *testing.T) (home, realExe string) {
	t.Helper()
	home = t.TempDir()
	versionDir := filepath.Join(home, ".yaver", "bin", "1.99.386", "linux-arm64")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realExe = filepath.Join(versionDir, "yaver")
	if err := os.WriteFile(realExe, []byte("#!/bin/sh\necho real binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".yaver", "bin", "1.99.386"),
		filepath.Join(home, ".yaver", "bin", "current")); err != nil {
		t.Fatal(err)
	}
	return home, realExe
}

// THE BRICK, as a test.
//
// ubuntu-4gb-hel1-1, 2026-08-01. ensureStableAutoStartExecutable computed
// stablePath = .../bin/current/linux-arm64/yaver, compared it to exePath as a
// STRING, saw two different strings, and proceeded to os.Remove(stablePath) —
// which, because `current` is a symlink to the version dir, deleted the running
// binary — then symlinked that path to itself. exec then failed with ELOOP,
// systemd reported status 203, and the unit sat in "activating" forever.
//
// If this test fails, the agent can destroy its own binary again.
func TestEnsureStableAutoStartExecutable_DoesNotDestroyBinaryBehindCurrentSymlink(t *testing.T) {
	home, realExe := layoutWithCurrentSymlink(t)
	t.Setenv("HOME", home)

	got := ensureStableAutoStartExecutable(realExe)

	// The binary must still exist, still be a regular file, and still be ours.
	info, err := os.Lstat(realExe)
	if err != nil {
		t.Fatalf("the running binary was DESTROYED: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the running binary was replaced by a symlink — this is the ELOOP brick")
	}
	body, err := os.ReadFile(realExe)
	if err != nil || string(body) == "" {
		t.Fatalf("binary unreadable after the call: %v", err)
	}

	// And whatever path it hands back must actually be executable content,
	// not a link that resolves to itself.
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("returned path %q does not resolve: %v", got, err)
	}
}

// The guard has to be identity-based, not string-based. Same file reached by
// two different names must be recognised.
func TestPathsSameFile_SeesThroughTheCurrentSymlink(t *testing.T) {
	home, realExe := layoutWithCurrentSymlink(t)
	viaCurrent := filepath.Join(home, ".yaver", "bin", "current", "linux-arm64", "yaver")

	if realExe == viaCurrent {
		t.Fatal("fixture broken — the two paths must differ as strings")
	}
	if !pathsSameFile(realExe, viaCurrent) {
		t.Fatal("pathsSameFile missed two names for one inode — the string guard is back")
	}
}

func TestPathsSameFile_DistinctFilesAreNotSame(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if pathsSameFile(a, b) {
		t.Fatal("two distinct files reported as the same")
	}
	if pathsSameFile(a, filepath.Join(dir, "missing")) {
		t.Fatal("a missing path must never compare equal")
	}
}

// The 2026-09-12 Silent Input 404, as a test.
//
// A LaunchAgent plist that hardcodes ~/.yaver/bin/1.99.411/darwin-arm64/yaver
// kept launching the old binary after auto-update repointed `current` at
// 1.99.465, so /vsr/* routes 404'd while the box looked online. reconcile must
// rewrite ONLY the ProgramArguments binary to the stable current path and leave
// every other byte — args, KeepAlive, log paths — untouched.
func TestRewriteLaunchdProgramBinary_RepointsVersionedPathOnly(t *testing.T) {
	home, realExe := layoutWithCurrentSymlink(t)
	_ = realExe
	stable := filepath.Join(home, ".yaver", "bin", "current", "linux-arm64", "yaver")
	stale := filepath.Join(home, ".yaver", "bin", "1.99.411", "darwin-arm64", "yaver")

	plist := filepath.Join(t.TempDir(), "io.yaver.agent.plist")
	body := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>io.yaver.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + stale + `</string>
        <string>serve</string>
        <string>--debug</string>
        <string>--work-dir=/Users/someone/Workspace/yaver.io</string>
    </array>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`
	if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	old, changed := rewriteLaunchdProgramBinary(plist, stable)
	if !changed {
		t.Fatal("a versioned ProgramArguments binary must be rewritten")
	}
	if old != stale {
		t.Fatalf("returned old %q, want %q", old, stale)
	}
	updated, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "<string>"+stable+"</string>") {
		t.Fatal("stable current path was not written into ProgramArguments")
	}
	if strings.Contains(string(updated), stale) {
		t.Fatal("stale versioned path survived the rewrite")
	}
	// Everything after the binary must be byte-identical.
	if !strings.Contains(string(updated), "<string>serve</string>") ||
		!strings.Contains(string(updated), "<key>KeepAlive</key>") {
		t.Fatal("rewrite touched bytes outside the first ProgramArguments string")
	}
}

func TestRewriteLaunchdProgramBinary_IdempotentAndSafe(t *testing.T) {
	stable := "/home/u/.yaver/bin/current/darwin-arm64/yaver"
	dir := t.TempDir()
	plist := filepath.Join(dir, "io.yaver.agent.plist")

	// Already stable → no rewrite.
	if err := os.WriteFile(plist, []byte("<key>ProgramArguments</key><array><string>"+stable+"</string></array>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed := rewriteLaunchdProgramBinary(plist, stable); changed {
		t.Fatal("an already-stable plist must not be rewritten on every startup")
	}

	// Missing plist → no rewrite, no panic.
	if _, changed := rewriteLaunchdProgramBinary(filepath.Join(dir, "nope.plist"), stable); changed {
		t.Fatal("a missing plist must not report a rewrite")
	}

	// No ProgramArguments → no rewrite.
	noArgs := filepath.Join(dir, "noargs.plist")
	if err := os.WriteFile(noArgs, []byte("<key>Label</key><string>x</string>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed := rewriteLaunchdProgramBinary(noArgs, stable); changed {
		t.Fatal("a plist without ProgramArguments must not be rewritten")
	}

	// A plist at the expected path may have been replaced or corrupted. Never
	// turn this helper into a general launchd-program rewrite primitive.
	unrelated := filepath.Join(dir, "unrelated.plist")
	if err := os.WriteFile(unrelated, []byte("<key>ProgramArguments</key><array><string>/usr/bin/true</string></array>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed := rewriteLaunchdProgramBinary(unrelated, stable); changed {
		t.Fatal("a non-Yaver launchd program must not be rewritten")
	}
}

// The stable path must be the version-independent `current` symlink target, not
// whichever version directory it happens to point at today.
func TestStableCachedYaverBinary_UsesCurrentSymlink(t *testing.T) {
	home := t.TempDir()
	versionDir := filepath.Join(home, ".yaver", "bin", "1.99.999", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "yaver"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(home, ".yaver", "bin", "current")
	if err := os.Symlink(filepath.Join(home, ".yaver", "bin", "1.99.999"), current); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	got := stableCachedYaverBinary()
	want := filepath.Join(home, ".yaver", "bin", "current", runtime.GOOS+"-"+runtime.GOARCH, "yaver")
	if got != want {
		t.Fatalf("got %q, want the stable current path %q", got, want)
	}
}

// When the paths genuinely differ, the link must still get created — the guard
// must not be so broad that it disables the feature it protects.
func TestEnsureStableAutoStartExecutable_StillLinksWhenTargetIsDistinct(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	versionDir := filepath.Join(home, ".yaver", "bin", "1.99.400", "linux-arm64")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realExe := filepath.Join(versionDir, "yaver")
	if err := os.WriteFile(realExe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No `current` symlink at all — stablePath is a genuinely new location.

	got := ensureStableAutoStartExecutable(realExe)
	want := filepath.Join(home, ".yaver", "bin", "current", "linux-arm64", "yaver")
	if got != want {
		t.Fatalf("got %q, want the stable path %q", got, want)
	}
	li, err := os.Lstat(want)
	if err != nil {
		t.Fatalf("stable link not created: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Fatal("stable path should be a symlink")
	}
	if !pathsSameFile(want, realExe) {
		t.Fatal("the stable link does not resolve to the real binary")
	}
	// No temp artefact left behind.
	if _, err := os.Lstat(want + ".tmp-link"); !os.IsNotExist(err) {
		t.Fatal("temp link was left on disk")
	}
}
