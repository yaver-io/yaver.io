package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// recording_androidemu.go — record the Android emulator (or a
// usb-attached device) via `adb shell screenrecord`. Captures to a
// temp file on the device, then adb-pulls it back to outPath when
// Stop is called. Limitation: Android's screenrecord caps at 3min;
// we warn in Available when no device is connected.

type androidEmuDriver struct{}

func (d *androidEmuDriver) Name() string { return "adb-screenrecord" }

func (d *androidEmuDriver) Available() (bool, string) {
	if _, err := exec.LookPath("adb"); err != nil {
		return false, "adb not found — install Android platform-tools"
	}
	out, err := exec.Command("adb", "devices").Output()
	if err != nil {
		return false, "adb devices failed: " + err.Error()
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, l := range lines[1:] { // skip header
		fields := strings.Fields(l)
		if len(fields) == 2 && fields[1] == "device" {
			return true, ""
		}
	}
	return false, "no Android device/emulator attached — start an emulator or plug in a device"
}

func (d *androidEmuDriver) Start(ctx context.Context, outPath, runID, taskID string) (driverState, error) {
	// Pick the first attached device so concurrent devices don't
	// interfere. Falls back to bare `adb` which uses the default.
	serial := firstAdbDevice()
	tmpPath := fmt.Sprintf("/sdcard/yaver-%s-%s.mp4", runID, taskID)

	args := []string{}
	if serial != "" {
		args = append(args, "-s", serial)
	}
	args = append(args, "shell", "screenrecord", "--bit-rate", "2000000", tmpPath)
	cmd := exec.Command("adb", args...)
	setProcGroup(cmd)
	if err := cmd.Start(); err != nil {
		return driverState{}, err
	}
	_ = ctx
	return driverState{Cmd: cmd, OutPath: outPath, TmpPath: tmpPath, Device: serial}, nil
}

func (d *androidEmuDriver) Stop(state driverState) error {
	if state.Cmd != nil && state.Cmd.Process != nil {
		_ = state.Cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- state.Cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			_ = state.Cmd.Process.Kill()
			<-done
		}
	}
	// Pull the recording off the device, then clean up.
	args := []string{}
	if state.Device != "" {
		args = append(args, "-s", state.Device)
	}
	pullArgs := append(args, "pull", state.TmpPath, state.OutPath)
	if out, err := exec.Command("adb", pullArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("adb pull failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	rmArgs := append(args, "shell", "rm", "-f", state.TmpPath)
	_ = exec.Command("adb", rmArgs...).Run()
	return nil
}

func firstAdbDevice() string {
	out, err := exec.Command("adb", "devices").Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n")[1:] {
		f := strings.Fields(l)
		if len(f) == 2 && f[1] == "device" {
			return f[0]
		}
	}
	return ""
}
