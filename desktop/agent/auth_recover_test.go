package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthRecoverRequiresProof(t *testing.T) {
	recoveryLimiter.reset()
	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"mode":"pair"}`))
	req.RemoteAddr = "192.168.1.10:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthRecoverRejectedWhenAgentHealthy(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("healthy-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	srv := &HTTPServer{token: "live-token"}
	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"healthy-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.20:40000"
	rec := httptest.NewRecorder()

	srv.handleAuthRecover(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestAuthRecoverSecretCannotStartDeviceCode(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-123"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-123","mode":"device-code"}`))
	req.RemoteAddr = "192.168.1.11:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestAuthRecoverHostTokenCanStartDeviceCode(t *testing.T) {
	recoveryLimiter.reset()
	oldVerify := verifyHostTokenFn
	oldRequest := requestDeviceCodeFn
	verifyHostTokenFn = func(bearer string) (bool, error) {
		return bearer == "host-token", nil
	}
	requestDeviceCodeFn = func(convexURL string) (*deviceCodeResponse, error) {
		return &deviceCodeResponse{
			DeviceCode: "dev-code",
			UserCode:   "user-code",
			ExpiresAt:  1735689600000,
		}, nil
	}
	defer func() {
		verifyHostTokenFn = oldVerify
		requestDeviceCodeFn = oldRequest
	}()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"mode":"device-code"}`))
	req.RemoteAddr = "192.168.1.12:40000"
	req.Header.Set("Authorization", "Bearer host-token")
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"mode":"device-code"`) {
		t.Fatalf("expected device-code response, got %s", rec.Body.String())
	}
}

func TestAuthRecoverPairWorksWithBootstrapSecret(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-456"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-456","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.13:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"pairCode"`) {
		t.Fatalf("expected pairCode in response, got %s", rec.Body.String())
	}
}

func TestAuthRecoverPairAllowedWhenAuthExpired(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("expired-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	srv := &HTTPServer{token: "stale-token"}
	srv.authExpired.Store(true)

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"expired-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.21:40000"
	rec := httptest.NewRecorder()

	srv.handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRecoverRateLimitsPerIP(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-789"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	mkReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-789","mode":"pair"}`))
		req.RemoteAddr = "192.168.1.14:40000"
		return req
	}

	first := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(first, mkReq())
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", first.Code)
	}

	second := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(second, mkReq())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", second.Code)
	}
}
