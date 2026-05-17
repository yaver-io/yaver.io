//go:build windows

package main

// runner_detach_windows.go — Windows stub. The detach mechanism
// uses POSIX setsid + signal(0) liveness which don't exist on
// Windows. Until a CreateProcess(DETACHED_PROCESS) port lands,
// detached subcommands on Windows run in the foreground and are
// tied to the controlling console.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const autodevDetachEnv = "YAVER_AUTODEV_DETACHED"

func autodevDetachActive() bool {
	return os.Getenv(autodevDetachEnv) == "1"
}

// spawnDetachedAutodev is a no-op on Windows: returns ("","") so
// runAutodevOrTest falls through to its in-foreground branch.
func spawnDetachedAutodev(_ string, _ []string, _ string) (int, string) {
	fmt.Fprintln(os.Stderr, "[autodev] detach not supported on Windows yet — running in foreground")
	return 0, ""
}

// tailDetachedAutodev / readAutodevPID / autodevPIDFile / safe-name
// helpers are also exposed so the rest of the codebase compiles.
// Their values are inert under the no-op detach path.

func autodevPIDFile(loopName string) string {
	tmp := os.Getenv("TEMP")
	if tmp == "" {
		tmp = os.Getenv("TMP")
	}
	if tmp == "" {
		tmp = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "Temp")
	}
	return filepath.Join(tmp, "yaver-autodev-"+safeFileSegment(loopName)+".pid")
}

func safeFileSegment(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_").Replace(s)
}

func safeStreamName(s string) string {
	return strings.ReplaceAll(s, ":", "_")
}

func readAutodevPID(_ string) (int, bool) {
	return 0, false
}

func tailDetachedAutodev(streamName string) {
	tailStream(streamName)
}

func findAutodevForkLogPath(_ string) string {
	return ""
}
