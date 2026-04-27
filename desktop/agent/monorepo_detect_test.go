package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repoRoot resolves the yaver.io repo root (where tests/fixtures/ lives) from
// the agent test cwd by walking up until tests/fixtures/ is visible.
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cwd
	for i := 0; i < 6; i++ {
		if st, err := os.Stat(filepath.Join(dir, "tests", "fixtures")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("repo root with tests/fixtures/ not found from %s", cwd)
	return ""
}

func TestDetectMonorepo_FixturesAsMonorepo(t *testing.T) {
	root := repoRoot(t)
	fixturesDir := filepath.Join(root, "tests", "fixtures")
	mr, err := DetectMonorepo(fixturesDir, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	if !mr.IsMonorepo {
		t.Fatalf("tests/fixtures has 3 framework subdirs — should be IsMonorepo=true; got %+v", mr.Frameworks)
	}

	wantFrameworks := map[string]bool{
		"androidNative": false,
		"flutter":       false,
		"iosNative":     false,
	}
	for _, p := range mr.Projects {
		if _, want := wantFrameworks[p.Framework]; want {
			wantFrameworks[p.Framework] = true
		}
	}
	for fw, found := range wantFrameworks {
		if !found {
			t.Errorf("expected to detect framework %q in tests/fixtures, got %v", fw, mr.Frameworks)
		}
	}

	// At least three projects (one per fixture). The root tests/fixtures/ itself
	// is not classified (no marker files) so the count is exactly the children.
	if len(mr.Projects) < 3 {
		t.Errorf("expected ≥3 projects, got %d: %v", len(mr.Projects), summary(mr.Projects))
	}
}

func TestDetectMonorepo_KotlinFixtureIsAndroidNative(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tests", "fixtures", "native-android-kotlin")
	mr, err := DetectMonorepo(dir, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	if mr.IsMonorepo {
		t.Errorf("standalone Kotlin fixture should NOT be flagged as monorepo, got projects=%v", summary(mr.Projects))
	}
	if len(mr.Projects) != 1 {
		t.Fatalf("expected exactly 1 project, got %d: %v", len(mr.Projects), summary(mr.Projects))
	}
	p := mr.Projects[0]
	if p.Framework != "androidNative" {
		t.Errorf("got framework=%q, want androidNative", p.Framework)
	}
	if !monorepoTestContains(p.Tags, "android") || !monorepoTestContains(p.Tags, "kotlin") || !monorepoTestContains(p.Tags, "mobile") {
		t.Errorf("missing expected tags: %v", p.Tags)
	}
}

func TestDetectMonorepo_SwiftFixtureIsIOSNative(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tests", "fixtures", "native-ios-swift")
	mr, err := DetectMonorepo(dir, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	if len(mr.Projects) == 0 {
		t.Fatal("expected at least 1 project")
	}
	// First project should be the root iOS-native project (xcodegen project.yml).
	root0 := mr.Projects[0]
	if root0.Framework != "iosNative" {
		t.Errorf("got framework=%q, want iosNative", root0.Framework)
	}
	if !monorepoTestContains(root0.Tags, "ios") || !monorepoTestContains(root0.Tags, "swift") {
		t.Errorf("missing ios/swift tags: %v", root0.Tags)
	}
}

func TestDetectMonorepo_FlutterFixture(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tests", "fixtures", "native-flutter-app")
	mr, err := DetectMonorepo(dir, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	if len(mr.Projects) == 0 {
		t.Fatal("expected at least 1 project")
	}
	root0 := mr.Projects[0]
	if root0.Framework != "flutter" {
		t.Errorf("got framework=%q, want flutter", root0.Framework)
	}
	if root0.Manifest != "pubspec.yaml" {
		t.Errorf("expected manifest=pubspec.yaml, got %q", root0.Manifest)
	}
}

func TestDetectMonorepo_HasManifestSetsMonorepo(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "yaver.workspace.yaml"), []byte("apps: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mr, err := DetectMonorepo(tmp, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	if !mr.HasManifest {
		t.Error("HasManifest should be true when yaver.workspace.yaml is present")
	}
	if !mr.IsMonorepo {
		t.Error("IsMonorepo should be true when yaver.workspace.yaml is present (even with zero projects)")
	}
}

func TestDetectMonorepo_SkipsNodeModulesAndBuild(t *testing.T) {
	tmp := t.TempDir()
	// Real project at root
	if err := os.WriteFile(filepath.Join(tmp, "pubspec.yaml"), []byte("name: a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Decoy package.json in node_modules — should NOT be detected as a project.
	mod := filepath.Join(tmp, "node_modules", "fake-pkg")
	if err := os.MkdirAll(mod, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "package.json"), []byte(`{"expo":"~50"}`), 0644); err != nil {
		t.Fatal(err)
	}
	// Decoy in build/ too.
	bld := filepath.Join(tmp, "build", "fake-app")
	if err := os.MkdirAll(bld, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bld, "pubspec.yaml"), []byte("name: b\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mr, err := DetectMonorepo(tmp, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	if len(mr.Projects) != 1 {
		t.Errorf("expected exactly 1 project (root pubspec only), got %d: %v", len(mr.Projects), summary(mr.Projects))
	}
}

func TestDetectMonorepo_RNInnerDirsNotDoubleCounted(t *testing.T) {
	tmp := t.TempDir()
	// Build a fake RN project: package.json at root with react-native, plus
	// android/settings.gradle.kts and an android/app/src/main/AndroidManifest.xml
	// that would otherwise classify the inner android/ as androidNative.
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(`{"dependencies":{"react-native":"0.81.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	androidDir := filepath.Join(tmp, "android", "app", "src", "main")
	if err := os.MkdirAll(androidDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "android", "settings.gradle.kts"), []byte("include(\":app\")\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "android", "build.gradle.kts"), []byte("// root build\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(androidDir, "AndroidManifest.xml"), []byte("<manifest/>"), 0644); err != nil {
		t.Fatal(err)
	}

	mr, err := DetectMonorepo(tmp, DetectOpts{})
	if err != nil {
		t.Fatalf("DetectMonorepo: %v", err)
	}
	frameworks := []string{}
	for _, p := range mr.Projects {
		frameworks = append(frameworks, p.Framework)
	}
	if len(mr.Projects) != 1 || mr.Projects[0].Framework != "react-native" {
		t.Errorf("expected exactly 1 react-native project (RN inner android/ should not double-count), got %v", frameworks)
	}
}

func TestDetectMonorepo_RejectsBadPath(t *testing.T) {
	if _, err := DetectMonorepo("", DetectOpts{}); err == nil {
		t.Error("expected error for empty rootDir")
	}
	if _, err := DetectMonorepo("/no/such/dir/should/exist", DetectOpts{}); err == nil {
		t.Error("expected error for nonexistent rootDir")
	}
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectMonorepo(f, DetectOpts{}); err == nil {
		t.Error("expected error when path is a file, not a dir")
	}
}

// Helpers
func monorepoTestContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func summary(projs []DetectedProject) []string {
	out := make([]string, len(projs))
	for i, p := range projs {
		out[i] = strings.Join([]string{p.RelPath, p.Framework}, ":")
	}
	sort.Strings(out)
	return out
}
