package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type systemdExecTarget struct {
	Unit   string
	Binary string
}

func resolvedYaverExecutable() string {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return ""
	}
	return canonicalBinaryPath(exe)
}

func preferredYaverBinaryPath() string {
	if exe := resolvedYaverExecutable(); exe != "" {
		return exe
	}
	if path, err := exec.LookPath("yaver"); err == nil {
		return canonicalBinaryPath(path)
	}
	return ""
}

func canonicalBinaryPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && strings.TrimSpace(resolved) != "" {
		path = resolved
	}
	return path
}

func discoverYaverBinariesOnPATH() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "yaver")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		canonical := canonicalBinaryPath(candidate)
		if canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out
}

func parseExecStartBinary(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "ExecStart=") {
		return ""
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
	raw = strings.TrimLeft(raw, "-")
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return canonicalBinaryPath(fields[0])
}

func detectSystemdExecTargets() []systemdExecTarget {
	if runtime.GOOS != "linux" {
		return nil
	}
	home, _ := os.UserHomeDir()
	candidates := []systemdExecTarget{
		{Unit: "/etc/systemd/system/yaver-agent.service"},
		{Unit: "/etc/systemd/system/yaver.service"},
	}
	if strings.TrimSpace(home) != "" {
		candidates = append(candidates,
			systemdExecTarget{Unit: filepath.Join(home, ".config", "systemd", "user", "yaver-agent.service")},
			systemdExecTarget{Unit: filepath.Join(home, ".config", "systemd", "user", "yaver.service")},
		)
	}
	var out []systemdExecTarget
	for _, candidate := range candidates {
		data, err := os.ReadFile(candidate.Unit)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			binary := parseExecStartBinary(line)
			if binary == "" {
				continue
			}
			out = append(out, systemdExecTarget{Unit: candidate.Unit, Binary: binary})
			break
		}
	}
	return out
}

func describeYaverBinaryDrift() []string {
	var warnings []string
	active := preferredYaverBinaryPath()
	pathBinaries := discoverYaverBinariesOnPATH()
	if len(pathBinaries) > 1 {
		warnings = append(warnings, fmt.Sprintf("multiple yaver binaries on PATH: %s (running %s)", strings.Join(pathBinaries, ", "), active))
	}
	if active != "" && len(pathBinaries) > 0 && pathBinaries[0] != active {
		warnings = append(warnings, fmt.Sprintf("PATH resolves yaver to %s but this process is running %s", pathBinaries[0], active))
	}
	for _, target := range detectSystemdExecTargets() {
		if active == "" || target.Binary == "" || target.Binary == active {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s points at %s but this process is running %s", target.Unit, target.Binary, active))
	}
	slices.Sort(warnings)
	return slices.Compact(warnings)
}

func logYaverBinaryDriftWarnings() {
	for _, warning := range describeYaverBinaryDrift() {
		log.Printf("[binary-paths] WARNING: %s", warning)
	}
}
