package main

// binary_discovery.go — find tool binaries by name even when the
// directory they live in is not on $PATH.
//
// Matters because:
//   - yaver often runs as a systemd user service with a minimal
//     environment. $PATH rarely includes ~/.local/bin, ~/.cargo/bin,
//     ~/.npm-global/bin, /snap/bin, /opt/homebrew/bin.
//   - Homebrew on Apple Silicon uses /opt/homebrew/bin; Intel uses
//     /usr/local/bin. Shell profiles normally add the right one,
//     but launchd-started processes don't read those profiles.
//   - Snap symlinks into /snap/bin/<tool>; apt + pipx + cargo + pnpm
//     each have their own prefix.
//
// The agent uses this result both to (a) report what's installed in
// /infra/summary so the mobile + web UIs show the truth, and (b)
// invoke tools via absolute path so a claude-code subprocess does
// not have to rediscover PATH itself.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DetectedBinary is one tool we found on disk. `Manager` is a
// best-effort guess based on the install prefix — "brew" for
// /opt/homebrew/bin/<tool>, "snap" for /snap/bin/<tool>, "cargo"
// for ~/.cargo/bin/<tool>, and so on. When we genuinely can't tell,
// it's left empty.
type DetectedBinary struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Manager string `json:"manager,omitempty"`
}

// commonInstallPrefixes returns the directories we probe beyond $PATH.
// Order matters — the first match wins — so more-specific (homebrew,
// cargo, pipx) come before generic ones (/usr/local/bin).
func commonInstallPrefixes() []string {
	home, _ := os.UserHomeDir()
	prefixes := []string{}

	switch runtime.GOOS {
	case "darwin":
		prefixes = append(prefixes,
			"/opt/homebrew/bin",       // Apple Silicon brew
			"/opt/homebrew/sbin",
			"/usr/local/bin",          // Intel brew
			"/usr/local/sbin",
		)
	case "linux":
		prefixes = append(prefixes,
			"/snap/bin",                           // snap installs
			"/var/lib/flatpak/exports/bin",        // flatpak installs (system-wide)
			filepath.Join(home, ".local/share/flatpak/exports/bin"), // flatpak user
			"/usr/local/bin",
			"/usr/bin",
			"/usr/sbin",
			"/bin",
		)
	case "windows":
		prefixes = append(prefixes,
			filepath.Join(os.Getenv("ProgramFiles"), "nodejs"),
			filepath.Join(os.Getenv("ProgramFiles"), "Git\\bin"),
			filepath.Join(os.Getenv("USERPROFILE"), "scoop\\shims"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs"),
		)
	}

	if home != "" {
		prefixes = append(prefixes,
			filepath.Join(home, ".local/bin"),     // pipx, uv, user pip
			filepath.Join(home, ".cargo/bin"),     // rustup + cargo install
			filepath.Join(home, ".npm-global/bin"),
			filepath.Join(home, ".bun/bin"),
			filepath.Join(home, ".deno/bin"),
			filepath.Join(home, "go/bin"),         // GOPATH default
			filepath.Join(home, ".yaver/runtimes/bin"),
			filepath.Join(home, ".yaver/bin"),
		)
	}
	return prefixes
}

// guessManagerForPath turns an install directory into a rough
// package-manager label. Used in the /infra/summary output so UIs
// can say "aider · pipx · ~/.local/bin/aider" instead of just the
// path.
func guessManagerForPath(p string) string {
	// Normalise to forward slashes for easier matching, works on all
	// platforms because Go's filepath uses / on non-Windows.
	lower := strings.ToLower(filepath.ToSlash(p))
	switch {
	case strings.Contains(lower, "/snap/"):
		return "snap"
	case strings.Contains(lower, "/flatpak/"):
		return "flatpak"
	case strings.Contains(lower, "/homebrew/"):
		return "brew"
	case strings.Contains(lower, "/.cargo/"):
		return "cargo"
	case strings.Contains(lower, "/.local/bin") || strings.Contains(lower, "/.local/pipx/"):
		return "pipx"
	case strings.Contains(lower, "/.npm-global/"):
		return "npm"
	case strings.Contains(lower, "/.bun/"):
		return "bun"
	case strings.Contains(lower, "/.deno/"):
		return "deno"
	case strings.Contains(lower, "/.yaver/runtimes"):
		return "yaver"
	case strings.HasPrefix(lower, "/usr/local/"):
		// Could be Intel brew OR a manual install.
		return "local"
	case strings.HasPrefix(lower, "/usr/"):
		return "system"
	case strings.Contains(lower, "/scoop/"):
		return "scoop"
	case strings.Contains(lower, "/nodejs"):
		return "winget"
	}
	return ""
}

// discoverBinary walks $PATH first, then falls back to the common
// install prefixes. Returns empty string if the binary can't be found
// anywhere. Windows .exe/.cmd suffixes are handled by exec.LookPath,
// but on Windows our explicit probes also try those suffixes.
func discoverBinary(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	suffixes := []string{""}
	if runtime.GOOS == "windows" {
		suffixes = []string{".exe", ".cmd", ".bat", ""}
	}
	for _, prefix := range commonInstallPrefixes() {
		for _, suffix := range suffixes {
			candidate := filepath.Join(prefix, name+suffix)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				// Best-effort executable check on unix. On windows we
				// trust the suffix.
				if runtime.GOOS == "windows" || info.Mode()&0o111 != 0 {
					return candidate
				}
			}
		}
	}
	return ""
}

