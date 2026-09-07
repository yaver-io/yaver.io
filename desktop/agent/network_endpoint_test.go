package main

import "testing"

func TestAgentHTTPBaseDualStack(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"192.0.2.10", "http://192.0.2.10:18080"},
		{"box.local", "http://box.local:18080"},
		{"2001:db8::10", "http://[2001:db8::10]:18080"},
		{"[2001:db8::10]", "http://[2001:db8::10]:18080"},
		{"fe80::10%en0", "http://[fe80::10%en0]:18080"},
	}
	for _, tt := range tests {
		if got := agentHTTPBase(tt.host, 18080); got != tt.want {
			t.Errorf("agentHTTPBase(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}
