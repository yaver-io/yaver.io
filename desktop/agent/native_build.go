package main

// Native build & deploy — friendly entrypoints for pushing native iOS Swift,
// native Android Kotlin, and Flutter apps from a headless dev machine to a
// connected device, simulator/emulator, TestFlight, or Play Store.
//
// This file is the back-end for `yaver iosNative` / `yaver androidNative` /
// `yaver flutter`, the matching `/builds` POST aliases (`platform`:
// "iosNative" | "androidNative" | "flutter" with a `target` field), and is
// also surfaced through the existing build_http.go path so mobile, web, and
// MCP all hit one orchestration. React Native + Hermes paths are untouched.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Friendly native platform names accepted by the CLI and /builds POST.
const (
	NativeIOS     = "iosNative"
	NativeAndroid = "androidNative"
	NativeFlutter = "flutter"
)

// New BuildPlatform constants for device-install paths that didn't exist yet.
// Keep the existing PlatformXcodeDeviceInstall as the iOS counterpart.
const (
	PlatformGradleDeviceInstall  BuildPlatform = "gradle-device-install"
	PlatformFlutterDeviceInstall BuildPlatform = "flutter-device-install"
)

// resolveNativePlatform maps (native alias, target) to the concrete BuildPlatform
// the existing build pipeline knows how to run.
//
// Targets:
//   - device     → install on a connected USB/WiFi device (xcrun devicectl / adb / flutter)
//   - simulator  → simulator/emulator install (currently same path as device for adb;
//     for iOS this falls back to a generic xcodebuild)
//   - testflight → build IPA for App Store Connect upload (use `yaver build push testflight`)
//   - playstore  → build AAB for Play Store upload (use `yaver build push playstore`)
//   - local      → just produce the artifact, no install or upload
func resolveNativePlatform(native, target string) (BuildPlatform, error) {
	if target == "" {
		target = "device"
	}
	target = strings.ToLower(target)
	switch native {
	case NativeIOS, "ios-native", "ios":
		switch target {
		case "device":
			return PlatformXcodeDeviceInstall, nil
		case "simulator", "sim":
			return PlatformXcodeBuild, nil
		case "testflight", "ipa", "local":
			return PlatformXcodeIPA, nil
		}
	case NativeAndroid, "android-native", "android":
		switch target {
		case "device", "emulator", "emu", "simulator", "sim":
			return PlatformGradleDeviceInstall, nil
		case "playstore", "aab":
			return PlatformGradleAAB, nil
		case "apk", "local":
			return PlatformGradleAPK, nil
		}
	case NativeFlutter:
		switch target {
		case "device", "emulator", "emu", "simulator", "sim":
			return PlatformFlutterDeviceInstall, nil
		case "ios", "ipa", "testflight":
			return PlatformFlutterIPA, nil
		case "playstore", "aab":
			return PlatformFlutterAAB, nil
		case "apk", "local":
			return PlatformFlutterAPK, nil
		}
	}
	return "", fmt.Errorf("unsupported native combo: platform=%q target=%q "+
		"(valid platforms: iosNative, androidNative, flutter)", native, target)
}

// dispatchDeviceInstall picks the right device-install path based on platform
// and the artifact extension. Called from BuildManager.monitorBuild after a
// successful build when InstallOnDevice == true.
func dispatchDeviceInstall(ctx context.Context, platform BuildPlatform, artifactPath string) (deviceID string, err error) {
	switch platform {
	case PlatformXcodeDeviceInstall:
		return installAppOnDevice(ctx, artifactPath)
	}

	// Heuristic: choose by artifact extension so Flutter / Gradle / .apk all flow
	// through the same adb path without needing extra constants.
	lower := strings.ToLower(artifactPath)
	switch {
	case strings.HasSuffix(lower, ".apk"):
		return installAPKOnAndroidDevice(ctx, artifactPath)
	case strings.HasSuffix(lower, ".aab"):
		return "", fmt.Errorf(".aab cannot be installed directly — use --target=playstore to upload, or build an .apk for device install")
	case strings.HasSuffix(lower, ".app"):
		return installAppOnDevice(ctx, artifactPath)
	case strings.HasSuffix(lower, ".ipa"):
		return "", fmt.Errorf(".ipa device install requires Apple Configurator or `xcrun devicectl install` with a signed IPA — try --target=device on a .app or push via TestFlight")
	}
	return "", fmt.Errorf("no device-install path for platform %s (artifact %s)", platform, filepath.Base(artifactPath))
}

