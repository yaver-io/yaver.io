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

	"github.com/yaver-io/agent/testkit"
)

type platformDeployPlan struct {
	Target     string                    `json:"target"`
	Script     string                    `json:"script"`
	Args       []string                  `json:"args"`
	Upload     bool                      `json:"upload"`
	Root       string                    `json:"root"`
	Validation *platformValidationConfig `json:"validation,omitempty"`
}

type platformValidationConfig struct {
	Driver          string `json:"driver,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Viewport        string `json:"viewport,omitempty"`
	MaxFlows        int    `json:"max_flows,omitempty"`
	MaxWallClockSec int    `json:"max_wall_clock_sec,omitempty"`
}

func platformDeployPlanFor(root, target string, upload bool, extra []string) (platformDeployPlan, error) {
	return platformDeployPlanForValidation(root, target, upload, extra, platformValidationConfig{})
}

func platformDeployPlanForValidation(root, target string, upload bool, extra []string, validation platformValidationConfig) (platformDeployPlan, error) {
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
	case "wear", "wear-os", "wearos", "android-wear", "android-watch":
		t = "wear-os"
		script = "scripts/deploy-wear-os.sh"
	case "watchos", "watch-os", "apple-watch", "applewatch":
		t = "watchos"
		script = "scripts/deploy-watchos.sh"
	case "ios", "testflight":
		t = "ios"
		script = "scripts/deploy-testflight.sh"
	case "carplay", "apple-car", "apple-carplay":
		t = "carplay"
		script = "scripts/deploy-carplay.sh"
	case "android", "android-auto", "auto", "playstore":
		t = "android"
		script = "scripts/deploy-playstore.sh"
	default:
		return platformDeployPlan{}, fmt.Errorf("unsupported platform deploy target %q (supported: tv, android-tv, tvos, wear-os, watchos, ios/testflight, carplay, android/android-auto/auto/playstore)", target)
	}
	if _, err := os.Stat(filepath.Join(root, script)); err != nil {
		return platformDeployPlan{}, fmt.Errorf("%s not found in %s", script, root)
	}
	args := append([]string{}, extra...)
	if upload {
		args = append(args, "--upload")
	}
	plan := platformDeployPlan{Target: t, Script: script, Args: args, Upload: upload, Root: root}
	if validation.Driver = normalizeReleaseValidationDriver(validation.Driver); validation.Driver != "" {
		if validation.Scope == "" {
			validation.Scope = validationScopeForDeployTarget(t)
		}
		if validation.Viewport == "" {
			validation.Viewport = validationViewportForDeployTarget(t)
		}
		if validation.MaxFlows < 0 {
			validation.MaxFlows = 0
		}
		if validation.MaxWallClockSec < 0 {
			validation.MaxWallClockSec = 0
		}
		plan.Validation = &validation
	}
	return plan, nil
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

func mcpMobilePlatformDeploy(directory, target string, upload, dryRun bool, timeoutSec int, validation platformValidationConfig) map[string]interface{} {
	root := normalizePlatformRoot(directory)

	plan, err := platformDeployPlanForValidation(root, target, upload, nil, validation)
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
	if plan.Validation != nil {
		validationResult, err := runPlatformReleaseValidation(ctx, root, *plan.Validation)
		if err != nil {
			return map[string]interface{}{"ok": false, "plan": plan, "validation": validationResult, "error": err.Error()}
		}
		if passed, _ := validationResult["passed"].(bool); !passed {
			return map[string]interface{}{"ok": false, "plan": plan, "validation": validationResult, "error": "release validation failed"}
		}
	}
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

func normalizeReleaseValidationDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "none", "off", "false", "skip":
		return ""
	case "cdp", "chrome", "chrome-cdp":
		return "cdp"
	case "selenium", "webdriver", "web-driver":
		return "selenium"
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

func validationScopeForDeployTarget(_ string) string {
	return "full"
}

func validationViewportForDeployTarget(target string) string {
	switch target {
	case "watchos", "wear-os":
		return "pixel7"
	case "tvos", "android-tv", "tv", "carplay":
		return "ipad11-landscape"
	case "android":
		return "pixel7"
	default:
		return "iphone15"
	}
}

func runPlatformReleaseValidation(ctx context.Context, root string, validation platformValidationConfig) (map[string]interface{}, error) {
	driver := normalizeReleaseValidationDriver(validation.Driver)
	if driver == "" {
		return map[string]interface{}{"skipped": true}, nil
	}
	req := testkit.AutoTestRequest{
		WorkDir:       root,
		Scope:         validation.Scope,
		Viewport:      validation.Viewport,
		Driver:        driver,
		Propose:       false,
		MaxFlows:      validation.MaxFlows,
		MaxWallClockS: validation.MaxWallClockSec,
		ACPowerOnly:   false,
	}
	res, err := testkit.RunAutoTest(ctx, req, nil)
	if res == nil {
		return map[string]interface{}{"driver": driver, "error": errorString(err)}, err
	}
	out := map[string]interface{}{
		"driver":         res.Driver,
		"scope":          res.Scope,
		"viewport":       res.Viewport,
		"passed":         res.Passed,
		"bugs_found":     res.BugsFound,
		"native_skipped": res.NativeSkipped,
		"results_dir":    res.ResultsDir,
		"run_id":         res.RunID,
	}
	if err != nil {
		out["error"] = err.Error()
		return out, err
	}
	return out, nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
