package main

// MCP tools for the cable-attached push flow (`yaver wire detect`,
// `yaver wire push`). Lets AI agents in Claude Desktop / Cursor /
// Codex etc. enumerate USB-attached phones and trigger a release-mode
// build + install over USB without shelling out to the CLI.
//
// Output shape mirrors the CLI's `--json` modes so the same parsing
// works whether the caller is a human in a terminal or a coding agent
// in a chat.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// mcpWireDetect lists every USB-attached iPhone/iPad (via xcrun devicectl
// / xctrace fallback) plus every Android device (via adb devices -l).
// Skips simulators, emulators, and WiFi-paired devices. ~10s ceiling on
// xcrun's response, so this is safely synchronous over MCP.
func mcpWireDetect() (interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	devices := append([]wireDevice{}, listIOSWireDevices(ctx)...)
	devices = append(devices, listAndroidWireDevices(ctx)...)
	return map[string]interface{}{
		"devices": devices,
		"count":   len(devices),
		"hint":    mcpWireDetectHint(devices),
	}, nil
}

// mcpWireDetectHint surfaces "the install paths your platform doesn't
// have" so AI agents can suggest fixes without re-running the command.
func mcpWireDetectHint(devices []wireDevice) string {
	hints := []string{}
	if _, err := exec.LookPath("xcrun"); err != nil {
		hints = append(hints, "xcrun missing — iOS detection skipped (install Xcode CLI tools)")
	}
	if _, err := exec.LookPath("adb"); err != nil {
		hints = append(hints, "adb missing — Android detection skipped (brew install android-platform-tools)")
	}
	if len(devices) == 0 {
		hints = append(hints, "no devices found — connect a phone over USB and accept the trust prompt")
	}
	return strings.Join(hints, "; ")
}

// mcpWirePushArgs is the JSON shape MCP clients send.
type mcpWirePushArgs struct {
	// Path to the project. Empty = agent's current workdir.
	Path string `json:"path"`
	// Specific device UDID/serial. Empty = first attached.
	Device string `json:"device"`
	// "ios" or "android" — empty auto-picks based on stack + OS.
	Platform string `json:"platform"`
	// "Debug" or "Release". Empty = "Release" (matches CLI default).
	Config string `json:"config"`
	// True = install but don't launch after. Default false.
	NoLaunch bool `json:"no_launch"`
	// Timeout in seconds. Empty = 1800 (30 min). xcodebuild on a
	// cold pod cache routinely hits 20+ min; tight defaults bite.
	TimeoutSec int `json:"timeout_sec"`
}

