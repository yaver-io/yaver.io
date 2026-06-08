package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// runQA dispatches `yaver qa run` — the app-test agent as a foreground CLI
// command (mirrors `yaver studio base`), so it runs without the daemon/auth: it
// builds the surface, drives the in-repo flows with the LLM brain + oracle bank,
// and prints the report card. The ops verbs (qa_run/qa_report) wrap the same
// path for the mobile/web UI.
func runQA(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, `yaver qa — agentic app-test agent

  run --package <pkg> [flags]   Drive yaver-tests/flows on a redroid surface and
                                report bugs (red box / crash / ANR / blank).

Flags:
  --package PKG        app package to launch (required)
  --apk PATH           install this APK first (optional)
  --flows-dir DIR      *.flow.yaml dir (default ./yaver-tests/flows)
  --mode catch|fix     default catch
  --base VERSION       restore a warm Yaver Base Image instead of cold boot
  --container NAME     redroid container (default yaver-qa)
  --host-workdir DIR   /data mount on the surface host (ssh: required)
  --ssh-host U@H       run on an on-prem host (default: local)
  --json               print the full report as JSON`)
		os.Exit(2)
	}
	if args[0] != "run" {
		fmt.Fprintf(os.Stderr, "unknown qa subcommand %q (try: run)\n", args[0])
		os.Exit(2)
	}

	fs := flag.NewFlagSet("qa run", flag.ExitOnError)
	pkg := fs.String("package", "", "app package to launch")
	apk := fs.String("apk", "", "APK to install first")
	flowsDir := fs.String("flows-dir", "", "flows dir (default ./yaver-tests/flows)")
	mode := fs.String("mode", "catch", "catch|fix")
	base := fs.String("base", "", "warm Yaver Base Image version")
	container := fs.String("container", "yaver-qa", "redroid container name")
	hostWorkDir := fs.String("host-workdir", "", "/data mount on the surface host")
	sshHost := fs.String("ssh-host", "", "on-prem host (default local)")
	asJSON := fs.Bool("json", false, "print full report as JSON")
	fs.Parse(args[1:])

	if strings.TrimSpace(*pkg) == "" && strings.TrimSpace(*apk) == "" {
		fmt.Fprintln(os.Stderr, "Pass --package (and optionally --apk).")
		os.Exit(2)
	}

	dir := strings.TrimSpace(*flowsDir)
	if dir == "" {
		cwd, _ := os.Getwd()
		dir = cwd + "/yaver-tests/flows"
	}
	flows, err := loadFlows(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load flows: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "→ %d flow(s) from %s\n", len(flows), dir)

	req := qaRunRequest{
		APK: *apk, Package: *pkg, FlowsDir: dir, Mode: *mode, Base: *base,
		Container: *container, HostWorkDir: *hostWorkDir, SSHHost: *sshHost,
	}
	resolved, err := qaConfigFromRequest(context.Background(), req, flows,
		func(l string) { fmt.Fprintf(os.Stderr, "  %s\n", l) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}

	report, err := runQAFlows(context.Background(), resolved.flowCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qa run: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return
	}

	fmt.Printf("\n=== app-test report (%s) ===\n", report.Mode)
	fmt.Printf("flows: %d   caught: %d   fixed: %d   %s\n",
		len(report.Flows), report.Caught, report.Fixed,
		map[bool]string{true: "PASS", false: "BUGS"}[report.Passed])
	for _, f := range report.Flows {
		fmt.Printf("  • %s — %d step(s), %d bug(s)\n", f.Name, f.Steps, f.Bugs)
	}
	for _, b := range report.Bugs {
		oc := b.Outcome
		if oc == "" {
			oc = "caught"
		}
		fmt.Printf("  [%s] %-8s %s — %s (%s)\n", strings.ToUpper(oc), b.Severity, b.Title, b.Oracle, b.Detail)
	}
}
