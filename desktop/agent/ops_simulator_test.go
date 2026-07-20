package main

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestParseSimctlDevices(t *testing.T) {
	// Trimmed real `xcrun simctl list devices available --json` shape.
	j := []byte(`{
	  "devices": {
	    "com.apple.CoreSimulator.SimRuntime.iOS-18-0": [
	      {"udid":"AAAA-1111","name":"iPhone 15","state":"Shutdown"},
	      {"udid":"BBBB-2222","name":"iPad Pro","state":"Booted"}
	    ],
	    "com.apple.CoreSimulator.SimRuntime.tvOS-18-0": [
	      {"udid":"CCCC-3333","name":"Apple TV","state":"Shutdown"}
	    ]
	  }
	}`)
	devices := parseSimctlDevices(j)
	if len(devices) != 3 {
		t.Fatalf("expected 3 devices, got %d", len(devices))
	}
	byUDID := map[string]SimDevice{}
	for _, d := range devices {
		byUDID[d.UDID] = d
	}
	if byUDID["AAAA-1111"].Name != "iPhone 15" || byUDID["AAAA-1111"].Runtime != "iOS-18-0" {
		t.Errorf("iPhone parse wrong: %+v", byUDID["AAAA-1111"])
	}
	if byUDID["BBBB-2222"].State != "Booted" {
		t.Errorf("expected iPad Booted, got %q", byUDID["BBBB-2222"].State)
	}
	if byUDID["CCCC-3333"].Runtime != "tvOS-18-0" {
		t.Errorf("tvOS runtime short name wrong: %q", byUDID["CCCC-3333"].Runtime)
	}
	for _, d := range devices {
		if d.Platform != "ios" {
			t.Errorf("simctl devices must be platform ios, got %q", d.Platform)
		}
	}
}

func TestParseAdbDevices(t *testing.T) {
	out := "List of devices attached\nemulator-5554\tdevice\n192.168.1.9:5555\toffline\n\n"
	devices := parseAdbDevices(out)
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d (%+v)", len(devices), devices)
	}
	if devices[0].UDID != "emulator-5554" || devices[0].State != "Booted" {
		t.Errorf("emulator parse wrong: %+v", devices[0])
	}
	if devices[1].State != "Shutdown" {
		t.Errorf("offline device should read Shutdown, got %q", devices[1].State)
	}
}

// Real end-to-end: on a Mac with Xcode, `simulator list` must actually shell to
// simctl and return the real device set — proving the whole path, not just the
// parser. Skipped where simctl is absent (CI Linux, a Mac without Xcode).
func TestSimulatorListIOSRealShellPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("iOS simulators need macOS")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devices, err := simulatorList(ctx, "ios")
	if err != nil {
		t.Fatalf("real simctl list failed: %v", err)
	}
	// A Mac with Xcode always has at least one simulator device.
	if len(devices) == 0 {
		t.Skip("no simulators installed on this host")
	}
	for _, d := range devices {
		if d.UDID == "" || d.Platform != "ios" {
			t.Errorf("malformed device from real simctl: %+v", d)
		}
	}
	t.Logf("real simctl returned %d simulator device(s)", len(devices))
}
