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

func TestDisplayProjectName_DualCapabilityReactNativeStillReadsMobile(t *testing.T) {
	got := displayProjectName("/tmp/sfmg", "", "Workspace", "react-native", true, true)
	if got != "sfmg / mobile" {
		t.Fatalf("got %q, want %q", got, "sfmg / mobile")
	}
}

func TestDisplayProjectName_WebProjectReadsWeb(t *testing.T) {
	got := displayProjectName("/tmp/carrotbet", "", "Workspace", "vite", false, true)
	if got != "carrotbet / web" {
		t.Fatalf("got %q, want %q", got, "carrotbet / web")
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

func TestHasProjectGitContext_AcceptsWorkspaceChildWithoutGit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	project := filepath.Join(tmp, "Workspace", "sfmg")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasProjectGitContext(project) {
		t.Fatalf("expected ~/Workspace/<proj> to be trusted without .git")
	}
}

func TestHasProjectGitContext_RejectsRandomPathWithoutGit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stray := filepath.Join(tmp, "tmp", "scratch", "lib-fixture")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if hasProjectGitContext(stray) {
		t.Fatalf("expected stray path with no git and no workspace parent to be rejected")
	}
}

// The Hot Reload tab is supposed to show mobile-capable projects found inside
// a larger repo like ~/Workspace/yaver.io, not just standalone repos. This
// test simulates that exact layout and locks in framework classification for
// the nested mobile projects the agent scanner is expected to surface.
func TestScanMobileProjects_DiscoversNestedFrameworksInsideYaverRepo(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	repo := filepath.Join(tmp, "Workspace", "yaver.io")
	mustMkdirAllMobileScan(t, filepath.Join(repo, ".git"))

	expoDir := filepath.Join(repo, "mobile")
	writeManifestFile(t, filepath.Join(expoDir, "package.json"), `{"name":"yaver-mobile","dependencies":{"expo":"~54.0.0","react-native-web":"^0.20.0"}}`)
	writeManifestFile(t, filepath.Join(expoDir, "app.json"), `{"expo":{"name":"Yaver Mobile"}}`)

	rnDir := filepath.Join(repo, "apps", "todo-rn")
	writeManifestFile(t, filepath.Join(rnDir, "package.json"), `{"name":"todo-rn","dependencies":{"react-native":"0.81.5"}}`)
	writeManifestFile(t, filepath.Join(rnDir, "app.json"), `{"name":"Todo RN"}`)

	flutterDir := filepath.Join(repo, "tests", "fixtures", "native-flutter-app")
	writeManifestFile(t, filepath.Join(flutterDir, "pubspec.yaml"), "name: yaver_native_flutter_app\n")

	swiftDir := filepath.Join(repo, "tests", "fixtures", "native-ios-swift")
	writeManifestFile(t, filepath.Join(swiftDir, "Package.swift"), "// swift-tools-version:5.9\n")

	kotlinDir := filepath.Join(repo, "tests", "fixtures", "native-android-kotlin")
	writeManifestFile(t, filepath.Join(kotlinDir, "settings.gradle.kts"), `rootProject.name = "android-fixture"`)
	writeManifestFile(t, filepath.Join(kotlinDir, "build.gradle.kts"), `plugins { id("com.android.application") }`)
	writeManifestFile(t, filepath.Join(kotlinDir, "app", "src", "main", "AndroidManifest.xml"), "<manifest package=\"com.example.fixture\" />")

	projects := scanMobileProjects()
	if len(projects) == 0 {
		t.Fatal("scanMobileProjects returned no projects")
	}

	assertProjectFramework(t, projects, expoDir, "expo")
	assertProjectFramework(t, projects, rnDir, "react-native")
	assertProjectFramework(t, projects, flutterDir, "flutter")
	assertProjectFramework(t, projects, swiftDir, "swift")
	assertProjectFramework(t, projects, kotlinDir, "kotlin")
}

