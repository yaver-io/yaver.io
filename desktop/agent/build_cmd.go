package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"
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
	case "ios", "ipa":
		// `yaver build ipa status` → status of the iOS build for the
		// current project; otherwise build a .ipa (no upload).
		if len(args) > 1 && args[1] == "status" {
			runBuildStatusSmart(append([]string{"ipa"}, args[2:]...))
		} else {
			runNativeReleaseBuild(NativeIOS, args[1:])
		}
	case "android", "aab":
		// `yaver build aab status` → status of the Android build for the
		// current project; otherwise build a .aab (no upload).
		if len(args) > 1 && args[1] == "status" {
			runBuildStatusSmart(append([]string{"aab"}, args[2:]...))
		} else {
			runNativeReleaseBuild(NativeAndroid, args[1:])
		}
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
		runBuildStatusSmart(args[1:])
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
  yaver build ios [repo-or-project-dir]      Discover iOS project and build IPA
  yaver build ipa [repo-or-project-dir]      Alias of 'build ios' (→ .ipa, no upload)
  yaver build android [repo-or-project-dir]  Discover Android project and build AAB
  yaver build aab [repo-or-project-dir]      Alias of 'build android' (→ .aab, no upload)
  yaver build gradle apk [--dir <path>]      Build Android APK via Gradle
  yaver build gradle aab [--dir <path>]      Build Android App Bundle via Gradle
  yaver build xcode ipa [--scheme <name>] [--dir <path>]  Build iOS IPA via Xcode
  yaver build xcode build [--scheme <name>] [--dir <path>] Xcode build (no archive)
  yaver build rn android [--dir <path>]      Build React Native Android
  yaver build rn ios [--dir <path>]          Build React Native iOS
  yaver build custom "<command>" [--dir <path>]  Run custom build command
  yaver build register <file>                Register pre-built artifact
  yaver build list                           List all builds
  yaver build status [<id>]                  Show build details (no id → newest build for this dir/repo)
  yaver build status ipa|aab|ios|android     Newest matching build for the current project
  yaver build ipa status                     Status of the iOS build for the current project
  yaver build aab status                     Status of the Android build for the current project

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

	c := buildUseColor()
	fmt.Printf("%s %s\n", tcol(c, cyanCode, "●"), tcol(c, cyanCode, fmt.Sprintf("Build %s started", build.ID)))
	fmt.Printf("  %s\n", tcol(c, dimCode, friendlyPlatform(build.Platform)))
	fmt.Println()
	if build.WorkDir != "" {
		fmt.Printf("  Work dir  %s\n", build.WorkDir)
	}
	fmt.Printf("  Command   %s\n", build.Command)
	fmt.Println()
	fmt.Println("  Runs in the background on this machine. Track it with:")
	fmt.Printf("    %s   progress, log tail, artifact\n", tcol(c, dimCode, "yaver build status "+build.ID))
	fmt.Printf("    %s                       stream full build output\n", tcol(c, dimCode, "yaver logs"))
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

// runBuildStatusSmart implements `yaver build status [<id>|ipa|aab|ios|android]`.
// With no id it resolves the most relevant build for the current working
// directory (monorepo / repo-root aware), optionally narrowed to an iOS or
// Android artifact.
func runBuildStatusSmart(args []string) {
	fs := flag.NewFlagSet("build status", flag.ExitOnError)
	dir := fs.String("dir", "", "Project/repo directory to match (defaults to cwd)")
	_ = fs.Parse(args)

	platformFilter := ""
	idArg := ""
	for _, a := range fs.Args() {
		switch strings.ToLower(a) {
		case "ipa", "ios":
			platformFilter = "ios"
		case "aab", "android":
			platformFilter = "android"
		default:
			if idArg == "" {
				idArg = a
			}
		}
	}

	// Explicit id wins — exact lookup, no directory matching.
	if idArg != "" {
		runBuildStatus(idArg)
		return
	}

	startDir := strings.TrimSpace(*dir)
	if startDir == "" {
		startDir, _ = os.Getwd()
	}
	id, matchedDir, err := resolveBuildIDForDir(startDir, platformFilter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Fprintln(os.Stderr, "  Tip: 'yaver build list' shows every build, or pass an id explicitly.")
		os.Exit(1)
	}
	c := buildUseColor()
	scope := "this directory"
	if matchedDir != "" {
		scope = matchedDir
	}
	fmt.Printf("%s\n\n", tcol(c, dimCode, "Matched build for "+scope))
	runBuildStatus(id)
}

