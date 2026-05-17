package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/yaver-io/agent/testkit"
)

func runAutotest(args []string) {
	if len(args) == 0 || args[0] == "start" || args[0] == "run" {
		if len(args) > 0 {
			args = args[1:]
		}
		runAutotestStart(args)
		return
	}
	switch args[0] {
	case "results":
		runAutotestResults(args[1:])
	case "suite":
		for _, vp := range testkit.AutoTestViewports {
			fmt.Printf("%s %dx%d dpr %.1f\n", vp.ID, vp.Width, vp.Height, vp.DPR)
		}
	default:
		fmt.Fprintln(os.Stderr, `Usage: yaver autotest [start|results|suite] [flags]

Runs Auto Test locally against yaver-tests specs. Default driver is Chrome
CDP; Selenium is opt-in via --driver selenium or .yaver/autotest.json.`)
		os.Exit(2)
	}
}

func runAutotestStart(args []string) {
	fs := flag.NewFlagSet("autotest start", flag.ExitOnError)
	workDir := fs.String("dir", ".", "project directory")
	scope := fs.String("scope", "full", "full | changed | screen:<name>")
	viewport := fs.String("viewport", "iphone15", "viewport preset")
	driver := fs.String("driver", "", "cdp | selenium (default: .yaver/autotest.json or cdp)")
	noStream := fs.Bool("no-stream", false, "do not request live stream metadata")
	propose := fs.Bool("propose", true, "record proposed fixes for failures")
	maxFlows := fs.Int("max-flows", 0, "maximum flows to run (0 = all)")
	acPowerOnly := fs.Bool("ac-power-only", false, "skip while on battery")
	fs.Parse(args)

	abs, err := filepath.Abs(*workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autotest: %v\n", err)
		os.Exit(2)
	}
	if *acPowerOnly {
		hs := testkit.SnapshotHost()
		if ok, why := testkit.ShouldRun(hs, true, 0); !ok {
			fmt.Fprintf(os.Stderr, "autotest skipped: %s\n", why)
			return
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	req := testkit.AutoTestRequest{
		WorkDir:     abs,
		Scope:       *scope,
		Viewport:    *viewport,
		Driver:      *driver,
		Stream:      !*noStream,
		Propose:     *propose,
		MaxFlows:    *maxFlows,
		ACPowerOnly: *acPowerOnly,
	}
	res, err := testkit.RunAutoTest(ctx, req, func(ev testkit.AutoTestEvent) {
		if ev.Flow != "" {
			fmt.Printf("[%s] %d/%d %s: %s\n", ev.Phase, ev.Progress, ev.Total, ev.Flow, ev.Message)
		} else {
			fmt.Printf("[%s] %s\n", ev.Phase, ev.Message)
		}
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "autotest: %v\n", err)
	}
	if res != nil {
		fmt.Printf("\nAuto Test %s: passed=%v bugs=%d proposed=%d nativeSkipped=%d\n",
			res.RunID, res.Passed, res.BugsFound, res.Proposed, res.NativeSkipped)
		fmt.Printf("Results: %s\n", res.ResultsDir)
		if !res.Passed {
			os.Exit(1)
		}
	}
	if err != nil {
		os.Exit(2)
	}
}

func runAutotestResults(args []string) {
	fs := flag.NewFlagSet("autotest results", flag.ExitOnError)
	workDir := fs.String("dir", ".", "project directory")
	runID := fs.String("run", "latest", "run id or latest")
	fs.Parse(args)
	abs, err := filepath.Abs(*workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autotest: %v\n", err)
		os.Exit(2)
	}
	path := filepath.Join(abs, ".yaver", "results", "runs")
	if *runID != "" && *runID != "latest" {
		path = filepath.Join(path, *runID, "results.md")
	} else {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "no autotest results found")
			os.Exit(1)
		}
		last := entries[len(entries)-1].Name()
		path = filepath.Join(path, last, "results.md")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "autotest: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(data))
}
