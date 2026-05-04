package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSwiftVersion_LinuxRelease(t *testing.T) {
	out := "Swift version 5.10 (swift-5.10-RELEASE)\n" +
		"Target: x86_64-unknown-linux-gnu\n"
	if got := parseSwiftVersion(out); got != "5.10" {
		t.Errorf("got %q, want 5.10", got)
	}
}

func TestParseSwiftVersion_LinuxPatch(t *testing.T) {
	out := "Swift version 5.9.2 (swift-5.9.2-RELEASE)\n"
	if got := parseSwiftVersion(out); got != "5.9.2" {
		t.Errorf("got %q, want 5.9.2", got)
	}
}

func TestParseSwiftVersion_AppleMac(t *testing.T) {
	// Apple's `swift --version` prefixes the version line — the
	// regex must still pull "6.0" out cleanly so a Mac dev's CLI
	// also reports a sensible value.
	out := "Apple Swift version 6.0 (swiftlang-6.0.0.6.4 clang-1600.0.20.10)\n" +
		"Target: arm64-apple-macosx14.0\n"
	if got := parseSwiftVersion(out); got != "6.0" {
		t.Errorf("got %q, want 6.0", got)
	}
}

func TestParseSwiftVersion_FallsBackToFirstLine(t *testing.T) {
	out := "weird unparseable banner\nTarget: ...\n"
	if got := parseSwiftVersion(out); got != "weird unparseable banner" {
		t.Errorf("fallback should return first line, got %q", got)
	}
}

func TestParseSwiftVersion_EmptyInput(t *testing.T) {
	if got := parseSwiftVersion(""); got != "" {
		t.Errorf("empty input should yield empty, got %q", got)
	}
}

func TestDetectSwiftToolchain_HandlesMissingBinary(t *testing.T) {
	// Pretend swift isn't on PATH by clobbering PATH for this test.
	// The probe must return a non-nil *SwiftToolchain so callers can
	// always read .Available without a nil check (regression guard
	// for the doctor + dashboard JSON encoders).
	t.Setenv("PATH", "/nonexistent-yaver-test-path")
	tc, err := DetectSwiftToolchain(context.Background())
	if err != nil {
		t.Fatalf("missing swift should not error, got %v", err)
	}
	if tc == nil {
		t.Fatal("returned nil *SwiftToolchain — callers can't read .Available")
	}
	if tc.Available {
		t.Errorf(".Available should be false when swift is missing, got %+v", tc)
	}
	if !strings.Contains(tc.Notes, "PATH") && !strings.Contains(tc.Notes, "install") {
		t.Errorf("notes should hint at install path, got %q", tc.Notes)
	}
}

func TestHasSwiftPackageManifest(t *testing.T) {
	dir := t.TempDir()
	if hasSwiftPackageManifest(dir) {
		t.Error("empty dir should not look like a SwiftPM project")
	}
	if err := os.WriteFile(filepath.Join(dir, "Package.swift"),
		[]byte("// swift-tools-version:5.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasSwiftPackageManifest(dir) {
		t.Error("dir with Package.swift should be detected")
	}
	// A Package.swift directory (not a file) must NOT count — would
	// only happen if a user mkdir'd it for some reason, but the bug
	// would be silent if we forgot the IsDir() guard.
	dir2 := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir2, "Package.swift"), 0o755); err != nil {
		t.Fatal(err)
	}
	if hasSwiftPackageManifest(dir2) {
		t.Error("Package.swift directory should be rejected")
	}
}

func TestExitCodeFromError_RealSubprocessExit(t *testing.T) {
	// Confirm exitCodeFromError unwraps both a bare ExitError and a
	// `%w`-wrapped one (which is what runSwiftSubcommand emits).
	cmd := exec.Command("sh", "-c", "exit 7")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-nil error from `exit 7`")
	}
	if code, ok := exitCodeFromError(err); !ok || code != 7 {
		t.Errorf("bare ExitError: got (%d, %v), want (7, true)", code, ok)
	}
	wrapped := errors.New("not a subprocess error")
	if _, ok := exitCodeFromError(wrapped); ok {
		t.Error("non-subprocess error must yield (_, false)")
	}
}
