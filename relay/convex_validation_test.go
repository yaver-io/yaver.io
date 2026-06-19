package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegister_OfficialRelayValidatesDeviceAndTokenViaConvex(t *testing.T) {
	var seen map[string]string
	convex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relay/validate" {
			t.Fatalf("path = %q, want /relay/validate", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode validation body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"userId":"user-1"}`))
	}))
	defer convex.Close()

	srv, addr, cleanup := startTestRelayQUICConvex(t, convex.URL)
	defer cleanup()
	_ = srv

	conn, resp, err := dialAndRegister(t, addr, "device-convex-1", "agent-token-1", "user-relay-pw")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer conn.CloseWithError(0, "test done")
	if !resp.OK || resp.Type != "registered" {
		t.Fatalf("expected registered response, got %+v", resp)
	}
	if seen["password"] != "user-relay-pw" {
		t.Fatalf("password = %q", seen["password"])
	}
	if seen["action"] != "register" {
		t.Fatalf("action = %q, want register", seen["action"])
	}
	if seen["deviceId"] != "device-convex-1" {
		t.Fatalf("deviceId = %q", seen["deviceId"])
	}
	if seen["token"] != "agent-token-1" {
		t.Fatalf("token = %q", seen["token"])
	}
}

func TestProxy_OfficialRelayRejectsConvexDeniedDeviceAccess(t *testing.T) {
	var seen map[string]string
	convex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relay/validate" {
			t.Fatalf("path = %q, want /relay/validate", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatalf("decode validation body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer convex.Close()

	srv := NewRelayServer(0, 0, "", convex.URL, "")
	req := httptest.NewRequest(http.MethodGet, "/d/device-owned-by-someone-else/health", nil)
	req.Header.Set("X-Relay-Password", "user-relay-pw")
	rr := httptest.NewRecorder()

	srv.handleProxy(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body=%s", rr.Code, rr.Body.String())
	}
	if seen["action"] != "proxy" {
		t.Fatalf("action = %q, want proxy", seen["action"])
	}
	if seen["deviceId"] != "device-owned-by-someone-else" {
		t.Fatalf("deviceId = %q", seen["deviceId"])
	}
	if seen["password"] != "user-relay-pw" {
		t.Fatalf("password = %q", seen["password"])
	}
}

func TestProxy_OfficialRelayRateLimitsByResolvedUser(t *testing.T) {
	convex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"userId":"user-rate-limited"}`))
	}))
	defer convex.Close()

	srv := NewRelayServer(0, 0, "", convex.URL, "")
	cfg := defaultAbuseGuardConfig()
	cfg.ProxyPerUserPerMin = 1
	cfg.ProxyBurstPerUser = 1
	cfg.ProxyPerIPPerMin = 100
	cfg.ProxyBurstPerIP = 100
	srv.abuseGuard = newAbuseGuard(cfg)

	req := httptest.NewRequest(http.MethodGet, "/d/device-rate-limit/health", nil)
	req.Header.Set("X-Relay-Password", "user-relay-pw")
	rr := httptest.NewRecorder()
	srv.handleProxy(rr, req)
	if rr.Code == http.StatusTooManyRequests {
		t.Fatalf("first request should pass user rate limiter, got 429")
	}

	req = httptest.NewRequest(http.MethodGet, "/d/device-rate-limit/health", nil)
	req.Header.Set("X-Relay-Password", "user-relay-pw")
	rr = httptest.NewRecorder()
	srv.handleProxy(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429 body=%s", rr.Code, rr.Body.String())
	}
}

func startTestRelayQUICConvex(t *testing.T, convexURL string) (*RelayServer, string, func()) {
	t.Helper()
	srv, addr, cleanup := startTestRelayQUIC(t, "")
	srv.convexURL = convexURL
	return srv, addr, cleanup
}
