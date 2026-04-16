package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScheduledLoopCommandUsesExecutablePath(t *testing.T) {
	workDir := filepath.Join(string(os.PathSeparator), "tmp", "yaver loop")
	cmd := scheduledLoopCommand(workDir, "demo-autodev")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !strings.Contains(cmd, `"`+exe+`" loop run "demo-autodev"`) {
		t.Fatalf("expected command to use executable path, got %q", cmd)
	}
	if !strings.Contains(cmd, `cd "`+workDir+`" &&`) {
		t.Fatalf("expected command to preserve quoted workdir, got %q", cmd)
	}
}
