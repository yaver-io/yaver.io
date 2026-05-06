package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindAndroidToolPath_PrefersManagedRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	root := filepath.Join(home, ".yaver", "runtimes", "android-sdk")
	tool := filepath.Join(root, "platform-tools", "adb")
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findAndroidToolPath("adb"); got != tool {
		t.Fatalf("findAndroidToolPath(adb) = %q, want %q", got, tool)
	}
}

func TestAndroidCommandLineToolsArchive_UsesOfficialGoogleHost(t *testing.T) {
	filename, url, ok := androidCommandLineToolsArchive()
	if !ok {
		t.Skip("unsupported platform for android command line tools")
	}
	if filename == "" || url == "" {
		t.Fatal("expected non-empty archive metadata")
	}
	if want := "https://dl.google.com/android/repository/"; len(url) < len(want) || url[:len(want)] != want {
		t.Fatalf("archive url = %q, want official Google repository host", url)
	}
}

func TestDetectedAndroidSDKRoot_FallsBackToStandardMacPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific Android SDK default path")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	root := filepath.Join(home, "Library", "Android", "sdk")
	adb := filepath.Join(root, "platform-tools", "adb")
	if err := os.MkdirAll(filepath.Dir(adb), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adb, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectedAndroidSDKRoot(); got != root {
		t.Fatalf("detectedAndroidSDKRoot() = %q, want %q", got, root)
	}
}
