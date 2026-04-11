package main

// `yaver update` — manual self-update from the latest GitHub release.
//
// Wraps the existing checkAutoUpdate machinery (which only runs when
// the user has opted in via cfg.AutoUpdate). Runs unconditionally
// when invoked from the CLI so the dev can pull the latest binary
// without waiting for the next agent boot.

import (
	"fmt"
	"os"
)

func runManualUpdate() {
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
