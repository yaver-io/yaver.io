package main

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// osexecLookPath is a tiny indirection so test_cmd.go can call into
// exec.LookPath without re-importing os/exec at every helper. Returns
// (path, err) — empty path on miss.
func osexecLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// runShell runs a command in `dir` with a 3s timeout and returns its
// stdout. Used by gitSHA / gitBranch in test_cmd.go for best-effort
// history enrichment.
func runShell(dir string, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}
