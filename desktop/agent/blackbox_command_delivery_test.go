package main

// Tests for the command-delivery count that the Hermes reload / deploy path
// relies on to avoid reporting a false "it's on your phone now" when no phone
// is actually listening on this agent (the remote self-hosted-box case).

import "testing"

func TestBroadcastCommand_CountsLiveListeners(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}

	// A session row with NO command-stream open (phone registered events but
	// isn't holding the SSE) must not count toward delivery.
	mgr.GetOrCreateSession("dev-silent", "ios", "AppUnderTest")
	if n := mgr.BroadcastCommand(BlackBoxCommand{Command: "reload"}); n != 0 {
		t.Fatalf("broadcast with only a listener-less session delivered to %d, want 0", n)
	}

	// Open two live command streams → broadcast reaches both.
	devA := mgr.GetSession("dev-silent")
	chA := devA.SubscribeCommands()
	defer devA.UnsubscribeCommands(chA)
	devB := mgr.GetOrCreateSession("dev-B", "android", "AppUnderTest")
	chB := devB.SubscribeCommands()
	defer devB.UnsubscribeCommands(chB)

	if n := mgr.BroadcastCommand(BlackBoxCommand{Command: "reload"}); n != 2 {
		t.Fatalf("broadcast delivered to %d live listeners, want 2", n)
	}
}

func TestSendCommandToDevice_FalseWithoutLiveListener(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}

	// No session at all.
	if mgr.SendCommandToDevice("ghost", BlackBoxCommand{Command: "reload"}) {
		t.Fatal("SendCommandToDevice returned true for a device with no session")
	}

	// Session exists but no command-stream is open → must be false so the
	// reload path falls back to a broadcast instead of silently dropping.
	sess := mgr.GetOrCreateSession("dev-A", "ios", "AppUnderTest")
	if mgr.SendCommandToDevice("dev-A", BlackBoxCommand{Command: "reload"}) {
		t.Fatal("SendCommandToDevice returned true for a session with no live listener")
	}

	ch := sess.SubscribeCommands()
	defer sess.UnsubscribeCommands(ch)
	if !mgr.SendCommandToDevice("dev-A", BlackBoxCommand{Command: "reload"}) {
		t.Fatal("SendCommandToDevice returned false despite a live listener")
	}
}

func TestReloadDeliveredTo_ParsesShapes(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want int
	}{
		{"absent", map[string]interface{}{"ok": true}, -1},
		{"float64", map[string]interface{}{"deliveredTo": float64(0)}, 0},
		{"float64-one", map[string]interface{}{"deliveredTo": float64(2)}, 2},
		{"int", map[string]interface{}{"deliveredTo": 1}, 1},
		{"not-a-map", "nope", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reloadDeliveredTo(c.in); got != c.want {
				t.Fatalf("reloadDeliveredTo(%v) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
