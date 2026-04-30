package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Sanity test for projectBundleIDMatches. We write representative
// manifest fragments to a tmp directory and verify each resolver
// path finds the id. Both iOS-only AND android-only trees must
// resolve — the regression we're preventing is an Android-only
// project (no ios/) being ignored because a previous iteration
// checked iOS first and short-circuited on "no iOS dir".
func TestProjectBundleIDMatches_Android(t *testing.T) {
	dir := t.TempDir()
	// Minimal Android tree, no ios/ directory at all.
	writeManifestFile(t, filepath.Join(dir, "android", "app", "build.gradle"), `
android {
    defaultConfig {
        applicationId "com.example.androidonly"
    }
}
`)
	if !projectBundleIDMatches(dir, "com.example.androidonly") {
		t.Fatalf("android-only project should have matched applicationId")
	}
	if projectBundleIDMatches(dir, "com.example.other") {
		t.Fatalf("android-only project must NOT match an unrelated id")
	}
}

func TestProjectBundleIDMatches_AndroidNamespaceAGP8(t *testing.T) {
	dir := t.TempDir()
	// AGP 8+ uses `namespace = "..."` in Kotlin DSL.
	writeManifestFile(t, filepath.Join(dir, "android", "app", "build.gradle.kts"), `
android {
    namespace = "com.example.agp8"
    defaultConfig { applicationId = "com.example.agp8" }
}
`)
	if !projectBundleIDMatches(dir, "com.example.agp8") {
		t.Fatalf("AGP 8 namespace should have matched")
	}
}

func TestProjectBundleIDMatches_AndroidManifest(t *testing.T) {
	dir := t.TempDir()
	// Old-school AndroidManifest.xml-based declaration.
	writeManifestFile(t, filepath.Join(dir, "android", "app", "src", "main", "AndroidManifest.xml"), `
<manifest xmlns:android="http://schemas.android.com/apk/res/android"
    package="com.example.legacy">
</manifest>
`)
	if !projectBundleIDMatches(dir, "com.example.legacy") {
		t.Fatalf("AndroidManifest.xml package= should have matched")
	}
}

func TestProjectBundleIDMatches_IOSOnly(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, filepath.Join(dir, "ios", "MyApp", "Info.plist"), `
<?xml version="1.0" encoding="UTF-8"?>
<plist><dict>
  <key>CFBundleIdentifier</key>
  <string>com.example.iosonly</string>
</dict></plist>
`)
	if !projectBundleIDMatches(dir, "com.example.iosonly") {
		t.Fatalf("iOS-only Info.plist should have matched")
	}
}

func TestProjectBundleIDMatches_IOSPbxproj(t *testing.T) {
	dir := t.TempDir()
	writeManifestFile(t, filepath.Join(dir, "ios", "MyApp.xcodeproj", "project.pbxproj"), `
PRODUCT_BUNDLE_IDENTIFIER = "com.example.pbxproj";
`)
	if !projectBundleIDMatches(dir, "com.example.pbxproj") {
		t.Fatalf("ios pbxproj quoted PRODUCT_BUNDLE_IDENTIFIER should have matched")
	}
	dir2 := t.TempDir()
	writeManifestFile(t, filepath.Join(dir2, "ios", "MyApp.xcodeproj", "project.pbxproj"), `
PRODUCT_BUNDLE_IDENTIFIER = com.example.pbxproj2;
`)
	if !projectBundleIDMatches(dir2, "com.example.pbxproj2") {
		t.Fatalf("ios pbxproj unquoted PRODUCT_BUNDLE_IDENTIFIER should have matched")
	}
}

func TestProjectBundleIDMatches_ExpoAppJson(t *testing.T) {
	// Expo app.json with only Android — iOS section absent. This is the
	// exact shape that made the user call out "don't tightly couple to iOS."
	dir := t.TempDir()
	writeManifestFile(t, filepath.Join(dir, "app.json"), `
{
  "expo": {
    "name": "androidExpoOnly",
    "android": { "package": "com.example.expoAndroid" }
  }
}
`)
	if !projectBundleIDMatches(dir, "com.example.expoAndroid") {
		t.Fatalf("Expo app.json android.package should have matched")
	}
}

func writeManifestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestDetectMonorepoLineageRecognisesAppsLayout — apps/<name> shape
// (turbo-style monorepos). Carrotbet has `apps/web/` for example.
func TestDetectMonorepoLineageRecognisesAppsLayout(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "carrotbet")
	app := filepath.Join(repo, "apps", "web")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, name := detectMonorepoLineage(app)
	if root != repo {
		t.Errorf("root = %q, want %q", root, repo)
	}
	if name != "apps/web" {
		t.Errorf("name = %q, want apps/web", name)
	}
}

