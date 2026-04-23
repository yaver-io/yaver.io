package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLocalHostShareRootFallsBackToAnyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "müşteri-paneli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	root, err := resolveLocalHostShareRoot("", dir)
	if err != nil {
		t.Fatalf("resolveLocalHostShareRoot() error = %v", err)
	}
	if root.Path != dir {
		t.Fatalf("root.Path = %q, want %q", root.Path, dir)
	}
	if root.ID != "" {
		t.Fatalf("root.ID = %q, want empty fallback id", root.ID)
	}
}
