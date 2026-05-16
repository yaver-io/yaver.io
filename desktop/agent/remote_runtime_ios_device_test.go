package main

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

var _ runtimeTarget = iosDeviceTarget{}

func TestRuntimeTargetFor_IOSDevice(t *testing.T) {
	tgt, err := runtimeTargetFor("ios-device")
	if err != nil {
		t.Fatalf("runtimeTargetFor(ios-device): %v", err)
	}
	if _, ok := tgt.(iosDeviceTarget); !ok {
		t.Fatalf("want iosDeviceTarget, got %T", tgt)
	}
}

func TestProbeIOSDeviceTarget_Identity(t *testing.T) {
	tg := probeIOSDeviceTarget()
	if tg.ID != "ios-device" || tg.Platform != "ios" {
		t.Fatalf("identity wrong: %+v", tg)
	}
	if runtime.GOOS != "darwin" {
		if tg.Enabled || !strings.Contains(tg.Reason, "macOS") {
			t.Fatalf("non-macOS host must be disabled with a macOS reason, got %+v", tg)
		}
	}
}

func TestRemoteRuntimeCapabilities_IncludeIOSDevice(t *testing.T) {
	for _, fw := range []string{"swift", "flutter"} {
		caps := remoteRuntimeCapabilitiesForProject(t.TempDir(), fw)
		found := false
		var ids []string
		for _, tg := range caps.Targets {
			ids = append(ids, tg.ID)
			if tg.ID == "ios-device" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s caps missing ios-device; got %v", fw, ids)
		}
	}
}

// iosDeviceTarget control routed through a real fake WDA server.
func TestIOSDeviceTarget_ControlViaWDA(t *testing.T) {
	f := newFakeWDA(t)
	t.Setenv("YAVER_WDA_BASE_URL", f.srv.URL)
	tgt := iosDeviceTarget{}
	ctx := context.Background()

	if err := tgt.Tap(ctx, "udid", 10, 20); err != nil {
		t.Fatalf("tap: %v", err)
	}
	if b := f.body("/session/sess-1/wda/tap/0"); b["x"].(float64) != 10 {
		t.Fatalf("tap not delivered to WDA: %v", b)
	}
	// Real iOS supports drag — must NOT return the simulator's
	// "not implemented" error.
	if err := tgt.Swipe(ctx, "udid", 0, 0, 5, 5, 200); err != nil {
		t.Fatalf("swipe on real device should work via WDA: %v", err)
	}
	if err := tgt.Text(ctx, "udid", "ab"); err != nil {
		t.Fatalf("text: %v", err)
	}
	if err := tgt.Key(ctx, "udid", "home"); err != nil {
		t.Fatalf("key home: %v", err)
	}
	tmp := t.TempDir() + "/shot.png"
	if err := tgt.Screenshot(ctx, "udid", tmp); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	dims := tgt.Dims(ctx, "udid")
	if dims.Width != 390 || dims.Height != 844 || dims.Rotation != "portrait" {
		t.Fatalf("dims from WDA window/size wrong: %+v", dims)
	}
}

func TestIOSDeviceTarget_AttachRequiresMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS attach path needs a wired iPhone + WDA; not unit-testable here")
	}
	if _, err := (iosDeviceTarget{}).Attach(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "macOS") {
		t.Fatalf("non-macOS Attach must fail with a macOS message, got %v", err)
	}
}

func TestIOSDeviceTarget_CanEncodeMatchesFfmpeg(t *testing.T) {
	_, ffmpegErr := exec.LookPath("ffmpeg")
	want := runtime.GOOS == "darwin" && ffmpegErr == nil
	if got := (iosDeviceTarget{}).CanEncodeRTPH264(); got != want {
		t.Fatalf("CanEncodeRTPH264 = %v, want %v (darwin=%v ffmpeg=%v)",
			got, want, runtime.GOOS == "darwin", ffmpegErr == nil)
	}
}