// resolveBuildIDForDir finds the newest build whose WorkDir belongs to the
// same project/repo as startDir. A running build always wins over a finished
// one. platformFilter is "", "ios" or "android".
func resolveBuildIDForDir(startDir, platformFilter string) (id string, matchedDir string, err error) {
	resp, lerr := localAgentRequest("GET", "/builds", nil)
	if lerr != nil {
		return "", "", fmt.Errorf("Error: %v", lerr)
	}
	var builds []BuildSummary
	if rerr := remarshal(resp, &builds); rerr != nil {
		return "", "", fmt.Errorf("Error parsing response: %v", rerr)
	}

	cwd, _ := filepath.Abs(startDir)
	repoRoot := findRepoRoot(cwd)
	if repoRoot == "" {
		repoRoot = cwd
	}

	var running, finished *BuildSummary
	for i := range builds {
		b := &builds[i]
		if b.WorkDir == "" {
			continue
		}
		if platformFilter != "" && platformFamily(b.Platform) != platformFilter {
			continue
		}
		bw, _ := filepath.Abs(b.WorkDir)
		bRepo := findRepoRoot(bw)
		if bRepo == "" {
			bRepo = bw
		}
		if !(sameOrUnder(cwd, bw) || sameOrUnder(bw, cwd) || pathsEqual(repoRoot, bRepo)) {
			continue
		}
		// builds came newest-first, so the first hit per bucket is newest.
		if b.Status == BuildStatusRunning {
			if running == nil {
				running = b
			}
		} else if finished == nil {
			finished = b
		}
	}

	pick := running
	if pick == nil {
		pick = finished
	}
	if pick == nil {
		what := "build"
		if platformFilter == "ios" {
			what = "iOS (.ipa) build"
		} else if platformFilter == "android" {
			what = "Android (.aab) build"
		}
		return "", "", fmt.Errorf("No %s found for %s.", what, repoRoot)
	}
	return pick.ID, pick.WorkDir, nil
}

// platformFamily buckets a platform into "ios", "android" or "" (other).
func platformFamily(p BuildPlatform) string {
	switch p {
	case PlatformFlutterIPA, PlatformXcodeIPA, PlatformRNIOS, PlatformExpoIOS,
		PlatformXcodeDeviceInstall, PlatformXcodeBuild:
		return "ios"
	case PlatformFlutterAAB, PlatformFlutterAPK, PlatformGradleAAB, PlatformGradleAPK,
		PlatformRNAndroid, PlatformExpoAndroid:
		return "android"
	default:
		return ""
	}
}

