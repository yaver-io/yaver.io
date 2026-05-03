package main

// resolveRunnerBinary finds the absolute path to a runner CLI (claude /
// codex / opencode) using a layered search:
//
//  1. exec.LookPath — uses the agent process's PATH.
//  2. Well-known per-user install dirs — ~/.npm-global/bin, ~/.local/bin,
//     ~/.bun/bin, ~/.cargo/bin, /opt/homebrew/bin, /usr/local/bin,
//     /snap/bin, /usr/bin. Covers npm-global, brew, cargo, bun.
//  3. `bash -lc 'command -v <name>'` — last-resort: ask the user's login
//     shell what IT thinks. Picks up shellrc PATH munging the agent never
//     sees.
//
// Why the layered search exists: when `yaver serve` runs as a systemd
// (Linux) or launchd (macOS) unit, PATH is the unit's hard-coded default
// — typically /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin.
// Most developers install claude/codex via `npm install -g`, which writes
// to ~/.npm-global/bin (or wherever `npm config get prefix`/bin points).
// That dir is on PATH for the user's interactive shell but NOT for the
// systemd unit. Result: the agent reports "claude is not installed" and
// /runner-auth/browser/start fails to spawn — even though `claude --version`
// works fine when the user types it in their terminal.
//
// This same gotcha hits BOTH:
//   - collectRunnerAuthStatusRows() — used by /agent/runners (the row
//     mobile + CLI poll to decide "show Sign In button or not").
//   - runnerBrowserAuthCommand() — actually spawns the auth subprocess.
//
// Returns "" when the binary genuinely cannot be found anywhere.

import (
	"context"
	osexec "os/exec"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// runnerResolveCache memoizes a successful lookup for ~30s so the
// /agent/runners polling endpoint doesn't fork bash every 1.5 seconds
// when the binary lives outside PATH. Empty results are NOT cached so
// "you just installed claude, refresh" works without a daemon restart.
var (
	runnerResolveCache   sync.Map // map[string]runnerResolveEntry
	runnerResolveTTL     = 30 * time.Second
	runnerResolveTimeout = 1500 * time.Millisecond
)

type runnerResolveEntry struct {
	path string
	at   time.Time
}

func resolveRunnerBinary(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if v, ok := runnerResolveCache.Load(name); ok {
		entry, _ := v.(runnerResolveEntry)
		if time.Since(entry.at) < runnerResolveTTL && entry.path != "" {
			return entry.path
		}
	}

	if path, err := osexec.LookPath(name); err == nil {
		runnerResolveCache.Store(name, runnerResolveEntry{path: path, at: time.Now()})
		return path
	}

	for _, candidate := range runnerCandidatePaths(name) {
		if isExecutableFile(candidate) {
			runnerResolveCache.Store(name, runnerResolveEntry{path: candidate, at: time.Now()})
			return candidate
		}
	}

	if runtime.GOOS != "windows" {
		if path := loginShellLookup(name); path != "" {
			runnerResolveCache.Store(name, runnerResolveEntry{path: path, at: time.Now()})
			return path
		}
	}

	return ""
}

func runnerCandidatePaths(name string) []string {
	home, _ := os.UserHomeDir()
	dirs := []string{}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			filepath.Join(home, ".local", "share", "pnpm"),
			filepath.Join(home, "n", "bin"),
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".asdf", "shims"),
			filepath.Join(home, ".nvm", "versions", "node"),
		)
	}
	dirs = append(dirs,
		"/opt/homebrew/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/snap/bin",
		"/usr/bin",
	)
	out := make([]string, 0, len(dirs)+4)
	for _, d := range dirs {
		out = append(out, filepath.Join(d, name))
		if runtime.GOOS == "windows" {
			out = append(out, filepath.Join(d, name+".exe"))
			out = append(out, filepath.Join(d, name+".cmd"))
		}
	}
	// nvm-managed installs live one node-version down; surface both
	// `versions/node/<v>/bin/<name>` symlinks the user might actually have.
	if home != "" {
		nvm := filepath.Join(home, ".nvm", "versions", "node")
		if entries, err := os.ReadDir(nvm); err == nil {
			for _, ent := range entries {
				if ent.IsDir() {
					out = append(out, filepath.Join(nvm, ent.Name(), "bin", name))
				}
			}
		}
	}
	return out
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// loginShellLookup runs `bash -lc 'command -v <name>'` so we pick up
// PATH mutations from .bashrc/.bash_profile/.zshrc that the agent's
// systemd-spawned process never sees. Bounded by runnerResolveTimeout
// so a hung shell can't stall the poll loop.
func loginShellLookup(name string) string {
	bash, err := osexec.LookPath("bash")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerResolveTimeout)
	defer cancel()
	cmd := osexec.CommandContext(ctx, bash, "-lc", "command -v "+shellEscape(name))
	cmd.Env = append(os.Environ(), "BASH_ENV=")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	if !isExecutableFile(path) {
		return ""
	}
	return path
}

