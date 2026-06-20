package main

// no_root_check.go — runtime safety guard against accidentally running
// `yaver serve` as root.
//
// Why this matters: Yaver's entire normal runtime state lives under
// $HOME/.yaver — config, vault, runner tokens, ledger, paired tokens,
// cached binaries. If a user one-shot `sudo yaver serve` (out of
// confusion or copy-paste from a tutorial), those files get created
// owned by root. The NEXT time the user runs `yaver` as themselves,
// they hit permission-denied errors that look like Yaver bugs but
// are actually self-inflicted file-ownership drift.
//
// Contract (documented + enforced by this check):
//
//   yaver auth, yaver serve, yaver code, yaver wire push, yaver vault,
//   yaver wireless push, yaver insert, yaver primary, ...
//   ──────────────────────────────────────────────────────────────────
//   These are the "regular path" commands. They write only under
//   $HOME/.yaver and bind only to ports >1024. They MUST NOT need
//   root, ever.
//
//   yaver serve --install-systemd / --install-launchd-daemon
//   yaver install <pkg>          (apt/dnf/pacman/brew dispatch)
//   yaver domain add             (writes /etc/hosts via sudo)
//   MCP sysadmin tools           (LLM-invoked, gated)
//   ──────────────────────────────────────────────────────────────────
//   These DO need root and are explicit opt-ins. Each prompts via
//   sudo at the moment of action, no surprise.
//
// Cagri-style trio: Cagri runs `yaver serve` as himself on his Linux
// box. He never sudos for runtime. This check warns + offers a clean
// exit if he or any user trips into the footgun.

import (
	"fmt"
	"os"
	"runtime"
)

// warnIfRunningAsRoot prints a clear warning when `yaver serve` is
// invoked under euid=0. Does NOT refuse to continue — there are
// legitimate root-as-only-user scenarios (managed-cloud boxes where
// the whole environment is root, containers without USER directive).
// But it makes the footgun visible BEFORE state files get created
// with root ownership.
//
// Returns true when running as root, false otherwise — caller can
// use the return to skip certain user-specific paths if desired.
func warnIfRunningAsRoot() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	euid := os.Geteuid()
	if euid != 0 {
		return false
	}
	// Heuristic: if HOME points at /root and there's no other user
	// home dir available, this IS a root-only environment (e.g.
	// fresh Docker container). Skip the warning — they have no
	// other choice.
	home, _ := os.UserHomeDir()
	if home == "" || home == "/root" {
		if _, err := os.Stat("/home"); err != nil {
			// No /home at all — root-only system, silent.
			return true
		}
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  ⚠️  yaver is running as root (euid=0).")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Yaver's normal runtime path does NOT need root. Files written")
	fmt.Fprintln(os.Stderr, "  to $HOME/.yaver will be owned by root, which breaks future")
	fmt.Fprintln(os.Stderr, "  non-root invocations with permission-denied errors that look")
	fmt.Fprintln(os.Stderr, "  like Yaver bugs but are actually file-ownership drift.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  If you ran 'sudo yaver serve' by habit, Ctrl-C and re-run")
	fmt.Fprintln(os.Stderr, "  without sudo. Yaver binds to ports >1024 and stores state in")
	fmt.Fprintln(os.Stderr, "  your home dir — root is never required for `serve`, `auth`,")
	fmt.Fprintln(os.Stderr, "  `code`, `vault`, `wire push`, etc.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Root IS required only when you explicitly opt in:")
	fmt.Fprintln(os.Stderr, "    yaver serve --install-systemd        (user unit, runs as you)")
	fmt.Fprintln(os.Stderr, "    sudo yaver serve --install-systemd-system (dedicated 'yaver' user + scoped sudo)")
	fmt.Fprintln(os.Stderr, "    yaver serve --install-launchd-daemon (writes /Library/...)")
	fmt.Fprintln(os.Stderr, "    yaver install <package>             (apt/dnf/pacman/brew)")
	fmt.Fprintln(os.Stderr, "    yaver domain add                    (rewrites /etc/hosts)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Continuing anyway (in case you're in a root-only environment).")
	fmt.Fprintln(os.Stderr, "")
	return true
}
