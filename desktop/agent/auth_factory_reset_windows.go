//go:build windows

package main

import "os/exec"

// applyDetachSysProcAttr is a no-op on Windows — setsid equivalent
// (CREATE_NEW_PROCESS_GROUP) is not needed for the auth factory-reset
// flow and the SysProcAttr fields differ enough that we keep it out
// of the platform-independent path.
func applyDetachSysProcAttr(cmd *exec.Cmd) {}
