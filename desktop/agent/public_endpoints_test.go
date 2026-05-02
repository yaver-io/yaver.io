package main

import (
	"reflect"
	"testing"
)

func TestConfiguredPublicEndpointsManualList(t *testing.T) {
	cfg := &Config{
		PublicEndpoints: []string{"198.51.100.20", "https://example.com/"},
	}
	got := configuredPublicEndpoints(cfg)
	want := []string{"198.51.100.20", "https://example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manual: got %#v, want %#v", got, want)
	}
}

func TestConfiguredPublicEndpointsManualBeatsCloudflare(t *testing.T) {
	cfg := &Config{
		PublicEndpoints: []string{"198.51.100.20"},
		CloudflareTunnels: []CloudflareTunnelConfig{
			{URL: "https://tunnel.example.com", Priority: 5},
		},
	}
	got := configuredPublicEndpoints(cfg)
	if len(got) < 1 || got[0] != "198.51.100.20" {
		t.Fatalf("manual entry must come first, got %#v", got)
	}
}

func TestConfiguredPublicEndpointsDeduplicates(t *testing.T) {
	cfg := &Config{
		PublicEndpoints: []string{"https://a.example.com", "https://a.example.com/"},
	}
	got := configuredPublicEndpoints(cfg)
	want := []string{"https://a.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dedupe: got %#v, want %#v", got, want)
	}
}
