package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a tiny test helper that writes `data` to `rel` under `root`,
// creating parent dirs as needed. Intentionally non-exported.
func writeFileNFT(t *testing.T, root, rel, data string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestNativeFingerprint_Empty(t *testing.T) {
	dir := t.TempDir()
	fp := ComputeNativeFingerprint(dir)
	if fp.Digest == "" {
		t.Fatal("digest must be non-empty even for empty project (hashes empty strings)")
	}
	// Every well-known path must appear in Files with empty hash.
	for _, p := range nativeFingerprintPaths {
		if _, ok := fp.Files[p]; !ok {
			t.Errorf("missing tracked path %q in fingerprint", p)
		}
	}
}

func TestNativeFingerprint_Stable(t *testing.T) {
	dir := t.TempDir()
	writeFileNFT(t, dir, "app.json", `{"expo":{"name":"demo","splash":{"backgroundColor":"#fff"}}}`)
	writeFileNFT(t, dir, "package.json", `{"dependencies":{"expo":"~54.0.0"}}`)
	a := ComputeNativeFingerprint(dir)
	b := ComputeNativeFingerprint(dir)
	if a.Digest != b.Digest {
		t.Fatalf("digest unstable: %s vs %s", a.Digest, b.Digest)
	}
}

func TestNativeFingerprint_DetectsSplashColorChange(t *testing.T) {
	dir := t.TempDir()
	writeFileNFT(t, dir, "app.json", `{"expo":{"splash":{"backgroundColor":"#000"}}}`)
	before := ComputeNativeFingerprint(dir)
	writeFileNFT(t, dir, "app.json", `{"expo":{"splash":{"backgroundColor":"#fff"}}}`)
	after := ComputeNativeFingerprint(dir)
	if before.Digest == after.Digest {
		t.Fatal("digest should change when app.json splash.backgroundColor changes")
	}
	delta := DiffNativeFingerprints(before, after)
	if len(delta.Changed) != 1 {
		t.Fatalf("expected exactly 1 changed path, got %d: %+v", len(delta.Changed), delta.Changed)
	}
	c := delta.Changed[0]
	if c.Path != "app.json" {
		t.Errorf("expected change in app.json, got %q", c.Path)
	}
	if !strings.Contains(c.Reason, "splash") {
		t.Errorf("reason should mention splash, got %q", c.Reason)
	}
}

func TestNativeFingerprint_DetectsPodfileChange(t *testing.T) {
	dir := t.TempDir()
	writeFileNFT(t, dir, "ios/Podfile", "platform :ios, '15.1'\n")
	before := ComputeNativeFingerprint(dir)
	writeFileNFT(t, dir, "ios/Podfile", "platform :ios, '15.5'\n")
	after := ComputeNativeFingerprint(dir)
	delta := DiffNativeFingerprints(before, after)
	if len(delta.Changed) != 1 || delta.Changed[0].Path != "ios/Podfile" {
		t.Fatalf("expected Podfile change, got %+v", delta.Changed)
	}
	if !strings.Contains(delta.Changed[0].Reason, "pod") {
		t.Errorf("reason should mention pods, got %q", delta.Changed[0].Reason)
	}
}

func TestNativeFingerprint_IgnoresPureJSChange(t *testing.T) {
	dir := t.TempDir()
	writeFileNFT(t, dir, "app.json", `{"expo":{"name":"demo"}}`)
	writeFileNFT(t, dir, "src/App.tsx", `export default () => null`)
	before := ComputeNativeFingerprint(dir)
	// Change JS only.
	writeFileNFT(t, dir, "src/App.tsx", `export default () => <View/>`)
	after := ComputeNativeFingerprint(dir)
	if before.Digest != after.Digest {
		t.Fatal("JS source changes must NOT alter the native fingerprint")
	}
	delta := DiffNativeFingerprints(before, after)
	if len(delta.Changed) != 0 {
		t.Fatalf("expected no changed files for a JS-only edit, got %+v", delta.Changed)
	}
}

func TestNativeFingerprint_DetectsInfoPlistGlob(t *testing.T) {
	dir := t.TempDir()
	// Mimic expo-prebuild output: ios/<target>/Info.plist.
	writeFileNFT(t, dir, "ios/Yaver/Info.plist", `<plist><key>foo</key></plist>`)
	before := ComputeNativeFingerprint(dir)
	writeFileNFT(t, dir, "ios/Yaver/Info.plist", `<plist><key>bar</key></plist>`)
	after := ComputeNativeFingerprint(dir)
	delta := DiffNativeFingerprints(before, after)
	found := false
	for _, c := range delta.Changed {
		if c.Path == "ios/Yaver/Info.plist" {
			found = true
			if !strings.Contains(c.Reason, "Info.plist") {
				t.Errorf("reason should mention Info.plist, got %q", c.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("expected ios/Yaver/Info.plist change, got %+v", delta.Changed)
	}
}

func TestNativeFingerprint_BaselineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFileNFT(t, dir, "app.json", `{"expo":{"version":"1.0.0"}}`)
	fp := ComputeNativeFingerprint(dir)
	SetNativeBaseline(dir, fp)
	defer ClearNativeBaseline(dir)

	got, ok := GetNativeBaseline(dir)
	if !ok {
		t.Fatal("baseline not stored")
	}
	if got.Digest != fp.Digest {
		t.Fatalf("baseline digest mismatch: %s vs %s", got.Digest, fp.Digest)
	}

	ClearNativeBaseline(dir)
	if _, ok := GetNativeBaseline(dir); ok {
		t.Fatal("baseline not cleared")
	}
}
