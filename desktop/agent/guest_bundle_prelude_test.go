package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectGuestSafePreludePrependsOnce(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "main.jsbundle")
	if err := os.WriteFile(bundlePath, []byte("console.log('bundle');\n"), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	if err := injectGuestSafePrelude(bundlePath); err != nil {
		t.Fatalf("injectGuestSafePrelude: %v", err)
	}
	firstPass, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle after first inject: %v", err)
	}
	if err := injectGuestSafePrelude(bundlePath); err != nil {
		t.Fatalf("injectGuestSafePrelude second pass: %v", err)
	}

	got, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	content := string(got)
	if !strings.Contains(content, "__YAVER_GUEST_SAFE_PRELUDE__") {
		t.Fatalf("expected guest-safe prelude marker in bundle, got %q", content)
	}
	if string(firstPass) != content {
		t.Fatal("expected second injection pass to leave bundle unchanged")
	}
	if !strings.Contains(content, "ExpoHaptics") || !strings.Contains(content, "RNCNetInfo") {
		t.Fatalf("expected wrapped module names in prelude, got %q", content)
	}
	if !strings.HasSuffix(content, "console.log('bundle');\n") {
		t.Fatalf("expected original bundle to remain after prelude, got %q", content)
	}
}
