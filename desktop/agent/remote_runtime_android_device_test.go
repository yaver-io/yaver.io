package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAdb installs a fake `adb` on PATH whose `devices -l` output is
// driven by the YAVER_TEST_ADB_DEVICES env var. This is the repo
// convention (real process, no mocks) — same shape as the stub-binary
// pattern in android_sdk_install_test.go.
func stubAdb(t *testing.T, deviceLines string) {
	t.Helper()
	dir := t.TempDir()
	adb := filepath.Join(dir, "adb")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"devices -l\"*) printf 'List of devices attached\\n%s\\n' \"$YAVER_TEST_ADB_DEVICES\" ;;\n" +
		"  version*) echo 'Android Debug Bridge version 1.0.41' ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(adb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Keep managed-runtime / SDK-root lookups from shadowing the stub.
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("YAVER_TEST_ADB_DEVICES", deviceLines)
}

const usbAndroidLine = "R52W60BEDXD          device usb:1-1 product:panther model:Pixel_7 device:panther transport_id:1"
const wifiAndroidLine = "192.168.1.50:5555      device product:bluejay model:Pixel_6a device:bluejay transport_id:2"

func TestProbeAndroidDeviceTarget_DisabledWhenNoDevice(t *testing.T) {
	stubAdb(t, "")
	target := probeAndroidDeviceTarget()
	if target.ID != "android-device" || target.Platform != "android" {
		t.Fatalf("unexpected target identity: %+v", target)
	}
	if target.Enabled {
		t.Fatalf("expected disabled with no device attached; got enabled")
	}
	if !strings.Contains(target.Reason, "no physical Android device") {
		t.Fatalf("reason should explain the missing device, got %q", target.Reason)
	}
}

func TestProbeAndroidDeviceTarget_EnabledWithPhysicalDevice(t *testing.T) {
	stubAdb(t, usbAndroidLine)
	target := probeAndroidDeviceTarget()
	if !target.Enabled {
		t.Fatalf("expected enabled with a USB device attached; reason=%q", target.Reason)
	}
	serial, err := resolveAttachedAndroidDeviceSerial(context.Background())
	if err != nil {
		t.Fatalf("resolve serial: %v", err)
	}
	if serial != "R52W60BEDXD" {
		t.Fatalf("serial = %q, want R52W60BEDXD", serial)
	}
	if strings.HasPrefix(serial, "emulator-") {
		t.Fatalf("physical resolution must never return an emulator serial: %q", serial)
	}
}

// A cabled device must win over a wireless one — lower latency for
// screenrecord + input replay.
func TestResolveAttachedAndroidDeviceSerial_PrefersUSB(t *testing.T) {
	stubAdb(t, usbAndroidLine+"\n"+wifiAndroidLine)
	serial, err := resolveAttachedAndroidDeviceSerial(context.Background())
	if err != nil {
		t.Fatalf("resolve serial: %v", err)
	}
	if serial != "R52W60BEDXD" {
		t.Fatalf("USB device should be preferred; got %q", serial)
	}
}

// Capabilities for the WebRTC frameworks must surface android-device
// alongside the emulator so a host with no emulator binary
// (linux/arm64) still has a usable Android target.
func TestRemoteRuntimeCapabilities_IncludeAndroidDevice(t *testing.T) {
	stubAdb(t, usbAndroidLine)
	for _, fw := range []string{"flutter", "kotlin"} {
		caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), fw)
		var ids []string
		hasDevice := false
		for _, tg := range caps.Targets {
			ids = append(ids, tg.ID)
			if tg.ID == "android-device" {
				hasDevice = true
			}
		}
		if !hasDevice {
			t.Fatalf("%s caps missing android-device target; got %v", fw, ids)
		}
	}
}
