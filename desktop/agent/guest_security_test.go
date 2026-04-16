package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuestAllowedPathBlocksGuestManagementEndpoints(t *testing.T) {
	for _, path := range []string{"/guests", "/guests/invite", "/guests/revoke", "/guests/config", "/guests/usage"} {
		if isGuestAllowedPath(path) {
			t.Fatalf("expected guest path %q to be blocked", path)
		}
	}
}

func TestGuestManagementHandlersRejectGuestRequests(t *testing.T) {
	srv := &HTTPServer{}
	cases := []struct {
		name    string
		method  string
		target  string
		handler http.HandlerFunc
	}{
		{name: "guest list", method: http.MethodGet, target: "/guests", handler: srv.handleGuestList},
		{name: "guest config", method: http.MethodGet, target: "/guests/config", handler: srv.handleGuestConfig},
		{name: "guest usage", method: http.MethodGet, target: "/guests/usage", handler: srv.handleGuestUsage},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.Header.Set("X-Yaver-Guest", "true")
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", rec.Code)
			}
		})
	}
}
