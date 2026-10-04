package main

import (
	"path/filepath"
	"testing"
)

func TestResolveVibingProjectPathAcceptsCreatedPhoneProjectDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveVibingProjectPath("", dir, "/fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("path = %q, want %q", got, filepath.Clean(dir))
	}
}

func TestResolveVibingProjectPathPrefersExplicitPath(t *testing.T) {
	got, err := resolveVibingProjectPath("/explicit/../project", "ignored", "/fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/project" {
		t.Fatalf("path = %q, want /project", got)
	}
}
