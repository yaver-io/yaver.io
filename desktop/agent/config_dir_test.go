package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDirHonorsExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAVER_CONFIG_DIR", dir)

	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if got != want {
		t.Fatalf("ConfigDir = %q, want %q", got, want)
	}

	if err := SaveConfig(&Config{AuthToken: "override-token"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("override config not written: %v", err)
	}
	if !strings.Contains(string(data), "override-token") {
		t.Fatalf("override config missing token: %s", data)
	}
}
