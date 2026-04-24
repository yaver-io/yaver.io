package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type HermesRuntimeInfo struct {
	ReactNativeVersion string
	ExpoSDKVersion     string
	HermesRef          string
}

var hermesBCVersionPattern = regexp.MustCompile(`HBC bytecode version:\s*([0-9]+)`)

func resolveHermesc(workDir string) (string, error) {
	if path, err := GetEmbeddedHermesc(); err == nil {
		return path, nil
	}
	if path := findProjectHermesc(workDir); path != "" {
		return path, nil
	}
	return buildProjectHermesc(workDir)
}

func detectHermesRuntimeInfo(workDir string) HermesRuntimeInfo {
	info := HermesRuntimeInfo{}
	for _, root := range hermescSearchRoots(workDir) {
		pkgPath := filepath.Join(root, "package.json")
		if info.ReactNativeVersion == "" || info.ExpoSDKVersion == "" {
			if data, err := os.ReadFile(pkgPath); err == nil {
				var pkg struct {
					Dependencies    map[string]string `json:"dependencies"`
					DevDependencies map[string]string `json:"devDependencies"`
				}
				if json.Unmarshal(data, &pkg) == nil {
					if info.ReactNativeVersion == "" {
						info.ReactNativeVersion = firstNonEmpty(
							pkg.Dependencies["react-native"],
							pkg.DevDependencies["react-native"],
						)
					}
					if info.ExpoSDKVersion == "" {
						info.ExpoSDKVersion = firstNonEmpty(
							pkg.Dependencies["expo"],
							pkg.DevDependencies["expo"],
						)
					}
				}
			}
		}
		if info.HermesRef == "" {
			if data, err := os.ReadFile(filepath.Join(root, "node_modules", "react-native", "sdks", ".hermesversion")); err == nil {
				info.HermesRef = strings.TrimSpace(string(data))
			}
		}
	}
	return info
}

func findProjectHermesc(workDir string) string {
	for _, root := range hermescSearchRoots(workDir) {
		for _, c := range hermescCandidates(root) {
			if _, err := os.Stat(c); err == nil && hermescBinaryRunnable(c) {
				_ = os.Chmod(c, 0o755)
				return c
			}
		}
	}
	return ""
}

func hermescBinaryRunnable(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func hermescBytecodeVersion(path string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return 0, err
	}
	m := hermesBCVersionPattern.FindStringSubmatch(string(out))
	if len(m) != 2 {
		return 0, fmt.Errorf("hermesc --version missing bytecode version: %s", strings.TrimSpace(string(out)))
	}
	return atoiDefault(m[1], 0), nil
}

