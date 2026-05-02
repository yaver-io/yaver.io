package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// M-6 (audit 2026-05-02): registering with an empty/short/reserved
// deviceId must be rejected at the QUIC handshake, not silently
// accepted. Pre-fix the agent could claim arbitrary subdomains
// (admin.<exposeDomain> in particular) just by connecting.
func TestRegister_RejectsBadDeviceIDShape(t *testing.T) {
	srv, addr, cleanup := startTestRelayQUIC(t, "test-pw")
	defer cleanup()
	_ = srv

	cases := []struct {
		name    string
		device  string
		wantMsg string
	}{
		// Too short (under 8 chars).
		{"too-short-1char", "a", "invalid deviceId shape"},
		{"too-short-7chars", "1234567", "invalid deviceId shape"},

		// Path-traversal shaped.
		{"slash", "abc/def/ghi", "invalid deviceId shape"},
		{"dotdot", "../etc/passwd", "invalid deviceId shape"},

		// Shell metacharacters.
		{"semicolon", "abc;rm-rf", "invalid deviceId shape"},
		{"dollar", "abc$(curl)", "invalid deviceId shape"},
		{"quote", `abc"def`, "invalid deviceId shape"},

		// Way too long (> 128).
		{"long", string(make([]byte, 200)), "invalid deviceId shape"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// pad bytes for the long case to ASCII so they aren't valid hex
			device := tc.device
			if tc.name == "long" {
				b := make([]byte, 200)
				for i := range b {
					b[i] = 'a'
				}
				device = string(b)
			}
			conn, resp, err := dialAndRegister(t, addr, device, "tok", "test-pw")
			if conn != nil {
				defer conn.CloseWithError(0, "test cleanup")
			}
			_ = err // ReadAll on a closed stream is normal here
			if resp.OK {
				t.Fatalf("expected refusal for deviceId=%q, got OK=true", device)
			}
			if resp.Message != tc.wantMsg {
				t.Fatalf("deviceId=%q: expected message %q, got %q", device, tc.wantMsg, resp.Message)
			}
		})
	}
}

// M-6: a deviceId that lower-cases to a reserved subdomain alias must
// be refused when expose-domain is configured (otherwise it would
// auto-claim e.g. admin.<exposeDomain>).
//
// Note: today's shape regex (8-char minimum) means none of the existing
// reserved aliases (admin/api/www/public/relay/mail/app — all <8 chars)
// can ever reach the reserved check via the QUIC path; the shape check
// rejects them first. The reserved-list gate is defense-in-depth for
// any future longer alias additions or shape-regex relaxation.
//
// We exercise both layers:
//   1. The shape-regex layer with a short reserved alias ("admin").
//   2. The reserved-list layer by injecting a long alias for the
//      duration of this test, since real reserved aliases are short.
func TestRegister_RejectsReservedDeviceIDWhenExposeConfigured(t *testing.T) {
	// Layer 1: short reserved alias is shape-rejected.
	srv, addr, cleanup := startTestRelayQUICWithExpose(t, "test-pw", "yaver.io")
	defer cleanup()
	_ = srv

	for _, name := range []string{"admin", "api", "www"} {
		t.Run("shape_rejects_short_alias_"+name, func(t *testing.T) {
			conn, resp, _ := dialAndRegister(t, addr, name, "tok", "test-pw")
			if conn != nil {
				defer conn.CloseWithError(0, "test cleanup")
			}
			if resp.OK {
				t.Fatalf("expected refusal for short reserved alias %q, got OK", name)
			}
			if resp.Message != "invalid deviceId shape" {
				t.Fatalf("alias=%q: expected shape rejection, got %q", name, resp.Message)
			}
		})
	}

	// Layer 2: inject a long-enough reserved alias and confirm it
	// trips the reserved-list path. Use t.Cleanup so the global
	// registry is restored even if the assertion below fatals.
	const longReserved = "monitoring"
	if reservedSubdomains[longReserved] {
		t.Fatalf("test bug: %q is already in reservedSubdomains; pick another", longReserved)
	}
	reservedSubdomains[longReserved] = true
	t.Cleanup(func() { delete(reservedSubdomains, longReserved) })

	conn, resp, _ := dialAndRegister(t, addr, longReserved, "tok", "test-pw")
	if conn != nil {
		defer conn.CloseWithError(0, "test cleanup")
	}
	if resp.OK {
		t.Fatalf("expected refusal for long reserved alias %q, got OK", longReserved)
	}
	if resp.Message != "deviceId reserved" {
		t.Fatalf("alias=%q: expected reserved rejection, got %q", longReserved, resp.Message)
	}

	// Same-name with different case still gets rejected (lower-cased
	// match).
	conn2, resp2, _ := dialAndRegister(t, addr, "MONITORING", "tok", "test-pw")
	if conn2 != nil {
		defer conn2.CloseWithError(0, "test cleanup")
	}
	if resp2.OK {
		t.Fatalf("expected refusal for upper-case reserved alias, got OK")
	}
	if resp2.Message != "deviceId reserved" {
		t.Fatalf("expected reserved rejection on upper-case alias, got %q", resp2.Message)
	}

	// And a non-reserved long deviceId still registers cleanly.
	conn3, resp3, _ := dialAndRegister(t, addr, "device-not-reserved", "tok", "test-pw")
	if conn3 != nil {
		defer conn3.CloseWithError(0, "test cleanup")
	}
	if !resp3.OK {
		t.Fatalf("expected non-reserved long deviceId to register, got %q", resp3.Message)
	}
}

// M-6: confirm the canonical reserved aliases ARE in the registry.
// Catches accidental list deletions in future refactors.
func TestReservedSubdomains_IncludesCanonicalAliases(t *testing.T) {
	for _, alias := range []string{"www", "api", "relay", "public", "admin", "mail", "app"} {
		if !reservedSubdomains[alias] {
			t.Errorf("expected reservedSubdomains[%q] = true", alias)
		}
	}
}

// startTestRelayQUICWithExpose mirrors startTestRelayQUIC but sets
// the expose-domain so the reserved-subdomain check fires.
func startTestRelayQUICWithExpose(t *testing.T, password, exposeDomain string) (*RelayServer, string, func()) {
	t.Helper()
	srv := NewRelayServer(0, 0, password, "", exposeDomain)

	tlsCfg, err := generateRelayTLS()
	if err != nil {
		t.Fatalf("tls: %v", err)
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	tr := &quic.Transport{Conn: udpConn}
	listener, err := tr.Listen(tlsCfg, &quic.Config{
		MaxIdleTimeout:  30 * time.Second,
		KeepAlivePeriod: 5 * time.Second,
	})
	if err != nil {
		udpConn.Close()
		t.Fatalf("quic listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			session, err := listener.Accept(ctx)
			if err != nil {
				return
			}
			go srv.handleAgentConnection(ctx, session)
		}
	}()
	addr := udpConn.LocalAddr().String()
	cleanup := func() {
		cancel()
		listener.Close()
		udpConn.Close()
	}
	return srv, addr, cleanup
}