// dispatchAutoLaunch tries to launch the just-installed app on the device so
// "Open App" actually opens it. Best-effort — failure here doesn't fail the build.
func dispatchAutoLaunch(ctx context.Context, platform BuildPlatform, deviceID, artifactPath string) error {
	lower := strings.ToLower(artifactPath)
	switch {
	case strings.HasSuffix(lower, ".app"):
		bundleID := readBundleIDFromApp(artifactPath)
		if bundleID == "" {
			return fmt.Errorf("could not read CFBundleIdentifier from %s", artifactPath)
		}
		return launchAppOnDevice(ctx, deviceID, bundleID)
	case strings.HasSuffix(lower, ".apk"):
		return launchAPKOnAndroidDevice(ctx, deviceID, artifactPath)
	}
	return nil
}

// installAPKOnAndroidDevice runs `adb install -r -t` against the first online
// device returned by `adb devices`. Returns the device serial.
func installAPKOnAndroidDevice(ctx context.Context, apkPath string) (string, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return "", fmt.Errorf("adb not found — install Android platform-tools (brew install android-platform-tools) to enable native Android device install")
	}
	serial, err := detectAndroidDevice(ctx)
	if err != nil {
		return "", err
	}
	if serial == "" {
		return "", fmt.Errorf("no Android device detected — connect a device with USB debugging enabled, or boot an emulator")
	}
	cmd := exec.CommandContext(ctx, "adb", "-s", serial, "install", "-r", "-t", apkPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return serial, fmt.Errorf("adb install failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return serial, nil
}

// launchAPKOnAndroidDevice starts the launcher activity declared by the APK's
// manifest. Uses aapt2/aapt to read the package + activity.
func launchAPKOnAndroidDevice(ctx context.Context, serial, apkPath string) error {
	pkg, activity := readAndroidLaunchInfo(apkPath)
	if pkg == "" {
		return fmt.Errorf("could not determine Android package from %s — is aapt2 installed?", apkPath)
	}
	target := pkg
	if activity != "" {
		target = pkg + "/" + activity
	}
	cmd := exec.CommandContext(ctx, "adb", "-s", serial, "shell", "am", "start", "-n", target)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("adb am start failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// detectAndroidDevice returns the first online "device" from `adb devices`.
// Returns ("", nil) when no device is attached so callers can produce a friendly error.
func detectAndroidDevice(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return "", fmt.Errorf("adb not found — install Android platform-tools")
	}
	out, err := exec.CommandContext(ctx, "adb", "devices").Output()
	if err != nil {
		return "", fmt.Errorf("adb devices failed: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			return fields[0], nil
		}
	}
	return "", nil
}

var (
	aaptPackageRe  = regexp.MustCompile(`package: name='([^']+)'`)
	aaptActivityRe = regexp.MustCompile(`launchable-activity: name='([^']+)'`)
)

// readAndroidLaunchInfo extracts (package, launcher activity) from an APK by
// running `aapt2 dump badging` (preferred) or falling back to `aapt`.
func readAndroidLaunchInfo(apkPath string) (pkg, activity string) {
	var out []byte
	if _, err := exec.LookPath("aapt2"); err == nil {
		out, _ = exec.Command("aapt2", "dump", "badging", apkPath).Output()
	}
	if len(out) == 0 {
		if _, err := exec.LookPath("aapt"); err == nil {
			out, _ = exec.Command("aapt", "dump", "badging", apkPath).Output()
		}
	}
	if m := aaptPackageRe.FindStringSubmatch(string(out)); len(m) >= 2 {
		pkg = m[1]
	}
	if m := aaptActivityRe.FindStringSubmatch(string(out)); len(m) >= 2 {
		activity = m[1]
	}
	return
}

// resolveNativeBuildCommand handles the two new BuildPlatform constants this
// file introduces. Called from resolveBuildCommand in builds.go via fallthrough.
func resolveNativeBuildCommand(platform BuildPlatform, workDir string, extraArgs []string) (command string, artifactPatterns []string, ok bool) {
	extra := strings.Join(extraArgs, " ")
	if extra != "" {
		extra = " " + extra
	}
	switch platform {
	case PlatformGradleDeviceInstall:
		// Build a debuggable APK so it actually installs on a real device
		// without Play Store signing. assembleDebug is what the Android
		// Studio Run button does under the hood.
		gradlew := "./gradlew"
		if _, err := os.Stat(filepath.Join(workDir, "gradlew")); err != nil {
			gradlew = "gradle"
		}
		task := "assembleDebug"
		if extra != "" {
			task = strings.TrimSpace(extra)
			extra = ""
		}
		return fmt.Sprintf("JAVA_HOME=%s %s %s", findJavaHome(), gradlew, task), []string{
			"app/build/outputs/apk/debug/*.apk",
			"app/build/outputs/apk/release/*.apk",
		}, true

	case PlatformFlutterDeviceInstall:
		// Flutter has its own device picker — `flutter build apk --debug`
		// produces an APK we can adb-install onto whichever device is attached.
		// Native Android only here; iOS device install for Flutter projects
		// goes through PlatformXcodeDeviceInstall when invoked with
		// --target=device on an iOS-only project (caller resolves).
		return "flutter build apk --debug" + extra, []string{
			"build/app/outputs/flutter-apk/app-debug.apk",
			"build/app/outputs/flutter-apk/app-release.apk",
		}, true
	}
	return "", nil, false
}

// runNativeIOS / runNativeAndroid / runNativeFlutter — top-level CLI entrypoints
// wired in main.go's switch cmd block. All three share the same flag surface.
func runNativeIOS(args []string)     { runNativeBuild(NativeIOS, args) }
func runNativeAndroid(args []string) { runNativeBuild(NativeAndroid, args) }
func runNativeFlutter(args []string) { runNativeBuild(NativeFlutter, args) }

func runNativeBuild(native string, args []string) {
	fs := flag.NewFlagSet("native "+native, flag.ExitOnError)
	target := fs.String("target", "device", "Build target: device | simulator | testflight | playstore | local")
	dir := fs.String("dir", "", "Project directory (defaults to cwd or first positional arg)")
	scheme := fs.String("scheme", "", "Xcode scheme override (iosNative only)")
	flavor := fs.String("flavor", "", "Gradle task / product flavor override (androidNative only)")
	install := fs.Bool("install", true, "Install on connected device when --target=device")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  yaver %s [project-dir] [--target=<device|simulator|testflight|playstore|local>]\n\nFlags:\n", native)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	workDir := *dir
	if workDir == "" && fs.NArg() > 0 {
		workDir = fs.Arg(0)
	}
	if workDir == "" {
		cwd, _ := os.Getwd()
		workDir = cwd
	}

	platform, err := resolveNativePlatform(native, *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(2)
	}

	extra := []string{}
	switch native {
	case NativeIOS:
		if *scheme != "" {
			extra = append(extra, *scheme)
		}
	case NativeAndroid:
		if *flavor != "" {
			extra = append(extra, *flavor)
		}
	}
	if fs.NArg() > 1 {
		extra = append(extra, fs.Args()[1:]...)
	}

	body := map[string]interface{}{
		"platform":        string(platform),
		"workDir":         workDir,
		"args":            extra,
		"installOnDevice": *install && (*target == "device" || *target == "simulator" || *target == "emulator"),
	}
	resp, err := localAgentRequest("POST", "/builds", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nIs the agent running? Start with 'yaver serve'.\n", err)
		os.Exit(1)
	}

	var build Build
	if err := remarshal(resp, &build); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Native build started: %s --target=%s\n", native, *target)
	fmt.Printf("  Build ID: %s\n", build.ID)
	fmt.Printf("  Platform: %s\n", build.Platform)
	fmt.Printf("  Work dir: %s\n", build.WorkDir)
	fmt.Printf("  Command:  %s\n", build.Command)
	fmt.Println()
	fmt.Printf("  yaver build status %s    Check status\n", build.ID)
	fmt.Printf("  yaver logs                 View build output\n")
	switch *target {
	case "testflight":
		fmt.Printf("  yaver build push testflight %s   Upload IPA after build completes\n", build.ID)
	case "playstore":
		fmt.Printf("  yaver build push playstore %s    Upload AAB after build completes\n", build.ID)
	}
}
