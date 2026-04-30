package testkit

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// runCmd runs a command in `dir` with a 3s timeout and returns its
// stdout. Empty string on any error — best-effort, never fails the run.
func runCmd(dir string, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, resolveTestkitCommandPath(name), args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return out.String()
}
