package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// nodeInstallVersion is the Node.js LTS shipped by the on-demand
// installer. Pinning avoids an extra HTTP call to nodejs.org/dist on
// every request and gives a stable baseline above the modern Expo SDK
// floor.
//
// Floor required by Expo SDK 53/54: Node ≥ 20.19.4. We deliberately
// ship the active LTS line (v22.x) instead of grazing the floor with
// v20.19.x because every customer-side Expo bump for the next year+
// would otherwise re-trigger this incident. v22 is the currently-active
// LTS and supports Expo 54 + RN 0.81 cleanly.
const nodeInstallVersion = "v22.12.0"

// nodeMinimumMajor / nodeMinimumMinor define the minimum Node version
// the agent considers acceptable for an Expo-aware project. If the
// existing binary at ~/.yaver/runtimes/node/bin/node is below this
// floor, installNodeRuntime re-downloads even though "something" is
// already present — otherwise customers stay stuck on stale runtimes
// after a single yaver upgrade.
const (
	nodeMinimumMajor = 20
	nodeMinimumMinor = 19
	nodeMinimumPatch = 4
)

// installNodeRuntime downloads the Node.js LTS tarball for the current
// platform into ~/.yaver/runtimes/node, sudo-free, so a fresh
// Linux/macOS dev box can be brought up from the phone without any
// terminal access. Returns the bin directory on success.
//
// progress (if non-nil) receives one human-readable line per phase
// (download, extract, ready). It is not closed.
func installNodeRuntime(ctx context.Context, progress func(string)) (string, error) {
	logf := func(s string) {
		if progress != nil {
			progress(s)
		}
	}

	tarName, urlPath, ok := nodeTarballForPlatform(nodeInstallVersion)
	if !ok {
		return "", fmt.Errorf("node runtime install: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	root := runtimeRoot()
	target := filepath.Join(root, "node")
	binDir := filepath.Join(target, "bin")

	if existing := nodeRuntimeExisting(binDir); existing != "" {
		if nodeVersionMeetsFloor(existing) {
			if err := ensureNodeCurrentSymlink(target); err != nil {
				return "", err
			}
			if err := ensureUserShellPathSetup(progress); err != nil {
				return "", err
			}
			if err := configureNpmUserPrefix(binDir, progress); err != nil {
				return "", err
			}
			logf(fmt.Sprintf("Node already installed at %s (%s)", binDir, existing))
			return binDir, nil
		}
		logf(fmt.Sprintf("Node at %s is %s — below Expo SDK floor (need ≥ v%d.%d.%d). Reinstalling.",
			binDir, existing, nodeMinimumMajor, nodeMinimumMinor, nodeMinimumPatch))
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create runtime root: %w", err)
	}

	tmpFile := filepath.Join(root, tarName)
	url := "https://nodejs.org/dist/" + urlPath
	logf(fmt.Sprintf("Downloading %s …", url))
	if err := downloadFile(ctx, url, tmpFile); err != nil {
		return "", fmt.Errorf("download node: %w", err)
	}
	defer os.Remove(tmpFile)

	logf("Extracting …")
	stage := filepath.Join(root, "node.new")
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", err
	}
	tarFlag := "-xzf"
	if strings.HasSuffix(tarName, ".tar.xz") {
		tarFlag = "-xJf"
	}
	cmd := exec.CommandContext(ctx, "tar", tarFlag, tmpFile, "-C", stage, "--strip-components=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(stage)
		return "", fmt.Errorf("extract node: %v: %s", err, strings.TrimSpace(string(out)))
	}

	if _, err := os.Stat(target); err == nil {
		_ = os.RemoveAll(target + ".old")
		if err := os.Rename(target, target+".old"); err != nil {
			_ = os.RemoveAll(stage)
			return "", fmt.Errorf("swap old node: %w", err)
		}
		defer os.RemoveAll(target + ".old")
	}
	if err := os.Rename(stage, target); err != nil {
		return "", fmt.Errorf("install node: %w", err)
	}

	if existing := nodeRuntimeExisting(binDir); existing != "" {
		if err := ensureNodeCurrentSymlink(target); err != nil {
			return "", err
		}
		if err := ensureUserShellPathSetup(progress); err != nil {
			return "", err
		}
		if err := configureNpmUserPrefix(binDir, progress); err != nil {
			return "", err
		}
		logf(fmt.Sprintf("Node ready: %s (%s)", binDir, existing))
		return binDir, nil
	}
	return "", fmt.Errorf("node binary missing after extract at %s", binDir)
}

func ensureNodeCurrentSymlink(target string) error {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return fmt.Errorf("resolve home dir for node-current symlink: %w", err)
	}
	localDir := filepath.Join(home, ".local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("create .local dir: %w", err)
	}
	linkPath := filepath.Join(localDir, "node-current")
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			if current, readErr := os.Readlink(linkPath); readErr == nil {
				if current == target {
					return nil
				}
			}
		}
		if removeErr := os.RemoveAll(linkPath); removeErr != nil {
			return fmt.Errorf("remove stale node-current link: %w", removeErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat node-current link: %w", err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("create node-current symlink: %w", err)
	}
	return nil
}

