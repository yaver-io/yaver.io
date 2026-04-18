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
	"strings"
	"time"
)

// nodeInstallVersion is the Node.js LTS shipped by the on-demand
// installer. Pinning avoids an extra HTTP call to nodejs.org/dist on
// every request and gives a stable baseline above the modern Expo SDK
// floor (Node ≥ 18).
const nodeInstallVersion = "v20.18.0"

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
		if err := ensureUserShellPathSetup(progress); err != nil {
			return "", err
		}
		if err := configureNpmUserPrefix(binDir, progress); err != nil {
			return "", err
		}
		logf(fmt.Sprintf("Node already installed at %s (%s)", binDir, existing))
		return binDir, nil
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
