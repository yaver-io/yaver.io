package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseExecStartBinary(t *testing.T) {
	line := "ExecStart=/usr/bin/yaver serve --debug --port 18080"
	if got := parseExecStartBinary(line); got != "/usr/bin/yaver" {
		t.Fatalf("parseExecStartBinary() = %q, want /usr/bin/yaver", got)
	}
}

func TestDetectSystemdExecTargetsReadsUserUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd path discovery is Linux-only")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatalf("mkdir unit dir: %v", err)
	}
	unitPath := filepath.Join(unitDir, "yaver-agent.service")
	unit := "[Service]\nExecStart=/usr/bin/yaver serve --debug --work-dir /home/yaver/.yaver\n"
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}

	targets := detectSystemdExecTargets()
	if len(targets) == 0 {
		t.Fatal("detectSystemdExecTargets() returned no targets")
	}
	if targets[0].Unit != unitPath {
		t.Fatalf("first unit = %q, want %q", targets[0].Unit, unitPath)
	}
	if targets[0].Binary != "/usr/bin/yaver" {
		t.Fatalf("first binary = %q, want /usr/bin/yaver", targets[0].Binary)
	}
}
