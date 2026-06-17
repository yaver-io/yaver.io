package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type platformDeployPlan struct {
	Target string   `json:"target"`
	Script string   `json:"script"`
	Args   []string `json:"args"`
	Upload bool     `json:"upload"`
	Root   string   `json:"root"`
}

func platformDeployPlanFor(root, target string, upload bool, extra []string) (platformDeployPlan, error) {
	t := strings.ToLower(strings.TrimSpace(target))
	var script string
	switch t {
	case "tv", "television":
		t = "tv"
		script = "scripts/deploy-tv.sh"
	case "android-tv", "androidtv", "leanback", "google-tv", "googletv":
		t = "android-tv"
		script = "scripts/deploy-android-tv.sh"
	case "tvos", "apple-tv", "appletv":
		t = "tvos"
		script = "scripts/deploy-tvos.sh"
	default:
		return platformDeployPlan{}, fmt.Errorf("unsupported platform deploy target %q (supported: tv, android-tv, tvos)", target)
	}
	if _, err := os.Stat(filepath.Join(root, script)); err != nil {
		return platformDeployPlan{}, fmt.Errorf("%s not found in %s", script, root)
	}
	args := append([]string{}, extra...)
	if upload {
		args = append(args, "--upload")
	}
	return platformDeployPlan{Target: t, Script: script, Args: args, Upload: upload, Root: root}, nil
}

func runDeployPlatformCmd(target string, args []string) {
	fs := flag.NewFlagSet("deploy "+target, flag.ExitOnError)
	buildOnly := fs.Bool("build-only", false, "Build/verify only; do not upload")
	dryRun := fs.Bool("dry-run", false, "Print the script command without running it")
	skipTVOS := fs.Bool("skip-tvos", false, "For deploy tv: skip the tvOS stage")
	skipAndroidTV := fs.Bool("skip-android-tv", false, "For deploy tv: skip the Android TV stage")
	fs.Parse(args)

	root, _, err := findDeployRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy %s: %v\n", target, err)
		os.Exit(2)
	}

	var extra []string
	if *skipTVOS {
		extra = append(extra, "--skip-tvos")
	}
	if *skipAndroidTV {
		extra = append(extra, "--skip-android-tv")
	}
	plan, err := platformDeployPlanFor(root, target, !*buildOnly, extra)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deploy %s: %v\n", target, err)
		os.Exit(2)
	}

	cmdline := append([]string{plan.Script}, plan.Args...)
	fmt.Printf("yaver deploy %s\n", plan.Target)
	fmt.Printf("repo: %s\n", root)
	fmt.Printf("run:  bash %s\n", strings.Join(cmdline, " "))
	if *dryRun {
		return
	}

	cmd := exec.Command("bash", cmdline...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "deploy %s: %v\n", target, err)
		os.Exit(1)
	}
}

func mcpMobilePlatformDeploy(directory, target string, upload, dryRun bool, timeoutSec int) map[string]interface{} {
	root := normalizePlatformRoot(directory)

	plan, err := platformDeployPlanFor(root, target, upload, nil)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	if dryRun {
		return map[string]interface{}{"ok": true, "dry_run": true, "plan": plan}
	}
	if timeoutSec <= 0 {
		timeoutSec = 1800
	}
	if timeoutSec < 60 {
		timeoutSec = 60
	}
	if timeoutSec > 7200 {
		timeoutSec = 7200
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmdline := append([]string{plan.Script}, plan.Args...)
	cmd := exec.CommandContext(ctx, "bash", cmdline...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	text := out.String()
	if len(text) > 12000 {
		text = text[len(text)-12000:]
	}
	if ctx.Err() == context.DeadlineExceeded {
		return map[string]interface{}{"ok": false, "timed_out": true, "plan": plan, "output_tail": text}
	}
	if err != nil {
		exitCode := 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		return map[string]interface{}{"ok": false, "exit_code": exitCode, "plan": plan, "output_tail": text}
	}
	return map[string]interface{}{"ok": true, "exit_code": 0, "plan": plan, "output_tail": text}
}
