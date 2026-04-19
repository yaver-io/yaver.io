package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Native fingerprinting for Expo / React-Native projects.
//
// Hot reload pushes a new Hermes bundle, which replaces JS only. Anything that
// lives in native resources (splash background, app icon, Info.plist entries,
// permission strings, bundle identifier, native pod list, Android gradle deps,
// splash images) cannot be changed that way — the device has to install a
// freshly-built binary.
//
// We compute a sha256 over a deterministic list of native-affecting files at
// dev-server start time. On every /dev/reload we recompute and compare: if the
// digest differs, the reload will visually lie about the splash / icon / etc.,
// so we flag `nativeChangesDetected=true` in the response and point at the
// files that drifted. The mobile client uses that to nudge the user toward a
// full rebuild (expo run:ios --device …).

// NativeFingerprint is the per-file view we expose back to callers — the path
// (relative to the project root) plus the individual file sha256. Callers can
// diff two fingerprints to tell the user exactly which file moved.
type NativeFingerprint struct {
	// Digest is sha256(hex(sha256(file1) + path1 + sha256(file2) + path2 …)).
	// Stable for any ordering, since we sort paths alphabetically first.
	Digest string `json:"digest"`
	// Files is a path → per-file sha256 map. Missing files get an empty string
	// so a freshly-added splash.png also counts as a change.
	Files map[string]string `json:"files"`
}

// nativeFingerprintPaths is the deterministic list of files that, when any of
// them changes, require a native rebuild for the change to actually show up
// on the phone.
//
// These are relative to the project root. Missing files are OK — we hash the
// empty byte string for them so "the file appeared" is also a change.
var nativeFingerprintPaths = []string{
	// Expo / RN config — splash background, icon, plugins, bundleId, permissions.
	"app.json",
	"app.config.js",
	"app.config.ts",
	"app.config.json",
	// package.json — adding/removing a native module requires a rebuild.
	"package.json",
	// iOS project / pods / native entitlements.
	"ios/Podfile",
	"ios/Podfile.lock",
	"ios/Podfile.properties.json",
	// Android build config / gradle deps.
	"android/build.gradle",
	"android/settings.gradle",
	"android/gradle.properties",
	"android/app/build.gradle",
	// Common splash / icon assets referenced from app.json.
	"assets/icon.png",
	"assets/splash.png",
	"assets/splash-icon.png",
	"assets/splash-dark.png",
	"assets/adaptive-icon.png",
	// RN CLI (non-Expo) apps keep their splash / launch screen here.
	"ios/SplashScreen.storyboard",
}

// ComputeNativeFingerprint returns a NativeFingerprint for the given project
// directory. Always non-error — unreadable / missing files simply contribute
// an empty content hash. The caller's workflow should never fail because a
// fingerprint couldn't be computed.
func ComputeNativeFingerprint(workDir string) NativeFingerprint {
	files := make(map[string]string, len(nativeFingerprintPaths))

	// Iterate deterministically so the concatenated digest is stable.
	sorted := append([]string(nil), nativeFingerprintPaths...)
	sort.Strings(sorted)

	// Also sweep ios/*/Info.plist — one per Xcode target and not at a fixed
	// subpath because the target name (Yaver, Talos, SFMG …) varies.
	if infoPlists, err := filepath.Glob(filepath.Join(workDir, "ios", "*", "Info.plist")); err == nil {
		for _, full := range infoPlists {
			rel, _ := filepath.Rel(workDir, full)
			sorted = append(sorted, rel)
		}
	}
	// Same idea for Android's AndroidManifest.xml — always at a known path
	// under expo prebuild, but be defensive.
	if manifests, err := filepath.Glob(filepath.Join(workDir, "android", "app", "src", "main", "AndroidManifest.xml")); err == nil {
		for _, full := range manifests {
			rel, _ := filepath.Rel(workDir, full)
			sorted = append(sorted, rel)
		}
	}
	sort.Strings(sorted)

	// De-dup — glob sweeps can collide with the static list.
	seen := make(map[string]bool, len(sorted))
	unique := sorted[:0]
	for _, p := range sorted {
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}
	sorted = unique

	overall := sha256.New()
	for _, rel := range sorted {
		full := filepath.Join(workDir, rel)
		per := hashFileQuiet(full)
		files[rel] = per
		fmt.Fprintf(overall, "%s\x00%s\x00", rel, per)
	}

	return NativeFingerprint{
		Digest: hex.EncodeToString(overall.Sum(nil)),
		Files:  files,
	}
}

