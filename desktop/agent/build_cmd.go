package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// remarshal converts a map[string]interface{} to a typed struct via JSON round-trip.
func remarshal(src interface{}, dst interface{}) error {
	data, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func runBuild(args []string) {
	if len(args) == 0 {
		printBuildUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "flutter":
		// `yaver build flutter <apk|aab|ipa>` is the legacy form; bare
		// `yaver build flutter [--target=...]` falls through to the new
		// native pipeline. Disambiguate by peeking at args[1].
		if len(args) > 1 && (args[1] == "apk" || args[1] == "aab" || args[1] == "ipa") {
			runBuildFlutter(args[1:])
		} else {
			runNativeFlutter(args[1:])
		}
	case "iosNative", "ios-native":
		runNativeIOS(args[1:])
	case "androidNative", "android-native":
		runNativeAndroid(args[1:])
	case "gradle":
		runBuildGradle(args[1:])
	case "xcode":
		runBuildXcode(args[1:])
	case "rn":
		runBuildRN(args[1:])
	case "custom":
		runBuildCustom(args[1:])
	case "list", "ls":
		runBuildList()
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver build status <id>")
			os.Exit(1)
		}
		runBuildStatus(args[1])
	case "register":
		runBuildRegister(args[1:])
	case "push":
		runBuildPush(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown build subcommand: %s\n\n", args[0])
		printBuildUsage()
		os.Exit(1)
	}
}

func printBuildUsage() {
	fmt.Print(`Usage:
  yaver build flutter apk [--dir <path>]     Build Flutter APK
  yaver build flutter aab [--dir <path>]     Build Flutter App Bundle
  yaver build flutter ipa [--dir <path>]     Build Flutter IPA (iOS)
  yaver build gradle apk [--dir <path>]      Build Android APK via Gradle
  yaver build gradle aab [--dir <path>]      Build Android App Bundle via Gradle
  yaver build xcode ipa [--scheme <name>] [--dir <path>]  Build iOS IPA via Xcode
  yaver build xcode build [--scheme <name>] [--dir <path>] Xcode build (no archive)
  yaver build rn android [--dir <path>]      Build React Native Android
  yaver build rn ios [--dir <path>]          Build React Native iOS
  yaver build custom "<command>" [--dir <path>]  Run custom build command
  yaver build register <file>                Register pre-built artifact
  yaver build list                           List all builds
  yaver build status <id>                    Show build details

Builds run on your dev machine. Artifacts are downloadable from mobile via P2P.
`)
}

// startBuildViaAgent sends a build request to the running agent's HTTP API.
func startBuildViaAgent(platform BuildPlatform, workDir string, extraArgs []string) {
	body := map[string]interface{}{
		"platform": string(platform),
		"workDir":  workDir,
		"args":     extraArgs,
	}
	resp, err := localAgentRequest("POST", "/builds", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is the agent running? Start with 'yaver serve'.")
		os.Exit(1)
	}

	var build Build
	if err := remarshal(resp, &build); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Build started: %s (%s)\n", build.ID, build.Platform)
	fmt.Printf("  Command: %s\n", build.Command)
	fmt.Printf("  Work dir: %s\n", build.WorkDir)
	fmt.Println()
	fmt.Printf("  yaver build status %s     Check status\n", build.ID)
	fmt.Printf("  yaver logs                 View build output\n")
}

func runBuildFlutter(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build flutter <apk|aab|ipa> [--dir <path>]")
		os.Exit(1)
	}

	target := args[0]
	fs := flag.NewFlagSet("build flutter", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args[1:])

	var platform BuildPlatform
	switch target {
	case "apk":
		platform = PlatformFlutterAPK
	case "aab":
		platform = PlatformFlutterAAB
	case "ipa":
		platform = PlatformFlutterIPA
	default:
		fmt.Fprintf(os.Stderr, "Unknown flutter target: %s (use apk, aab, or ipa)\n", target)
		os.Exit(1)
	}

	startBuildViaAgent(platform, *dir, fs.Args())
}

func runBuildGradle(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build gradle <apk|aab> [--dir <path>]")
		os.Exit(1)
	}

	target := args[0]
	fs := flag.NewFlagSet("build gradle", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args[1:])

	var platform BuildPlatform
	switch target {
	case "apk":
		platform = PlatformGradleAPK
	case "aab":
		platform = PlatformGradleAAB
	default:
		fmt.Fprintf(os.Stderr, "Unknown gradle target: %s (use apk or aab)\n", target)
		os.Exit(1)
	}

	startBuildViaAgent(platform, *dir, fs.Args())
}

func runBuildXcode(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build xcode <ipa|build> [--scheme <name>] [--dir <path>]")
		os.Exit(1)
	}

	target := args[0]
	fs := flag.NewFlagSet("build xcode", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	scheme := fs.String("scheme", "", "Xcode scheme")
	fs.Parse(args[1:])

	var platform BuildPlatform
	switch target {
	case "ipa":
		platform = PlatformXcodeIPA
	case "build":
		platform = PlatformXcodeBuild
	default:
		fmt.Fprintf(os.Stderr, "Unknown xcode target: %s (use ipa or build)\n", target)
		os.Exit(1)
	}

	extra := fs.Args()
	if *scheme != "" {
		extra = append([]string{*scheme}, extra...)
	}

	startBuildViaAgent(platform, *dir, extra)
}

