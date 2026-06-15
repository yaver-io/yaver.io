package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEgressTargetAllowed locks down the legal guardrails: disabled-by-default
// posture, RFC1918/loopback refusal (anti-LAN-pivot), and the port allowlist.
func TestEgressTargetAllowed(t *testing.T) {
	defaultPol := EgressProxyPolicy{Enabled: true} // ports default to 80/443
	privPol := EgressProxyPolicy{Enabled: true, AllowPrivateTargets: true, AllowedPorts: []int{80, 443, 9000}}

	cases := []struct {
		name   string
		pol    EgressProxyPolicy
		target string
		wantOK bool
	}{
		{"public ip 443", defaultPol, "8.8.8.8:443", true},
		{"public ip 80", defaultPol, "1.1.1.1:80", true},
		{"private 10/8 refused", defaultPol, "10.0.0.5:443", false},
		{"private 192.168 refused", defaultPol, "192.168.1.10:443", false},
		{"loopback refused", defaultPol, "127.0.0.1:443", false},
		{"link-local refused", defaultPol, "169.254.1.1:443", false},
		{"port not allowed", defaultPol, "8.8.8.8:9000", false},
		{"empty target", defaultPol, "", false},
		{"no port", defaultPol, "8.8.8.8", false},
		{"bad port", defaultPol, "8.8.8.8:0", false},
		{"private allowed when opted in", privPol, "127.0.0.1:9000", true},
	}
	for _, c := range cases {
		_, _, ok, reason := egressTargetAllowed(c.pol, c.target)
		if ok != c.wantOK {
			t.Errorf("%s: egressTargetAllowed(%q) ok=%v want=%v (reason=%q)", c.name, c.target, ok, c.wantOK, reason)
		}
	}
}

// startEchoTarget starts a loopback TCP echo server — stands in for the
// "public" destination the collector wants to reach.
func startEchoTarget(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) { _, _ = io.Copy(conn, conn) }(c)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

// startEgressProvider stands up the B side: an httptest server exposing
// /egress/proxy behind a bearer-token gate (simulating s.auth), driven by the
// given policy override.
func startEgressProvider(t *testing.T, token string, pol EgressProxyPolicy) (baseURL string, stop func()) {
	t.Helper()
	egressPolicyOverride = &pol

	mux := http.NewServeMux()
	srv := &HTTPServer{}
	mux.HandleFunc("/egress/proxy", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		srv.handleEgressProxy(w, r)
	})
	ts := httptest.NewServer(mux)
	return ts.URL, func() {
		ts.Close()
		egressPolicyOverride = nil
	}
}

// connectThroughBridge drives a raw HTTP CONNECT through the bridge and returns
// the status line plus the tunneled conn (after a 200) for the happy path.
func connectThroughBridge(t *testing.T, bridgeAddr, target string) (status string, conn net.Conn) {
	t.Helper()
	c, err := net.DialTimeout("tcp", bridgeAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		_ = c.Close()
		t.Fatalf("read CONNECT status: %v", err)
	}
	return strings.TrimSpace(line), c
}

// TestEgressBridgeEndToEnd proves the full chain works: a client CONNECTs to the
// local bridge, which tunnels (authenticated) to the provider's /egress/proxy,
// which dials the target and pipes bytes back.
func TestEgressBridgeEndToEnd(t *testing.T) {
	echoAddr, stopEcho := startEchoTarget(t)
	defer stopEcho()

	_, echoPortStr, _ := net.SplitHostPort(echoAddr)
	var echoPort int
	fmt.Sscanf(echoPortStr, "%d", &echoPort)

	token := "test-owner-token"
	baseURL, stopProvider := startEgressProvider(t, token, EgressProxyPolicy{
		Enabled:             true,
		AllowPrivateTargets: true, // echo target is loopback
		AllowedPorts:        []int{echoPort},
	})
	defer stopProvider()

	bridge, err := startEgressBridge("test-bridge", baseURL, token, nil)
	if err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	defer bridge.close()

	status, conn := connectThroughBridge(t, bridge.addr(), echoAddr)
	if !strings.Contains(status, "200") {
		t.Fatalf("expected 200 Connection Established, got %q", status)
	}
	defer conn.Close()

	// Bytes must round-trip through bridge -> provider -> echo target.
	payload := "hello-through-peer-egress"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read echo through tunnel: %v", err)
	}
	if string(buf) != payload {
		t.Fatalf("echo mismatch: got %q want %q", string(buf), payload)
	}
}

// TestEgressBridgeRefusals confirms the guardrails actually block at runtime:
// disabled lending, a private target without opt-in, and a disallowed port all
// fail the CONNECT (the bridge surfaces a non-200).
func TestEgressBridgeRefusals(t *testing.T) {
	echoAddr, stopEcho := startEchoTarget(t)
	defer stopEcho()
	_, echoPortStr, _ := net.SplitHostPort(echoAddr)
	var echoPort int
	fmt.Sscanf(echoPortStr, "%d", &echoPort)

	token := "test-owner-token"

	t.Run("disabled lending", func(t *testing.T) {
		baseURL, stop := startEgressProvider(t, token, EgressProxyPolicy{Enabled: false})
		defer stop()
		bridge, _ := startEgressBridge("b-disabled", baseURL, token, nil)
		defer bridge.close()
		status, conn := connectThroughBridge(t, bridge.addr(), echoAddr)
		if conn != nil {
			conn.Close()
		}
		if strings.Contains(status, "200") {
			t.Fatalf("disabled lending must NOT establish a tunnel, got %q", status)
		}
	})

	t.Run("private target without opt-in", func(t *testing.T) {
		baseURL, stop := startEgressProvider(t, token, EgressProxyPolicy{
			Enabled:      true,
			AllowedPorts: []int{echoPort},
			// AllowPrivateTargets defaults false — loopback echo must be refused.
		})
		defer stop()
		bridge, _ := startEgressBridge("b-priv", baseURL, token, nil)
		defer bridge.close()
		status, conn := connectThroughBridge(t, bridge.addr(), echoAddr)
		if conn != nil {
			conn.Close()
		}
		if strings.Contains(status, "200") {
			t.Fatalf("private target without opt-in must be refused, got %q", status)
		}
	})

	t.Run("wrong auth token", func(t *testing.T) {
		baseURL, stop := startEgressProvider(t, token, EgressProxyPolicy{
			Enabled: true, AllowPrivateTargets: true, AllowedPorts: []int{echoPort},
		})
		defer stop()
		bridge, _ := startEgressBridge("b-auth", baseURL, "WRONG-TOKEN", nil)
		defer bridge.close()
		status, conn := connectThroughBridge(t, bridge.addr(), echoAddr)
		if conn != nil {
			conn.Close()
		}
		if strings.Contains(status, "200") {
			t.Fatalf("wrong token must NOT establish a tunnel (not an open proxy), got %q", status)
		}
	})
}

func TestEgressProxyVerbsRegistered(t *testing.T) {
	for _, name := range []string{"egress_proxy_status", "egress_proxy_set", "egress_via_peer_start", "egress_via_peer_stop", "egress_via_peer_list"} {
		opsRegistryMu.RLock()
		spec, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Errorf("ops verb %q not registered", name)
			continue
		}
		if spec.AllowGuest {
			t.Errorf("ops verb %q must be owner-only (egress lending is sensitive)", name)
		}
	}
}
