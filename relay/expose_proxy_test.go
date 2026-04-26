package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// tryExposeProxy must NOT eat path-based device routes
// (/d/{deviceId}/...), bus routes, or /health, even when the
// request's Host header is a subdomain of expose-domain. This was
// breaking web-headless against public.yaver.io: every
// `public.yaver.io/d/<id>/...` got 404'd as "subdomain 'public' not
// registered" before the path mux even saw it.
//
// Regression test for the smoke that surfaced this.
func TestTryExposeProxy_skipsPathBasedDeviceRoutes(t *testing.T) {
	s := NewRelayServer(0, 0, "", "", "yaver.io")
	cases := []struct {
		host string
		path string
	}{
		{"public.yaver.io", "/d/abc123/health"},
		{"public.yaver.io", "/d/abc123/dev/events"},
		{"relay.yaver.io", "/d/xyz/info"},
		{"public.yaver.io", "/bus/publish"},
		{"public.yaver.io", "/bus/subscribe"},
		{"public.yaver.io", "/health"},
		{"public.yaver.io", "/presence"},
		{"public.yaver.io", "/presence?ids=abc123"},
		{"public.yaver.io", "/tunnels"},
		{"public.yaver.io", "/admin/status"},
		{"public.yaver.io", "/admin/set-password"},
		{"public.yaver.io", "/admin/bandwidth"},
		{"public.yaver.io", "/"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "http://"+tc.host+tc.path, nil)
		req.Host = tc.host
		w := httptest.NewRecorder()
		handled := s.tryExposeProxy(w, req)
		if handled {
			t.Errorf("Host=%q Path=%q: expose handler ate the request (status=%d body=%q) — should fall through to path mux",
				tc.host, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestTryExposeProxy_handlesUnregisteredSubdomainForRealExposeRequests(t *testing.T) {
	s := NewRelayServer(0, 0, "", "", "yaver.io")
	// A real expose request: GET arbitrary-path on a subdomain that
	// hasn't been registered. Should still 404 with the existing
	// "subdomain not registered" message.
	req := httptest.NewRequest("GET", "http://myapp.yaver.io/some/app/route", nil)
	req.Host = "myapp.yaver.io"
	w := httptest.NewRecorder()
	if !s.tryExposeProxy(w, req) {
		t.Fatalf("expose handler returned false on a real expose-domain request")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unregistered subdomain, got %d body=%q",
			w.Code, w.Body.String())
	}
}

func TestTryExposeProxy_skipsNonExposeDomain(t *testing.T) {
	s := NewRelayServer(0, 0, "", "", "yaver.io")
	// Host doesn't end in .yaver.io — must fall through to path mux
	// regardless of whatever path it carries.
	req := httptest.NewRequest("GET", "http://10.0.0.5:8080/d/abc/health", nil)
	req.Host = "10.0.0.5:8080"
	w := httptest.NewRecorder()
	if s.tryExposeProxy(w, req) {
		t.Fatalf("expose handler ate a non-yaver.io host (status=%d)", w.Code)
	}
}
