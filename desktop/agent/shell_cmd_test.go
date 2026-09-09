package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

// Regression (2026-09-09): `yaver shell primary` walked an expired Tailscale
// candidate for 15-40 seconds before trying the healthy relay. The operation
// probe must promote the relay before any terminal WebSocket is opened.
func TestDialFirstTerminalCandidatePromotesLiveRelay(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			http.Error(w, "stale route", http.StatusServiceUnavailable)
			return
		}
		<-r.Context().Done()
	}))
	defer dead.Close()

	upgrader := websocket.Upgrader{}
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			defer conn.Close()
		}
	}))
	defer live.Close()

	candidates := []RemoteAgentCandidate{
		{DeviceID: "device-test", BaseURL: dead.URL, Kind: "tailscale"},
		{DeviceID: "device-test", BaseURL: live.URL, Kind: "relay"},
	}
	started := time.Now()
	conn, label, err := dialFirstTerminalCandidate(candidates, "token", "")
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	defer conn.Close()
	if !strings.Contains(label, "via relay") {
		t.Fatalf("live relay was not promoted, label=%q", label)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("dead preferred route delayed live relay by %s", elapsed)
	}
}

func TestTerminalCandidateTimeoutsAreBounded(t *testing.T) {
	if got := terminalCandidateTimeout("tailscale"); got > 2*time.Second {
		t.Fatalf("dead overlay timeout %s is too slow for relay fallback", got)
	}
	if got := terminalCandidateTimeout("relay"); got < terminalCandidateTimeout("tailscale") {
		t.Fatalf("public TLS relay needs at least the direct-route budget, got %s", got)
	}
}
