package main

import (
	"os"
	"path/filepath"
	"testing"
)

// findSystemHermesc's sandbox branch keys on the YAVER_ANDROID_* env vars
// (via sandboxConfigFromEnv), NOT runtime.GOOS, so it's exercisable on any
// host. We point a fake rootfs at a temp dir, drop a stub hermesc inside it,
// and assert we get back the rootfs-INTERNAL path (the proot-wrapped exec
// resolves that path against -r <rootfs>).

func TestFindSystemHermesc_SandboxReturnsRootfsInternalPath(t *testing.T) {
	rootfs := t.TempDir()
	inner := "/usr/local/libexec/yaver/hermesc"
	host := filepath.Join(rootfs, "usr/local/libexec/yaver/hermesc")
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host, []byte("#!/bin/true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Activate the sandbox config. Both vars are required by sandboxConfigFromEnv.
	t.Setenv(envSandboxRootfs, rootfs)
	t.Setenv(envSandboxProot, "/data/app/libproot.so")

	got := findSystemHermesc()
	if got != inner {
		t.Fatalf("expected rootfs-internal path %q, got %q", inner, got)
	}

	// The stub should have been chmod'd executable as a side effect.
	if fi, err := os.Stat(host); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("expected hermesc to be made executable, mode=%v", fi.Mode())
	}
}

func TestFindSystemHermesc_SandboxFallsToSecondPath(t *testing.T) {
	rootfs := t.TempDir()
	inner := "/opt/yaver/bin/hermesc"
	host := filepath.Join(rootfs, "opt/yaver/bin/hermesc")
	if err := os.MkdirAll(filepath.Dir(host), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(envSandboxRootfs, rootfs)
	t.Setenv(envSandboxProot, "/data/app/libproot.so")

	if got := findSystemHermesc(); got != inner {
		t.Fatalf("expected %q, got %q", inner, got)
	}
}

func TestFindSystemHermesc_SandboxMissingReturnsEmpty(t *testing.T) {
	rootfs := t.TempDir() // empty — no hermesc anywhere
	t.Setenv(envSandboxRootfs, rootfs)
	t.Setenv(envSandboxProot, "/data/app/libproot.so")

	if got := findSystemHermesc(); got != "" {
		t.Fatalf("expected empty (no prewarmed hermesc), got %q", got)
	}
}

// A directory at the well-known path must NOT be mistaken for the binary.
func TestFindSystemHermesc_SandboxIgnoresDirectory(t *testing.T) {
	rootfs := t.TempDir()
	host := filepath.Join(rootfs, "usr/local/libexec/yaver/hermesc")
	if err := os.MkdirAll(host, 0o755); err != nil { // a DIR named hermesc
		t.Fatal(err)
	}
	t.Setenv(envSandboxRootfs, rootfs)
	t.Setenv(envSandboxProot, "/data/app/libproot.so")

	if got := findSystemHermesc(); got != "" {
		t.Fatalf("expected empty (dir is not a binary), got %q", got)
	}
}
