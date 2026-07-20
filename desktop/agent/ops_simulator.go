package main

// ops_simulator.go — verb "simulator": manage the remote box's iOS simulators
// and Android emulators (list / boot / shutdown / create / delete). This is the
// lifecycle layer under WebRTC RN simulator streaming
// (docs/architecture/WEBRTC_RN_SIMULATOR_STREAMING.md): a session boots a device
// here, streams it, and this verb lets any surface (CLI / MCP / web / mobile)
// list what's available, create the device type you're testing for, and — per
// the scale-to-zero discipline — shut down or delete idle sims so a booted pool
// doesn't sit on disk + RAM.
//
// iOS goes through `xcrun simctl` (macOS only). Android goes through
// `avdmanager` / `adb` / `emulator`. Every action degrades to a clear error on a
// host that can't run the tool rather than pretending.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type opsSimulatorPayload struct {
	// Action: list | boot | shutdown | create | delete. Defaults to list.
	Action string `json:"action,omitempty"`
	// Platform: ios | android. Defaults to ios.
	Platform string `json:"platform,omitempty"`
	// UDID / device id — for boot / shutdown / delete.
	UDID string `json:"udid,omitempty"`
	// For create: a device type (e.g. "iPhone 15") and a runtime hint.
	DeviceType string `json:"deviceType,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	Name       string `json:"name,omitempty"`
}

// SimDevice is one simulator/emulator as reported to callers.
type SimDevice struct {
	UDID     string `json:"udid"`
	Name     string `json:"name"`
	State    string `json:"state"` // Booted | Shutdown | ...
	Runtime  string `json:"runtime,omitempty"`
	Platform string `json:"platform"` // ios | android
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "simulator",
		Description: "Manage the remote box's iOS simulators / Android emulators for WebRTC streaming: list, boot, shutdown, create, delete. Keeps the sim pool scale-to-zero — shut down or delete idle devices.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action":     map[string]interface{}{"type": "string", "enum": []string{"list", "boot", "shutdown", "create", "delete"}, "default": "list"},
				"platform":   map[string]interface{}{"type": "string", "enum": []string{"ios", "android"}, "default": "ios"},
				"udid":       map[string]interface{}{"type": "string"},
				"deviceType": map[string]interface{}{"type": "string"},
				"runtime":    map[string]interface{}{"type": "string"},
				"name":       map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsSimulatorHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsSimulatorHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsSimulatorPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	action := strings.TrimSpace(strings.ToLower(p.Action))
	if action == "" {
		action = "list"
	}
	platform := strings.TrimSpace(strings.ToLower(p.Platform))
	if platform == "" {
		platform = "ios"
	}
	if platform != "ios" && platform != "android" {
		return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("unknown platform %q (use ios or android)", platform)}
	}

	// A bounded deadline so a wedged simctl/adb never hangs the ops call.
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	fail := func(err error) OpsResult { return OpsResult{OK: false, Code: "simulator_failed", Error: err.Error()} }

	switch action {
	case "list":
		devices, err := simulatorList(ctx, platform)
		if err != nil {
			return fail(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"platform": platform, "devices": devices, "count": len(devices)}}
	case "boot":
		if strings.TrimSpace(p.UDID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "boot requires a udid"}
		}
		if err := simulatorBoot(ctx, platform, p.UDID); err != nil {
			return fail(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"action": "boot", "udid": p.UDID}}
	case "shutdown":
		if strings.TrimSpace(p.UDID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "shutdown requires a udid"}
		}
		if err := simulatorShutdown(ctx, platform, p.UDID); err != nil {
			return fail(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"action": "shutdown", "udid": p.UDID}}
	case "create":
		udid, err := simulatorCreate(ctx, platform, p)
		if err != nil {
			return fail(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"action": "create", "udid": udid}}
	case "delete":
		if strings.TrimSpace(p.UDID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "delete requires a udid"}
		}
		if err := simulatorDelete(ctx, platform, p.UDID); err != nil {
			return fail(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"action": "delete", "udid": p.UDID}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("unknown action %q", action)}
}

// ---- iOS (simctl) + Android (adb/avdmanager) implementations ----

func simulatorList(ctx context.Context, platform string) ([]SimDevice, error) {
	if platform == "ios" {
		if runtime.GOOS != "darwin" {
			return nil, fmt.Errorf("iOS simulators need macOS")
		}
		out, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devices", "available", "--json").Output()
		if err != nil {
			return nil, fmt.Errorf("simctl list: %w", err)
		}
		return parseSimctlDevices(out), nil
	}
	// Android: adb-attached emulators/devices.
	out, err := exec.CommandContext(ctx, "adb", "devices").Output()
	if err != nil {
		return nil, fmt.Errorf("adb devices: %w", err)
	}
	return parseAdbDevices(string(out)), nil
}

// parseSimctlDevices reads `simctl list devices --json` into a flat list.
func parseSimctlDevices(jsonOut []byte) []SimDevice {
	var doc struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(jsonOut, &doc); err != nil {
		return nil
	}
	devices := []SimDevice{}
	for runtimeName, list := range doc.Devices {
		// runtimeName looks like "com.apple.CoreSimulator.SimRuntime.iOS-18-0".
		short := runtimeName
		if i := strings.LastIndex(runtimeName, "SimRuntime."); i >= 0 {
			short = runtimeName[i+len("SimRuntime."):]
		}
		for _, d := range list {
			devices = append(devices, SimDevice{
				UDID: d.UDID, Name: d.Name, State: d.State, Runtime: short, Platform: "ios",
			})
		}
	}
	return devices
}

// parseAdbDevices reads `adb devices` (skips the header + empty lines).
func parseAdbDevices(out string) []SimDevice {
	devices := []SimDevice{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		state := "Shutdown"
		if fields[1] == "device" {
			state = "Booted"
		}
		devices = append(devices, SimDevice{UDID: fields[0], Name: fields[0], State: state, Platform: "android"})
	}
	return devices
}

func simulatorBoot(ctx context.Context, platform, udid string) error {
	if platform == "ios" {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("iOS simulators need macOS")
		}
		// simctl errors if already booted; treat that as success.
		out, err := exec.CommandContext(ctx, "xcrun", "simctl", "boot", udid).CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(out)), "current state: booted") {
			return fmt.Errorf("simctl boot: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	// Android emulators are booted by name via the `emulator` binary; adb can't
	// boot a cold AVD. Surface a clear message rather than a misleading no-op.
	return fmt.Errorf("android emulator boot is by AVD name via the emulator binary, not adb udid; use the device picker")
}

func simulatorShutdown(ctx context.Context, platform, udid string) error {
	if platform == "ios" {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("iOS simulators need macOS")
		}
		out, err := exec.CommandContext(ctx, "xcrun", "simctl", "shutdown", udid).CombinedOutput()
		if err != nil && !strings.Contains(strings.ToLower(string(out)), "current state: shutdown") {
			return fmt.Errorf("simctl shutdown: %s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	out, err := exec.CommandContext(ctx, "adb", "-s", udid, "emu", "kill").CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb emu kill: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func simulatorCreate(ctx context.Context, platform string, p opsSimulatorPayload) (string, error) {
	if platform != "ios" {
		return "", fmt.Errorf("android AVD creation goes through avdmanager with a system image; not yet wired")
	}
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("iOS simulators need macOS")
	}
	deviceType := strings.TrimSpace(p.DeviceType)
	if deviceType == "" {
		return "", fmt.Errorf("create requires a deviceType (e.g. \"iPhone 15\")")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "yaver-" + strings.ReplaceAll(deviceType, " ", "-")
	}
	args := []string{"simctl", "create", name, deviceType}
	if rt := strings.TrimSpace(p.Runtime); rt != "" {
		args = append(args, rt)
	}
	out, err := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl create: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil // simctl create prints the new UDID
}

func simulatorDelete(ctx context.Context, platform, udid string) error {
	if platform != "ios" {
		return fmt.Errorf("android AVD deletion goes through avdmanager delete avd; not yet wired")
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("iOS simulators need macOS")
	}
	out, err := exec.CommandContext(ctx, "xcrun", "simctl", "delete", udid).CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl delete: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