// TestMobileCapableProjects_FiltersWebOnly guards the /projects/mobile
// payload against Next/Vite leakage. The shared discovery cache holds
// every detected project (web + mobile) so /projects/all and
// /projects/web can reuse the same walk; without this filter, tapping
// a Next.js project in Hot Reload kicked off a Hermes build for a
// folder with no React Native runtime and produced an opaque "could
// not start dev server" alert. The screenshot that motivated this
// regression had `yaver / web` and `carrotbet / web` showing up in
// the Hot Reload list — both pure-web projects that have no business
// on a phone.
func TestMobileCapableProjects_FiltersWebOnly(t *testing.T) {
	in := []MobileProject{
		{Name: "yaver / mobile", Path: "/repo/mobile", Framework: "expo", MobileCapable: true, WebCapable: true},
		{Name: "yaver / web", Path: "/repo/web", Framework: "next", WebCapable: true, MobileCapable: false},
		{Name: "carrotbet / web", Path: "/repo/apps/web", Framework: "vite", WebCapable: true, MobileCapable: false},
		{Name: "fixture / kotlin", Path: "/repo/fix/k", Framework: "kotlin", MobileCapable: true},
	}
	out := mobileCapableProjects(in)
	if len(out) != 2 {
		t.Fatalf("want 2 mobile-capable projects, got %d (%+v)", len(out), out)
	}
	for _, p := range out {
		if !p.MobileCapable {
			t.Fatalf("filter let through web-only project: %+v", p)
		}
	}
	// Empty input must still return a non-nil slice so jsonReply
	// emits `[]` instead of `null` — the mobile client's FlatList
	// silently drops a null `projects` field and renders the empty
	// state with no error, which is hard to debug.
	if got := mobileCapableProjects(nil); got == nil {
		t.Fatalf("nil input must produce non-nil empty slice for stable JSON")
	}
}

// TestScanMobileProjects_DemoShowcaseRenamesUnderYaverRepo guards the
// Hot Reload list against the "yaver (todo-rn) / mobile" leak. Apps
// under `<repo>/demo/{mobile,web}/<app>/` exist purely to demo the
// Yaver Feedback SDK (the videos shoot RN Todo from inside Yaver
// hot-reload AND standalone, so the rendered name must read as a
// discoverable showcase, not an internal subproject the user could
// break by tapping). Without the showcase override every entry in
// `yaver.io/demo/mobile/*` renders with the host repo prefix and
// looks dangerous.
func TestScanMobileProjects_DemoShowcaseRenamesUnderYaverRepo(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	repo := filepath.Join(tmp, "Workspace", "yaver.io")
	mustMkdirAllMobileScan(t, filepath.Join(repo, ".git"))

	// Demo Expo app with a friendly app.json name → must show "Todo RN / mobile".
	todoRN := filepath.Join(repo, "demo", "mobile", "todo-rn")
	writeManifestFile(t, filepath.Join(todoRN, "package.json"), `{"name":"todo-rn","dependencies":{"expo":"~54.0.0"}}`)
	writeManifestFile(t, filepath.Join(todoRN, "app.json"), `{"expo":{"name":"Todo RN"}}`)

	// Demo Kotlin fixture with no friendly name → falls back to dir leaf.
	todoKt := filepath.Join(repo, "demo", "mobile", "todo-kt")
	writeManifestFile(t, filepath.Join(todoKt, "settings.gradle.kts"), `rootProject.name = "todo-kt"`)
	writeManifestFile(t, filepath.Join(todoKt, "build.gradle.kts"), `plugins { id("com.android.application") }`)
	writeManifestFile(t, filepath.Join(todoKt, "app", "src", "main", "AndroidManifest.xml"), `<manifest package="io.yaver.todokt"/>`)

	// The yaver mobile app itself lives at <repo>/mobile, NOT under
	// demo/ — must keep the standard naming so the user can still
	// distinguish "yaver / mobile" from the showcases.
	yaverMobile := filepath.Join(repo, "mobile")
	writeManifestFile(t, filepath.Join(yaverMobile, "package.json"), `{"name":"yaver-mobile","dependencies":{"expo":"~54.0.0"}}`)
	writeManifestFile(t, filepath.Join(yaverMobile, "app.json"), `{"expo":{"name":"Yaver"}}`)

	projects := scanMobileProjects()
	wantNameForPath := map[string]string{
		todoRN:      "Todo RN / mobile",
		todoKt:      "todo-kt / mobile",
		yaverMobile: "yaver / mobile",
	}
	got := map[string]string{}
	for _, p := range projects {
		got[filepath.Clean(p.Path)] = p.Name
	}
	for path, want := range wantNameForPath {
		clean := filepath.Clean(path)
		if name, ok := got[clean]; !ok {
			t.Fatalf("project %s not discovered (got %d projects)", path, len(projects))
		} else if name != want {
			t.Fatalf("project %s name = %q, want %q", path, name, want)
		}
	}
}

