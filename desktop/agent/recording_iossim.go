package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// recording_iossim.go — record the iOS Simulator via
// `xcrun simctl io booted recordVideo <out.mp4>`. xcrun emits a
// QuickTime-compatible mp4 on SIGINT. Only usable on macOS with
// Xcode + at least one booted simulator.

type iosSimDriver struct{}

func (d *iosSimDriver) Name() string { return "xcrun-simctl" }

func (d *iosSimDriver) Available() (bool, string) {
	if runtime.GOOS != "darwin" {
		return false, "iOS simulator recording requires macOS"
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return false, "xcrun not found — install Xcode and run `xcode-select --install`"
	}
	// Check for a booted simulator. If none, we warn but still claim
	// not-available — the user has to boot one first.
	out, err := exec.Command("xcrun", "simctl", "list", "devices", "booted").Output()
	if err != nil {
		return false, "xcrun simctl list failed: " + err.Error()
	}
	if !strings.Contains(string(out), "(Booted)") {
		return false, "no iOS Simulator is booted — open Simulator.app or run `xcrun simctl boot <UDID>`"
	}
	return true, ""
}

func (d *iosSimDriver) Start(ctx context.Context, outPath, runID, taskID string) (driverState, error) {
	// `xcrun simctl io booted recordVideo` writes mp4 to outPath and
	// expects SIGINT to finalize. It refuses if a file already exists,
	// so we delete first (caller ensured the dir).
	_ = removeFileIfExists(outPath)

	cmd := exec.Command("xcrun", "simctl", "io", "booted", "recordVideo", "--codec=h264", outPath)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		return driverState{}, err
	}
	_ = ctx
	return driverState{Cmd: cmd, OutPath: outPath}, nil
}

func (d *iosSimDriver) Stop(state driverState) error {
	if state.Cmd == nil || state.Cmd.Process == nil {
		return fmt.Errorf("xcrun-simctl: no process to stop")
	}
	_ = state.Cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- state.Cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(8 * time.Second):
		_ = state.Cmd.Process.Kill()
		<-done
		return fmt.Errorf("xcrun-simctl: stop timed out")
	}
}
