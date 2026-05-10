package main

import (
	"reflect"
	"testing"
	"time"
)

func TestParseIPv4FromEchoBody(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "203.0.113.42", "203.0.113.42"},
		{"trailing newline", "203.0.113.42\n", "203.0.113.42"},
		{"trailing whitespace", "  203.0.113.42  \n", "203.0.113.42"},
		{"multi-line first valid", "ip=203.0.113.42\n203.0.113.42\n", "203.0.113.42"},
		{"loopback rejected", "127.0.0.1", ""},
		{"link-local rejected", "169.254.1.1", ""},
		{"multicast rejected", "224.0.0.1", ""},
		{"unspecified rejected", "0.0.0.0", ""},
		{"ipv6 rejected", "2001:db8::1", ""},
		{"garbage", "not-an-ip", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseIPv4FromEchoBody(tc.in); got != tc.want {
				t.Fatalf("parseIPv4FromEchoBody(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizedEndpointMatches(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"http://198.51.100.20:18080", "http://198.51.100.20:18080", true},
		{"http://198.51.100.20:18080", "http://198.51.100.20:8443", false},
		{"198.51.100.20", "http://198.51.100.20:18080", true},
		{"https://198.51.100.20:18443", "http://198.51.100.20:18443", true},
		{"https://a.example.com", "https://b.example.com", false},
		{"", "http://198.51.100.20:18080", false},
	}
	for _, tc := range cases {
		got := normalizedEndpointMatches(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("normalizedEndpointMatches(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPublicEndpointsWithAutoIP_AppendsAndSkipsWhenDisabled(t *testing.T) {
	t.Cleanup(resetAutoPublicIPCache)

	// Pre-warm the cache so detectAutoPublicIP() returns immediately
	// without doing real network probes.
	resetAutoPublicIPCache()
	autoPublicIPCache.mu.Lock()
	autoPublicIPCache.ip = "203.0.113.42"
	autoPublicIPCache.ts = time.Now()
	autoPublicIPCache.mu.Unlock()

	// Disabled — should not append even if cache is populated.
	cfg := &Config{DisableAutoPublicIP: true}
	got := publicEndpointsWithAutoIP(cfg, 18080)
	if len(got) != 0 {
		t.Fatalf("expected no endpoints when DisableAutoPublicIP=true; got %v", got)
	}

	// Enabled — should append the cached IP.
	cfg.DisableAutoPublicIP = false
	got = publicEndpointsWithAutoIP(cfg, 18080)
	want := []string{"http://203.0.113.42:18080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v; got %v", want, got)
	}

	// Manual entry suppresses auto-detected duplicate of the same host.
	cfg.PublicEndpoints = []string{"203.0.113.42"}
	got = publicEndpointsWithAutoIP(cfg, 18080)
	if len(got) != 1 || got[0] != "203.0.113.42" {
		t.Fatalf("manual entry should suppress auto duplicate; got %v", got)
	}
}