// TestParseAppName_NativeFixtures covers the kotlin/swift parsers
// added so the moved native fixtures get friendly launcher labels in
// the Hot Reload list — matching what Expo's app.json already gives
// Bento. Also pins the Flutter parser to prefer the Android launcher
// label (literal) over the snake_case pubspec name, since the launcher
// label is the actual user-facing display name on the home screen.
func TestParseAppName_NativeFixtures(t *testing.T) {
	t.Run("kotlin_strings_xml", func(t *testing.T) {
		dir := t.TempDir()
		writeManifestFile(t, filepath.Join(dir, "app", "src", "main", "res", "values", "strings.xml"),
			`<?xml version="1.0" encoding="utf-8"?>`+"\n"+
				`<resources><string name="app_name">Todo Kt</string></resources>`)
		if got := parseAppName(dir, "kotlin"); got != "Todo Kt" {
			t.Fatalf("kotlin app_name = %q, want %q", got, "Todo Kt")
		}
	})

	t.Run("kotlin_falls_back_to_settings_gradle", func(t *testing.T) {
		dir := t.TempDir()
		// No strings.xml — only the gradle settings.
		writeManifestFile(t, filepath.Join(dir, "settings.gradle.kts"), `rootProject.name = "todo-kt"`)
		if got := parseAppName(dir, "kotlin"); got != "todo-kt" {
			t.Fatalf("kotlin gradle fallback = %q, want %q", got, "todo-kt")
		}
	})

	t.Run("kotlin_strings_at_string_ref_skipped", func(t *testing.T) {
		dir := t.TempDir()
		// Some templates set app_name to a @string/* reference of a
		// different key; we don't chase it (would recurse into the
		// other entry). Caller falls back to settings.gradle.
		writeManifestFile(t, filepath.Join(dir, "app", "src", "main", "res", "values", "strings.xml"),
			`<resources><string name="app_name">@string/launcher_name</string></resources>`)
		writeManifestFile(t, filepath.Join(dir, "settings.gradle.kts"), `rootProject.name = "todo-kt"`)
		if got := parseAppName(dir, "kotlin"); got != "todo-kt" {
			t.Fatalf("kotlin @string/ ref must skip to settings.gradle, got %q", got)
		}
	})

	t.Run("swift_info_plist_display_name", func(t *testing.T) {
		dir := t.TempDir()
		writeManifestFile(t, filepath.Join(dir, "TodoSwift", "Info.plist"),
			`<?xml version="1.0"?><plist><dict>`+
				`<key>CFBundleDisplayName</key><string>Todo Swift</string>`+
				`<key>CFBundleName</key><string>TodoSwift</string>`+
				`</dict></plist>`)
		if got := parseAppName(dir, "swift"); got != "Todo Swift" {
			t.Fatalf("swift display name = %q, want %q", got, "Todo Swift")
		}
	})

	t.Run("swift_xcodegen_project_yml", func(t *testing.T) {
		dir := t.TempDir()
		// xcodegen project that auto-generates Info.plist at build
		// time — no plist on disk, so the parser falls back to the
		// INFOPLIST_KEY_* setting in project.yml. This is the exact
		// shape demo/mobile/todo-swift uses.
		writeManifestFile(t, filepath.Join(dir, "project.yml"), `
name: TodoSwift
targets:
  TodoSwift:
    settings:
      base:
        GENERATE_INFOPLIST_FILE: "YES"
        INFOPLIST_KEY_CFBundleDisplayName: "Todo Swift"
`)
		if got := parseAppName(dir, "swift"); got != "Todo Swift" {
			t.Fatalf("swift xcodegen INFOPLIST_KEY = %q, want %q", got, "Todo Swift")
		}
	})

	t.Run("swift_falls_back_to_project_yml_name", func(t *testing.T) {
		dir := t.TempDir()
		writeManifestFile(t, filepath.Join(dir, "project.yml"), `name: TodoSwift`)
		if got := parseAppName(dir, "swift"); got != "TodoSwift" {
			t.Fatalf("swift project.yml name = %q, want %q", got, "TodoSwift")
		}
	})

	t.Run("swift_skips_build_setting_placeholder", func(t *testing.T) {
		dir := t.TempDir()
		// CFBundleName=$(PRODUCT_NAME) only resolves at build time.
		// Treat it as absent so the parser falls through to a real
		// human-readable label rather than literally rendering "$(…)".
		writeManifestFile(t, filepath.Join(dir, "TodoSwift", "Info.plist"),
			`<plist><dict><key>CFBundleName</key><string>$(PRODUCT_NAME)</string></dict></plist>`)
		writeManifestFile(t, filepath.Join(dir, "project.yml"), `name: TodoSwift`)
		if got := parseAppName(dir, "swift"); got != "TodoSwift" {
			t.Fatalf("swift placeholder must be skipped, got %q", got)
		}
	})

	t.Run("flutter_prefers_android_label_over_pubspec", func(t *testing.T) {
		dir := t.TempDir()
		writeManifestFile(t, filepath.Join(dir, "pubspec.yaml"), "name: todo_flutter\n")
		writeManifestFile(t, filepath.Join(dir, "android", "app", "src", "main", "AndroidManifest.xml"), `
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
  <application android:label="Todo Flutter" android:name=".App" />
</manifest>`)
		if got := parseAppName(dir, "flutter"); got != "Todo Flutter" {
			t.Fatalf("flutter android label = %q, want %q", got, "Todo Flutter")
		}
	})

	t.Run("flutter_resolves_string_ref", func(t *testing.T) {
		dir := t.TempDir()
		writeManifestFile(t, filepath.Join(dir, "pubspec.yaml"), "name: todo_flutter\n")
		writeManifestFile(t, filepath.Join(dir, "android", "app", "src", "main", "AndroidManifest.xml"), `
<manifest><application android:label="@string/app_name"/></manifest>`)
		writeManifestFile(t, filepath.Join(dir, "android", "app", "src", "main", "res", "values", "strings.xml"),
			`<resources><string name="app_name">Todo Flutter</string></resources>`)
		if got := parseAppName(dir, "flutter"); got != "Todo Flutter" {
			t.Fatalf("flutter @string/app_name resolution = %q, want %q", got, "Todo Flutter")
		}
	})

	t.Run("flutter_pubspec_fallback", func(t *testing.T) {
		dir := t.TempDir()
		writeManifestFile(t, filepath.Join(dir, "pubspec.yaml"), "name: todo_flutter\n")
		if got := parseAppName(dir, "flutter"); got != "todo_flutter" {
			t.Fatalf("flutter pubspec fallback = %q, want %q", got, "todo_flutter")
		}
	})
}