// TestDetectMonorepoLineageRecognisesMobileLayout — `mobile/` at root,
// no `apps/` wrapper. Carrotbet's mobile RN app sits here.
func TestDetectMonorepoLineageRecognisesMobileLayout(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "carrotbet")
	mobile := filepath.Join(repo, "mobile")
	if err := os.MkdirAll(mobile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, name := detectMonorepoLineage(mobile)
	if root != repo {
		t.Errorf("root = %q, want %q", root, repo)
	}
	if name != "mobile" {
		t.Errorf("name = %q, want mobile", name)
	}
}

// TestDetectMonorepoLineageRecognisesYaverWorkspace — yaver.workspace.yaml
// counts as a monorepo root marker even without .git.
func TestDetectMonorepoLineageRecognisesYaverWorkspace(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "wsproj")
	app := filepath.Join(repo, "apps", "frontend")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "yaver.workspace.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, name := detectMonorepoLineage(app)
	if root != repo {
		t.Errorf("root = %q, want %q", root, repo)
	}
	if name != "apps/frontend" {
		t.Errorf("name = %q, want apps/frontend", name)
	}
}

// TestDetectMonorepoLineageStandalone — single-package repo where the
// project IS the repo root: no monorepo lineage should be reported.
func TestDetectMonorepoLineageStandalone(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "sfmg")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, name := detectMonorepoLineage(repo)
	if root != "" || name != "" {
		t.Errorf("expected standalone, got root=%q name=%q", root, name)
	}
}

// TestDetectMonorepoLineageYaverIoDogfoodLayout — yaver.io's own repo
// has `web/` (Next.js dashboard) + `mobile/` (Expo RN). The dogfood
// flow (Settings → Dogfood) relies on the scanner detecting BOTH
// subdirs as separate projects pointing at the same monorepo root.
// This test simulates that exact layout.
func TestDetectMonorepoLineageYaverIoDogfoodLayout(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "yaver.io")
	web := filepath.Join(repo, "web")
	mobile := filepath.Join(repo, "mobile")
	if err := os.MkdirAll(web, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mobile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Both subdirs must report the same monorepo root.
	rootW, nameW := detectMonorepoLineage(web)
	if rootW != repo {
		t.Errorf("web root = %q, want %q", rootW, repo)
	}
	if nameW != "web" {
		t.Errorf("web name = %q, want web", nameW)
	}
	rootM, nameM := detectMonorepoLineage(mobile)
	if rootM != repo {
		t.Errorf("mobile root = %q, want %q", rootM, repo)
	}
	if nameM != "mobile" {
		t.Errorf("mobile name = %q, want mobile", nameM)
	}
	// Sanity: same root, different names — the dashboard uses this
	// to group both rows under the yaver.io repo entry.
	if rootW != rootM {
		t.Errorf("web + mobile should share the same monorepo root: web=%q mobile=%q", rootW, rootM)
	}
	if nameW == nameM {
		t.Errorf("web + mobile should have distinct app names; both = %q", nameW)
	}
}

func TestDisplayProjectName_RepoFirstStandalone(t *testing.T) {
	got := displayProjectName("/tmp/sfmg", "", "SFMG", "react-native", true, false)
	if got != "sfmg / mobile" {
		t.Fatalf("got %q, want %q", got, "sfmg / mobile")
	}
}

func TestDisplayProjectName_RepoFirstNestedApp(t *testing.T) {
	got := displayProjectName("/tmp/yaver", "", "todo", "kotlin", true, false)
	if got != "yaver (todo) / mobile" {
		t.Fatalf("got %q, want %q", got, "yaver (todo) / mobile")
	}
}

func TestDisplayProjectName_RepoFirstRootMobileSubdir(t *testing.T) {
	got := displayProjectName("/tmp/yaver.io", "mobile", "Yaver", "react-native", true, false)
	if got != "yaver / mobile" {
		t.Fatalf("got %q, want %q", got, "yaver / mobile")
	}
}

func TestRepoRootForProject_FindsAncestorGitRoot(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "yaver")
	app := filepath.Join(repo, "tests", "fixtures", "native-android-kotlin")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := repoRootForProject(app)
	if got != repo {
		t.Fatalf("got %q, want %q", got, repo)
	}
}

func TestHasProjectGitContext_WalksDeepFixtureAncestors(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "yaver.io")
	fixture := filepath.Join(repo, "tests", "fixtures", "native-ios-swift")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasProjectGitContext(fixture) {
		t.Fatalf("expected fixture path to inherit git context from repo root")
	}
}
