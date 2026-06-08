//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestCIProcessGroupReaping proves the gap-C teardown: a CI job that spawns a
// background process (orphaned when the runner exits) is SIGKILLed by the
// process-group teardown rather than left running on the box.
func TestCIProcessGroupReaping(t *testing.T) {
	store := NewRunnerStore(10)
	run := store.Start(RunnerRun{JobName: "pgtest"})
	pidFile := filepath.Join(t.TempDir(), "child.pid")

	// sh launches a long-lived sleep in the background (same process group),
	// records its pid, then exits — leaving the sleep as an orphan that the
	// pgroup teardown must reap.
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $! > "+pidFile+"; sleep 0.2")
	code, err := streamCmdToRun(store, run.ID, cmd)
	store.Finish(run.ID, code, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 0 {
		t.Fatalf("no child pid recorded (file=%q)", string(raw))
	}

	// Give the SIGKILL a moment to land + the OS to reap.
	time.Sleep(400 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL) // cleanup if the test fails
		t.Fatalf("orphaned child %d survived process-group teardown", pid)
	}
}
