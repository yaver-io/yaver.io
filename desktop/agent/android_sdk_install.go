package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	androidCommandLineToolsRevision = "14742923"
	androidRemoteRuntimeAPILevel    = "35"
)

func androidSDKRoot() string {
	return filepath.Join(runtimeRoot(), "android-sdk")
}

func androidSDKManagedBinDir() string {
	return filepath.Join(androidSDKRoot(), "bin")
}

func androidCommandLineToolsArchive() (filename, url string, ok bool) {
	switch runtime.GOOS {
	case "linux":
		filename = "commandlinetools-linux-" + androidCommandLineToolsRevision + "_latest.zip"
	case "darwin":
		filename = "commandlinetools-mac-" + androidCommandLineToolsRevision + "_latest.zip"
	default:
		return "", "", false
	}
	return filename, "https://dl.google.com/android/repository/" + filename, true
}

func androidSDKCandidateRoots() []string {
	var roots []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		for _, existing := range roots {
			if existing == p {
				return
			}
		}
		roots = append(roots, p)
	}
	add(os.Getenv("ANDROID_SDK_ROOT"))
	add(os.Getenv("ANDROID_HOME"))
	add(androidSDKRoot())
	return roots
}

func androidToolRelativePath(name string) string {
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

func findAndroidToolPath(name string) string {
	if p, err := lookPathWithRuntimes(name); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	rel := androidToolRelativePath(name)
	if rel == "" {
		return ""
	}
	for _, root := range androidSDKCandidateRoots() {
		candidate := filepath.Join(root, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func androidSystemImagePackage() string {
	arch := "x86_64"
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		arch = "arm64-v8a"
	}
	return fmt.Sprintf("system-images;android-%s;google_apis;%s", androidRemoteRuntimeAPILevel, arch)
}

func installAndroidSDKRuntime(ctx context.Context, progress func(string)) error {
	logf := func(s string) {
		if progress != nil {
			progress(s)
		}
	}

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return fmt.Errorf("android sdk install: unsupported platform %s", runtime.GOOS)
	}

	javaPlan, _ := metaInstallPlan("java")
	if checkInstalled("java") != "✓" {
		logf("Java runtime missing. Installing OpenJDK 17 first.")
		if err := runInstallPlan(ctx, javaPlan, progress); err != nil {
			return fmt.Errorf("java: %w", err)
		}
	}

	root := androidSDKRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create android sdk root: %w", err)
	}

	sdkmanagerPath := findAndroidToolPath("sdkmanager")
	if sdkmanagerPath == "" {
		archiveName, downloadURL, ok := androidCommandLineToolsArchive()
		if !ok {
			return fmt.Errorf("android command line tools not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		tmpZip := filepath.Join(root, archiveName)
		logf("Downloading Android command-line tools …")
		if err := downloadFile(ctx, downloadURL, tmpZip); err != nil {
			return fmt.Errorf("download android command-line tools: %w", err)
		}
		defer os.Remove(tmpZip)

		stage := filepath.Join(root, "cmdline-tools.new")
		_ = os.RemoveAll(stage)
		if err := unzipArchive(tmpZip, stage); err != nil {
			_ = os.RemoveAll(stage)
			return fmt.Errorf("extract android command-line tools: %w", err)
		}
		finalRoot := filepath.Join(root, "cmdline-tools")
		latest := filepath.Join(finalRoot, "latest")
		_ = os.RemoveAll(finalRoot)
		if err := os.MkdirAll(finalRoot, 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(stage, "cmdline-tools"), latest); err != nil {
			return fmt.Errorf("install android command-line tools: %w", err)
		}
		_ = os.RemoveAll(stage)
		sdkmanagerPath = filepath.Join(latest, "bin", "sdkmanager")
	}

	if err := ensureAndroidSDKWrappers(); err != nil {
		return err
	}
	if err := ensureUserShellPathSetup(progress); err != nil {
		return err
	}

	packages := []string{
		"platform-tools",
		"emulator",
		fmt.Sprintf("platforms;android-%s", androidRemoteRuntimeAPILevel),
		androidSystemImagePackage(),
	}
	logf("Installing Android SDK packages for remote runtime …")
	if err := runAndroidSDKManager(ctx, "--licenses"); err != nil {
		return err
	}
	args := append([]string{"--install"}, packages...)
	if err := runAndroidSDKManager(ctx, args...); err != nil {
		return err
	}
	if err := ensureAndroidSDKWrappers(); err != nil {
		return err
	}
	if err := ensureDefaultYaverAVD(ctx, progress); err != nil {
		return err
	}
	logf("Android SDK remote-runtime host tools ready.")
	return nil
}

func runAndroidSDKManager(ctx context.Context, args ...string) error {
	sdkmanagerPath := findAndroidToolPath("sdkmanager")
	if sdkmanagerPath == "" {
		return fmt.Errorf("sdkmanager not found after Android command-line tools install")
	}
	baseArgs := []string{"--sdk_root=" + androidSDKRoot()}
	baseArgs = append(baseArgs, args...)
	cmd := exec.CommandContext(ctx, sdkmanagerPath, baseArgs...)
	cmd.Env = append(augmentEnv(nil), "ANDROID_HOME="+androidSDKRoot(), "ANDROID_SDK_ROOT="+androidSDKRoot())
	cmd.Stdin = strings.NewReader(strings.Repeat("y\n", 64))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sdkmanager %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureAndroidSDKWrappers() error {
	binDir := androidSDKManagedBinDir()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create android sdk bin dir: %w", err)
	}
	for _, name := range []string{"adb", "emulator", "sdkmanager", "avdmanager"} {
		target := filepath.Join(androidSDKRoot(), androidToolRelativePath(name))
		if _, err := os.Stat(target); err != nil {
			continue
		}
		link := filepath.Join(binDir, name)
		if info, err := os.Lstat(link); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if current, readErr := os.Readlink(link); readErr == nil && current == target {
					continue
				}
			}
			if removeErr := os.RemoveAll(link); removeErr != nil {
				return fmt.Errorf("remove stale android sdk wrapper %s: %w", link, removeErr)
			}
		}
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("link %s -> %s: %w", link, target, err)
		}
	}
	return nil
}

