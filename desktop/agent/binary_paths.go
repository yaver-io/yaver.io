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

func npmAgentCacheRoot() string {
	if root := canonicalBinaryPath(os.Getenv("YAVER_AGENT_CACHE_DIR")); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return canonicalBinaryPath(filepath.Join(home, ".yaver", "bin"))
}

func isExpectedNPMWrapperBinaryPair(pathBinary string, active string) bool {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_INSTALL_SOURCE")), "npm") {
		return false
	}
	pathBinary = canonicalBinaryPath(pathBinary)
	active = canonicalBinaryPath(active)
	if pathBinary == "" || active == "" {
		return false
	}
	pkg := strings.TrimSpace(os.Getenv("YAVER_NPM_PACKAGE"))
	if pkg == "" {
		pkg = "yaver-cli"
	}
	wrapperSuffix := filepath.Join("node_modules", pkg, "bin", "yaver")
	if runtime.GOOS == "windows" {
		wrapperSuffix += ".cmd"
	}
	if !strings.HasSuffix(pathBinary, wrapperSuffix) {
		return false
	}
	cacheRoot := npmAgentCacheRoot()
	return cacheRoot != "" && strings.HasPrefix(active, cacheRoot+string(os.PathSeparator))
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
	if active != "" && len(pathBinaries) > 0 && pathBinaries[0] != active && !isExpectedNPMWrapperBinaryPair(pathBinaries[0], active) {
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
	// We're npm-only as of 1.99.124. The warning's original purpose was
	// to flag drift between an apt-installed and a brew-installed yaver
	// (or a manually-cp'd binary). With one canonical install path, the
	// "yaver shim resolves to the npm wrapper, process is the cached
	// agent binary" case is the EXPECTED relationship, not drift.
	//
	// Keep the underlying check available for `yaver doctor` and for
	// users who explicitly opt in via YAVER_DEBUG_BINARY_PATHS=1, but
	// don't spam every command invocation.
	if os.Getenv("YAVER_DEBUG_BINARY_PATHS") == "" && os.Getenv("YAVER_DEBUG") == "" {
		return
	}
	for _, warning := range describeYaverBinaryDrift() {
		log.Printf("[binary-paths] WARNING: %s", warning)
	}
}
