package main

import (
	"context"
	"testing"
	"time"
)

func TestCoarseRegion(t *testing.T) {
	cases := []struct {
		country, continent, want string
	}{
		{"US", "NA", "us"},
		{"DE", "EU", "eu"},
		{"TR", "AS", "ap"}, // Turkey's continentCode is AS in ip-api
		{"GB", "EU", "eu"},
		{"BR", "SA", "sa"},
		{"ZA", "AF", "af"},
		{"AU", "OC", "oc"},
		{"CA", "NA", "na"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := coarseRegion(c.country, c.continent); got != c.want {
			t.Errorf("coarseRegion(%q,%q) = %q, want %q", c.country, c.continent, got, c.want)
		}
	}
}

func TestParseASN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AS24940 Hetzner Online GmbH", "AS24940"},
		{"AS13335 Cloudflare, Inc.", "AS13335"},
		{"Hetzner Online GmbH", ""},
		{"", ""},
		{"ASXYZ not a number", ""},
		{"as7922 Comcast", "AS7922"},
	}
	for _, c := range cases {
		if got := parseASN(c.in); got != c.want {
			t.Errorf("parseASN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDetectEgressIdentityDisabled verifies the opt-out short-circuits before
// any network probe.
func TestDetectEgressIdentityDisabled(t *testing.T) {
	resetEgressCache()
	id := detectEgressIdentity(context.Background(), &Config{DisableAutoPublicIP: true}, false)
	if id.Source != "disabled" {
		t.Fatalf("expected source 'disabled', got %q", id.Source)
	}
	if id.IP != "" {
		t.Fatalf("disabled egress must not carry an IP, got %q", id.IP)
	}
}

// TestDetectEgressIdentityWithStubbedGeo injects a cached IP (so detectAutoPublicIP
// returns without a network call) and stubs geo resolution, then checks the
// assembled identity and that the second call is served from cache.
func TestDetectEgressIdentityWithStubbedGeo(t *testing.T) {
	resetEgressCache()

	// Inject a fresh public-IP cache entry so no network probe happens.
	autoPublicIPCache.mu.Lock()
	autoPublicIPCache.ip = "203.0.113.10"
	autoPublicIPCache.ts = time.Now()
	autoPublicIPCache.mu.Unlock()
	defer resetAutoPublicIPCache()

	var geoCalls int
	orig := resolveEgressGeo
	resolveEgressGeo = func(ctx context.Context, ip string) (EgressIdentity, bool) {
		geoCalls++
		if ip != "203.0.113.10" {
			t.Errorf("geo called with unexpected ip %q", ip)
		}
		return EgressIdentity{
			Country:     "DE",
			Region:      "eu",
			RegionName:  "Saxony",
			City:        "Falkenstein",
			ASN:         "AS24940",
			Org:         "Hetzner",
			Stable:      true,
			StableKnown: true,
			GeoSource:   "stub",
		}, true
	}
	defer func() { resolveEgressGeo = orig }()

	id := detectEgressIdentity(context.Background(), &Config{}, false)
	if id.Source != "probe" {
		t.Fatalf("first call source = %q, want 'probe'", id.Source)
	}
	if id.IP != "203.0.113.10" || id.Country != "DE" || id.Region != "eu" || id.ASN != "AS24940" {
		t.Fatalf("identity not assembled from geo: %+v", id)
	}
	if !id.Stable || !id.StableKnown {
		t.Fatalf("expected stable+known for hosting egress: %+v", id)
	}

	// Second call within TTL for the same IP must be served from cache (no new
	// geo lookup).
	id2 := detectEgressIdentity(context.Background(), &Config{}, false)
	if id2.Source != "cache" {
		t.Fatalf("second call source = %q, want 'cache'", id2.Source)
	}
	if geoCalls != 1 {
		t.Fatalf("geo resolved %d times, want exactly 1 (second call should hit cache)", geoCalls)
	}
	if id2.IP != id.IP || id2.ASN != id.ASN {
		t.Fatalf("cached identity drifted: %+v vs %+v", id2, id)
	}
}

func TestRuntimeEgressVerbRegistered(t *testing.T) {
	opsRegistryMu.RLock()
	spec, ok := opsRegistry["runtime_egress"]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatal("runtime_egress ops verb not registered")
	}
	if spec.AllowGuest {
		t.Fatal("runtime_egress must be owner-only (egress IP is owner provenance)")
	}
	if spec.Handler == nil {
		t.Fatal("runtime_egress has no handler")
	}
}
