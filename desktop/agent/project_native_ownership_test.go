package main

// Closed-loop tests: build the exact directory shapes that produced the
// phantoms on the author's machine, and assert the scan's classifier reports
// ONE app with the framework the user actually develops in.

import (
	"os"
	"path/filepath"
	"testing"
)

// expoAppTree writes an Expo app with the generated native shells RN creates —
// the shape of ~/Workspace/sfmg and ~/Workspace/carrotbet.
func expoAppTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"dependencies":{"expo":"~52.0.0","react-native":"0.76.0"}}`)
	// RN's generated iOS shell.
	write("ios/Podfile", "platform :ios")
	write("ios/MyApp/Info.plist", "<plist/>")
	write("ios/MyApp.xcodeproj/project.pbxproj", "// pbxproj")
	// RN's generated Android shell.
	write("android/build.gradle", "// root gradle")
	write("android/settings.gradle", "include ':app'")
	write("android/app/build.gradle", `apply plugin: "com.android.application"`)
	write("android/app/src/main/AndroidManifest.xml", "<manifest/>")
	return root
}

// The headline case. Before the ownership rule this produced three projects:
// expo at the root, swift ALSO at the root (the ios shell resolved up and stole
// it), and kotlin at android/.
func TestExpoAppIsOneProjectNotThree(t *testing.T) {
	root := expoAppTree(t)

	if got := classifyProjectMarker(filepath.Join(root, "package.json"), "package.json", root); got.Framework != "expo" {
		t.Fatalf("root must classify as expo, got %q", got.Framework)
	}

	// The generated iOS shell must not become a Swift project, and above all
	// must not claim the app's own directory.
	plist := filepath.Join(root, "ios", "MyApp", "Info.plist")
	if got := classifyProjectMarker(plist, "Info.plist", filepath.Dir(plist)); got.Framework != "" {
		t.Errorf("generated ios shell became a %q project at %q — this is what relabelled the repo root",
			got.Framework, got.Dir)
	}

	// The generated Android shell must not become a Kotlin project.
	gradle := filepath.Join(root, "android", "build.gradle")
	if got := classifyProjectMarker(gradle, "build.gradle", filepath.Dir(gradle)); got.Framework != "" {
		t.Errorf("generated android shell became a %q project at %q", got.Framework, got.Dir)
	}
}

// Flutter generates far more shells than RN. All of them are output.
func TestFlutterNativeShellsAreNotProjects(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	mk("pubspec.yaml", "name: todo\ndependencies:\n  flutter:\n    sdk: flutter\n")
	mk("ios/Runner/Info.plist", "<plist/>")
	mk("android/build.gradle", "// gradle")
	mk("android/app/src/main/AndroidManifest.xml", "<manifest/>")
	mk("macos/Runner/Info.plist", "<plist/>")

	if got := classifyProjectMarker(filepath.Join(root, "pubspec.yaml"), "pubspec.yaml", root); got.Framework != "flutter" {
		t.Fatalf("root must be flutter, got %q", got.Framework)
	}
	for _, shell := range []struct{ path, marker string }{
		{filepath.Join(root, "ios", "Runner", "Info.plist"), "Info.plist"},
		{filepath.Join(root, "macos", "Runner", "Info.plist"), "Info.plist"},
		{filepath.Join(root, "android", "build.gradle"), "build.gradle"},
	} {
		if got := classifyProjectMarker(shell.path, shell.marker, filepath.Dir(shell.path)); got.Framework != "" {
			t.Errorf("%s became a %q project", shell.path, got.Framework)
		}
	}
}

// The rule must not swallow genuine native apps: nothing above their ios/
// declares an RN or Flutter manifest.
func TestStandaloneNativeAppsStillDetected(t *testing.T) {
	// A standalone iOS app.
	ios := t.TempDir()
	os.MkdirAll(filepath.Join(ios, "MyApp.xcodeproj"), 0o755)
	os.WriteFile(filepath.Join(ios, "MyApp.xcodeproj", "project.pbxproj"), []byte("//"), 0o644)
	os.MkdirAll(filepath.Join(ios, "MyApp"), 0o755)
	plist := filepath.Join(ios, "MyApp", "Info.plist")
	os.WriteFile(plist, []byte("<plist/>"), 0o644)
	if got := classifyProjectMarker(plist, "Info.plist", filepath.Dir(plist)); got.Framework != "swift" {
		t.Errorf("standalone iOS app must stay swift, got %q", got.Framework)
	}

	// A standalone Android app.
	and := t.TempDir()
	os.WriteFile(filepath.Join(and, "build.gradle"), []byte("//"), 0o644)
	os.WriteFile(filepath.Join(and, "settings.gradle"), []byte("include ':app'"), 0o644)
	os.MkdirAll(filepath.Join(and, "app", "src", "main"), 0o755)
	os.WriteFile(filepath.Join(and, "app", "build.gradle"), []byte(`apply plugin: "com.android.application"`), 0o644)
	os.WriteFile(filepath.Join(and, "app", "src", "main", "AndroidManifest.xml"), []byte("<manifest/>"), 0o644)
	if got := classifyProjectMarker(filepath.Join(and, "build.gradle"), "build.gradle", and); got.Framework != "kotlin" {
		t.Errorf("standalone Android app must stay kotlin, got %q", got.Framework)
	}
}

// `talos (Contents) / mobile` came from a COMPILED .app bundle's Info.plist.
func TestCompiledBundlesAreNotProjects(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "build", "Talos.app", "Contents")
	os.MkdirAll(inner, 0o755)
	plist := filepath.Join(inner, "Info.plist")
	os.WriteFile(plist, []byte("<plist/>"), 0o644)

	if !isBuiltAppBundlePath(plist) {
		t.Fatal("a path inside a .app must be recognised as build output")
	}
	if got := classifyProjectMarker(plist, "Info.plist", inner); got.Framework != "" {
		t.Errorf("compiled bundle became a %q project — this is the `talos (Contents)` phantom", got.Framework)
	}
	for _, p := range []string{
		filepath.Join(root, "X.framework", "Info.plist"),
		filepath.Join(root, "DerivedData", "x", "Info.plist"),
		filepath.Join(root, "Out.xcarchive", "Info.plist"),
	} {
		if !isBuiltAppBundlePath(p) {
			t.Errorf("%s should be treated as build output", p)
		}
	}
	// Source must NOT be mistaken for output.
	if isBuiltAppBundlePath(filepath.Join(root, "ios", "MyApp", "Info.plist")) {
		t.Error("a source Info.plist must not be classed as build output")
	}
}

// A plain Node package.json above a native dir must NOT claim ownership —
// only a real RN/Expo/Flutter manifest does.
func TestPlainNodeRepoDoesNotOwnNativeDirs(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"express":"4"}}`), 0o644)
	if declaresCrossPlatformApp(root) {
		t.Error("a plain Node package must not own native shells")
	}
	os.MkdirAll(filepath.Join(root, "ios"), 0o755)
	if owner := nativeShellOwner(filepath.Join(root, "ios")); owner != "" {
		t.Errorf("plain Node repo must not own ios/, got owner %q", owner)
	}
}

func TestNativeShellOwnerResolvesNestedShellPaths(t *testing.T) {
	root := expoAppTree(t)
	for _, sub := range []string{
		filepath.Join(root, "ios"),
		filepath.Join(root, "ios", "MyApp"),
		filepath.Join(root, "android"),
		filepath.Join(root, "android", "app"),
	} {
		if owner := nativeShellOwner(sub); owner != root {
			t.Errorf("nativeShellOwner(%s) = %q, want %q", sub, owner, root)
		}
	}
	if owner := nativeShellOwner(root); owner != "" {
		t.Errorf("the app root is not owned by anything, got %q", owner)
	}
}
