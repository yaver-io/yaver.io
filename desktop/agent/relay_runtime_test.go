package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeRelayConfigsFallsBackToCachedRelays(t *testing.T) {
	cfg := &Config{
		CachedRelayPassword: "cached-global",
		CachedRelayServers: []RelayServerConfig{
			{ID: "public-free", QuicAddr: "relay.example.com:4433", HttpURL: "https://public.yaver.io", Password: "cached-per-relay"},
		},
	}

	got := runtimeRelayConfigs(cfg)
	if len(got) != 1 || got[0].ID != "public-free" {
		t.Fatalf("runtimeRelayConfigs() = %+v, want cached relay", got)
	}
	if pw := runtimeRelayPassword(cfg); pw != "cached-global" {
		t.Fatalf("runtimeRelayPassword() = %q, want cached-global", pw)
	}
}

func TestRuntimeRelayConfigsAppendsCachedFallbackRelays(t *testing.T) {
	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{ID: "private", QuicAddr: "private.example.com:4433", HttpURL: "https://private.example.com"},
		},
		CachedRelayServers: []RelayServerConfig{
			{ID: "public-free", QuicAddr: "public.example.com:4433", HttpURL: "https://public.yaver.io"},
		},
	}

	got := runtimeRelayConfigs(cfg)
	if len(got) != 2 {
		t.Fatalf("runtimeRelayConfigs() len = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "private" || got[1].ID != "public-free" {
		t.Fatalf("runtimeRelayConfigs() = %+v, want private first then public fallback", got)
	}
}

func TestRuntimeRelayConfigsDedupesCachedFallbackRelays(t *testing.T) {
	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{ID: "private", QuicAddr: "relay.example.com:4433", HttpURL: "https://relay.example.com"},
		},
		CachedRelayServers: []RelayServerConfig{
			{ID: "same", QuicAddr: "relay.example.com:4433", HttpURL: "https://relay.example.com"},
			{ID: "public-free", QuicAddr: "public.example.com:4433", HttpURL: "https://public.yaver.io"},
		},
	}

	got := runtimeRelayConfigs(cfg)
	if len(got) != 2 {
		t.Fatalf("runtimeRelayConfigs() len = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "private" || got[1].ID != "public-free" {
		t.Fatalf("runtimeRelayConfigs() = %+v, want duplicate skipped and public fallback appended", got)
	}
}

func TestCurrentRelayCredentialsUsesCachedPerRelayPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := SaveConfig(&Config{
		AuthToken:           "token-123",
		CachedRelayPassword: "cached-global",
		CachedRelayServers: []RelayServerConfig{
			{ID: "public-free", QuicAddr: "relay.example.com:4433", Password: "cached-per-relay"},
		},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	token, password := currentRelayCredentials("relay.example.com:4433")
	if token != "token-123" {
		t.Fatalf("token = %q, want token-123", token)
	}
	if password != "cached-per-relay" {
		t.Fatalf("password = %q, want cached-per-relay", password)
	}
}

func TestRelayWatchdogUsesCachedRelays(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := SaveConfig(&Config{
		AuthToken: "token-123",
		CachedRelayServers: []RelayServerConfig{
			{ID: "public-free", QuicAddr: "relay.example.com:4433"},
		},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	atomic.StoreInt32(&relayTunnelsLive, 0)
	cancelled := make(chan struct{}, 1)
	rm := newRelayManager(context.Background(), "device-123", "token-123", "127.0.0.1:18080", "relay-pw", "")
	rm.noTunnelSince = time.Now().Add(-2 * time.Minute)
	rm.registerAttemptCancel("relay.example.com:4433", func() { cancelled <- struct{}{} })

	rm.watchdogRelayTunnel()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("watchdogRelayTunnel() did not force reconnect for cached relay")
	}
}

func TestRelayHealthUsesCachedRelays(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"tunnels":1,"version":"test"}`))
	}))
	defer server.Close()

	if err := SaveConfig(&Config{
		CachedRelayServers: []RelayServerConfig{
			{ID: "public-free", QuicAddr: "127.0.0.1:4433", HttpURL: server.URL},
		},
	}); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	rm := newRelayManager(context.Background(), "device-123", "token-123", "127.0.0.1:18080", "relay-pw", "")
	rm.checkRelayHealth(server.Client())

	status := rm.healthStatus[server.URL]
	if status == nil {
		t.Fatalf("healthStatus missing cached relay %s", server.URL)
	}
	if !status.OK || status.Tunnels != 1 || status.Version != "test" {
		t.Fatalf("healthStatus = %+v, want ok tunnels=1 version=test", status)
	}
}
