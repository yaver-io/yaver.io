package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	case "run":
		runTestSDK(args[1:])
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
  yaver test run [path] [flags]       Run yaver-test-sdk specs (yaver-tests/**/*.test.yaml)
  yaver test unit [--dir <path>]      Auto-detect and run unit tests
  yaver test flutter [--dir <path>]   Run Flutter tests
  yaver test android [--dir <path>]   Run Android tests (Gradle + emulator)
  yaver test ios [--dir <path>]       Run iOS tests (Xcode + simulator)
  yaver test e2e [--dir <path>]       Run E2E tests (Playwright/Cypress/Maestro)
  yaver test list                     List test sessions
  yaver test status <id>              Show test results

'yaver test run' is the embedded yaver-test-sdk runner — pure Go, no
external Playwright/Selenium needed. See yaver-tests/example.test.yaml
in the repo root for the spec format. Targets: web only today;
ios-sim/android-emu/device land in M5.
`)
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
