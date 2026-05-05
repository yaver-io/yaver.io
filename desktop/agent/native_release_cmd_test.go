package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleasePlatformForCandidate(t *testing.T) {
	cases := []struct {
		native string
		stack  string
		want   BuildPlatform
	}{
		{NativeIOS, "flutter", PlatformFlutterIPA},
		{NativeIOS, "expo", PlatformXcodeIPA},
		{NativeIOS, "react-native", PlatformXcodeIPA},
		{NativeIOS, "native-ios", PlatformXcodeIPA},
		{NativeAndroid, "flutter", PlatformFlutterAAB},
		{NativeAndroid, "expo", PlatformGradleAAB},
		{NativeAndroid, "react-native", PlatformGradleAAB},
		{NativeAndroid, "native-android", PlatformGradleAAB},
	}
	for _, tc := range cases {
		got, err := releasePlatformForCandidate(tc.native, tc.stack)
		if err != nil {
			t.Fatalf("%s/%s: unexpected error: %v", tc.native, tc.stack, err)
		}
		if got != tc.want {
			t.Fatalf("%s/%s: got %s want %s", tc.native, tc.stack, got, tc.want)
		}
	}
}

func TestDiscoverNativeProjectCandidatesFiltersByPlatform(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("mobile/package.json", `{"dependencies":{"expo":"~54.0.0","react-native":"0.81.5"}}`)
	mustWrite("apps/swiftapp/ios/MyApp.xcodeproj/project.pbxproj", `PRODUCT_BUNDLE_IDENTIFIER = com.example.swift;`)
	mustWrite("packages/kotlin/android/build.gradle", `plugins { id "com.android.application" }`)
	mustWrite("packages/kotlin/android/app/build.gradle", `android { defaultConfig { applicationId "com.example.kotlin" } }`)

	iosHits := discoverNativeProjectCandidates(root, NativeIOS)
	if len(iosHits) != 2 {
		t.Fatalf("ios hits=%d want 2 (%v)", len(iosHits), iosHits)
	}
	androidHits := discoverNativeProjectCandidates(root, NativeAndroid)
	if len(androidHits) != 2 {
		t.Fatalf("android hits=%d want 2 (%v)", len(androidHits), androidHits)
	}
	for _, hit := range iosHits {
		if hit.Stack == "native-android" {
			t.Fatalf("ios discovery should not include android-only project: %+v", hit)
		}
	}
	for _, hit := range androidHits {
		if hit.Stack == "native-ios" {
			t.Fatalf("android discovery should not include ios-only project: %+v", hit)
		}
	}
}

func TestDiscoverNativeProjectCandidates_YaverRepoFallsBackToRootMobile(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runGitCmd(t, root, "init")
	runGitCmd(t, root, "remote", "add", "origin", "https://github.com/kivanccakmak/yaver.io")
	mustWrite("mobile/package.json", `{"dependencies":{"expo":"~54.0.0","react-native":"0.81.5"}}`)
	if err := os.MkdirAll(filepath.Join(root, "desktop", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := filepath.Join(root, "desktop", "agent")
	wantMobile, err := filepath.EvalSymlinks(filepath.Join(root, "mobile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, native := range []string{NativeIOS, NativeAndroid} {
		hits := discoverNativeProjectCandidates(start, native)
		if len(hits) != 1 {
			t.Fatalf("%s hits=%d want 1 (%v)", native, len(hits), hits)
		}
		if hits[0].Path != wantMobile {
			t.Fatalf("%s path=%q want %q", native, hits[0].Path, wantMobile)
		}
		if hits[0].Stack != "expo" {
			t.Fatalf("%s stack=%q want expo", native, hits[0].Stack)
		}
	}
}