func pathsEqual(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// sameOrUnder reports whether child is parent itself or nested inside it.
func sameOrUnder(child, parent string) bool {
	c := filepath.Clean(child)
	p := filepath.Clean(parent)
	if c == p {
		return true
	}
	return strings.HasPrefix(c, p+string(filepath.Separator))
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

	c := buildUseColor()
	glyph, label, color := humanBuildState(build)

	// Headline: ● Build abc123 — Running
	headline := fmt.Sprintf("Build %s — %s", build.ID, label)
	if build.ExitCode != nil && build.Status == BuildStatusFailed && *build.ExitCode != 0 {
		headline += fmt.Sprintf(" (exit %d)", *build.ExitCode)
	}
	fmt.Printf("%s %s\n", tcol(c, color, glyph), tcol(c, color, headline))
	fmt.Printf("  %s\n", tcol(c, dimCode, friendlyPlatform(build.Platform)))

	// Timing line — duration so far (running) or total time taken.
	switch build.Status {
	case BuildStatusRunning:
		if d, ok := sinceTime(build.StartedAt); ok {
			fmt.Printf("  Building for %s\n", fmtBuildDur(d))
		}
	default:
		if d, ok := betweenTime(build.StartedAt, build.FinishedAt); ok {
			line := "Took " + fmtBuildDur(d)
			if ago, ok := sinceTime(build.FinishedAt); ok {
				line += fmt.Sprintf("  ·  finished %s ago", fmtBuildDur(ago))
			}
			fmt.Printf("  %s\n", line)
		}
	}

	fmt.Println()
	if build.WorkDir != "" {
		fmt.Printf("  Work dir  %s\n", build.WorkDir)
	}
	if build.Command != "" {
		fmt.Printf("  Command   %s\n", build.Command)
	}

	switch build.Status {
	case BuildStatusRunning:
		fmt.Println()
		tail := fetchBuildLogTail(build.ExecID, 8)
		if len(tail) > 0 {
			fmt.Println("  Still building — latest output:")
			for _, ln := range tail {
				fmt.Printf("  %s %s\n", tcol(c, dimCode, "│"), ln)
			}
		} else {
			fmt.Println("  Still building — no output captured yet.")
		}
		fmt.Println()
		fmt.Printf("  Follow live   %s\n", tcol(c, dimCode, "yaver logs"))
		fmt.Printf("  Re-check      %s\n", tcol(c, dimCode, "yaver build status "+build.ID))

	case BuildStatusCompleted:
		fmt.Println()
		if build.ArtifactName != "" {
			fmt.Printf("  Artifact  %s  (%s)\n", build.ArtifactName, formatSize(build.ArtifactSize))
			if build.ArtifactPath != "" {
				fmt.Printf("  Path      %s\n", build.ArtifactPath)
			}
			if build.ArtifactHash != "" {
				fmt.Printf("  SHA256    %s\n", build.ArtifactHash)
			}
		}
		if next := pushHint(build); next != "" {
			fmt.Println()
			fmt.Printf("  Ship it   %s\n", tcol(c, dimCode, next))
		}

	case BuildStatusFailed:
		fmt.Println()
		if build.Error != "" {
			fmt.Printf("  %s %s\n", tcol(c, redCode, "Error:"), build.Error)
		}
		tail := fetchBuildLogTail(build.ExecID, 12)
		if len(tail) > 0 {
			fmt.Println()
			fmt.Println("  Last output before failure:")
			for _, ln := range tail {
				fmt.Printf("  %s %s\n", tcol(c, dimCode, "│"), ln)
			}
		}
		fmt.Println()
		fmt.Printf("  Full log  %s\n", tcol(c, dimCode, "yaver logs"))

	case BuildStatusCancelled:
		fmt.Println()
		fmt.Println("  This build was cancelled before it finished.")
	}
}

// ── Human-friendly build output helpers ─────────────────────────────────────

const (
	dimCode   = "2"
	greenCode = "32"
	redCode   = "31"
	cyanCode  = "36"
	yellow    = "33"
)

// buildUseColor reports whether ANSI styling should be emitted: stdout is a
// TTY and the user has not opted out via NO_COLOR.
func buildUseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func tcol(enabled bool, code, s string) string {
	if !enabled || code == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// humanBuildState maps a build to (glyph, label, ANSI color).
func humanBuildState(b Build) (glyph, label, color string) {
	switch b.Status {
	case BuildStatusRunning:
		return "●", "Running", cyanCode
	case BuildStatusCompleted:
		return "✓", "Succeeded", greenCode
	case BuildStatusFailed:
		return "✗", "Failed", redCode
	case BuildStatusCancelled:
		return "⊘", "Cancelled", yellow
	default:
		return "•", string(b.Status), ""
	}
}

// friendlyPlatform turns a BuildPlatform into a readable description.
func friendlyPlatform(p BuildPlatform) string {
	switch p {
	case PlatformFlutterAPK, PlatformGradleAPK:
		return "Android APK  ·  " + string(p)
	case PlatformFlutterAAB, PlatformGradleAAB, PlatformRNAndroid, PlatformExpoAndroid:
		return "Android App Bundle (.aab)  ·  " + string(p)
	case PlatformFlutterIPA, PlatformXcodeIPA, PlatformRNIOS, PlatformExpoIOS:
		return "iOS app (.ipa)  ·  " + string(p)
	case PlatformXcodeBuild:
		return "Xcode build (no archive)  ·  " + string(p)
	case PlatformXcodeDeviceInstall:
		return "iOS device install  ·  " + string(p)
	case PlatformHermesBundlePush:
		return "Hermes bundle push  ·  " + string(p)
	case PlatformCustom:
		return "Custom build  ·  " + string(p)
	default:
		return string(p)
	}
}

// parseBuildTime parses the RFC3339 timestamps builds are stamped with.
func parseBuildTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func sinceTime(s string) (time.Duration, bool) {
	t, ok := parseBuildTime(s)
	if !ok {
		return 0, false
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	return d, true
}

func betweenTime(a, b string) (time.Duration, bool) {
	ta, ok := parseBuildTime(a)
	if !ok {
		return 0, false
	}
	tb, ok := parseBuildTime(b)
	if !ok {
		return 0, false
	}
	d := tb.Sub(ta)
	if d < 0 {
		d = 0
	}
	return d, true
}

// fmtBuildDur renders a duration as e.g. "6m 12s", "45s", "1h 03m".
func fmtBuildDur(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// pushHint suggests the upload command for a finished artifact.
func pushHint(b Build) string {
	lp := strings.ToLower(b.ArtifactPath)
	switch {
	case strings.HasSuffix(lp, ".ipa"):
		return "yaver build push testflight " + b.ID
	case strings.HasSuffix(lp, ".aab"):
		return "yaver build push playstore " + b.ID
	default:
		return ""
	}
}

// fetchBuildLogTail returns the last n non-empty lines of the build's exec
// output (stdout+stderr merged), best-effort. Returns nil if unavailable.
func fetchBuildLogTail(execID string, n int) []string {
	if execID == "" {
		return nil
	}
	resp, err := localAgentRequest("GET", "/exec/"+execID, nil)
	if err != nil {
		return nil
	}
	exec, ok := resp["exec"].(map[string]interface{})
	if !ok {
		return nil
	}
	var sb strings.Builder
	if s, ok := exec["stdout"].(string); ok {
		sb.WriteString(s)
	}
	if s, ok := exec["stderr"].(string); ok {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(s)
	}
	raw := strings.Split(strings.ReplaceAll(sb.String(), "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, ln := range raw {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if len(ln) > 200 {
			ln = ln[:197] + "..."
		}
		lines = append(lines, ln)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
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
