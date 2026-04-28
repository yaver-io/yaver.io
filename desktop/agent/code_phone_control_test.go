package main

import (
	"strings"
	"testing"
)

// code_phone_control_test.go pins target alias resolution. The push
// step itself is exercised end-to-end by the existing
// phone_two_agent_test.go suite — what's worth pinning here is that
// no typo silently routes the bundle to the wrong place.

func TestResolvePushTarget_DevHwAliases(t *testing.T) {
	for _, raw := range []string{"dev-hw", "DEV-HW", "DevHw", "local"} {
		got, err := resolvePushTarget(raw)
		if err != nil {
			t.Errorf("resolvePushTarget(%q) errored: %v", raw, err)
			continue
		}
		if got.baseURL != "http://127.0.0.1:18080" {
			t.Errorf("dev-hw alias %q resolved to %q; want http://127.0.0.1:18080", raw, got.baseURL)
		}
		if got.label != "dev-hw" {
			t.Errorf("dev-hw alias %q label = %q; want dev-hw", raw, got.label)
		}
	}
}

func TestResolvePushTarget_CloudUsesEnvOverride(t *testing.T) {
	t.Setenv("YAVER_CLOUD_URL", "https://kivanc-prod.cloud.yaver.io")
	got, err := resolvePushTarget("yaver-cloud")
	if err != nil {
		t.Fatalf("resolvePushTarget: %v", err)
	}
	if got.baseURL != "https://kivanc-prod.cloud.yaver.io" {
		t.Fatalf("YAVER_CLOUD_URL ignored; got %q", got.baseURL)
	}
}

func TestResolvePushTarget_CloudDefault(t *testing.T) {
	t.Setenv("YAVER_CLOUD_URL", "")
	got, err := resolvePushTarget("yaver-cloud")
	if err != nil {
		t.Fatalf("resolvePushTarget: %v", err)
	}
	if got.baseURL != "https://cloud.yaver.io" {
		t.Fatalf("default cloud URL changed; got %q", got.baseURL)
	}
}

func TestResolvePushTarget_LiteralURL(t *testing.T) {
	for _, raw := range []string{"https://example.com", "http://localhost:9000/", "https://relay.yaver.io/d/abc/"} {
		got, err := resolvePushTarget(raw)
		if err != nil {
			t.Errorf("resolvePushTarget(%q) errored: %v", raw, err)
			continue
		}
		// Trailing slash should be stripped — the receive endpoint is
		// always concatenated with /phone/projects/receive.
		if strings.HasSuffix(got.baseURL, "/") {
			t.Errorf("trailing slash not stripped from %q → %q", raw, got.baseURL)
		}
	}
}

func TestResolvePushTarget_UnknownAliasIsRejected(t *testing.T) {
	cases := []string{"hetznwer", "yaver_cloud", "DEV HW", "ftp://x"}
	for _, raw := range cases {
		_, err := resolvePushTarget(raw)
		if err == nil {
			t.Errorf("resolvePushTarget(%q) accepted a typo — would route bundle to the wrong place", raw)
		}
	}
}

func TestResolvePushTarget_EmptyIsRejected(t *testing.T) {
	if _, err := resolvePushTarget(""); err == nil {
		t.Fatal("empty target must error rather than default silently")
	}
	if _, err := resolvePushTarget("   "); err == nil {
		t.Fatal("whitespace-only target must error rather than default silently")
	}
}
