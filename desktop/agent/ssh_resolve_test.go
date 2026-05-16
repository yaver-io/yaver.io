package main

import "testing"

func TestFirstPreferredTailscaleIP(t *testing.T) {
	got := firstPreferredTailscaleIP("fd7a:115c:a1e0::1\n100.64.0.5\n")
	if got != "100.64.0.5" {
		t.Fatalf("firstPreferredTailscaleIP() = %q, want 100.64.0.5", got)
	}
}

func TestFirstPreferredTailscaleIPFallsBackToFirstNonEmpty(t *testing.T) {
	got := firstPreferredTailscaleIP("\nfd7a:115c:a1e0::1\n")
	if got != "fd7a:115c:a1e0::1" {
		t.Fatalf("firstPreferredTailscaleIP() = %q, want fd7a:115c:a1e0::1", got)
	}
}

func TestIsLikelyDockerBridgeIP(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "172.17.0.1", want: true},
		{host: "172.18.0.1", want: true},
		{host: "172.31.0.1", want: true},
		{host: "172.18.0.2", want: false},
		{host: "172.20.5.10", want: false},
		{host: "100.64.0.5", want: false},
		{host: "not-an-ip", want: false},
	}
	for _, tc := range tests {
		if got := isLikelyDockerBridgeIP(tc.host); got != tc.want {
			t.Fatalf("isLikelyDockerBridgeIP(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestIsYaverHTTPRelayHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{host: "000ca94b-158d-42ab-a02e-edab5a6d9d06.yaver.io", want: true},
		{host: "4a6a5095-8e4e-4b77-bc66-e62668f4d9fd.dev.yaver.io", want: true},
		{host: "https://000ca94b-158d-42ab-a02e-edab5a6d9d06.yaver.io", want: false}, // caller strips scheme first
		{host: "yaver.io", want: false},
		{host: "relay.yaver.io", want: false},
		{host: "test.yaver.io", want: false},                 // not a UUID label
		{host: "12345678-1234-1234-1234-12345678abcd.example.com", want: false},
		{host: "yaver-test-ephemeral", want: false},
		{host: "157.180.114.179", want: false},
		{host: "", want: false},
	}
	for _, tc := range tests {
		if got := isYaverHTTPRelayHost(tc.host); got != tc.want {
			t.Fatalf("isYaverHTTPRelayHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// Regression: `yaver primary auth` resolved a public endpoint to
// "157.180.114.179:18080" and handed the HTTP-API port to ssh, which
// has no host:port syntax → "Could not resolve hostname …:18080".
// resolveSSHHost's public-endpoint branch must hand ssh a bare host.
func TestBareHostNoPort(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "157.180.114.179:18080", want: "157.180.114.179"}, // the bug
		{in: "157.180.114.179", want: "157.180.114.179"},        // bare host untouched
		{in: "example-host:22", want: "example-host"},
		{in: "example-host", want: "example-host"},
		{in: "[::1]:18080", want: "::1"},
		{in: "[2001:db8::1]:443", want: "2001:db8::1"},
		{in: "[::1]", want: "::1"},
		{in: "  157.180.114.179:18080  ", want: "157.180.114.179"},
		{in: "", want: ""},
	}
	for _, tc := range tests {
		if got := bareHostNoPort(tc.in); got != tc.want {
			t.Fatalf("bareHostNoPort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
