package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAbuseGuardHTTPMiddleware_RateLimitsProxyByIP(t *testing.T) {
	cfg := defaultAbuseGuardConfig()
	cfg.ProxyPerIPPerMin = 1
	cfg.ProxyBurstPerIP = 1
	cfg.HTTPPerIPPerMin = 100
	cfg.HTTPBurstPerIP = 100
	g := newAbuseGuard(cfg)

	h := g.httpMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/d/device123/health", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("first proxy request expected 204, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/d/device123/health", nil)
	req.RemoteAddr = "203.0.113.10:1235"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second proxy request expected 429, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.10:1236"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("general health bucket should be separate, got %d", w.Code)
	}
}

func TestReadCappedBody_Returns413InsteadOfTruncating(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/d/device123/dev/build-native", strings.NewReader("abcdef"))
	req.ContentLength = 6
	w := httptest.NewRecorder()

	body, ok := readCappedBody(w, req, 5)
	if ok {
		t.Fatalf("expected body read to fail over cap, got ok body=%q", string(body))
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}

func TestAbuseGuardDeviceConcurrency(t *testing.T) {
	cfg := defaultAbuseGuardConfig()
	cfg.MaxConcurrentPerDevice = 1
	g := newAbuseGuard(cfg)

	if !g.tryEnterDevice("device-1") {
		t.Fatal("first device request should enter")
	}
	if g.tryEnterDevice("device-1") {
		t.Fatal("second concurrent device request should be denied")
	}
	g.leaveDevice("device-1")
	if !g.tryEnterDevice("device-1") {
		t.Fatal("device request should enter after leave")
	}
}

func TestDefaultBodyCapKeepsHermesBytecodeReloadHeadroom(t *testing.T) {
	cfg := defaultAbuseGuardConfig()
	if cfg.MaxRequestBodyBytes < 32<<20 {
		t.Fatalf("default proxy request cap too small for Hermes/dev bundle envelopes: %d", cfg.MaxRequestBodyBytes)
	}
}

func TestAbuseGuardQUICRegisterThrottle(t *testing.T) {
	cfg := defaultAbuseGuardConfig()
	cfg.QUICRegisterPerIPPerMin = 1
	cfg.QUICRegisterBurstPerIP = 1
	g := newAbuseGuard(cfg)

	if !g.allowQUICRegister("198.51.100.4:4433") {
		t.Fatal("first registration should be allowed")
	}
	if g.allowQUICRegister("198.51.100.4:4434") {
		t.Fatal("second registration should be throttled")
	}
	if !g.allowQUICRegister("198.51.100.5:4433") {
		t.Fatal("different IP should have an independent registration bucket")
	}
}

// TestClientIP_TrustedProxyGating locks relay-audit finding #1: forwarding
// headers are honored ONLY behind a trusted proxy; a direct-connect attacker
// cannot spoof CF-Connecting-IP to mint a fresh rate-limit bucket per request.
func TestClientIP_TrustedProxyGating(t *testing.T) {
	g := newAbuseGuard(defaultAbuseGuardConfig()) // default trusted set = Cloudflare

	// Behind a trusted proxy (peer IP in a Cloudflare range) → header honored.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "173.245.48.5:443" // 173.245.48.0/20 is Cloudflare
	req.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if got := g.clientIP(req); got != "9.9.9.9" {
		t.Fatalf("trusted proxy: expected forwarded client 9.9.9.9, got %q", got)
	}

	// THE ACTUAL DEPLOYMENT: nginx reverse proxy on localhost forwards X-Real-IP.
	// Must be honored or every request keys on 127.0.0.1 (one shared bucket).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Real-IP", "203.0.113.77")
	if got := g.clientIP(req); got != "203.0.113.77" {
		t.Fatalf("nginx localhost proxy: expected real client 203.0.113.77, got %q", got)
	}

	// Dockerized relay: immediate peer is the bridge gateway (private IP).
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:40000"
	req.Header.Set("X-Forwarded-For", "198.51.100.9, 172.17.0.1")
	if got := g.clientIP(req); got != "198.51.100.9" {
		t.Fatalf("docker gateway proxy: expected real client 198.51.100.9, got %q", got)
	}

	// Direct connect from an untrusted peer → spoofed header IGNORED, keyed on
	// the real socket IP. This is the whole point of the fix.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:1234" // not a trusted proxy
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	req.Header.Set("X-Forwarded-For", "5.6.7.8")
	if got := g.clientIP(req); got != "203.0.113.10" {
		t.Fatalf("SECURITY: untrusted peer spoofed its rate-limit key to %q", got)
	}

	// Explicit RELAY_TRUSTED_PROXIES override is respected.
	t.Setenv("RELAY_TRUSTED_PROXIES", "10.0.0.0/8")
	g2 := newAbuseGuard(defaultAbuseGuardConfig())
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:80"
	req.Header.Set("X-Real-IP", "8.8.8.8")
	if got := g2.clientIP(req); got != "8.8.8.8" {
		t.Fatalf("custom trusted proxy: expected 8.8.8.8, got %q", got)
	}
	// A Cloudflare IP is no longer trusted once overridden.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "173.245.48.5:443"
	req.Header.Set("X-Real-IP", "8.8.8.8")
	if got := g2.clientIP(req); got != "173.245.48.5" {
		t.Fatalf("override should distrust Cloudflare, keyed real IP; got %q", got)
	}
}
