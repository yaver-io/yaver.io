package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// runtimeRoot returns ~/.yaver/runtimes — the sudo-free directory where
// the agent installs language runtimes (Node.js, etc.) on demand so a
// fresh, headless Linux/macOS dev box can be brought up entirely from
// the phone without ever needing terminal access.
func runtimeRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "runtimes")
}

// runtimeBinDirs returns the bin/ directories under runtimeRoot that
// should be prepended to PATH for spawned subprocesses so they pick
// up agent-managed tools (Node, etc.) before any system fallback.
// Empty result means no extra dirs and no augmentation needed.
func runtimeBinDirs() []string {
	root := runtimeRoot()
	candidates := []string{
		filepath.Join(root, "node", "bin"),
		filepath.Join(root, "android-sdk", "bin"),
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
		)
	}
	var out []string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// augmentEnv returns env (defaulting to os.Environ()) with the agent's
// runtime bin directories prepended to PATH, so spawned npm / npx /
// node calls find the agent-managed runtime first. Pass-through for
// non-runtime envs. Windows is a no-op (no runtimes shipped there).
func augmentEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	if runtime.GOOS == "windows" {
		return env
	}
	extras := runtimeBinDirs()
	if len(extras) == 0 {
		return env
	}
	prepend := strings.Join(extras, string(os.PathListSeparator))
	out := make([]string, 0, len(env)+1)
	pathSet := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			existing := strings.TrimPrefix(kv, "PATH=")
			out = append(out, "PATH="+prepend+string(os.PathListSeparator)+existing)
			pathSet = true
			continue
		}
		out = append(out, kv)
	}
	if !pathSet {
		out = append(out, "PATH="+prepend+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return out
}

// lookPathWithRuntimes prefers agent-managed runtime bins before the
// ambient PATH so readiness checks agree with subprocess execution.
func lookPathWithRuntimes(name string) (string, error) {
	for _, dir := range runtimeBinDirs() {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return exec.LookPath(name)
}
