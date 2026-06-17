package main

import "testing"

func TestHTTPServerDirectBindHostRelayOnly(t *testing.T) {
	if got := (&HTTPServer{}).directBindHost(); got != "0.0.0.0" {
		t.Fatalf("default direct bind host = %q, want 0.0.0.0", got)
	}
	if got := (&HTTPServer{relayOnly: true}).directBindHost(); got != "127.0.0.1" {
		t.Fatalf("relay-only direct bind host = %q, want 127.0.0.1", got)
	}
}
