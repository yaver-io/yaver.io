package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Reproduces the talos failure: build.gradle pins versionCode 337 and the
// generic deploy used to bump only versionName, so every Play upload re-used
// 337 → "Version code 337 has already been used". The fix increments the
// integer versionCode by 1 alongside the semver bump.
func TestBumpDetectedVersions_AndroidVersionCodeIncrements(t *testing.T) {
	root := t.TempDir()
	bg := filepath.Join("mobile", "android", "app", "build.gradle")
	full := filepath.Join(root, bg)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	const src = `android {
    defaultConfig {
        applicationId "works.talos.mobile"
        versionCode 337
        versionName "1.9.76"
    }
}
`
	if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	sites := []DetectedVersionSite{
		{App: "mobile", File: bg, Kind: "gradle-version-name"},
		{App: "mobile", File: bg, Kind: "gradle-version-code"},
	}
	ctx := &deployAllCtx{}
	if _, err := bumpDetectedVersions(root, sites, "patch", false, ctx); err != nil {
		t.Fatalf("bump: %v", err)
	}

	gotName, err := readVersionSite(full, "gradle-version-name")
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "1.9.77" {
		t.Errorf("versionName = %q, want 1.9.77", gotName)
	}
	gotCode, err := readGradleVersionCode(full)
	if err != nil {
		t.Fatal(err)
	}
	if gotCode != 338 {
		t.Errorf("versionCode = %d, want 338", gotCode)
	}

	// Re-running bumps again from the in-place file (337→338→339), so a
	// second deploy never re-uploads a code Play has already seen.
	if _, err := bumpDetectedVersions(root, sites, "patch", false, ctx); err != nil {
		t.Fatalf("second bump: %v", err)
	}
	gotCode, _ = readGradleVersionCode(full)
	if gotCode != 339 {
		t.Errorf("versionCode after 2nd bump = %d, want 339", gotCode)
	}
}

// dry-run must not touch build.gradle (deploy all --dry-run is read-only,
// it may inspect a repo owned by another session).
func TestBumpDetectedVersions_DryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	bg := filepath.Join("mobile", "android", "app", "build.gradle")
	full := filepath.Join(root, bg)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	const src = "android { defaultConfig { versionCode 50\n versionName \"2.0.0\" } }\n"
	if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sites := []DetectedVersionSite{
		{App: "mobile", File: bg, Kind: "gradle-version-name"},
		{App: "mobile", File: bg, Kind: "gradle-version-code"},
	}
	if _, err := bumpDetectedVersions(root, sites, "patch", true, &deployAllCtx{}); err != nil {
		t.Fatalf("dry-run bump: %v", err)
	}
	code, _ := readGradleVersionCode(full)
	if code != 50 {
		t.Errorf("dry-run mutated versionCode to %d, want 50 unchanged", code)
	}
	name, _ := readVersionSite(full, "gradle-version-name")
	if name != "2.0.0" {
		t.Errorf("dry-run mutated versionName to %q, want 2.0.0 unchanged", name)
	}
}

// detectVersionSites surfaces the Android versionCode site so the bump knows
// to increment it.
func TestDetectVersionSites_IncludesAndroidVersionCode(t *testing.T) {
	root := t.TempDir()
	mustWriteTree(t, filepath.Join(root, "mobile", "app.json"), `{"expo":{"version":"1.0.0"}}`)
	mustWriteTree(t, filepath.Join(root, "mobile", "android", "app", "build.gradle"),
		"defaultConfig { versionCode 12\n versionName \"1.0.0\" }")

	sites := detectVersionSites(root)
	var hasName, hasCode bool
	for _, s := range sites {
		if s.Kind == "gradle-version-name" {
			hasName = true
		}
		if s.Kind == "gradle-version-code" {
			hasCode = true
		}
	}
	if !hasName || !hasCode {
		t.Errorf("detectVersionSites: hasName=%v hasCode=%v, want both true", hasName, hasCode)
	}
}

func mustWriteTree(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
