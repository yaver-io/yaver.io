package main

// sandbox_proot.go — run PTY/runner subprocesses inside a userspace proot
// chroot on Android so the real coding-agent CLIs (claude / codex / opencode)
// execute against a full Linux userland (Alpine arm64 rootfs) without root and
// without tripping Android's W^X exec rule.
//
// Why env-gating instead of a build tag:
//   The wrap activates ONLY when YAVER_ANDROID_ROOTFS + YAVER_ANDROID_PROOT
//   are present in the process environment. Those are set exclusively by the
//   Android SandboxService when it launches `libyaver.so serve`. On macOS,
//   Linux servers, CI — the vars are absent, so sandboxWrapCmd is a no-op and
//   nothing about the existing build changes. buildProotArgv stays a pure
//   function so it is unit-testable on any host (see sandbox_proot_test.go).
//
// Layering (see docs/coding-agent-on-device.md):
//   The Go agent itself runs NATIVE (static binary from jniLibs) — it binds
//   loopback, opens /dev/ptmx, talks HTTP/QUIC. proot wraps only the *child*
//   subprocess (the shell behind /ws/terminal, and runner spawns), which is
//   the part that needs the rootfs + dynamic loader. claude-code is
//   network-bound, so the ptrace overhead is negligible.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Environment variables the Android launcher sets to activate the sandbox.
const (
	envSandboxRootfs   = "YAVER_ANDROID_ROOTFS"    // extracted Alpine rootfs dir
	envSandboxProot    = "YAVER_ANDROID_PROOT"     // path to the proot executable (libproot.so)
	envSandboxLoader   = "YAVER_ANDROID_LOADER"    // path to proot's loader (libproot-loader.so)
	envSandboxTmp      = "YAVER_ANDROID_TMP"       // writable tmp dir for proot (PROOT_TMP_DIR)
	envSandboxCredHome = "YAVER_ANDROID_CRED_HOME" // host home holding mirrored runner creds (.claude/.codex/opencode)
)

// sandboxCredBindDirs are the per-runner credential/state directories, RELATIVE
// to the sandbox cred-home, that we bind into the rootfs /root so a runner
// authed on the host (or mirrored from desktop via runner_auth_mirror.go) is
// instantly authed INSIDE proot. Without these, AcceptMirrorPayload writes to
// the agent's host $HOME/.claude while `claude` inside proot reads
// /root/.claude — two different paths, so the mirrored token would never reach
// the real CLI. Binding closes that loop.
//
//	claude   → ~/.claude/.credentials.json   (mirror-supported)
//	codex    → ~/.codex/auth.json            (mirror-supported)
//	opencode → ~/.local/share/opencode/auth.json + ~/.config/opencode (on-device
//	           login / BYOK provider config; not yet mirror-supported, but the
//	           bind makes its state persist across rootfs rebuilds)
var sandboxCredBindDirs = []string{
	".claude",
	".codex",
	".config/opencode",
	".local/share/opencode",
}

// sandboxConfig is the resolved set of paths needed to build a proot
// invocation. Construct it from the environment (sandboxConfigFromEnv) at the
// process boundary; pass it explicitly into buildProotArgv so the argv builder
// has no hidden global state and tests can exercise it directly.
type sandboxConfig struct {
	Proot    string // proot executable
	Loader   string // PROOT_LOADER (optional; proot finds its own if empty)
	Rootfs   string // -r <rootfs>
	Tmp      string // PROOT_TMP_DIR (optional)
	CredHome string // host home whose runner cred dirs bind into /root ("" = no cred binds)
}

// sandboxConfigFromEnv returns the sandbox config and true when the Android
// launcher has activated the sandbox. Returns (_, false) on every other
// platform / invocation so callers stay a no-op.
func sandboxConfigFromEnv() (sandboxConfig, bool) {
	rootfs := strings.TrimSpace(os.Getenv(envSandboxRootfs))
	proot := strings.TrimSpace(os.Getenv(envSandboxProot))
	if rootfs == "" || proot == "" {
		return sandboxConfig{}, false
	}
	return sandboxConfig{
		Proot:    proot,
		Loader:   strings.TrimSpace(os.Getenv(envSandboxLoader)),
		Rootfs:   rootfs,
		Tmp:      strings.TrimSpace(os.Getenv(envSandboxTmp)),
		CredHome: strings.TrimSpace(os.Getenv(envSandboxCredHome)),
	}, true
}