func TestDemoShowcaseProject(t *testing.T) {
	cases := map[string]struct {
		dir          string
		wantSurface  string
		wantLeaf     string
	}{
		"mobile_under_yaver":   {"/Users/k/Workspace/yaver.io/demo/mobile/todo-rn", "mobile", "todo-rn"},
		"web_under_yaver":      {"/Users/k/Workspace/yaver.io/demo/web/todo-web", "web", "todo-web"},
		"deep_nested":          {"/repo/demo/mobile/bento/src/screens", "mobile", "bento"},
		"vendored_demo":        {"/host-repo/vendor/yaver/demo/mobile/x", "mobile", "x"},
		"non_demo":             {"/Users/k/Workspace/sfmg/mobile", "", ""},
		"demo_without_surface": {"/repo/demo/something/else", "", ""},
		"demo_at_root":         {"/repo/demo/mobile", "", ""}, // need a leaf after the surface
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			surf, leaf := demoShowcaseProject(tc.dir)
			if surf != tc.wantSurface || leaf != tc.wantLeaf {
				t.Fatalf("demoShowcaseProject(%q) = (%q, %q), want (%q, %q)", tc.dir, surf, leaf, tc.wantSurface, tc.wantLeaf)
			}
		})
	}
}

func mustMkdirAllMobileScan(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func assertProjectFramework(t *testing.T, projects []MobileProject, path, wantFramework string) {
	t.Helper()
	for _, p := range projects {
		if filepath.Clean(p.Path) != filepath.Clean(path) {
			continue
		}
		if p.Framework != wantFramework {
			t.Fatalf("project %s framework = %q, want %q", path, p.Framework, wantFramework)
		}
		if !p.MobileCapable {
			t.Fatalf("project %s should be mobile-capable", path)
		}
		return
	}
	t.Fatalf("project %s was not discovered", path)
}
