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
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		switch runtime.GOOS {
		case "darwin":
			add(filepath.Join(home, "Library", "Android", "sdk"))
		case "linux":
			add(filepath.Join(home, "Android", "Sdk"))
			add(filepath.Join(home, "Android", "sdk"))
		}
	}
	add("/opt/android-sdk")
	add(androidSDKRoot())
	return roots
}

func looksLikeAndroidSDKRoot(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	for _, rel := range []string{
		filepath.Join("platform-tools", "adb"),
		"platforms",
		"build-tools",
		filepath.Join("cmdline-tools", "latest"),
	} {
		if info, err := os.Stat(filepath.Join(root, rel)); err == nil {
			if strings.HasSuffix(rel, "adb") {
				if !info.IsDir() {
					return true
				}
				continue
			}
			if info.IsDir() {
				return true
			}
		}
	}
	return false
}

func detectedAndroidSDKRoot() string {
	for _, root := range androidSDKCandidateRoots() {
		if looksLikeAndroidSDKRoot(root) {
			return root
		}
	}
	return ""
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

// emulatorHostSupported reports whether Google publishes an Android
// Emulator host binary for the given GOOS/GOARCH. Google ships the
// emulator only for linux-x86_64, darwin-x86_64, darwin-arm64 and
// windows — there is NO linux-aarch64 build. On an aarch64 Linux host
// `sdkmanager` has no `emulator` package at all, so requesting it
// aborts the entire SDK install. This is independent of /dev/kvm:
// even TCG software emulation needs the host binary to exist.
func emulatorHostSupported(goos, goarch string) bool {
	return !(goos == "linux" && goarch == "arm64")
}

// androidEmulatorHostSupported is emulatorHostSupported for the
// running host.
func androidEmulatorHostSupported() bool {
	return emulatorHostSupported(runtime.GOOS, runtime.GOARCH)
}

// androidRuntimeSDKPackages is the sdkmanager package set for the
// remote-runtime host. platform-tools (adb) and the build platform
// are always useful — they back `yaver wire` native builds even where
// the emulator can't run. emulator + system image are only added on
// hosts that actually have a published emulator binary; adding them
// on linux/arm64 makes `sdkmanager --install` fail the whole batch.
func androidRuntimeSDKPackages() []string {
	pkgs := []string{
		"platform-tools",
		fmt.Sprintf("platforms;android-%s", androidRemoteRuntimeAPILevel),
	}
	if androidEmulatorHostSupported() {
		pkgs = append(pkgs, "emulator", androidSystemImagePackage())
	}
	return pkgs
}

func androidSystemImagePackage() string {
	// Match the system image ABI to the host architecture. Apple
	// Silicon (darwin/arm64) runs arm64-v8a images natively under
	// HVF; x86-64 hosts use x86_64 images. We never reach here on
	// linux/arm64 — Google ships no emulator host binary for that
	// arch (see emulatorHostSupported), so there'd be nothing to run
	// an arm64-v8a image with. Pulling the wrong ABI forces cross-arch
	// QEMU translation (5+ min boots, unusable reload cycles), so this
	// must stay arch-matched.
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64-v8a"
	}
	// google_atd (Android Test Driver) is ~40% smaller than
	// google_apis_playstore and headless-optimized — the right call for
	// a Yaver remote runtime that doesn't need Play Store / sign-in
	// surfaces. The dev's app still installs via adb, just like a
	// playstore image.
	return fmt.Sprintf("system-images;android-%s;google_atd;%s", androidRemoteRuntimeAPILevel, arch)
}

// installAndroidSDKRuntime downloads + installs OpenJDK 17 (if absent)
// and the Android command-line tools + SDK packages. It pulls hundreds
// of MB and mutates the user's shell PATH, so it MUST NOT run unprompted:
// `approved` is the structural enforcement of the never-install-without-
// approval contract. The only callers are (a) the build/deploy preflight
// after installDeps:true, and (b) an explicit `yaver install` command —
// both pass approved=true. Anything else gets a hard refusal.
func installAndroidSDKRuntime(ctx context.Context, approved bool, progress func(string)) error {
	logf := func(s string) {
		if progress != nil {
			progress(s)
		}
	}

	if !approved {
		return fmt.Errorf("android sdk install requires explicit approval — " +
			"re-invoke build/deploy with installDeps:true, or run `yaver install android-sdk`")
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

	packages := androidRuntimeSDKPackages()
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
	if !androidEmulatorHostSupported() {
		logf("Android emulator host binary is not published for " +
			runtime.GOOS + "/" + runtime.GOARCH + " — installed adb + " +
			"platform tools only. The android-emulator remote-runtime " +
			"target stays disabled here; stream from a physical device " +
			"(`yaver wire`) or a macOS / x86-64-Linux host instead.")
		logf("Android SDK remote-runtime host tools ready (no emulator).")
		return nil
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