// buildProotArgv constructs the full argv that runs `inner` inside the proot
// rootfs. `inner` is the original command + args (e.g. ["/bin/sh"] or
// ["claude","-p","fix the bug"]). `workDir` is the cwd INSIDE the rootfs
// ("" → /root). The returned slice is [proot, <proot flags>, ...inner].
//
// Pure function: no env reads, no side effects. Mirrors the proot-distro login
// invocation Termux uses, trimmed to what the spike needs.
func buildProotArgv(c sandboxConfig, inner []string, workDir string) []string {
	if workDir == "" {
		workDir = "/root"
	}
	argv := []string{
		c.Proot,
		"--kill-on-exit",   // reap the whole tree when proot dies
		"--link2symlink",   // hardlink emulation for npm/git on a single-FS rootfs
		"-r", c.Rootfs,     // new root
		"-b", "/dev",       // bind host /dev (gives us /dev/ptmx, /dev/null, ...)
		"-b", "/proc",      // node/git read /proc
		"-b", "/sys",       // some tools stat /sys
		"-b", "/dev/urandom:/dev/random", // many phones lack a fast /dev/random
	}
	// Bind the host runner-cred dirs into /root so a mirrored / on-device login
	// reaches the real CLI inside the rootfs (see sandboxCredBindDirs).
	for _, rel := range sandboxCredBindDirs {
		if c.CredHome == "" {
			break
		}
		host := filepath.Join(c.CredHome, rel)
		argv = append(argv, "-b", host+":/root/"+rel)
	}
	argv = append(argv, "-w", workDir) // cwd inside the rootfs
	argv = append(argv, inner...)
	return argv
}

// sandboxEnv returns the environment to hand the proot process. It strips the
// host PATH (which points at Android, not the rootfs) and sets a rootfs PATH +
// PROOT_* knobs. callerEnv is the cmd's existing env (used to preserve TERM and
// any YAVER_* the agent set); everything else is replaced.
func sandboxEnv(c sandboxConfig, callerEnv []string) []string {
	term := "xterm-256color"
	preserved := []string{}
	for _, kv := range callerEnv {
		if strings.HasPrefix(kv, "TERM=") {
			term = strings.TrimPrefix(kv, "TERM=")
			continue
		}
		// Preserve YAVER_* hints (host-share, etc.) so they survive into the
		// rootfs, but drop everything host-specific (PATH, HOME, LD_*).
		if strings.HasPrefix(kv, "YAVER_") {
			preserved = append(preserved, kv)
		}
	}
	env := []string{
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=" + term,
		"LANG=C.UTF-8",
		"PROOT_NO_SECCOMP=1", // ptrace+seccomp conflict on many Android kernels
	}
	if c.Loader != "" {
		env = append(env, "PROOT_LOADER="+c.Loader)
	}
	if c.Tmp != "" {
		env = append(env, "PROOT_TMP_DIR="+c.Tmp)
	}
	env = append(env, preserved...)
	return env
}

// sandboxWrapCmd rewrites cmd in place to execute inside the proot rootfs when
// the Android sandbox is active. No-op (returns cmd unchanged) everywhere else.
//
// The cmd's existing Dir is interpreted as a path INSIDE the rootfs and becomes
// proot's -w; cmd.Dir is then cleared because the proot process itself runs
// from the native cwd. Stdin/Stdout/Stderr and the *os.File the caller will
// pty.Start() are untouched — proot transparently forwards them to the child.
func sandboxWrapCmd(cmd *exec.Cmd) *exec.Cmd {
	if cmd == nil {
		return cmd
	}
	cfg, ok := sandboxConfigFromEnv()
	if !ok {
		return cmd
	}
	// proot refuses a bind whose host source is missing; ensure the cred dirs
	// exist before launch so the very first runner spawn (before any mirror)
	// still starts. Best-effort: a failure here just means that bind is skipped
	// by proot, which degrades to on-device login.
	if cfg.CredHome != "" {
		for _, rel := range sandboxCredBindDirs {
			_ = os.MkdirAll(filepath.Join(cfg.CredHome, rel), 0o700)
		}
	}
	inner := cmd.Args
	if len(inner) == 0 {
		inner = []string{cmd.Path}
	}
	workDir := cmd.Dir
	argv := buildProotArgv(cfg, inner, workDir)

	cmd.Path = cfg.Proot
	cmd.Args = argv
	cmd.Env = sandboxEnv(cfg, cmd.Env)
	cmd.Dir = "" // proot runs from the agent's native cwd; -w sets the inner cwd
	return cmd
}
