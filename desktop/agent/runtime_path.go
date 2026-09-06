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
		// The Flutter SDK the agent itself installs (flutter_install.go →
		// flutterRoot). Without this, POST /install/flutter reported success
		// while every `flutter run` still failed with 'executable file not
		// found in $PATH' — the installer put the SDK where the exec env
		// never looked. Measured live 2026-07-26 on ubuntu-4gb-hel1-1.
		filepath.Join(flutterRoot(), "bin"),
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
// non-runtime envs. Windows uses the same private runtime layout: Node's
// official zip is expanded into node/bin so npm.cmd and npx.cmd resolve
// without modifying the user's machine-wide PATH.
func augmentEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	extras := runtimeBinDirs()
	out := make([]string, 0, len(env)+1)
	if len(extras) > 0 {
		prepend := strings.Join(extras, string(os.PathListSeparator))
		pathSet := false
		for _, kv := range env {
			if i := strings.IndexByte(kv, '='); i > 0 && strings.EqualFold(kv[:i], "PATH") {
				existing := kv[i+1:]
				out = append(out, "PATH="+prepend+string(os.PathListSeparator)+existing)
				pathSet = true
				continue
			}
			out = append(out, kv)
		}
		if !pathSet {
			out = append(out, "PATH="+prepend+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
	} else {
		out = append(out, env...)
	}
	// Gradle and the Android tools locate the SDK through ANDROID_HOME /
	// ANDROID_SDK_ROOT, not PATH. A launchd-started daemon inherits neither (the
	// user set them in their shell profile), so an Android build failed with
	// "SDK location not found" on a machine whose SDK the agent had just found.
	// Only fill what is MISSING — never override an operator's explicit choice.
	out = appendMissingEnv(out, androidSDKEnvIfDiscovered())
	return out
}

// resolveSpawnPath resolves a tool name to an absolute path for
// exec.Command, consulting the agent-managed runtime dirs FIRST.
//
// Why this exists: exec.Command(name) resolves the binary via the AGENT
// process's PATH — which systemd/launchd fixed at boot — while augmentEnv
// only fixes the CHILD's PATH. So a toolchain the agent itself installed
// under ~/.yaver/runtimes (or flutterRoot) stayed "executable file not
// found" until the agent was restarted with a luckier PATH, even though
// every readiness probe (lookPathWithRuntimes) said present. Presence must
// be a per-spawn probe, never a boot-time fact. Unknown tools pass through
// unchanged so exec fails with the recognizable 'not found' the
// missing-toolchain remedy path parses.
func resolveSpawnPath(name string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name // already a path — respect it
	}
	if p, err := lookPathWithRuntimes(name); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	return name
}

// lookPathWithRuntimes prefers agent-managed runtime bins before the
// ambient PATH so readiness checks agree with subprocess execution.
func lookPathWithRuntimes(name string) (string, error) {
	for _, dir := range runtimeBinDirs() {
		for _, candidateName := range runtimeBinaryNames(name, runtime.GOOS) {
			candidate := filepath.Join(dir, candidateName)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	return exec.LookPath(name)
}

// runtimeBinaryNames mirrors Windows PATHEXT for agent-managed tools. Unlike
// exec.LookPath, the explicit runtime-dir probe does not add extensions for us.
// npm and npx are .cmd shims in the official Node zip, while node is .exe.
func runtimeBinaryNames(name, goos string) []string {
	if goos != "windows" || filepath.Ext(name) != "" {
		return []string{name}
	}
	return []string{name + ".exe", name + ".cmd", name + ".bat", name}
}

// appendMissingEnv adds KEY=VALUE pairs only when KEY is absent from env.
func appendMissingEnv(env []string, extras []string) []string {
	if len(extras) == 0 {
		return env
	}
	present := make(map[string]bool, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			present[kv[:i]] = true
		}
	}
	for _, kv := range extras {
		i := strings.IndexByte(kv, '=')
		if i <= 0 || present[kv[:i]] {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// androidSDKEnvIfDiscovered returns ANDROID_HOME/ANDROID_SDK_ROOT for the first
// real SDK on this machine, or nothing when there is none to point at.
func androidSDKEnvIfDiscovered() []string {
	for _, root := range androidSDKCandidateRoots() {
		if root != "" && looksLikeAndroidSDKRoot(root) {
			return []string{"ANDROID_HOME=" + root, "ANDROID_SDK_ROOT=" + root}
		}
	}
	return nil
}
