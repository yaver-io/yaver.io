package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveReachPingLookupHintElevatedSlots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"settings": map[string]any{
				"primaryDeviceId":   "primary-device-123",
				"secondaryDeviceId": "secondary-device-456",
			},
		})
	}))
	defer srv.Close()

	cfg := &Config{AuthToken: "test-token", ConvexSiteURL: srv.URL}
	ctx := context.Background()

	got, err := resolveReachPingLookupHint(ctx, cfg, "primary")
	if err != nil {
		t.Fatalf("primary resolve: %v", err)
	}
	if got != "primary-device-123" {
		t.Fatalf("primary resolved to %q", got)
	}

	got, err = resolveReachPingLookupHint(ctx, cfg, "secondary")
	if err != nil {
		t.Fatalf("secondary resolve: %v", err)
	}
	if got != "secondary-device-456" {
		t.Fatalf("secondary resolved to %q", got)
	}

	got, err = resolveReachPingLookupHint(ctx, cfg, "mac-mini")
	if err != nil {
		t.Fatalf("plain hint resolve: %v", err)
	}
	if got != "mac-mini" {
		t.Fatalf("plain hint resolved to %q", got)
	}
}
