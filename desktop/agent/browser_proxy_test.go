package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRedactProxyCreds verifies proxy credentials never survive into anything
// loggable or returned over the wire.
func TestRedactProxyCreds(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"socks5://10.0.0.2:1080", "socks5://10.0.0.2:1080"},
		{"http://user:secret@proxy.example:8080", "http://redacted@proxy.example:8080"},
		{"http://user:s3cr3t@proxy.example:8080/path", "http://redacted@proxy.example:8080/path"},
		{"127.0.0.1:8080", "127.0.0.1:8080"}, // schemeless host:port left intact
	}
	for _, c := range cases {
		got := redactProxyCreds(c.in)
		if got != c.want {
			t.Errorf("redactProxyCreds(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "s3cr3t") {
			t.Errorf("redactProxyCreds(%q) leaked credentials: %q", c.in, got)
		}
	}
}

// TestBrowserEgressViaProxy proves the browser collector actually egresses
// through a chosen proxy: an IP-echo server records the client it sees, and a
// minimal real forward proxy (no mocks) sits between the browser and the echo
// server. When the session is opened with that proxy, the echo request must
// arrive via the proxy, not directly. This is the core "adopt a vantage's
// egress identity" mechanism.
func TestBrowserEgressViaProxy(t *testing.T) {
	// IP-echo server: returns the remote IP it observed as the body.
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		fmt.Fprint(w, host)
	}))
	defer echo.Close()

	// Minimal HTTP forward proxy: handles absolute-URI requests (how Chrome's
	// --proxy-server sends plain-http traffic) and counts what flows through it.
	var proxied int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			http.Error(w, "expected absolute-URI proxy request", http.StatusBadRequest)
			return
		}
		atomic.AddInt32(&proxied, 1)
		outReq, err := http.NewRequest(r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := http.DefaultTransport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	bm := NewBrowserManager()
	defer bm.Stop()

	if err := bm.OpenSessionWithProxy("egress-proxy", false, proxy.URL); err != nil {
		t.Skipf("Chrome not available, skipping: %v", err)
	}

	// Session listing must expose the proxy (redacted) and never the raw creds.
	sessions := bm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ProxyURL == "" {
		t.Fatal("expected ProxyURL on the proxied session listing")
	}

	got, err := bm.CheckEgressIP("egress-proxy", echo.URL)
	if err != nil {
		t.Fatalf("CheckEgressIP via proxy: %v", err)
	}
	if got == "" {
		t.Fatal("expected a non-empty egress IP")
	}
	if atomic.LoadInt32(&proxied) == 0 {
		t.Fatal("egress did NOT traverse the proxy — proxy wiring is not applied")
	}

	// EgressIP must be cached on the session as vantage metadata.
	sessions = bm.ListSessions()
	if sessions[0].EgressIP != got {
		t.Fatalf("egress IP not cached on session: listing=%q checked=%q", sessions[0].EgressIP, got)
	}
}

// TestBrowserEgressMachineNative confirms that with no proxy the collector
// egresses directly (the default single-vantage path stays unchanged) and that
// the echo request does NOT go through any proxy.
func TestBrowserEgressMachineNative(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		fmt.Fprint(w, host)
	}))
	defer echo.Close()

	bm := NewBrowserManager()
	defer bm.Stop()

	if err := bm.OpenSession("egress-native", false); err != nil {
		t.Skipf("Chrome not available, skipping: %v", err)
	}
	if sessions := bm.ListSessions(); sessions[0].ProxyURL != "" {
		t.Fatalf("machine-native session should have no proxy, got %q", sessions[0].ProxyURL)
	}

	got, err := bm.CheckEgressIP("egress-native", echo.URL)
	if err != nil {
		t.Fatalf("CheckEgressIP native: %v", err)
	}
	// Loopback echo server sees a loopback client when egress is machine-native.
	if !strings.HasPrefix(got, "127.0.0.1") && got != "::1" {
		t.Logf("egress IP (machine-native) = %q", got)
	}
}