func runBuildRN(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build rn <android|ios> [--dir <path>]")
		os.Exit(1)
	}

	target := args[0]
	fs := flag.NewFlagSet("build rn", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args[1:])

	var platform BuildPlatform
	switch target {
	case "android":
		platform = PlatformRNAndroid
	case "ios":
		platform = PlatformRNIOS
	default:
		fmt.Fprintf(os.Stderr, "Unknown rn target: %s (use android or ios)\n", target)
		os.Exit(1)
	}

	startBuildViaAgent(platform, *dir, fs.Args())
}

func runBuildCustom(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build custom \"<command>\" [--dir <path>]")
		os.Exit(1)
	}

	command := args[0]
	fs := flag.NewFlagSet("build custom", flag.ExitOnError)
	dir := fs.String("dir", "", "Working directory")
	fs.Parse(args[1:])

	startBuildViaAgent(PlatformCustom, *dir, []string{command})
}

func runBuildRegister(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build register <file.apk|file.ipa|file.aab>")
		os.Exit(1)
	}

	filePath := args[0]
	body := map[string]interface{}{
		"artifactPath": filePath,
	}
	resp, err := localAgentRequest("POST", "/builds/register", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var build Build
	remarshal(resp, &build)
	fmt.Printf("Registered artifact: %s (%s, %d bytes)\n", build.ArtifactName, build.ID, build.ArtifactSize)
	fmt.Printf("  SHA256: %s\n", build.ArtifactHash)
}

func runBuildList() {
	resp, err := localAgentRequest("GET", "/builds", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var builds []BuildSummary
	if err := remarshal(resp, &builds); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	if len(builds) == 0 {
		fmt.Println("No builds. Start one with 'yaver build flutter apk'.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPLATFORM\tSTATUS\tARTIFACT\tSIZE")
	for _, b := range builds {
		size := ""
		if b.ArtifactSize > 0 {
			size = formatSize(b.ArtifactSize)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.ID, b.Platform, b.Status, b.ArtifactName, size)
	}
	w.Flush()
}

func runBuildStatus(id string) {
	resp, err := localAgentRequest("GET", "/builds/"+id, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var build Build
	if err := remarshal(resp, &build); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Build %s\n", build.ID)
	fmt.Printf("  Platform:  %s\n", build.Platform)
	fmt.Printf("  Status:    %s\n", build.Status)
	fmt.Printf("  Command:   %s\n", build.Command)
	fmt.Printf("  Started:   %s\n", build.StartedAt)
	if build.FinishedAt != "" {
		fmt.Printf("  Finished:  %s\n", build.FinishedAt)
	}
	if build.ArtifactName != "" {
		fmt.Printf("  Artifact:  %s (%s)\n", build.ArtifactName, formatSize(build.ArtifactSize))
		fmt.Printf("  SHA256:    %s\n", build.ArtifactHash)
	}
	if build.Error != "" {
		fmt.Printf("  Error:     %s\n", build.Error)
	}
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func runBuildPush(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: yaver build push <testflight|playstore> <build-id>")
		os.Exit(1)
	}

	target := args[0]
	buildID := args[1]

	// Get build info from agent
	resp, err := localAgentRequest("GET", "/builds/"+buildID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var build Build
	remarshal(resp, &build)
	if build.ArtifactPath == "" {
		fmt.Fprintln(os.Stderr, "Build has no artifact. Wait for build to complete.")
		os.Exit(1)
	}

	switch target {
	case "testflight":
		if !strings.HasSuffix(strings.ToLower(build.ArtifactPath), ".ipa") {
			fmt.Fprintln(os.Stderr, "TestFlight requires an .ipa file. Build with: yaver build flutter ipa")
			os.Exit(1)
		}
		fmt.Printf("Uploading %s to TestFlight...\n", build.ArtifactName)
		if err := uploadToTestFlight(build.ArtifactPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Upload complete. Build will appear in TestFlight shortly.")

	case "playstore":
		if !strings.HasSuffix(strings.ToLower(build.ArtifactPath), ".aab") {
			fmt.Fprintln(os.Stderr, "Play Store requires an .aab file. Build with: yaver build flutter aab")
			os.Exit(1)
		}
		fmt.Printf("Uploading %s to Play Store (internal track)...\n", build.ArtifactName)
		if err := uploadToPlayStore(build.ArtifactPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Upload complete. Build will appear in internal testing track.")

	default:
		fmt.Fprintf(os.Stderr, "Unknown push target: %s (use testflight or playstore)\n", target)
		os.Exit(1)
	}
}

// guessPlatformFromFile guesses build platform from file extension.
func guessPlatformFromFile(path string) BuildPlatform {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".apk"):
		return PlatformFlutterAPK
	case strings.HasSuffix(lower, ".aab"):
		return PlatformFlutterAAB
	case strings.HasSuffix(lower, ".ipa"):
		return PlatformFlutterIPA
	default:
		return PlatformCustom
	}
}
