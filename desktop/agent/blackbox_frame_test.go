package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func resetPhoneFrames() {
	phoneFrames = &phoneFrameStore{frames: make(map[string]phoneFrame)}
}

func TestPhoneFrameStoreNewerThanBaseline(t *testing.T) {
	resetPhoneFrames()
	if phoneFrames.baselineSeq("dev-A") != 0 {
		t.Fatalf("empty baseline should be 0")
	}
	s1 := phoneFrames.set("dev-A", []byte("jpegA"), "image/jpeg", "")
	base := phoneFrames.baselineSeq("dev-A")
	if base != s1 {
		t.Fatalf("baseline = %d, want %d", base, s1)
	}
	s2 := phoneFrames.set("dev-A", []byte("jpegB"), "image/jpeg", "")
	if s2 <= base {
		t.Fatalf("second seq %d must exceed baseline %d — else a caller accepts a stale frame", s2, base)
	}
	f, ok := phoneFrames.get("dev-A")
	if !ok || string(f.data) != "jpegB" {
		t.Fatalf("get returned wrong/stale frame: %+v", f)
	}
}

// device_screenshot must NOT return the pre-existing (stale) frame — only one
// captured AFTER the request, or the closed-loop test would pass on the old
// screen and never catch a broken reload.
func TestCaptureDeviceScreenshotWaitsForFreshFrame(t *testing.T) {
	resetPhoneFrames()
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	dev := mgr.GetOrCreateSession("dev-A", "ios", "Yaver")
	ch := dev.SubscribeCommands()
	defer dev.UnsubscribeCommands(ch)

	// A stale frame already sits in the store.
	phoneFrames.set("dev-A", []byte("STALE"), "image/jpeg", "")

	// The phone (simulated) uploads a FRESH frame shortly after the command.
	go func() {
		<-ch // capture_screenshot command received
		time.Sleep(30 * time.Millisecond)
		phoneFrames.set("dev-A", []byte("FRESH"), "image/jpeg", "")
	}()

	res, code, errStr := captureDeviceScreenshot(mgr, "dev-A", 2000)
	if errStr != "" {
		t.Fatalf("screenshot failed: %s / %s", code, errStr)
	}
	img, _ := res["image"].(string)
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("FRESH"))
	if img != want {
		t.Fatalf("returned a stale frame instead of the fresh one")
	}
}

func TestCaptureDeviceScreenshotNoListener(t *testing.T) {
	resetPhoneFrames()
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	mgr.GetOrCreateSession("dev-silent", "ios", "Yaver") // registered, not holding the stream
	_, code, errStr := captureDeviceScreenshot(mgr, "dev-silent", 500)
	if errStr == "" || code != "no_listener" {
		t.Fatalf("expected no_listener when nothing is listening, got code=%q err=%q", code, errStr)
	}
}

// A Flutter app is detected from its dart project markers; a plain Dart package
// (bare pubspec, no flutter SDK) must NOT be labelled flutter and routed to the
// mobile preview lanes.
func TestDetectFrameworkDistinguishesFlutterFromPlainDart(t *testing.T) {
	flutterDir := t.TempDir()
	os.WriteFile(filepath.Join(flutterDir, "pubspec.yaml"),
		[]byte("name: todo\ndependencies:\n  flutter:\n    sdk: flutter\n  cupertino_icons: ^1.0.0\n"), 0o600)
	if fw := detectFramework(flutterDir); fw != "flutter" {
		t.Fatalf("flutter app detected as %q, want flutter", fw)
	}

	dartDir := t.TempDir()
	os.WriteFile(filepath.Join(dartDir, "pubspec.yaml"),
		[]byte("name: my_cli\ndependencies:\n  args: ^2.0.0\n"), 0o600)
	if fw := detectFramework(dartDir); fw == "flutter" {
		t.Fatalf("a plain Dart package was mislabelled flutter")
	}

	// lib/main.dart alone is a strong Flutter signal too.
	flutterByLib := t.TempDir()
	os.WriteFile(filepath.Join(flutterByLib, "pubspec.yaml"), []byte("name: app\n"), 0o600)
	os.MkdirAll(filepath.Join(flutterByLib, "lib"), 0o755)
	os.WriteFile(filepath.Join(flutterByLib, "lib", "main.dart"), []byte("void main() {}\n"), 0o600)
	if fw := detectFramework(flutterByLib); fw != "flutter" {
		t.Fatalf("flutter-by-lib/main.dart detected as %q, want flutter", fw)
	}
}
