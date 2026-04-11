package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/yaver-io/agent/testkit"
)

// gitSHA / gitBranch are tiny shell-outs used to enrich history
// entries. Defined here (rather than in testkit) so the testkit
// package stays free of git assumptions and can be unit-tested in any
// directory.
func gitSHA(dir string) string {
	out, err := osexecLookPath("git")
	if err != nil || out == "" {
		return ""
	}
	return strings.TrimSpace(runShell(dir, "git", "rev-parse", "HEAD"))
}

func gitBranch(dir string) string {
	out, err := osexecLookPath("git")
	if err != nil || out == "" {
		return ""
	}
	return strings.TrimSpace(runShell(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"))
}

// osexecLookPath is split out so we don't need to repeat the os/exec
// import for one call. Defined in test_cmd_helpers.go.

func runTest(args []string) {
	if len(args) == 0 {
		printTestUsage()
		os.Exit(0)
	}

	switch args[0] {
	case "init":
		runTestInit(args[1:])
	case "run":
		runTestSDK(args[1:])
	case "record":
		runTestRecord(args[1:])
	case "debug":
		runTestDebug(args[1:])
	case "history":
		runTestHistory(args[1:])
	case "flake":
		runTestFlake(args[1:])
	case "sync":
		runTestSync(args[1:])
	case "report":
		runTestReport(args[1:])
	case "schedule":
		runTestSchedule(args[1:])
	case "unit":
		runTestUnit(args[1:])
	case "flutter":
		runTestFramework("flutter_test", "flutter test --reporter compact", args[1:])
	case "android":
		runTestAndroid(args[1:])
	case "ios":
		runTestIOS(args[1:])
	case "e2e":
		runTestE2E(args[1:])
	case "list", "ls":
		runTestList()
	case "status":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: yaver test status <id>")
			os.Exit(1)
		}
		runTestStatus(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown test subcommand: %s\n\n", args[0])
		printTestUsage()
		os.Exit(1)
	}
}

func printTestUsage() {
	fmt.Print(`Usage:
  yaver test init [flags]             Scaffold yaver-tests/ with example specs
  yaver test run [path] [flags]       Run yaver-test-sdk specs (yaver-tests/**/*.test.yaml)
  yaver test record [flags]           Open a browser, record clicks/inputs, write a YAML spec
  yaver test history [path]           Show recent runs from local .history.jsonl
  yaver test flake [path]             Per-spec failure ratios from local history
  yaver test sync [flags]             Local pass markers (for GH Actions short-circuit)
  yaver test report [path] [-o out]   Render latest run history as a standalone HTML file
  yaver test schedule <cron> [root]   Register a cron entry with the agent scheduler
  yaver test unit [--dir <path>]      Auto-detect and run unit tests (legacy spawn)
  yaver test flutter [--dir <path>]   Run Flutter tests (legacy spawn)
  yaver test android [--dir <path>]   Run Android tests (legacy spawn)
  yaver test ios [--dir <path>]       Run iOS tests (legacy spawn)
  yaver test e2e [--dir <path>]       Run E2E tests (legacy spawn)
  yaver test list                     List test sessions
  yaver test status <id>              Show test results

'yaver test run' is the embedded yaver-test-sdk runner — pure Go, no
external Playwright/Selenium needed. Targets supported today:
  - web           (via embedded Chromium, chromedp / CDP)
  - web           (via geckodriver for firefox browser)
  - android-emu   (via emulator + adb + uiautomator selectors)
  - android device (via USB + adb + uiautomator selectors)
  - ios-sim       (via simctl — boot/install/launch/screenshot, taps via coords)
  - ios device    (via libimobiledevice — install/launch/screenshot)

Run 'yaver install list' to see which integrations are installed on
this machine, or 'yaver test init' to drop example specs into a new
project.
`)
}

func runTestRecord(args []string) {
	fs := flag.NewFlagSet("test record", flag.ExitOnError)
	url := fs.String("url", "", "URL to open in the browser")
	name := fs.String("name", "recorded", "spec name (also drives the YAML filename)")
	out := fs.String("out", "", "output path (default: yaver-tests/<name>.test.yaml)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: yaver test record --url <url> [flags]

Opens a Chrome window pointed at --url, records every click and form
input you make, and writes a yaver-test-sdk YAML spec to disk when
you close the browser. Edit the resulting file to tighten selectors
or add assertions.

Flags:`)
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if *url == "" {
		fs.Usage()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Fprintln(os.Stderr, "▶ Recording — perform the flow you want to test, then close the browser to save.")
	spec, err := testkit.Record(ctx, testkit.RecordOptions{
		Name:    *name,
		URL:     *url,
		OutPath: *out,
	})
	outPath := *out
	if outPath == "" {
		outPath = "yaver-tests/" + *name + ".test.yaml"
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "record error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, testkit.FormatRecordSummary(spec, outPath))
}

func runTestHistory(args []string) {
	root := "yaver-tests"
	if len(args) > 0 {
		root = args[0]
	}
	abs, _ := filepath.Abs(root)
	hist := &testkit.History{Path: testkit.HistoryPathFor(abs)}
	entries, err := hist.Tail(20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("No runs in history. Run `yaver test run` first.")
		return
	}
	for _, e := range entries {
		mark := "✓"
		if e.Failed > 0 {
			mark = "✗"
		}
		ts := e.StartedAt.Format("2006-01-02 15:04:05")
		branch := ""
		if e.GitBranch != "" {
			branch = " · " + e.GitBranch
		}
		flaky := ""
		if e.FlakyCount > 0 {
			flaky = fmt.Sprintf(" · %d flaky", e.FlakyCount)
		}
		fmt.Printf("%s %s  %d/%d passed (%dms)%s%s\n",
			mark, ts, e.Passed, e.Total, e.DurationMS, branch, flaky)
		for _, sp := range e.Specs {
			if !sp.Passed {
				fmt.Printf("    ✗ %s: %s\n", sp.Name, sp.Error)
			}
		}
	}
}

// runTestDebug is a thin wrapper around tcpdump + optional axe-core
// install. Off by default; dev runs it on demand when CDP-based
// network capture doesn't answer the question they have.
func runTestDebug(args []string) {
	if len(args) == 0 {
		fmt.Println(`Usage:
  yaver test debug --capture-packets --iface <if> --duration <N> --out <file>
                   Shell out to tcpdump for N seconds, write pcap to <file>.
                   Needs sudo; open the pcap in Wireshark afterwards.
                   Defaults: iface=en0 (macOS) / any (linux), duration=30s.

  yaver test debug --install-axe
                   Download the axe-core bundle to ~/.yaver/axe-core.js so the
                   a11y: step works without a stub.`)
		return
	}
	fs := flag.NewFlagSet("test debug", flag.ExitOnError)
	capturePackets := fs.Bool("capture-packets", false, "run tcpdump for --duration")
	installAxe := fs.Bool("install-axe", false, "download axe-core to ~/.yaver/axe-core.js")
	iface := fs.String("iface", "", "network interface (default: en0 on macOS, any on linux)")
	filter := fs.String("filter", "", "tcpdump BPF filter")
	durationStr := fs.String("duration", "30s", "capture duration (e.g. 30s, 2m)")
	out := fs.String("out", "", "output pcap path")
	fs.Parse(args)

	switch {
	case *installAxe:
		runInstallAxe()
	case *capturePackets:
		dur, err := time.ParseDuration(*durationStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad --duration: %v\n", err)
			os.Exit(2)
		}
		if *out == "" {
			fmt.Fprintln(os.Stderr, "error: --out <file> is required for --capture-packets")
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "▶ tcpdump starting (%s, %s) — Ctrl+C to stop\n", *iface, dur)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err = testkit.CapturePackets(ctx, testkit.CapturePacketsOptions{
			Interface: *iface,
			Filter:    *filter,
			Duration:  dur,
			OutPath:   *out,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "✓ wrote %s — open in Wireshark or `tcpdump -r %s`\n", *out, *out)
	default:
		fmt.Fprintln(os.Stderr, "nothing to do — pass --capture-packets or --install-axe")
		os.Exit(2)
	}
}

// runInstallAxe pulls the official axe-core bundle from jsdelivr to
// ~/.yaver/axe-core.js. Run once per machine; the file is versioned
// so the dev can bump by deleting and re-running.
func runInstallAxe() {
	home, _ := os.UserHomeDir()
	if home == "" {
		fmt.Fprintln(os.Stderr, "error: cannot resolve home dir")
		os.Exit(1)
	}
	dir := filepath.Join(home, ".yaver")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "axe-core.js")
	fmt.Printf("=> downloading axe-core.js to %s\n", path)
	cmd := exec.Command("curl", "-fsSL", "-o", path,
		"https://cdn.jsdelivr.net/npm/axe-core@4/axe.min.js")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "curl: %v — retry later or set YAVER_AXE_CORE_PATH\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ axe-core installed")
}

// runTestInit drops a starter `yaver-tests/` directory into the
// user's project with a README, a gitignore, and one or two example
// specs. Idempotent — re-running it doesn't clobber anything.
func runTestInit(args []string) {
	fs := flag.NewFlagSet("test init", flag.ExitOnError)
	dir := fs.String("dir", ".", "project directory")
	webURL := fs.String("url", "", "local web dev server URL (default http://127.0.0.1:3000)")
	flavor := fs.String("flavor", "both", "which examples to emit: web | rn | both")
	fs.Parse(args)

	files, err := testkit.Scaffold(testkit.ScaffoldOptions{
		Dir:    *dir,
		WebURL: *webURL,
		Flavor: *flavor,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(testkit.ScaffoldSummary(files))
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  cd " + *dir)
	fmt.Println("  yaver test run                # run everything")
	fmt.Println("  yaver test run --watch        # vibe-coding loop")
	fmt.Println("  yaver test record --url http://127.0.0.1:3000  # record a new spec")
}

// runTestSync prints local pass markers and (when --check <sha> is
// passed) exits non-zero if the given SHA hasn't been verified locally
// yet. Designed to be called from a GH Actions step that wants to
// skip a redundant cloud run.
func runTestSync(args []string) {
	fs := flag.NewFlagSet("test sync", flag.ExitOnError)
	check := fs.String("check", "", "exit 0 if this SHA has a local pass marker, 1 otherwise")
	root := fs.String("root", "yaver-tests", "spec root directory")
	fs.Parse(args)
	abs, _ := filepath.Abs(*root)

	if *check != "" {
		short := *check
		if len(short) > 7 {
			short = short[:7]
		}
		if testkit.HasPassMarker(abs, *check) {
			fmt.Printf("✓ %s already passed locally\n", short)
			os.Exit(0)
		}
		fmt.Printf("✗ %s has no local pass marker — run yaver test run\n", short)
		os.Exit(1)
	}

	markers, err := testkit.LatestPassMarkers(abs, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(markers) == 0 {
		fmt.Println("No pass markers yet. Run `yaver test run` first.")
		return
	}
	for _, m := range markers {
		fmt.Println(testkit.FormatMarker(m))
	}
}

// runTestSchedule registers a cron expression with the agent so the
// embedded runner fires automatically. Reuses the agent's existing
// /schedules infrastructure (the same one cron jobs already use for
// AI tasks). Solo dev never has to remember to run tests.
func runTestSchedule(args []string) {
	if len(args) < 2 || args[0] == "list" {
		runTestScheduleList(args)
		return
	}
	cron := args[0]
	specRoot := "yaver-tests"
	if len(args) >= 2 && args[1] != "" {
		specRoot = args[1]
	}

	// POST /schedules to the local agent — registers a "yaver test
	// run" entry that the agent's existing scheduler will fire.
	body := map[string]interface{}{
		"name":      "yaver-test-sdk",
		"cron":      cron,
		"command":   "yaver test run " + specRoot,
		"work_dir":  ".",
		"on_failure_notify": true,
		"hardware_aware":    true,
	}
	resp, err := localAgentRequest("POST", "/schedules", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\nIs the agent running? Start with 'yaver serve'.\n", err)
		os.Exit(1)
	}
	fmt.Printf("Scheduled: %v\n", resp["id"])
}

func runTestScheduleList(args []string) {
	resp, err := localAgentRequest("GET", "/schedules", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	enc, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(enc))
}

func runTestFlake(args []string) {
	root := "yaver-tests"
	if len(args) > 0 {
		root = args[0]
	}
	abs, _ := filepath.Abs(root)
	hist := &testkit.History{Path: testkit.HistoryPathFor(abs)}
	stats, err := hist.FlakeReport(100)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(stats) == 0 {
		fmt.Println("No history yet. Run `yaver test run` a few times to populate.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SPEC\tTOTAL\tPASSED\tFAILED\tFLAKY\tFAIL %")
	for _, st := range stats {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%.1f%%\n",
			st.Name, st.Total, st.Passed, st.Failed, st.Flaky, st.FailureRatio()*100)
	}
	w.Flush()
}

// runTestSDK is the entry point for `yaver test run` — the embedded
// yaver-test-sdk runner. Walks `yaver-tests/` (or the path the user
// passed) for *.test.yaml files and executes them with the chromedp
// driver. Output: pretty TTY by default; --json or --junit for CI.
func runTestSDK(args []string) {
	fs := flag.NewFlagSet("test run", flag.ExitOnError)
	jsonOut := fs.String("json", "", "write JSON results to this file")
	junitOut := fs.String("junit", "", "write JUnit XML results to this file")
	headful := fs.Bool("headful", false, "run the browser visibly (overrides spec.headful)")
	verbose := fs.Bool("verbose", false, "log every step to stderr while running")
	artifactDir := fs.String("artifacts", "", "directory for screenshots/traces (default: <spec dir>/.yaver-test-results)")
	updateSnaps := fs.Bool("update-snapshots", false, "update visual snapshot baselines instead of diffing")
	watch := fs.Bool("watch", false, "watch the spec directory and re-run on file change (vibe-coding loop)")
	requireAC := fs.Bool("ac-power-only", false, "skip runs while on battery (saves laptop juice)")
	maxLoad := fs.Float64("max-load", 0, "skip runs when 1-min load average exceeds this (0 = no limit)")
	concurrency := fs.Int("concurrency", 1, "number of specs to run in parallel (each spawns its own Chromium)")
	retries := fs.Int("retries", 0, "re-run a failing spec up to N times before declaring it failed (flake guard)")
	noHistory := fs.Bool("no-history", false, "don't append the run to <spec dir>/.history.jsonl")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage: yaver test run [path] [flags]

Runs every *.test.yaml under <path> (default: ./yaver-tests). Each
spec is executed against an embedded Chromium via CDP — no external
Playwright or Selenium installation required. Failure artifacts are
written next to the specs by default.

Flags:`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	root := "yaver-tests"
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: %s does not exist. Create %s/example.test.yaml to get started.\n", root, root)
		os.Exit(2)
	}

	specs, err := testkit.DiscoverSpecs(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	if len(specs) == 0 {
		fmt.Fprintf(os.Stderr, "no *.test.yaml files under %s — nothing to run\n", abs)
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Install the interactive self-heal handler so a failing step
	// that SelectorReplaceFromSelfHeal can't rescue gets one shot at
	// a claude-backed fix. No-op when claude isn't on PATH.
	registerInteractiveFixHandler()

	snapCfg := testkit.DefaultSnapshotConfig()
	if *updateSnaps {
		snapCfg.Mode = testkit.SnapshotModeUpdate
	}

	opts := testkit.RunOptions{
		ArtifactsDir: *artifactDir,
		Headful:      *headful,
		VerboseLog:   *verbose,
		Snapshot:     snapCfg,
		FlakeRetries: *retries,
	}

	// Hardware-aware throttling: skip the run if the dev is on battery
	// or the load average is too high. Solo dev never wants their
	// laptop drained by background tests.
	if *requireAC || *maxLoad > 0 {
		hs := testkit.SnapshotHost()
		if ok, why := testkit.ShouldRun(hs, *requireAC, *maxLoad); !ok {
			fmt.Fprintf(os.Stderr, "skipping run: %s\n", why)
			os.Exit(0)
		}
	}

	if *watch {
		fmt.Fprintf(os.Stderr, "watching %s — Ctrl+C to stop\n", abs)
		err := testkit.Watch(ctx, abs, opts, func(r *testkit.Result) {
			ts := testkit.FormatTimestamp(time.Now())
			if r.Passed {
				fmt.Fprintf(os.Stderr, "[%s] ✓ %s (%s)\n", ts, r.Spec.Name, r.Duration().Round(time.Millisecond))
			} else {
				fmt.Fprintf(os.Stderr, "[%s] ✗ %s\n", ts, r.Spec.Name)
				if r.Err != nil {
					fmt.Fprintf(os.Stderr, "        %s\n", r.Err.Error())
				}
				for _, st := range r.Steps {
					if st.Err != nil {
						fmt.Fprintf(os.Stderr, "        [%s %d] %s — %s\n", st.Phase, st.Index, st.Description, st.Err.Error())
					}
				}
			}
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)
			os.Exit(2)
		}
		return
	}

	suite := testkit.RunSuite(ctx, specs, opts, *concurrency)

	// Append to local history (JSONL) so the dev — and the mobile app
	// over P2P — can browse past runs without any cloud round trip.
	if !*noHistory {
		hist := &testkit.History{Path: testkit.HistoryPathFor(abs)}
		_ = hist.AppendSuite(suite, gitSHA(abs), gitBranch(abs), runtime.GOOS)
	}

	// Write a pass marker if everything succeeded. Lets a future
	// `yaver test sync` (or a GH Actions step) skip a redundant
	// cloud run when this SHA already passed locally.
	if suite.Passed() {
		_ = testkit.WritePassMarker(abs, gitSHA(abs), gitBranch(abs), runtime.GOOS,
			len(suite.Results), suite.FinishedAt.Sub(suite.StartedAt))
	} else {
		// Publish failure-only notifications into the local stream so
		// the mobile app sees them on next poll. The webhook (if
		// YAVER_TEST_NOTIFY_URL is set) fires inside Append.
		nc := testkit.NewNotificationCenter(testkit.NotificationsPathFor(abs), 100)
		testkit.PublishSuiteFailures(nc, suite, gitSHA(abs), gitBranch(abs))
	}

	suite.WriteTTY(os.Stdout)
	if *jsonOut != "" {
		f, err := os.Create(*jsonOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: write json: %v\n", err)
			os.Exit(3)
		}
		defer f.Close()
		if err := suite.WriteJSON(f); err != nil {
			fmt.Fprintf(os.Stderr, "error: write json: %v\n", err)
			os.Exit(3)
		}
	}
	if *junitOut != "" {
		f, err := os.Create(*junitOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: write junit: %v\n", err)
			os.Exit(3)
		}
		defer f.Close()
		if err := suite.WriteJUnit(f); err != nil {
			fmt.Fprintf(os.Stderr, "error: write junit: %v\n", err)
			os.Exit(3)
		}
	}

	if !suite.Passed() {
		os.Exit(1)
	}
}

func startTestViaAgent(framework, command, workDir, testType string) {
	body := map[string]interface{}{
		"framework": framework,
		"command":   command,
		"workDir":   workDir,
		"testType":  testType,
	}
	resp, err := localAgentRequest("POST", "/tests", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Is the agent running? Start with 'yaver serve'.")
		os.Exit(1)
	}

	var ts TestSession
	remarshal(resp, &ts)
	fmt.Printf("Test started: %s (%s)\n", ts.ID, ts.Framework)
	fmt.Printf("  Command: %s\n", ts.Command)
	fmt.Printf("  Type: %s\n", ts.TestType)
	fmt.Println()
	fmt.Printf("  yaver test status %s\n", ts.ID)
}

func runTestUnit(args []string) {
	fs := flag.NewFlagSet("test unit", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args)
	startTestViaAgent("", "", *dir, "unit")
}

func runTestFramework(framework, command string, args []string) {
	fs := flag.NewFlagSet("test "+framework, flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args)
	startTestViaAgent(framework, command, *dir, "unit")
}

func runTestAndroid(args []string) {
	fs := flag.NewFlagSet("test android", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args)
	startTestViaAgent("espresso", "", *dir, "unit")
}

func runTestIOS(args []string) {
	fs := flag.NewFlagSet("test ios", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args)
	startTestViaAgent("xctest", "", *dir, "unit")
}

func runTestE2E(args []string) {
	fs := flag.NewFlagSet("test e2e", flag.ExitOnError)
	dir := fs.String("dir", "", "Project directory")
	fs.Parse(args)
	// Auto-detect e2e framework
	startTestViaAgent("", "", *dir, "e2e")
}

func runTestList() {
	resp, err := localAgentRequest("GET", "/tests", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var sessions []TestSession
	remarshal(resp, &sessions)

	if len(sessions) == 0 {
		fmt.Println("No test sessions. Run 'yaver test unit' to start.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tFRAMEWORK\tSTATUS\tPASSED\tFAILED")
	for _, s := range sessions {
		passed, failed := 0, 0
		if s.Results != nil {
			passed = s.Results.Passed
			failed = s.Results.Failed
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n", s.ID, s.Framework, s.Status, passed, failed)
	}
	w.Flush()
}

func runTestStatus(id string) {
	resp, err := localAgentRequest("GET", "/tests/"+id, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var ts TestSession
	remarshal(resp, &ts)

	fmt.Printf("Test %s\n", ts.ID)
	fmt.Printf("  Framework: %s\n", ts.Framework)
	fmt.Printf("  Type:      %s\n", ts.TestType)
	fmt.Printf("  Status:    %s\n", ts.Status)
	fmt.Printf("  Command:   %s\n", ts.Command)
	if ts.Results != nil {
		fmt.Printf("  Results:   %d passed, %d failed, %d skipped (%d total)\n",
			ts.Results.Passed, ts.Results.Failed, ts.Results.Skipped, ts.Results.Total)
		if len(ts.Results.Failures) > 0 {
			fmt.Println("  Failures:")
			for _, f := range ts.Results.Failures {
				fmt.Printf("    - %s\n", f.Name)
			}
		}
	}
}
