package main

// `yaver update` — manual self-update from the latest GitHub release.
//
// Wraps the existing checkAutoUpdate machinery (which only runs when
// the user has opted in via cfg.AutoUpdate). Runs unconditionally
// when invoked from the CLI so the dev can pull the latest binary
// without waiting for the next agent boot.

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"time"
)

func runManualUpdate() {
	if handled, err := runManualUpdateViaNPM(); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "npm upgrade failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Force the update path by faking a config with AutoUpdate=true.
	// We deliberately don't persist anything: this run only.
	cfg := &Config{AutoUpdate: true}

	fmt.Println("yaver update")
	fmt.Println("============")
	fmt.Println()

	checkAutoUpdate(cfg)

	fmt.Println()
	fmt.Println("Tip: enable auto-updates so the agent does this on every boot:")
	fmt.Println("  yaver auto-update enable")

	// `checkAutoUpdate` returns silently when there's nothing to do;
	// the user sees the [auto-update] log lines either way.
	os.Exit(0)
}

func runManualUpdateViaNPM() (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_INSTALL_SOURCE")), "npm") {
		return false, nil
	}

	pkg := strings.TrimSpace(os.Getenv("YAVER_NPM_PACKAGE"))
	if pkg == "" {
		pkg = "yaver-cli"
	}
	npm, err := osexec.LookPath("npm")
	if err != nil {
		return true, fmt.Errorf("npm not found in PATH")
	}

	fmt.Println("yaver update")
	fmt.Println("============")
	fmt.Println()
	fmt.Printf("Detected npm-managed install. Running: npm install -g %s@latest\n", pkg)
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := osexec.CommandContext(ctx, npm, "install", "-g", pkg+"@latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return true, err
	}

	fmt.Println()
	fmt.Println("npm upgrade complete.")
	fmt.Println("Tip: `yaver --version` prints the active agent version after the wrapper refreshes.")
	return true, nil
}
