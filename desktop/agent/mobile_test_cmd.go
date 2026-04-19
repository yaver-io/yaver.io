package main

// mobile_test_cmd.go — `yaver mobile-test ...` passthrough to the
// headless mobile surrogate.
//
// The real work lives in the `yaver-mobile-headless` npm package
// (mobile-headless/ in this repo). We don't bundle a Node runtime
// inside the Go binary, so this command just execs the npm-installed
// binary. When it's missing, print a one-line install hint — the
// user almost always has `npm` since they're using Yaver's mobile
// workflow anyway.

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func runMobileTest(args []string) {
	bin, err := exec.LookPath("yaver-mobile-headless")
	if err != nil {
		// One-line hint, exit cleanly. No scary stack trace.
		fmt.Fprintln(os.Stderr, "yaver mobile-test requires yaver-mobile-headless, which is a separate Node package.")
		fmt.Fprintln(os.Stderr, "Install once with:")
		fmt.Fprintln(os.Stderr, "    npm install -g yaver-mobile-headless")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Then re-run `yaver mobile-test ...`. If you don't have npm:")
		fmt.Fprintln(os.Stderr, "    yaver install node")
		os.Exit(127)
	}

	// Replace the current process with the node binary. That way
	// stdin/stdout/stderr pass through transparently (the headless
	// tool emits JSONL, so callers can pipe to jq) and signal
	// handling matches a native CLI.
	argv := append([]string{bin}, args...)
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "exec yaver-mobile-headless failed:", err)
		os.Exit(1)
	}
}
