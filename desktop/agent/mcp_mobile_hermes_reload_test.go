package main

// Tests for the Phase 7 cross-device targeting path. These exercise the
// BlackBox routing in isolation — they do NOT spin up a full HTTPServer
// or dev-server-manager because mcpMobileHermesReload talks over a real
// localAgentRequest loopback (which needs auth + a live agent), which
// is heavy for a unit test. The behaviours we care about are:
//
//   1. BlackBoxManager.SendCommandToDevice delivers ONLY to the scoped
//      device's listener — sibling sessions don't see the command.
//   2. SendCommandToDevice returns false for an unknown id, so the
//      /dev/reload caller's fallback-to-broadcast branch can fire.
//   3. BroadcastCommand still hits every session (regression guard for
//      the legacy broadcast path).
//   4. mobileHermesReloadArgs JSON tags match the wire shape the MCP
//      tool advertises in mcp_tools.go.

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSendCommandToDevice_DeliversOnlyToScopedSession(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}

	devA := mgr.GetOrCreateSession("dev-A", "ios", "AppUnderTest")
	devB := mgr.GetOrCreateSession("dev-B", "android", "AppUnderTest")

	listenA := devA.SubscribeCommands()
	defer devA.UnsubscribeCommands(listenA)
	listenB := devB.SubscribeCommands()
	defer devB.UnsubscribeCommands(listenB)

	cmd := BlackBoxCommand{Command: "reload"}
	if ok := mgr.SendCommandToDevice("dev-B", cmd); !ok {
		t.Fatalf("SendCommandToDevice(dev-B) returned false; expected true")
	}

	select {
	case got := <-listenB:
		if got.Command != "reload" {
			t.Fatalf("dev-B got command %q; want reload", got.Command)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("dev-B did not receive command within 200ms")
	}

	// dev-A must NOT have received anything — that's the whole point
	// of Phase 7 cross-device targeting.
	select {
	case got := <-listenA:
		t.Fatalf("dev-A unexpectedly received scoped command: %+v", got)
	case <-time.After(50 * time.Millisecond):
		// ok — silence is correct
	}
}

func TestSendCommandToDevice_UnknownDeviceReturnsFalse(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	// No session created for "ghost-id".
	if ok := mgr.SendCommandToDevice("ghost-id", BlackBoxCommand{Command: "reload"}); ok {
		t.Fatal("SendCommandToDevice(ghost-id) returned true; expected false so the /dev/reload caller falls back to broadcast")
	}
}

func TestSendCommandToDevice_EmptyIDReturnsFalse(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	if ok := mgr.SendCommandToDevice("", BlackBoxCommand{Command: "reload"}); ok {
		t.Fatal("SendCommandToDevice(\"\") returned true; expected false")
	}
}

func TestBroadcastCommand_HitsEverySession(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}

	devA := mgr.GetOrCreateSession("dev-A", "ios", "AppUnderTest")
	devB := mgr.GetOrCreateSession("dev-B", "android", "AppUnderTest")

	listenA := devA.SubscribeCommands()
	defer devA.UnsubscribeCommands(listenA)
	listenB := devB.SubscribeCommands()
	defer devB.UnsubscribeCommands(listenB)

	mgr.BroadcastCommand(BlackBoxCommand{Command: "reload"})

	for label, ch := range map[string]chan BlackBoxCommand{"dev-A": listenA, "dev-B": listenB} {
		select {
		case got := <-ch:
			if got.Command != "reload" {
				t.Fatalf("%s got command %q; want reload", label, got.Command)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("%s did not receive broadcast within 200ms", label)
		}
	}
}

func TestMobileHermesReloadArgs_JSONTags(t *testing.T) {
	// Guard the MCP wire shape — mcp_tools.go declares the inputSchema
	// with `target_device_id` and `mode`. If somebody renames the Go
	// fields without updating the tool schema, the MCP call silently
	// drops the args. Catch that here.
	raw := []byte(`{"device_id":"box-1","target_device_id":"dev-B","mode":"dev"}`)
	var args mobileHermesReloadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if args.DeviceID != "box-1" {
		t.Fatalf("DeviceID = %q; want box-1 (json tag drift?)", args.DeviceID)
	}
	if args.TargetDeviceID != "dev-B" {
		t.Fatalf("TargetDeviceID = %q; want dev-B (json tag drift?)", args.TargetDeviceID)
	}
	if args.Mode != "dev" {
		t.Fatalf("Mode = %q; want dev (json tag drift?)", args.Mode)
	}
}

func TestMobileHermesReloadBody_SeparatesAgentDeviceFromSdkTarget(t *testing.T) {
	body := mobileHermesReloadBody(mobileHermesReloadArgs{
		DeviceID:       "box-1",
		TargetDeviceID: "phone-sdk-1",
		Mode:           "bundle",
	})
	if _, leaked := body["device_id"]; leaked {
		t.Fatalf("body leaked device_id to /dev/reload: %#v", body)
	}
	if got := body["targetDeviceId"]; got != "phone-sdk-1" {
		t.Fatalf("targetDeviceId = %#v; want phone-sdk-1", got)
	}
	if got := body["mode"]; got != "bundle" {
		t.Fatalf("mode = %#v; want bundle", got)
	}
}
