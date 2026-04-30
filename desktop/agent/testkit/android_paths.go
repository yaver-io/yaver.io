package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func managedAndroidSDKRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "runtimes", "android-sdk")
}

func testkitAndroidToolRelativePath(name string) string {
	switch strings.TrimSpace(name) {
	case "adb":
		return filepath.Join("platform-tools", "adb")
	case "emulator":
		return filepath.Join("emulator", "emulator")
	case "sdkmanager", "avdmanager":
		return filepath.Join("cmdline-tools", "latest", "bin", name)
	default:
		return ""
	}
}

func resolveTestkitCommandPath(name string) string {
	if path, err := exec.LookPath(name); err == nil && strings.TrimSpace(path) != "" {
		return path
	}
	rel := testkitAndroidToolRelativePath(name)
	if rel == "" {
		return name
	}
	roots := []string{
		os.Getenv("ANDROID_SDK_ROOT"),
		os.Getenv("ANDROID_HOME"),
		managedAndroidSDKRoot(),
	}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		candidate := filepath.Join(root, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}
