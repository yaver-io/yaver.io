package main

import "testing"

func TestParseQuickTunnelURL(t *testing.T) {
	line := "2026-10-11T10:00:00Z INF |  https://fuzzy-lamp-abc.trycloudflare.com  |"
	if got := parseQuickTunnelURL(line); got != "https://fuzzy-lamp-abc.trycloudflare.com" {
		t.Fatalf("got %q", got)
	}
	if got := parseQuickTunnelURL("POST https://api.trycloudflare.com/tunnel"); got != "" {
		t.Fatalf("control host must not be treated as a tunnel: %q", got)
	}
}

func TestAutoTunnelDecision(t *testing.T) {
	if r, ok := autoTunnelDecision(&Config{}, false, true); ok || r != "tailscale_active" {
		t.Fatalf("tailscale should win: %q %v", r, ok)
	}
	if _, ok := autoTunnelDecision(&Config{}, false, false); !ok {
		t.Fatal("should run by default without tailscale")
	}
	if r, ok := autoTunnelDecision(&Config{CloudflareTunnels: []CloudflareTunnelConfig{{URL: "https://x"}}}, false, false); ok || r != "user_tunnel_configured" {
		t.Fatal(r)
	}
}

func TestAutoTunnelURLOnlyWhenUp(t *testing.T) {
	setAutoTunnel("backoff", "tunnel_exited", "https://old.trycloudflare.com", "")
	if autoTunnelURL() != "" {
		t.Fatal("dead tunnel URL must not be advertised")
	}
	setAutoTunnel("up", "", "https://a.trycloudflare.com", "")
	if autoTunnelURL() == "" {
		t.Fatal("live URL missing")
	}
}

func TestCloudflaredAsset(t *testing.T) {
	if n, tgz, err := cloudflaredAsset("darwin", "arm64"); err != nil || !tgz || n != "cloudflared-darwin-arm64.tgz" {
		t.Fatal(n, tgz, err)
	}
	if _, _, err := cloudflaredAsset("linux", "riscv64"); err == nil {
		t.Fatal("expected unsupported arch error")
	}
}
