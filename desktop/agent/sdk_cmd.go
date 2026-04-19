package main

import (
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

type sdkInstallPlan struct {
	Family         string
	Platform       string
	PackageManager string
	PackageName    string
	Command        string
	Args           []string
	NextSteps      []string
}

func runSDK(args []string) {
	if len(args) == 0 {
		printSDKUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "add", "install", "inject":
		runSDKAdd(args[1:])
	case "list", "ls":
		printSDKList()
	default:
		fmt.Fprintf(os.Stderr, "Unknown sdk subcommand: %s\n\n", args[0])
		printSDKUsage()
		os.Exit(1)
	}
}

func printSDKUsage() {
	fmt.Print(`Usage:
  yaver sdk add <core|feedback> [--dir <path>] [--platform <name>]
  yaver sdk list

Examples:
  yaver sdk add feedback                 Auto-detect project and inject the Feedback SDK
  yaver sdk add feedback --platform flutter
  yaver sdk add core                    Auto-detect project and add the programmatic SDK
  yaver sdk add core --platform python

Yaver is the single entry point:
  1. Install Yaver once (for example: npm install -g yaver-cli)
  2. Use 'yaver sdk add ...' to inject the right SDK into each project
  3. Use 'yaver ci add ...' to drop a GitHub Actions workflow that runs the same umbrella install on CI
  4. Use 'yaver install ...' for machine-wide toolchains like Hermes, CI tools, browsers, and Android SDK
`)
}

func printSDKList() {
	fmt.Println("Available SDK families:")
	fmt.Println("  feedback   In-app visual feedback loop, black box recorder, hot reload control")
	fmt.Println("             Platforms: expo, react-native, flutter, web")
	fmt.Println("  core       Programmatic Yaver client SDK")
	fmt.Println("             Platforms: js, python, flutter, go")
}

func runSDKAdd(args []string) {
	fs := flag.NewFlagSet("sdk add", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	platform := fs.String("platform", "", "Platform/framework override")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver sdk add <core|feedback> [--dir <path>] [--platform <name>]")
		os.Exit(1)
	}

	if err := performSDKAdd(fs.Arg(0), *dir, *platform); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func performSDKAdd(family, dir, platform string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	plan, err := buildSDKInstallPlan(absDir, family, platform)
	if err != nil {
		return err
	}

	fmt.Printf("Project:  %s\n", absDir)
	fmt.Printf("SDK:      %s\n", plan.Family)
	fmt.Printf("Platform: %s\n", plan.Platform)
	fmt.Printf("Install:  %s %s\n\n", plan.Command, strings.Join(plan.Args, " "))

	cmd := osexec.Command(plan.Command, plan.Args...)
	cmd.Dir = absDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s install failed: %w", plan.PackageManager, err)
	}

	if plan.Family == "feedback" && plan.Platform == "expo" {
		if notes, err := postInstallExpoFeedback(absDir); err == nil {
			plan.NextSteps = append(notes, plan.NextSteps...)
		} else {
			plan.NextSteps = append([]string{fmt.Sprintf("Could not patch Expo config automatically: %v", err)}, plan.NextSteps...)
		}
	}

	fmt.Println("\nNext steps:")
	for _, step := range plan.NextSteps {
		fmt.Printf("  - %s\n", step)
	}
	return nil
}

func buildSDKInstallPlan(dir, family, platform string) (*sdkInstallPlan, error) {
	family = strings.ToLower(strings.TrimSpace(family))
	platform = strings.ToLower(strings.TrimSpace(platform))

	switch family {
	case "feedback":
		return buildFeedbackInstallPlan(dir, platform)
	case "core":
		return buildCoreInstallPlan(dir, platform)
	default:
		return nil, fmt.Errorf("unknown sdk family %q (use core or feedback)", family)
	}
}

func buildFeedbackInstallPlan(dir, platform string) (*sdkInstallPlan, error) {
	if platform == "" {
		platform = detectFeedbackPlatform(dir)
	}
	switch platform {
	case "expo":
		pm := detectPackageManager(dir)
		cmd, args := jsInstallCommand(pm, "yaver-feedback-react-native")
		return &sdkInstallPlan{
			Family:         "feedback",
			Platform:       "expo",
			PackageManager: pm,
			PackageName:    "yaver-feedback-react-native",
			Command:        cmd,
			Args:           args,
			NextSteps: []string{
				"Add `initExpo()` and `<FeedbackModal />` to your root app component.",
				"Run `yaver expo start` to launch the dev session through Yaver.",
			},
		}, nil
	case "react-native", "rn":
		pm := detectPackageManager(dir)
		cmd, args := jsInstallCommand(pm, "yaver-feedback-react-native")
		return &sdkInstallPlan{
			Family:         "feedback",
			Platform:       "react-native",
			PackageManager: pm,
			PackageName:    "yaver-feedback-react-native",
			Command:        cmd,
			Args:           args,
			NextSteps: []string{
				"Initialize `YaverFeedback` in your debug app entrypoint.",
				"Add `<FeedbackModal />` or the floating button in your root component.",
			},
		}, nil
	case "flutter":
		return &sdkInstallPlan{
			Family:         "feedback",
			Platform:       "flutter",
			PackageManager: "flutter",
			PackageName:    "yaver_feedback",
			Command:        "flutter",
			Args:           []string{"pub", "add", "yaver_feedback"},
			NextSteps: []string{
				"Initialize `YaverFeedback.init(...)` in debug builds.",
				"Add `YaverFeedbackButton` or the overlay widget to your app root.",
			},
		}, nil
	case "web":
		pm := detectPackageManager(dir)
		cmd, args := jsInstallCommand(pm, "yaver-feedback-web")
		return &sdkInstallPlan{
			Family:         "feedback",
			Platform:       "web",
			PackageManager: pm,
			PackageName:    "yaver-feedback-web",
			Command:        cmd,
			Args:           args,
			NextSteps: []string{
				"Call `YaverFeedback.init({ trigger: 'floating-button' })` in development mode.",
				"The first time the user clicks the feedback button, they sign in to Yaver via the SDK's in-app modal (Apple / Google / GitHub / GitLab / Microsoft / email).",
				"Run `yaver serve` on your dev machine if the agent is not already running.",
			},
		}, nil
	default:
		return nil, fmt.Errorf("could not detect a supported feedback SDK target in %s (supported: expo, react-native, flutter, web)", dir)
	}
}

func buildCoreInstallPlan(dir, platform string) (*sdkInstallPlan, error) {
	if platform == "" {
		platform = detectCorePlatform(dir)
	}
	switch platform {
	case "js", "javascript", "typescript", "web":
		pm := detectPackageManager(dir)
		cmd, args := jsInstallCommand(pm, "yaver-sdk")
		return &sdkInstallPlan{
			Family:         "core",
			Platform:       "js",
			PackageManager: pm,
			PackageName:    "yaver-sdk",
			Command:        cmd,
			Args:           args,
			NextSteps: []string{
				"Import `YaverClient` from `yaver-sdk` in your app or service.",
			},
		}, nil
	case "python", "py":
		manager, cmd, args := pythonSDKInstallCommand(dir)
		return &sdkInstallPlan{
			Family:         "core",
			Platform:       "python",
			PackageManager: manager,
			PackageName:    "yaver",
			Command:        cmd,
			Args:           args,
			NextSteps: []string{
				"Import `YaverClient` from `yaver` and connect it to your agent URL/token.",
			},
		}, nil
	case "flutter", "dart":
		return &sdkInstallPlan{
			Family:         "core",
			Platform:       "flutter",
			PackageManager: "flutter",
			PackageName:    "yaver",
			Command:        "flutter",
			Args:           []string{"pub", "add", "yaver"},
			NextSteps: []string{
				"Import `package:yaver/yaver.dart` and create a `YaverClient`.",
			},
		}, nil
	case "go":
		return &sdkInstallPlan{
			Family:         "core",
			Platform:       "go",
			PackageManager: "go",
			PackageName:    "github.com/kivanccakmak/yaver.io/sdk/go/yaver",
			Command:        "go",
			Args:           []string{"get", "github.com/kivanccakmak/yaver.io/sdk/go/yaver"},
			NextSteps: []string{
				"Import `github.com/kivanccakmak/yaver.io/sdk/go/yaver` and create a client.",
			},
		}, nil
	default:
		return nil, fmt.Errorf("could not detect a supported core SDK target in %s (supported: js, python, flutter, go)", dir)
	}
}

func detectFeedbackPlatform(dir string) string {
	if isExpoProject(dir) {
		return "expo"
	}
	switch detectFramework(dir) {
	case "react-native":
		return "react-native"
	case "flutter":
		return "flutter"
	case "nextjs", "vite", "react":
		return "web"
	}
	if hasFile(dir, "package.json") {
		return "web"
	}
	return ""
}

func detectCorePlatform(dir string) string {
	if hasFile(dir, "go.mod") {
		return "go"
	}
	if hasFile(dir, "pubspec.yaml") {
		return "flutter"
	}
	if hasFile(dir, "pyproject.toml") || hasFile(dir, "requirements.txt") || hasFile(dir, "setup.py") {
		return "python"
	}
	if hasFile(dir, "package.json") {
		return "js"
	}
	return ""
}

func jsInstallCommand(pm, pkg string) (string, []string) {
	switch pm {
	case "yarn":
		return "yarn", []string{"add", pkg}
	case "pnpm":
		return "pnpm", []string{"add", pkg}
	default:
		return "npm", []string{"install", pkg}
	}
}

func pythonSDKInstallCommand(dir string) (manager, cmd string, args []string) {
	if hasFile(dir, "poetry.lock") || pyprojectContains(dir, "[tool.poetry]") {
		return "poetry", "poetry", []string{"add", "yaver"}
	}
	if hasFile(dir, "uv.lock") || (hasFile(dir, "pyproject.toml") && commandOnPath("uv")) {
		return "uv", "uv", []string{"add", "yaver"}
	}
	if commandOnPath("python3") {
		return "pip", "python3", []string{"-m", "pip", "install", "yaver"}
	}
	return "pip", "pip", []string{"install", "yaver"}
}

func pyprojectContains(dir, needle string) bool {
	data, err := readSmallFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

func commandOnPath(name string) bool {
	_, err := osexec.LookPath(name)
	return err == nil
}

func postInstallExpoFeedback(dir string) ([]string, error) {
	appJSON := filepath.Join(dir, "app.json")
	appConfigJS := filepath.Join(dir, "app.config.js")
	appConfigTS := filepath.Join(dir, "app.config.ts")

	if _, err := os.Stat(appJSON); err == nil {
		if err := addPluginToAppJSON(appJSON); err != nil {
			return nil, err
		}
		return []string{"Added `yaver-feedback-react-native` to `app.json` plugins."}, nil
	}
	if _, err := os.Stat(appConfigJS); err == nil {
		return []string{"Detected `app.config.js` — add `\"yaver-feedback-react-native\"` to `expo.plugins` manually."}, nil
	}
	if _, err := os.Stat(appConfigTS); err == nil {
		return []string{"Detected `app.config.ts` — add `\"yaver-feedback-react-native\"` to `expo.plugins` manually."}, nil
	}
	return []string{"No Expo app config file found — add `\"yaver-feedback-react-native\"` to your Expo plugins manually if needed."}, nil
}
