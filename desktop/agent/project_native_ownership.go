package main

// project_native_ownership.go — "who owns this native directory?"
//
// The bug this fixes, seen on the author's own machine 2026-07-22:
//
//	sfmg (android) / mobile   kotlin   ~/Workspace/sfmg/android
//	sfmg / mobile             swift    ~/Workspace/sfmg          ← the REPO ROOT
//
// `~/Workspace/sfmg` is ONE Expo app — its package.json depends on `expo` and
// `react-native`. Yaver reported it three times under two frameworks it does
// not develop in, and the swift entry claimed the repo root itself, so the
// project the user actually works on was labelled with the wrong language.
// Same for carrotbet. The Reload tab's "Available apps" list showed the same
// phantoms, because both surfaces read the same scan.
//
// Cause: classifyProjectMarker judges each marker file in isolation. An RN or
// Flutter app CONTAINS a generated `ios/` (with Info.plist + .xcodeproj) and
// `android/` (with build.gradle) — those are its build system, not separate
// projects. The scan's only dedupe (`nestedDup`) suppresses nesting within the
// SAME framework, so `expo` at the root and `swift`/`kotlin` beneath it never
// collided and all three survived.
//
// The rule, stated as the model rather than as a patch:
//
//	A native platform directory is a PROJECT only when nothing above it
//	already declares itself the app. `ios/`, `android/`, `macos/`, `windows/`,
//	`linux/` and `web/` directly beneath an RN/Expo (`package.json` with
//	react-native or expo) or Flutter (`pubspec.yaml`) manifest are that app's
//	OUTPUT — the same way `build/` is.
//
// What this deliberately does NOT claim: that there is no Swift or Kotlin in
// there. RN and Flutter apps routinely carry real native code — custom native
// modules, an AppDelegate, platform channels — and that code is genuinely
// Swift and Kotlin. The claim is narrower and is about IDENTITY: the app is
// one Expo app whose native shells happen to contain Swift and Kotlin, not
// three apps. A user picking a project to vibe on wants the Expo app; the
// native sources come with it.
//
// A standalone native app is unaffected, because nothing above its `ios/`
// declares an RN or Flutter manifest.

import (
	"os"
	"path/filepath"
	"strings"
)

// nativeShellDirs are the platform directories RN and Flutter generate. Flutter
// adds macos/windows/linux/web; RN adds ios/android. `web` is included because
// `flutter create` generates it and it is not a separate web project.
var nativeShellDirs = map[string]bool{
	"ios": true, "android": true, "macos": true,
	"windows": true, "linux": true, "web": true,
}

// nativeShellOwner reports the directory of the RN/Expo/Flutter app that owns
// `dir` as generated native output, or "" when `dir` is a project in its own
// right.
//
// It walks up looking for a `nativeShellDirs` segment whose PARENT carries an
// app manifest. Bounded to a few levels because the interesting cases are
// shallow (`<app>/ios`, `<app>/ios/Runner`, `<app>/android/app`) and an
// unbounded walk on every marker would put filesystem reads in the scan's
// critical path.
func nativeShellOwner(dir string) string {
	cur := filepath.Clean(dir)
	for i := 0; i < 6; i++ {
		parent := filepath.Dir(cur)
		if parent == cur || parent == string(filepath.Separator) {
			return ""
		}
		if nativeShellDirs[strings.ToLower(filepath.Base(cur))] {
			// cur is e.g. <app>/ios — does <app> declare itself?
			if declaresCrossPlatformApp(parent) {
				return parent
			}
		}
		cur = parent
	}
	return ""
}

// declaresCrossPlatformApp reports whether dir holds an RN/Expo or Flutter
// manifest — i.e. whether it is the app that owns any native shells beneath it.
//
// Reads the manifest rather than trusting the directory name: a folder called
// `mobile` proves nothing, and `package.json` alone is just Node.
func declaresCrossPlatformApp(dir string) bool {
	if projectFileExists(filepath.Join(dir, "pubspec.yaml")) {
		return true
	}
	pkg := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkg)
	if err != nil {
		return false
	}
	content := string(data)
	// Same predicates classifyProjectMarker uses for expo / react-native, kept
	// deliberately in step with it: if that function learns a new way to spot
	// an RN app, this must learn it too or the phantoms come back.
	return strings.Contains(content, `"expo"`) ||
		strings.Contains(content, `"react-native":`) ||
		strings.Contains(content, `"react-native" :`)
}

// isBuiltAppBundlePath reports whether a path sits inside a COMPILED artifact
// rather than source.
//
// This is the other phantom the scan produced: `talos (Contents) / mobile` as
// swift, from a `Contents/Info.plist` inside a built `.app` bundle. Every
// `.app`, `.framework`, `.xcarchive` and `.xcodeproj` is build output, and an
// Info.plist inside one describes a binary, not a project the user can open.
func isBuiltAppBundlePath(path string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		lower := strings.ToLower(seg)
		if strings.HasSuffix(lower, ".app") ||
			strings.HasSuffix(lower, ".framework") ||
			strings.HasSuffix(lower, ".xcarchive") ||
			strings.HasSuffix(lower, ".appex") ||
			lower == "deriveddata" {
			return true
		}
	}
	return false
}