// hashFileQuiet returns sha256 hex of the file contents, or "" if the file is
// missing / unreadable. We deliberately do not return an error — missing
// files become part of the digest as the empty string.
func hashFileQuiet(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NativeFingerprintDelta lists every path whose per-file hash changed between
// two fingerprints, plus a classification hint the mobile client can show.
type NativeFingerprintDelta struct {
	Changed []NativeFingerprintChange `json:"changed"`
}

// NativeFingerprintChange describes a single file's change between fingerprints.
type NativeFingerprintChange struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reason string `json:"reason"` // human-readable why-this-needs-rebuild
}

// DiffNativeFingerprints returns the list of paths whose hash differs between
// `from` and `to`. The hint for each path is derived from the path itself so
// the mobile UI can say e.g. "splash color changed — rebuild required".
func DiffNativeFingerprints(from, to NativeFingerprint) NativeFingerprintDelta {
	var changes []NativeFingerprintChange
	seen := make(map[string]bool, len(from.Files)+len(to.Files))
	for p := range from.Files {
		seen[p] = true
	}
	for p := range to.Files {
		seen[p] = true
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b := from.Files[p]
		a := to.Files[p]
		if a == b {
			continue
		}
		changes = append(changes, NativeFingerprintChange{
			Path:   p,
			Before: b,
			After:  a,
			Reason: nativeChangeReason(p),
		})
	}
	return NativeFingerprintDelta{Changed: changes}
}

// nativeChangeReason returns a short explanation of why a change to the given
// path can't be picked up by a Hermes-only hot reload.
func nativeChangeReason(path string) string {
	switch {
	case path == "app.json" || path == "app.config.js" || path == "app.config.ts" || path == "app.config.json":
		return "expo config — splash/icon/plugins/permissions resolve at prebuild time"
	case path == "package.json":
		return "dependency list — new native modules need pod install + rebuild"
	case filepath.Base(path) == "Podfile" || filepath.Base(path) == "Podfile.lock" || filepath.Base(path) == "Podfile.properties.json":
		return "iOS pods — rebuild required to link new native code"
	case filepath.Ext(path) == ".gradle" || filepath.Base(path) == "gradle.properties":
		return "Android gradle config — rebuild required"
	case filepath.Base(path) == "Info.plist":
		return "iOS Info.plist — permissions/bundle settings baked into the binary"
	case filepath.Base(path) == "AndroidManifest.xml":
		return "Android manifest — permissions/components baked into the APK"
	case filepath.Dir(path) == "assets" || filepath.Dir(path) == filepath.Join("assets"):
		return "splash/icon asset — expo-splash-screen embeds these at prebuild time"
	case filepath.Base(path) == "SplashScreen.storyboard":
		return "iOS launch storyboard — compiled into the app bundle"
	default:
		return "native file change — rebuild required"
	}
}

// ─── DevServerManager glue ─────────────────────────────────────────────
//
// We keep a per-session baseline fingerprint captured at Start() time. The
// /dev/reload handler reads + recomputes to report the delta without
// mutating state (so repeated reloads against the same native change keep
// saying "yes this is still a native change"). The user can explicitly
// call RefreshNativeBaseline() after they've kicked off a rebuild, so the
// next /dev/reload compares against the new state.

// nativeBaselineMu protects nativeBaseline.
var nativeBaselineMu sync.RWMutex
var nativeBaseline = make(map[string]NativeFingerprint) // workDir → fingerprint

// SetNativeBaseline stores the baseline fingerprint for a given work dir.
// Called by DevServerManager.Start so every /dev/reload afterwards can diff
// against it.
func SetNativeBaseline(workDir string, fp NativeFingerprint) {
	if workDir == "" {
		return
	}
	nativeBaselineMu.Lock()
	nativeBaseline[workDir] = fp
	nativeBaselineMu.Unlock()
}

// GetNativeBaseline returns the stored baseline (ok=false if none exists yet).
func GetNativeBaseline(workDir string) (NativeFingerprint, bool) {
	nativeBaselineMu.RLock()
	defer nativeBaselineMu.RUnlock()
	fp, ok := nativeBaseline[workDir]
	return fp, ok
}

// ClearNativeBaseline forgets the baseline for a given workDir. Called by Stop.
func ClearNativeBaseline(workDir string) {
	if workDir == "" {
		return
	}
	nativeBaselineMu.Lock()
	delete(nativeBaseline, workDir)
	nativeBaselineMu.Unlock()
}
