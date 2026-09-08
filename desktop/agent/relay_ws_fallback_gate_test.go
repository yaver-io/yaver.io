package main

import "testing"

// The relay now routes request/response HTTP through websocket tunnels. Keep
// that recovery lane on by default so an ICMP/UDP-blocking network does not turn
// a healthy HTTPS-capable box into "online but unreachable".
func TestRelayWSFallbackIsOnByDefault(t *testing.T) {
	t.Setenv("YAVER_RELAY_WS_FALLBACK", "")
	if !relayWSFallbackEnabled() {
		t.Fatal("websocket relay fallback is OFF by default — UDP-blocking networks have no HTTPS recovery lane")
	}
}

func TestRelayWSFallbackHasExplicitOptOut(t *testing.T) {
	for _, on := range []string{"1", "true", "yes", "on", "ON", "True", "garbage"} {
		t.Setenv("YAVER_RELAY_WS_FALLBACK", on)
		if !relayWSFallbackEnabled() {
			t.Errorf("YAVER_RELAY_WS_FALLBACK=%q should leave the default-on fallback enabled", on)
		}
	}
	for _, off := range []string{"0", "false", "no", "off", "disable", "disabled"} {
		t.Setenv("YAVER_RELAY_WS_FALLBACK", off)
		if relayWSFallbackEnabled() {
			t.Errorf("YAVER_RELAY_WS_FALLBACK=%q must disable the fallback", off)
		}
	}
}

func TestRelayWSFallbackOnlyHandlesTransportFailure(t *testing.T) {
	t.Setenv("YAVER_RELAY_WS_FALLBACK", "")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "udp blocked", err: assertError("dial relay: timeout: no recent network activity"), want: true},
		{name: "relay restart", err: assertError("dial relay: connection refused"), want: true},
		{name: "bad password", err: assertError("registration rejected: invalid relay password (reason=bad_password)"), want: false},
		{name: "dead token", err: assertError("registration rejected: relay session expired (reason=dead_token)"), want: false},
		{name: "wrong owner", err: assertError("registration rejected: device mismatch (reason=device_mismatch)"), want: false},
		{name: "pin mismatch", err: assertError("relay SPKI pin mismatch"), want: false},
		{name: "legacy refusal", err: assertError("registration rejected: unsupported credential"), want: false},
		{name: "no failure", err: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldTryRelayWSFallback(tc.err); got != tc.want {
				t.Fatalf("shouldTryRelayWSFallback(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRelayWSFallbackOptOutBlocksTransportRecovery(t *testing.T) {
	t.Setenv("YAVER_RELAY_WS_FALLBACK", "0")
	if shouldTryRelayWSFallback(assertError("dial relay: timeout")) {
		t.Fatal("explicit WebSocket fallback opt-out was ignored")
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