func buildProjectHermesc(workDir string) (string, error) {
	info := detectHermesRuntimeInfo(workDir)
	if info.HermesRef == "" {
		return "", errors.New("project Hermes ref not found (.hermesversion missing)")
	}
	cfgDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(cfgDir, "hermesc-cache", safeHermesRefSegment(info.HermesRef), runtime.GOOS+"-"+runtime.GOARCH)
	cachedBin := filepath.Join(cacheRoot, "build", "bin", "hermesc")
	if _, err := os.Stat(cachedBin); err == nil && hermescBinaryRunnable(cachedBin) {
		return cachedBin, nil
	}
	if err := ensureHermescBuildDeps(); err != nil {
		return "", err
	}
	log.Printf("[hermesc] building from source for %s/%s at ref %s (one-time ~1-2 min; cached in %s)", runtime.GOOS, runtime.GOARCH, info.HermesRef, cacheRoot)
	tmpRoot := cacheRoot + ".tmp"
	_ = os.RemoveAll(tmpRoot)
	_ = os.MkdirAll(filepath.Dir(tmpRoot), 0o755)
	cloneArgs := []string{"clone", "--depth", "1", "--branch", info.HermesRef, "https://github.com/facebook/hermes.git", tmpRoot}
	log.Printf("[hermesc] cloning facebook/hermes @ %s ...", info.HermesRef)
	if out, err := exec.Command("git", cloneArgs...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("clone hermes %s: %v: %s", info.HermesRef, err, strings.TrimSpace(string(out)))
	}
	log.Printf("[hermesc] configuring + compiling hermesc (this is the slow part) ...")
	cmakeArgs := []string{"-S", ".", "-B", "build", "-DCMAKE_BUILD_TYPE=Release"}
	if _, err := exec.LookPath("ninja"); err == nil {
		cmakeArgs = append(cmakeArgs, "-G", "Ninja")
	}
	cfgCmd := exec.Command("cmake", cmakeArgs...)
	cfgCmd.Dir = tmpRoot
	if out, err := cfgCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("configure hermesc: %v: %s", err, strings.TrimSpace(string(out)))
	}
	buildCmd := exec.Command("cmake", "--build", "build", "--target", "hermesc", "-j2")
	buildCmd.Dir = tmpRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build hermesc: %v: %s", err, strings.TrimSpace(string(out)))
	}
	builtBin := filepath.Join(tmpRoot, "build", "bin", "hermesc")
	if _, err := os.Stat(builtBin); err != nil {
		return "", fmt.Errorf("built hermesc missing at %s", builtBin)
	}
	_ = os.RemoveAll(cacheRoot)
	if err := os.Rename(tmpRoot, cacheRoot); err != nil {
		return "", err
	}
	if !hermescBinaryRunnable(cachedBin) {
		return "", fmt.Errorf("built hermesc is not runnable on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return cachedBin, nil
}

// Binaries required to clone + configure + compile hermesc from source.
// Listed once so the detect/install/verify paths all agree.
var hermescRequiredBins = []string{"git", "cmake", "python3", "gcc", "g++"}

// ensureHermescBuildDeps verifies the host has everything needed to build
// hermesc from Meta's pinned source. Missing deps trigger a one-time
// auto-install on Linux via the native package manager (apt / dnf / yum
// / pacman). This keeps the "no manual fix on the box" contract from
// CLAUDE.md — a fresh Hetzner arm64 box goes from empty to fully capable
// of Hermes reload without the dev ever SSHing in.
//
// Behaviour on non-Linux platforms (macOS, Windows): no auto-install —
// brew/chocolatey are too invasive to run from a daemon; we return a
// clear error with the list of missing binaries so the user can install
// them. macOS's usual path is a pre-built hermesc via GetEmbeddedHermesc,
// so this branch is rarely hit there anyway.
func ensureHermescBuildDeps() error {
	missing := findMissingHermescBins()
	if len(missing) == 0 {
		return nil
	}

	if runtime.GOOS == "linux" {
		if err := autoInstallHermescBuildDepsLinux(missing); err != nil {
			return err
		}
		// Re-verify after install — apt / dnf occasionally succeed
		// without actually putting the binary on PATH (e.g. a broken
		// packaged symlink). Better to return a loud error here than
		// let resolveHermesc loop forever on the next reload.
		if still := findMissingHermescBins(); len(still) > 0 {
			return fmt.Errorf("hermesc build deps still missing after install attempt: %s", strings.Join(still, ", "))
		}
		return nil
	}

	return fmt.Errorf("missing hermesc build dependencies: %s", strings.Join(missing, ", "))
}

func findMissingHermescBins() []string {
	var missing []string
	for _, bin := range hermescRequiredBins {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	return missing
}

// autoInstallHermescBuildDepsLinux runs a non-interactive install of the
// host's package manager for the missing binaries. Safe to call from a
// request handler: all invocations use `-y` / `--noconfirm` so nothing
// prompts stdin, and we wrap with a 5-minute timeout so a wedged package
// manager can't freeze a reload forever.
func autoInstallHermescBuildDepsLinux(missing []string) error {
	pm, pkgs := detectLinuxPackageManagerForHermesc(missing)
	if pm == "" {
		return fmt.Errorf(
			"no supported Linux package manager (apt-get / dnf / yum / pacman) detected; please install these manually and retry: %s",
			strings.Join(missing, ", "),
		)
	}

	log.Printf("[hermesc] first-time toolchain setup — installing %s via %s (~1-2 min on fresh box)", strings.Join(missing, ", "), pm)

	// Apt needs `update` before `install` or it'll 404 on a fresh box
	// whose cached package list has expired. Failure here is non-fatal —
	// network hiccups shouldn't kill the install outright.
	if pm == "apt-get" {
		if out, err := runLinuxWithPrivilege(pm, []string{"update", "-qq"}, 3*time.Minute); err != nil {
			log.Printf("[hermesc] apt-get update warning: %v — proceeding anyway\n%s", err, strings.TrimSpace(string(out)))
		}
	}

	var installArgs []string
	switch pm {
	case "apt-get":
		installArgs = append([]string{"install", "-y", "--no-install-recommends"}, pkgs...)
	case "dnf", "yum":
		installArgs = append([]string{"install", "-y"}, pkgs...)
	case "pacman":
		installArgs = append([]string{"-S", "--noconfirm", "--needed"}, pkgs...)
	}

	out, err := runLinuxWithPrivilege(pm, installArgs, 5*time.Minute)
	if err != nil {
		return fmt.Errorf(
			"%s install failed: %v\n%s\nRun manually: sudo %s %s",
			pm, err, strings.TrimSpace(string(out)), pm, strings.Join(installArgs, " "),
		)
	}
	log.Printf("[hermesc] build toolchain installed via %s", pm)
	return nil
}

// detectLinuxPackageManagerForHermesc picks the first supported package
// manager on PATH and returns the distro-specific package names for the
// missing binaries. Returns ("", nil) when the host has none of the
// known managers — the caller surfaces a manual-install error in that
// case instead of silently degrading.
func detectLinuxPackageManagerForHermesc(missing []string) (string, []string) {
	// binary → package map, per distro family. gcc+g++ both land in
	// build-essential on Debian/Ubuntu; on RHEL/Fedora they're
	// separate packages. Keep the tables narrow — only the
	// hermescRequiredBins set needs entries here.
	apt := map[string]string{
		"git": "git", "cmake": "cmake", "python3": "python3",
		"gcc": "build-essential", "g++": "build-essential",
		"ninja": "ninja-build",
	}
	rpm := map[string]string{
		"git": "git", "cmake": "cmake", "python3": "python3",
		"gcc": "gcc", "g++": "gcc-c++", "ninja": "ninja-build",
	}
	pac := map[string]string{
		"git": "git", "cmake": "cmake", "python3": "python",
		"gcc": "gcc", "g++": "gcc", "ninja": "ninja",
	}

	var (
		pm   string
		tbl  map[string]string
		have = func(name string) bool {
			_, err := exec.LookPath(name)
			return err == nil
		}
	)
	switch {
	case have("apt-get"):
		pm, tbl = "apt-get", apt
	case have("dnf"):
		pm, tbl = "dnf", rpm
	case have("yum"):
		pm, tbl = "yum", rpm
	case have("pacman"):
		pm, tbl = "pacman", pac
	default:
		return "", nil
	}

	seen := map[string]bool{}
	var pkgs []string
	for _, bin := range missing {
		pkg, ok := tbl[bin]
		if !ok {
			pkg = bin
		}
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		pkgs = append(pkgs, pkg)
	}
	return pm, pkgs
}

// runLinuxWithPrivilege runs a Linux package-manager command with the
// privileges it needs. If the agent is uid 0 (systemd root service,
// docker container with the default root user), runs the command
// directly. Otherwise prepends `sudo -n` (non-interactive) so the call
// fails fast with exit 1 when a password would be required — the one
// thing we must never do from a request handler is block on stdin.
//
// `DEBIAN_FRONTEND=noninteractive` keeps apt-get from opening dialog
// prompts (e.g. after a kernel upgrade offering to restart services).
func runLinuxWithPrivilege(name string, args []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	if os.Geteuid() == 0 {
		cmd = exec.CommandContext(ctx, name, args...)
	} else {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{"-n", name}, args...)...)
	}
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	return cmd.CombinedOutput()
}

func atoiDefault(v string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}

func safeHermesRefSegment(v string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(strings.TrimSpace(v))
}
