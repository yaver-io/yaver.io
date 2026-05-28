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

