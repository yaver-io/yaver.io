package main

import (
	"os"
	"path/filepath"
	"testing"
)

// resolveMobileProject + detectMobileStack are the bits that decide
// "what gets pushed when the user runs `yaver wire push` in some
// random directory." Get this wrong and we either silently push the
// wrong app or refuse to find an obvious one — both ship-blockers.

func writeJSON_t(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectMobileStack(t *testing.T) {
	t.Run("flutter", func(t *testing.T) {
		dir := t.TempDir()
		writeJSON_t(t, filepath.Join(dir, "pubspec.yaml"), "name: foo\n")
		if got := detectMobileStack(dir); got != "flutter" {
			t.Fatalf("flutter: got %q", got)
		}
	})

	t.Run("expo", func(t *testing.T) {
		dir := t.TempDir()
		writeJSON_t(t, filepath.Join(dir, "package.json"), `{"dependencies":{"expo":"~54.0.0"}}`)
		if got := detectMobileStack(dir); got != "expo" {
			t.Fatalf("expo: got %q", got)
		}
	})

	t.Run("bare-rn", func(t *testing.T) {
		dir := t.TempDir()
		writeJSON_t(t, filepath.Join(dir, "package.json"), `{"dependencies":{"react-native":"0.81.5"}}`)
		if got := detectMobileStack(dir); got != "react-native" {
			t.Fatalf("react-native: got %q", got)
		}
	})

	t.Run("expo-wins-over-rn", func(t *testing.T) {
		// Both deps present — Expo must win because Expo projects
		// also list react-native as a transitive dep.
		dir := t.TempDir()
		writeJSON_t(t, filepath.Join(dir, "package.json"),
			`{"dependencies":{"expo":"~54.0.0","react-native":"0.81.5"}}`)
		if got := detectMobileStack(dir); got != "expo" {
			t.Fatalf("expo+rn: got %q (must prefer expo)", got)
		}
	})

	t.Run("native-android", func(t *testing.T) {
		dir := t.TempDir()
		writeJSON_t(t, filepath.Join(dir, "android", "build.gradle"), "// gradle\n")
		if got := detectMobileStack(dir); got != "native-android" {
			t.Fatalf("native-android: got %q", got)
		}
	})

	t.Run("nothing", func(t *testing.T) {
		dir := t.TempDir()
		writeJSON_t(t, filepath.Join(dir, "README.md"), "")
		if got := detectMobileStack(dir); got != "" {
			t.Fatalf("nothing: got %q", got)
		}
	})
}

func TestResolveMobileProjectWalksSubdirs(t *testing.T) {
	// Repo root with no mobile project at top-level — but a real
	// expo project under mobile/. Mirrors the yaver.io repo layout.
	root := t.TempDir()
	writeJSON_t(t, filepath.Join(root, "README.md"), "monorepo")
	writeJSON_t(t, filepath.Join(root, "mobile", "package.json"),
		`{"dependencies":{"expo":"~54.0.0"}}`)

	got, stack := resolveMobileProject(root)
	if stack != "expo" {
		t.Fatalf("stack: got %q, want expo", stack)
	}
	if got != filepath.Join(root, "mobile") {
		t.Fatalf("root: got %q, want %s", got, filepath.Join(root, "mobile"))
	}
}

func TestResolveMobileProjectAppsGlob(t *testing.T) {
	// pnpm/turbo monorepos put mobile under apps/<name>.
	root := t.TempDir()
	writeJSON_t(t, filepath.Join(root, "package.json"), `{}`) // ← no expo at root
	writeJSON_t(t, filepath.Join(root, "apps", "phone", "package.json"),
		`{"dependencies":{"expo":"~54.0.0"}}`)

	got, stack := resolveMobileProject(root)
	if stack != "expo" {
		t.Fatalf("stack: got %q", stack)
	}
	want := filepath.Join(root, "apps", "phone")
	if got != want {
		t.Fatalf("root: got %q, want %s", got, want)
	}
}

func TestResolveMobileProjectNoMatch(t *testing.T) {
	root := t.TempDir()
	writeJSON_t(t, filepath.Join(root, "src", "main.go"), "package main")
	root2, stack := resolveMobileProject(root)
	if stack != "" || root2 != "" {
		t.Fatalf("expected ('','') got (%q,%q)", root2, stack)
	}
}
