package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityCORSRejectsUntrustedBrowserOrigin(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden preflight for untrusted origin, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("untrusted origin received CORS allow header %q", got)
	}
}

func TestSecurityCORSAllowsFirstPartyAndNoOriginClients(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://dashboard.yaver.io")
	req.Header.Set("Access-Control-Request-Headers", "authorization,x-device-id,x-platform,x-app-name")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected first-party preflight to pass, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.yaver.io" {
		t.Fatalf("expected echoed first-party origin, got %q", got)
	}
	if got := resp.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Client-Platform") {
		t.Fatalf("expected platform header to be allowed for RN-web/Selenium clients, got %q", got)
	}
	if got := resp.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Cache-Control") {
		t.Fatalf("expected cache-control header to be allowed for browser Dogfood preview probes, got %q", got)
	}
	for _, header := range []string{"X-Device-ID", "X-Platform", "X-App-Name"} {
		if got := resp.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, header) {
			t.Fatalf("expected feedback SDK header %s to be allowed for RN-web hosts, got %q", header, got)
		}
	}
	if got := resp.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "X-Yaver-DevServer") || !strings.Contains(got, "Retry-After") {
		t.Fatalf("expected browser Dogfood startup headers to be exposed, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	resp = httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard for no-Origin compatibility clients, got %q", got)
	}
}
