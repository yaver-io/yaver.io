package main

import (
	"net/http/httptest"
	"testing"
)

func TestWSOriginAllowed(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "no origin", host: "127.0.0.1:18080", origin: "", want: true},
		{name: "same host", host: "agent.local:18080", origin: "http://agent.local:18080", want: true},
		{name: "trusted yaver origin", host: "192.168.1.9:18080", origin: "https://yaver.io", want: true},
		{name: "trusted localhost origin", host: "192.168.1.9:18080", origin: "http://localhost:3000", want: true},
		{name: "untrusted origin", host: "192.168.1.9:18080", origin: "https://evil.example", want: false},
		{name: "invalid origin", host: "192.168.1.9:18080", origin: "://bad", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+"/ws/metrics", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := wsOriginAllowed(req); got != tt.want {
				t.Fatalf("wsOriginAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