// nodeTarballForPlatform returns (filename, urlPath, ok) for the
// current OS/arch. The url path is appended to https://nodejs.org/dist/.
func nodeTarballForPlatform(version string) (string, string, bool) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		name := fmt.Sprintf("node-%s-linux-x64.tar.xz", version)
		return name, fmt.Sprintf("%s/%s", version, name), true
	case "linux/arm64":
		name := fmt.Sprintf("node-%s-linux-arm64.tar.xz", version)
		return name, fmt.Sprintf("%s/%s", version, name), true
	case "darwin/amd64":
		name := fmt.Sprintf("node-%s-darwin-x64.tar.gz", version)
		return name, fmt.Sprintf("%s/%s", version, name), true
	case "darwin/arm64":
		name := fmt.Sprintf("node-%s-darwin-arm64.tar.gz", version)
		return name, fmt.Sprintf("%s/%s", version, name), true
	}
	return "", "", false
}

// nodeVersionMeetsFloor returns true when the version string (e.g. "v20.19.4"
// or "v22.12.0") is at or above the Expo SDK 53/54 minimum. Symlinks like
// the test box's "/usr/bin/node v22.x" go through fine; old vendored Node
// installs at v20.18.x are flagged for reinstall.
func nodeVersionMeetsFloor(version string) bool {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 1 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	if major > nodeMinimumMajor {
		return true
	}
	if major < nodeMinimumMajor {
		return false
	}
	if len(parts) < 2 {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	if minor > nodeMinimumMinor {
		return true
	}
	if minor < nodeMinimumMinor {
		return false
	}
	if len(parts) < 3 {
		return false
	}
	// patch may be "4", "4-darwin", or "4 (build ...)"
	patchToken := parts[2]
	if i := strings.IndexAny(patchToken, "-+ \t"); i >= 0 {
		patchToken = patchToken[:i]
	}
	patch, err := strconv.Atoi(patchToken)
	if err != nil {
		return false
	}
	return patch >= nodeMinimumPatch
}

// nodeRuntimeExisting returns the version string from `node --version`
// in binDir, or "" if no usable binary lives there.
func nodeRuntimeExisting(binDir string) string {
	bin := filepath.Join(binDir, "node")
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func detectManagedOrSystemNode() (path, version string) {
	if p, v := detectBinaryWithVersion("node", "--version"); p != "" {
		return p, v
	}
	binDir := runtimeNodeBinDir()
	if binDir == "" {
		return "", ""
	}
	if v := nodeRuntimeExisting(binDir); v != "" {
		return filepath.Join(binDir, "node"), v
	}
	return "", ""
}

// downloadFile fetches url into dstPath. Existing file is overwritten
// atomically via a .part rename. Honors ctx for cancellation.
func downloadFile(ctx context.Context, url, dstPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	tmp := dstPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dstPath)
}