// mcpWirePush runs the same dispatch logic as `yaver wire push` but
// captures stdout/stderr to a log file under ~/.yaver/logs/ and returns
// a structured result. The caller gets:
//
//   {
//     ok, exit_code, device, platform, stack, log_path,
//     log_tail (last ~30 lines), elapsed_sec
//   }
//
// On timeout we kill the subprocess and return ok=false with
// exit_code=-1; the partial log is still readable.
func mcpWirePush(args mcpWirePushArgs) (interface{}, error) {
	root := strings.TrimSpace(args.Path)
	if root == "" {
		// Prefer the AI session's pinned cwd; see mcp_session_cwd.go.
		root = ResolveMCPCwd()
		if root == "" {
			return nil, fmt.Errorf("cwd: no session cwd and os.Getwd() returned empty")
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("bad path %q: %w", root, err)
	}

	resolvedRoot, stack := resolveMobileProject(abs)
	if stack == "" {
		return nil, fmt.Errorf(
			"no mobile project detected at %s (or its mobile/, app/, apps/*, packages/* subdirs)",
			abs,
		)
	}

	platform := strings.ToLower(strings.TrimSpace(args.Platform))
	if platform == "" {
		platform = pickPlatformForStack(stack)
	}
	if platform != "ios" && platform != "android" {
		return nil, fmt.Errorf("platform must be ios or android (got %q)", args.Platform)
	}

	timeoutSec := args.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 1800
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	device, err := pickWireDevice(ctx, platform, args.Device)
	if err != nil {
		return nil, err
	}

	cfg := strings.TrimSpace(args.Config)
	if cfg == "" {
		cfg = "Release"
	}
	if cfg != "Debug" && cfg != "Release" {
		return nil, fmt.Errorf("config must be Debug or Release (got %q)", args.Config)
	}

	// Set up a log file we can hand back to the caller. They can
	// poll it via /files/read or the streams endpoint while the
	// build runs (in a future iteration we could wire it through
	// the existing /streams API for real-time SSE).
	homeDir, _ := os.UserHomeDir()
	logsDir := filepath.Join(homeDir, ".yaver", "logs")
	_ = os.MkdirAll(logsDir, 0o755)
	logName := fmt.Sprintf("wire-push-%s-%s-%s.log",
		platform,
		strings.ReplaceAll(device.UDID, ":", ""),
		time.Now().Format("20060102-150405"),
	)
	logPath := filepath.Join(logsDir, logName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log: %w", err)
	}
	defer logFile.Close()

	startedAt := time.Now()
	opts := wirePushOpts{
		device:   device.UDID,
		platform: platform,
		config:   cfg,
		noLaunch: args.NoLaunch,
	}

	// dispatchWirePushTo is a tee-friendly variant that takes a writer
	// instead of using os.Stdout. Defined below.
	dispatchErr := dispatchWirePushTo(ctx, resolvedRoot, stack, platform, device, opts, logFile)
	elapsed := int(time.Since(startedAt).Seconds())

	exitCode := 0
	ok := dispatchErr == nil
	errMsg := ""
	if dispatchErr != nil {
		errMsg = dispatchErr.Error()
		// Best-effort exit-code extraction. Not all errors are exec
		// errors (e.g. context timeout) — in those cases we surface
		// -1 to make the failure obvious.
		exitCode = -1
		if ee, _ := dispatchErr.(*exec.ExitError); ee != nil {
			exitCode = ee.ExitCode()
		}
	}

	tail := readTailLines(logPath, 30)

	return map[string]interface{}{
		"ok":          ok,
		"exit_code":   exitCode,
		"device":      device,
		"platform":    platform,
		"stack":       stack,
		"path":        resolvedRoot,
		"config":      cfg,
		"log_path":    logPath,
		"log_tail":    tail,
		"elapsed_sec": elapsed,
		"error":       errMsg,
	}, nil
}

// dispatchWirePushTo mirrors dispatchWirePush + the underlying runners,
// but every subprocess gets its stdout/stderr redirected to `out` (a
// log file owned by the MCP caller) instead of os.Stdout. Keeps the
// CLI's own `runStreaming` (with cmd.Stdout = os.Stdout) intact for
// terminal users.
func dispatchWirePushTo(
	ctx context.Context,
	root, stack, platform string,
	dev wireDevice,
	opts wirePushOpts,
	out *os.File,
) error {
	switch stack {
	case "expo", "react-native":
		return wirePushExpoOrRNTo(ctx, root, stack, platform, dev, opts, out)
	case "flutter":
		return wirePushFlutterTo(ctx, root, dev, opts, out)
	case "native-ios":
		if platform != "ios" {
			return fmt.Errorf("native-ios project but platform=%s", platform)
		}
		return wirePushNativeIOSTo(ctx, root, dev, opts, out)
	case "native-android":
		if platform != "android" {
			return fmt.Errorf("native-android project but platform=%s", platform)
		}
		return wirePushNativeAndroidTo(ctx, root, dev, opts, out)
	}
	return fmt.Errorf("unsupported stack %q", stack)
}

func wirePushExpoOrRNTo(
	ctx context.Context,
	root, stack, platform string,
	dev wireDevice,
	opts wirePushOpts,
	out *os.File,
) error {
	// Wire push is always native — JS is bundled into the .app/.apk
	// at build time. Generates ios/ + android/ if missing (Expo
	// projects often gitignore these and rely on prebuild).
	if err := ensureNativeProjectDirs(root, stack); err != nil {
		return err
	}
	if platform == "ios" {
		return wirePushNativeIOSTo(ctx, filepath.Join(root, "ios"), dev, opts, out)
	}
	return wirePushNativeAndroidTo(ctx, root, dev, opts, out)
}

func wirePushFlutterTo(ctx context.Context, root string, dev wireDevice, opts wirePushOpts, out *os.File) error {
	if _, err := exec.LookPath("flutter"); err != nil {
		return fmt.Errorf("flutter CLI not found")
	}
	args := []string{"run", "-d", dev.UDID}
	if opts.release() {
		args = append(args, "--release")
	}
	return runCapturedTo(ctx, root, "flutter", args, nil, out)
}

func wirePushNativeIOSTo(ctx context.Context, root string, dev wireDevice, opts wirePushOpts, out *os.File) error {
	if _, err := exec.LookPath("xcodebuild"); err != nil {
		return fmt.Errorf("xcodebuild not found — install Xcode")
	}
	cfgName := "Debug"
	if opts.release() {
		cfgName = "Release"
	}
	derived := filepath.Join(os.TempDir(), "yaver-wire-derived-"+filepath.Base(root))
	scheme, ws, proj, err := pickXcodeTarget(root)
	if err != nil {
		return err
	}
	buildArgs := []string{
		"-scheme", scheme,
		"-configuration", cfgName,
		"-destination", "id=" + dev.UDID,
		"-derivedDataPath", derived,
		"-allowProvisioningUpdates",
		"build",
	}
	if ws != "" {
		buildArgs = append([]string{"-workspace", ws}, buildArgs...)
	} else if proj != "" {
		buildArgs = append([]string{"-project", proj}, buildArgs...)
	}
	if err := runCapturedTo(ctx, root, "xcodebuild", buildArgs, nil, out); err != nil {
		return fmt.Errorf("xcodebuild failed: %w", err)
	}
	appPath := findAppInDerivedData(root)
	if appPath == "" {
		patterns := []string{
			filepath.Join(derived, "Build", "Products", cfgName+"-iphoneos", "*.app"),
		}
		appPath = detectArtifact("", patterns)
	}
	if appPath == "" {
		return fmt.Errorf("could not locate built .app under %s", derived)
	}
	if _, err := installAppOnDevice(ctx, appPath); err != nil {
		return err
	}
	if opts.noLaunch {
		return nil
	}
	bid := readBundleIDFromApp(appPath)
	if bid == "" {
		return nil
	}
	return launchAppOnDevice(ctx, dev.UDID, bid)
}

func wirePushNativeAndroidTo(ctx context.Context, root string, dev wireDevice, opts wirePushOpts, out *os.File) error {
	gradlew := filepath.Join(root, "gradlew")
	androidDir := root
	if !wireExists(gradlew) {
		gradlew = filepath.Join(root, "android", "gradlew")
		androidDir = filepath.Join(root, "android")
	}
	if !wireExists(gradlew) {
		return fmt.Errorf("no gradlew script at %s/gradlew or %s/android/gradlew", root, root)
	}
	target := "installDebug"
	if opts.release() {
		target = "installRelease"
	}
	env := append(os.Environ(), "ANDROID_SERIAL="+dev.UDID)
	if err := runCapturedTo(ctx, androidDir, gradlew, []string{target}, env, out); err != nil {
		return err
	}
	if opts.noLaunch {
		return nil
	}
	pkg := readAndroidPackageFromGradle(androidDir)
	if pkg == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "adb", "-s", dev.UDID, "shell", "monkey",
		"-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// runCapturedTo runs `bin args...` in dir with stdout+stderr both
// redirected to `out`. Used by the MCP path so xcodebuild/gradle
// output ends up in the caller's log file instead of the agent's
// stdout.
func runCapturedTo(ctx context.Context, dir, bin string, args []string, env []string, out *os.File) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

// readTailLines returns the last n lines of a file (or all of them if
// shorter). Best-effort: returns "" if the file is unreadable.
func readTailLines(path string, n int) string {
	if path == "" || n <= 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
