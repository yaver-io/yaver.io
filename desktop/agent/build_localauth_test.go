package main

import (
	"net/http"
	"testing"
)

// TestIsLocalLoopbackRequest pins the rule that lets `yaver build` skip auth:
// a direct loopback hit is local (admitted), but anything carrying a
// relay/cloudflared forwarding header, or from a non-loopback IP, is not.
func TestIsLocalLoopbackRequest(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       bool
	}{
		{"loopback v4 direct", "127.0.0.1:54321", "", true},
		{"loopback v6 direct", "[::1]:54321", "", true},
		{"localhost literal", "localhost:8080", "", true},
		{"loopback but forwarded (relay/cf)", "127.0.0.1:54321", "203.0.113.7", false},
		{"lan ip direct", "192.168.1.50:40000", "", false},
		{"public ip direct", "203.0.113.7:40000", "", false},
		{"empty remote", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/builds", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := isLocalLoopbackRequest(r); got != tc.want {
				t.Fatalf("isLocalLoopbackRequest(%q, xff=%q) = %v, want %v",
					tc.remoteAddr, tc.xff, got, tc.want)
			}
		})
	}
}