// discoveryCache memoises lookups for ~60s — the user can add a
// binary between polls and we'll pick it up; we just don't want to
// re-stat a dozen directories every /infra/summary call.
var (
	discoveryMu     sync.Mutex
	discoveryCache  = map[string]discoveryEntry{}
	discoveryWindow = 60 * time.Second
)

type discoveryEntry struct {
	path string
	when time.Time
}

// DiscoverBinary is the exported, cached wrapper around discoverBinary.
// Use this for anything on the hot path (`/infra/summary`,
// `/install/list`, runner bootstrap).
func DiscoverBinary(name string) string {
	discoveryMu.Lock()
	entry, ok := discoveryCache[name]
	discoveryMu.Unlock()
	if ok && time.Since(entry.when) < discoveryWindow {
		return entry.path
	}
	path := discoverBinary(name)
	discoveryMu.Lock()
	discoveryCache[name] = discoveryEntry{path: path, when: time.Now()}
	discoveryMu.Unlock()
	return path
}

// knownProbeBinaries is the set of names we advertise in the
// /infra/summary `binaries` field. Kept small on purpose so the
// probe is cheap; anything else can be resolved on demand via
// DiscoverBinary.
func knownProbeBinaries() []string {
	return []string{
		// AI runners
		"claude", "codex", "aider", "opencode",
		// Model runtimes
		"ollama",
		// Language runtimes
		"node", "npm", "pnpm", "yarn", "bun",
		"python3", "python", "pip3", "pip", "uv", "pipx",
		"go", "cargo", "rustc", "gem", "composer", "dart", "flutter",
		// Package managers
		"brew", "apt-get", "dnf", "pacman", "zypper", "apk",
		"snap", "flatpak", "winget", "choco", "scoop",
		// Dev tools
		"git", "gh", "docker", "rg", "jq", "fd", "bat", "fzf",
		"sqlite3", "psql", "cloudflared", "tailscale",
	}
}

// DiscoverInstalledBinaries returns one DetectedBinary per tool we
// found on this machine. Used by /infra/summary so the Tools &
// Machine UI can show "what's already there, and where it lives".
func DiscoverInstalledBinaries() []DetectedBinary {
	out := make([]DetectedBinary, 0, 32)
	for _, name := range knownProbeBinaries() {
		path := DiscoverBinary(name)
		if path == "" {
			continue
		}
		out = append(out, DetectedBinary{
			Name:    name,
			Path:    path,
			Manager: guessManagerForPath(path),
		})
	}
	return out
}
