package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayPasswordForBaseUsesConfiguredRelayPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &Config{
		RelayPassword: "relay-secret",
		RelayServers: []RelayServerConfig{
			{ID: "relay-1", HttpURL: "https://relay.example.com", Password: "per-relay-secret"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got, err := relayPasswordForBase("https://relay.example.com/d/device-123")
	if err != nil {
		t.Fatalf("relayPasswordForBase() error = %v", err)
	}
	if got != "per-relay-secret" {
		t.Fatalf("relayPasswordForBase() = %q, want per-relay-secret", got)
	}
}

func TestRemoteAgentJSONRejectsUntrustedRelayOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{ID: "relay-1", HttpURL: "https://relay.example.com", Password: "per-relay-secret"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	baseURL := server.URL + "/d/device-123"
	var out map[string]any
	err := remoteAgentJSON(context.Background(), baseURL, "token-123", http.MethodGet, "/health", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("remoteAgentJSON() error = %v, want untrusted relay error", err)
	}
}

func TestExecHTTPAddsRelayPasswordHeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("Authorization header = %q", got)
		}
		if got := r.Header.Get("X-Relay-Password"); got != "relay-secret" {
			t.Fatalf("X-Relay-Password header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
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

	resp, err := execHTTP(http.MethodGet, strings.TrimRight(server.URL, "/")+"/d/device-123/health", "token-123", nil)
	if err != nil {
		t.Fatalf("execHTTP() error = %v", err)
	}
	if resp["ok"] != true {
		t.Fatalf("execHTTP() response ok = %v, want true", resp["ok"])
	}
}

func TestRelayPasswordForBaseRejectsInsecureRemoteRelay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{ID: "relay-1", HttpURL: "http://relay.example.com", Password: "relay-secret"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	_, err := relayPasswordForBase("http://relay.example.com/d/device-123")
	if err == nil || !strings.Contains(err.Error(), "refusing insecure relay url") {
		t.Fatalf("relayPasswordForBase() error = %v, want insecure relay rejection", err)
	}
}
