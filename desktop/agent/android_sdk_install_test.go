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

// Google publishes the Android Emulator host binary for linux-x86_64,
// darwin-x86_64, darwin-arm64 and windows only. linux-aarch64 has no
// build, so `sdkmanager --install emulator` on an ARM Linux box aborts
// the whole batch. emulatorHostSupported encodes exactly that matrix.
func TestEmulatorHostSupported(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         bool
	}{
		{"linux", "amd64", true},
		{"linux", "arm64", false}, // no linux-aarch64 emulator binary
		{"darwin", "arm64", true},
		{"darwin", "amd64", true},
		{"windows", "amd64", true},
	}
	for _, c := range cases {
		if got := emulatorHostSupported(c.goos, c.goarch); got != c.want {
			t.Errorf("emulatorHostSupported(%q,%q)=%v want %v", c.goos, c.goarch, got, c.want)
		}
	}
}

// platform-tools and the build platform must always be installed (they
// back `yaver wire` builds even where the emulator can't run); the
// emulator + system image must appear iff the host supports them, and
// a system image must never be requested without an emulator to run it.
func TestAndroidRuntimeSDKPackages(t *testing.T) {
	pkgs := androidRuntimeSDKPackages()

	if !containsPkgPrefix(pkgs, "platform-tools") {
		t.Fatalf("platform-tools must always be installed; got %v", pkgs)
	}

	hasEmu := containsPkgPrefix(pkgs, "emulator")
	wantEmu := emulatorHostSupported(runtime.GOOS, runtime.GOARCH)
	if hasEmu != wantEmu {
		t.Fatalf("emulator present=%v but host-supported=%v (%s/%s); pkgs=%v",
			hasEmu, wantEmu, runtime.GOOS, runtime.GOARCH, pkgs)
	}

	if containsPkgPrefix(pkgs, "system-images;") && !hasEmu {
		t.Fatalf("system image requested without an emulator to run it: %v", pkgs)
	}
}

func containsPkgPrefix(pkgs []string, prefix string) bool {
	for _, p := range pkgs {
		if len(p) >= len(prefix) && p[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
