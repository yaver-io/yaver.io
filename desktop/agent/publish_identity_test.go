package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIdentityTestIPA(t *testing.T, bundleID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.ipa")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("Payload/Example.app/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<?xml version="1.0"?><plist><dict><key>CFBundleIdentifier</key><string>` + bundleID + `</string></dict></plist>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyIPAIdentity(t *testing.T) {
	path := writeIdentityTestIPA(t, "com.acme.app")
	if err := verifyIPAIdentity(path, "com.acme.app"); err != nil {
		t.Fatal(err)
	}
	if err := verifyIPAIdentity(path, "com.other.app"); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("wrong bundle ID must fail before upload, got %v", err)
	}
}