func ensureDefaultYaverAVD(ctx context.Context, progress func(string)) error {
	emulatorPath := findAndroidToolPath("emulator")
	avdmanagerPath := findAndroidToolPath("avdmanager")
	if emulatorPath == "" || avdmanagerPath == "" {
		return fmt.Errorf("android emulator tools missing after install")
	}
	listCmd := exec.CommandContext(ctx, emulatorPath, "-list-avds")
	listCmd.Env = append(augmentEnv(nil), "ANDROID_HOME="+androidSDKRoot(), "ANDROID_SDK_ROOT="+androidSDKRoot())
	out, err := listCmd.Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return nil
	}
	name := "Yaver_API_" + androidRemoteRuntimeAPILevel
	cmd := exec.CommandContext(ctx, avdmanagerPath,
		"--silent", "create", "avd",
		"--force",
		"--name", name,
		"--package", androidSystemImagePackage(),
		"--device", "pixel_7",
	)
	cmd.Env = append(augmentEnv(nil), "ANDROID_HOME="+androidSDKRoot(), "ANDROID_SDK_ROOT="+androidSDKRoot())
	cmd.Stdin = strings.NewReader("no\n")
	out, err = cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("create default AVD: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if progress != nil {
		progress("Created default Android AVD: " + name)
	}
	return nil
}

func unzipArchive(srcZip, destDir string) error {
	reader, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, file := range reader.File {
		target := filepath.Join(destDir, file.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(destDir) {
			return fmt.Errorf("invalid zip path: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, file.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		if err := out.Close(); err != nil {
			rc.Close()
			return err
		}
		if err := rc.Close(); err != nil {
			return err
		}
	}
	return nil
}
