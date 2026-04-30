package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveTestkitCommandPath_FindsManagedAndroidSDK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	bin := filepath.Join(home, ".yaver", "runtimes", "android-sdk", "platform-tools", "adb")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveTestkitCommandPath("adb"); got != bin {
		t.Fatalf("resolveTestkitCommandPath(adb) = %q, want %q", got, bin)
	}
}
