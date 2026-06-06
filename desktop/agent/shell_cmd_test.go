package main

import (
	"strings"
	"testing"
)

func TestTerminalWSURL(t *testing.T) {
	cases := []struct {
		name, base, token, shell string
		want                     string
		wantErr                  bool
	}{
		{
			name:  "https→wss (relay/public endpoint)",
			base:  "https://abc123.yaver.io",
			token: "tok",
			want:  "wss://abc123.yaver.io/ws/terminal?token=tok",
		},
		{
			name:  "http→ws (LAN agent)",
			base:  "http://192.168.1.20:18080/",
			token: "tok",
			want:  "ws://192.168.1.20:18080/ws/terminal?token=tok",
		},
		{
			name:  "relay path-style base",
			base:  "https://relay.example.com/d/dev-123",
			token: "tok",
			want:  "wss://relay.example.com/d/dev-123/ws/terminal?token=tok",
		},
		{
			name:  "with shell override",
			base:  "http://127.0.0.1:18080",
			token: "tok",
			shell: "/bin/bash",
			want:  "ws://127.0.0.1:18080/ws/terminal?token=tok&shell=/bin/bash",
		},
		{
			name:    "bad scheme",
			base:    "ftp://nope",
			token:   "tok",
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := terminalWSURL(c.base, c.token, c.shell)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for base %q", c.base)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("terminalWSURL:\n got %q\nwant %q", got, c.want)
			}
			// Sanity: token must always be present (WS clients rely on it).
			if !strings.Contains(got, "token="+c.token) {
				t.Fatalf("token missing from %q", got)
			}
		})
	}
}
