package main

import "testing"

// The websocket relay fallback must stay OFF until the relay can actually route
// over it. relay/server.go handleProxy resolves a device out of s.tunnels (the
// QUIC map) and never consults the websocket tunnel, so an agent that falls back
// to websocket does not get a slower path — it disappears, and every request
// answers "502 device not connected to relay".
//
// A Mac mini on 1.99.302 flapped between QUIC and this fallback every ~2 minutes
// and was reachable 33% of the time. The auto-heal read the 502s the fallback
// itself caused as proof the tunnel was dead, and re-applied the cure.
func TestRelayWSFallbackIsOffByDefault(t *testing.T) {
	t.Setenv("YAVER_RELAY_WS_FALLBACK", "")
	if relayWSFallbackEnabled() {
		t.Fatal("websocket relay fallback is ON by default — it makes the device UNREACHABLE, " +
			"because relay handleProxy cannot deliver over a websocket tunnel")
	}
}

func TestRelayWSFallbackIsOptIn(t *testing.T) {
	for _, on := range []string{"1", "true", "yes", "on", "ON", "True"} {
		t.Setenv("YAVER_RELAY_WS_FALLBACK", on)
		if !relayWSFallbackEnabled() {
			t.Errorf("YAVER_RELAY_WS_FALLBACK=%q should enable the fallback", on)
		}
	}
	for _, off := range []string{"0", "false", "no", "off", "", "garbage"} {
		t.Setenv("YAVER_RELAY_WS_FALLBACK", off)
		if relayWSFallbackEnabled() {
			t.Errorf("YAVER_RELAY_WS_FALLBACK=%q must NOT enable the fallback", off)
		}
	}
}
