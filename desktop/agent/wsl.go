package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type wslRuntimeInfo struct {
	IsWSL   bool
	Version int
}

func detectWSLRuntime() wslRuntimeInfo {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		if containsWSL2Marker(strings.ToLower(os.Getenv("WSL_INTEROP"))) {
			return wslRuntimeInfo{IsWSL: true, Version: 2}
		}
	}

	combined := strings.ToLower(readTextFile("/proc/version") + "\n" + readTextFile("/proc/sys/kernel/osrelease"))
	if !strings.Contains(combined, "microsoft") {
		return wslRuntimeInfo{}
	}
	if containsWSL2Marker(combined) {
		return wslRuntimeInfo{IsWSL: true, Version: 2}
	}
	return wslRuntimeInfo{IsWSL: true, Version: 1}
}

func isWSL() bool {
	return detectWSLRuntime().IsWSL
}

func printWSL2RequirementWarning() {
	rt := detectWSLRuntime()
	if !rt.IsWSL || rt.Version != 1 {
		return
	}

	fmt.Println("Warning: detected WSL1.")
	fmt.Println("Yaver depends on WSL2 on Windows hosts.")
	fmt.Println("Upgrade this distro to WSL2, then rerun this command.")
	fmt.Println()
}

func containsWSL2Marker(s string) bool {
	return strings.Contains(s, "wsl2")
}

func readTextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func preferredUnixShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	return "sh"
}
