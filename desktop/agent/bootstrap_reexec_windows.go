//go:build windows

package main

// reexecReplaceProcess on Windows can't truly replace the process
// image (no syscall.Exec equivalent). Windows doesn't run under
// systemd anyway, so the previous fork-and-exit behavior is fine
// here. Spawn `yaver serve` as a detached child and exit cleanly;
// the user's launcher (NSSM, Service Control Manager, or just a
// terminal) will pick it back up via Restart= equivalents.

import (
	"fmt"
	"os"
	osexec "os/exec"
)

func reexecReplaceProcess(execPath string, args []string) error {
	cmd := osexec.Command(execPath, args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("relaunch serve: %w", err)
	}
	os.Exit(0)
	return nil
}
