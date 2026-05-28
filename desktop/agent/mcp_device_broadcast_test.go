package main

// Tests for device_broadcast_command (Phase 8 "no dev box" path). We
// drive the inner runDeviceBroadcastCommand directly so we don't need
// to spin up a full HTTPServer — but the wrapper just forwards to it,
// so coverage is equivalent.

import (
	"testing"
	"time"
)

func TestDeviceBroadcastCommand_ScopedDelivery(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	devA := mgr.GetOrCreateSession("dev-A", "ios", "AppUnderTest")
	devB := mgr.GetOrCreateSession("dev-B", "android", "AppUnderTest")
	chA := devA.SubscribeCommands()
	defer devA.UnsubscribeCommands(chA)
	chB := devB.SubscribeCommands()
	defer devB.UnsubscribeCommands(chB)

	got := runDeviceBroadcastCommand(mgr, deviceBroadcastCommandArgs{
		Command:        "reload",
		TargetDeviceID: "dev-B",
	})
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true; result=%+v", got["ok"], got)
	}
	if got["mode"] != "scoped" {
		t.Fatalf("mode = %v, want scoped", got["mode"])
	}
	if got["reachedSession"] != true {
		t.Fatalf("reachedSession = %v, want true", got["reachedSession"])
	}
	select {
	case cmd := <-chB:
		if cmd.Command != "reload" {
			t.Fatalf("dev-B got %q, want reload", cmd.Command)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("dev-B did not receive scoped command")
	}
	select {
	case cmd := <-chA:
		t.Fatalf("dev-A leaked the scoped command: %+v", cmd)
	case <-time.After(50 * time.Millisecond):
		// good — silence is correct
	}
}

func TestDeviceBroadcastCommand_ScopedMissingSessionReportsFalse(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	got := runDeviceBroadcastCommand(mgr, deviceBroadcastCommandArgs{
		Command:        "reload",
		TargetDeviceID: "ghost",
	})
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true even when scoped target missing (caller decides next step)", got["ok"])
	}
	if got["mode"] != "scoped" {
		t.Fatalf("mode = %v, want scoped", got["mode"])
	}
	if got["reachedSession"] != false {
		t.Fatalf("reachedSession = %v, want false for missing target", got["reachedSession"])
	}
}

func TestDeviceBroadcastCommand_BroadcastWhenNoTarget(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	devA := mgr.GetOrCreateSession("dev-A", "ios", "AppUnderTest")
	devB := mgr.GetOrCreateSession("dev-B", "android", "AppUnderTest")
	chA := devA.SubscribeCommands()
	defer devA.UnsubscribeCommands(chA)
	chB := devB.SubscribeCommands()
	defer devB.UnsubscribeCommands(chB)

	got := runDeviceBroadcastCommand(mgr, deviceBroadcastCommandArgs{Command: "reload"})
	if got["mode"] != "broadcast" {
		t.Fatalf("mode = %v, want broadcast", got["mode"])
	}
	for label, ch := range map[string]chan BlackBoxCommand{"dev-A": chA, "dev-B": chB} {
		select {
		case cmd := <-ch:
			if cmd.Command != "reload" {
				t.Fatalf("%s got %q, want reload", label, cmd.Command)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("%s did not receive broadcast", label)
		}
	}
}

func TestDeviceBroadcastCommand_DataIsPassedThrough(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	dev := mgr.GetOrCreateSession("dev-A", "ios", "AppUnderTest")
	ch := dev.SubscribeCommands()
	defer dev.UnsubscribeCommands(ch)

	runDeviceBroadcastCommand(mgr, deviceBroadcastCommandArgs{
		Command:        "open_app",
		Data:           map[string]interface{}{"app": "settings"},
		TargetDeviceID: "dev-A",
	})

	select {
	case cmd := <-ch:
		if cmd.Command != "open_app" {
			t.Fatalf("command = %q, want open_app", cmd.Command)
		}
		if got, ok := cmd.Data["app"].(string); !ok || got != "settings" {
			t.Fatalf("data.app = %v, want settings", cmd.Data["app"])
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive command")
	}
}

func TestDeviceBroadcastCommand_MissingCommandReturnsError(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	got := runDeviceBroadcastCommand(mgr, deviceBroadcastCommandArgs{})
	if got["ok"] != false {
		t.Fatalf("ok = %v, want false for missing command", got["ok"])
	}
	if got["error"] == nil {
		t.Fatal("expected error field for missing command")
	}
}

func TestDeviceBroadcastCommand_NilManagerReturnsNoBlackbox(t *testing.T) {
	got := runDeviceBroadcastCommand(nil, deviceBroadcastCommandArgs{Command: "reload"})
	if got["ok"] != false {
		t.Fatalf("ok = %v, want false when manager nil", got["ok"])
	}
	if got["mode"] != "no_blackbox" {
		t.Fatalf("mode = %v, want no_blackbox", got["mode"])
	}
}
