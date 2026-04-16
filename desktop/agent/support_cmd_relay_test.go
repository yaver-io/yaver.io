package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSupportRequestAddsRelayPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer yv_supp_token" {
			t.Fatalf("Authorization header = %q", got)
		}
		if got := r.Header.Get("X-Relay-Password"); got != "relay-secret" {
			t.Fatalf("X-Relay-Password header = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{ID: "relay-1", HttpURL: server.URL, Password: "relay-secret"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	req, err := supportRequest(http.MethodGet, server.URL+"/d/device-123/exec/abc", "yv_supp_token", nil)
	if err != nil {
		t.Fatalf("supportRequest() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer yv_supp_token" {
		t.Fatalf("Authorization header = %q", got)
	}
	if got := req.Header.Get("X-Relay-Password"); got != "relay-secret" {
		t.Fatalf("X-Relay-Password header = %q", got)
	}
}
