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
// ("" → /root). `extraBinds` are additional "host:guest" (or bare "host")
// bind specs appended after the standard binds — used by the build path to
// mount the project dir into the rootfs. The returned slice is
// [proot, <proot flags>, ...inner].
//
// Pure function: no env reads, no side effects. Mirrors the proot-distro login
// invocation Termux uses, trimmed to what the spike needs.
func buildProotArgv(c sandboxConfig, inner []string, workDir string, extraBinds ...string) []string {
	if workDir == "" {
		workDir = "/root"
	}
	argv := []string{
		c.Proot,
		"--kill-on-exit", // reap the whole tree when proot dies
		"--link2symlink", // hardlink emulation for npm/git on a single-FS rootfs
		"-r", c.Rootfs,   // new root
		"-b", "/dev", // bind host /dev (gives us /dev/ptmx, /dev/null, ...)
		"-b", "/proc", // node/git read /proc
		"-b", "/sys", // some tools stat /sys
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
	for _, b := range extraBinds {
		if strings.TrimSpace(b) == "" {
			continue
		}
		argv = append(argv, "-b", b)
	}
	argv = append(argv, "-w", workDir) // cwd inside the rootfs
	argv = append(argv, inner...)
	return argv
}

// ensureSandboxCredDirs creates the runner cred dirs so proot's binds don't
// fail on a missing host source (proot refuses a bind whose source is absent).
// Best-effort — a failure just degrades that bind to on-device login.
func ensureSandboxCredDirs(c sandboxConfig) {
	if c.CredHome == "" {
		return
	}
	for _, rel := range sandboxCredBindDirs {
		_ = os.MkdirAll(filepath.Join(c.CredHome, rel), 0o700)
	}
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
	ensureSandboxCredDirs(cfg)
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

// sandboxWrapBuildCmd is sandboxWrapCmd for BUILD subprocesses — Metro/Expo,
// hermesc, and project dep installs invoked by /dev/build-native on Android.
// No-op off-sandbox (same env gate as sandboxWrapCmd).
//
// It differs from sandboxWrapCmd in two ways the build needs:
//
//  1. Project binding. The build's cwd (cmd.Dir) is a HOST path on the Android
//     fs (e.g. the phone-projects/<slug> tree). All build artifacts live under
//     it (.yaver-build/main.jsbundle, the hermesc -out/tmp paths). We bind that
//     dir into the rootfs at its OWN absolute path (-b dir:dir) and keep -w dir,
//     so every absolute path the build already constructs resolves UNCHANGED
//     inside proot — no path translation anywhere. (A rootfs-internal cmd.Dir
//     like /root/... — which doesn't exist on the native fs — is left as a plain
//     -w with no bind, matching sandboxWrapCmd.)
//
//  2. Env. The runner wrap installs a minimal rootfs env; the build needs the
//     caller's NODE_OPTIONS / NODE_ENV / EXPO_* / baked-Convex vars preserved
//     (sandboxBuildEnv), with only PATH/HOME/host-specific knobs overridden to
//     the rootfs values so node/npx/expo resolve against the Alpine toolchain.
func sandboxWrapBuildCmd(cmd *exec.Cmd) *exec.Cmd {
	if cmd == nil {
		return cmd
	}
	cfg, ok := sandboxConfigFromEnv()
	if !ok {
		return cmd
	}
	ensureSandboxCredDirs(cfg)
	inner := cmd.Args
	if len(inner) == 0 {
		inner = []string{cmd.Path}
	}
	workDir := cmd.Dir
	var binds []string
	// Bind the project tree at its own path iff cmd.Dir is an existing host
	// directory. proot auto-creates the mount point inside the rootfs.
	if workDir != "" && filepath.IsAbs(workDir) {
		if fi, err := os.Stat(workDir); err == nil && fi.IsDir() {
			binds = append(binds, workDir+":"+workDir)
		}
	}
	argv := buildProotArgv(cfg, inner, workDir, binds...)

	cmd.Path = cfg.Proot
	cmd.Args = argv
	cmd.Env = sandboxBuildEnv(cfg, cmd.Env)
	cmd.Dir = ""
	return cmd
}

// sandboxBuildEnv layers the caller's build-relevant env on top of the minimal
// rootfs env (sandboxEnv). The rootfs PATH/HOME/TERM/LANG/PROOT_* win (so node,
// npx, expo resolve against the Alpine toolchain inside proot); everything else
// the caller set — NODE_OPTIONS, NODE_ENV, EXPO_*, the baked Convex URL — is
// carried through. Host-specific keys are dropped so they can't override the
// rootfs values.
func sandboxBuildEnv(c sandboxConfig, callerEnv []string) []string {
	env := sandboxEnv(c, callerEnv)
	seen := map[string]bool{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			seen[kv[:i]] = true
		}
	}
	for _, kv := range callerEnv {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		// Skip keys the rootfs env already owns, plus host-specific knobs that
		// must NOT leak into the rootfs (they'd point at Android paths).
		if seen[k] || k == "PATH" || k == "HOME" || k == "PWD" || k == "TMPDIR" ||
			strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "PROOT_") {
			continue
		}
		env = append(env, kv)
		seen[k] = true
	}
	return env
}
