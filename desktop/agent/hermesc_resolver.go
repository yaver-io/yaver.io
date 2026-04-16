package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	tmpRoot := cacheRoot + ".tmp"
	_ = os.RemoveAll(tmpRoot)
	_ = os.MkdirAll(filepath.Dir(tmpRoot), 0o755)
	cloneArgs := []string{"clone", "--depth", "1", "--branch", info.HermesRef, "https://github.com/facebook/hermes.git", tmpRoot}
	if out, err := exec.Command("git", cloneArgs...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("clone hermes %s: %v: %s", info.HermesRef, err, strings.TrimSpace(string(out)))
	}
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

func ensureHermescBuildDeps() error {
	var missing []string
	for _, bin := range []string{"git", "cmake", "python3", "gcc", "g++"} {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing hermesc build dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
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
