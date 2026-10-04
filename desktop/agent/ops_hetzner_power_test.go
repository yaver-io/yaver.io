package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHetznerPowerUsesExactBoundedProviderAction(t *testing.T) {
	originalBase := hetznerAPIBase
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"action":{"id":91,"status":"success"}}`))
	}))
	hetznerAPIBase = server.URL
	t.Cleanup(func() { hetznerAPIBase = originalBase; server.Close() })

	if err := hetznerServerPower("test-token", "123", "shutdown"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/servers/123/actions/shutdown" {
		t.Fatalf("unexpected provider path %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatal("provider credential was not confined to the authorization header")
	}
	if err := hetznerServerPower("test-token", "123", "delete"); err == nil {
		t.Fatal("unsupported destructive action accepted")
	}
}

func TestHetznerPowerVerbIsCompanionDiscoverableAndFailsClosedWithoutAccount(t *testing.T) {
	spec, ok := func() (opsVerbSpec, bool) {
		opsRegistryMu.RLock()
		defer opsRegistryMu.RUnlock()
		value, found := opsRegistry["hetzner_power"]
		return value, found
	}()
	if !ok || !spec.AllowCompanion {
		t.Fatal("hetzner_power must be discoverable by owner companion surfaces")
	}
	result := spec.Handler(OpsContext{}, []byte(`{"action":"shutdown","serverId":"123","confirm":false}`))
	if result.OK || result.Code != "no_account" {
		// Credential availability is checked before confirmation to avoid an
		// action oracle on endpoints that do not own a Hetzner credential.
		t.Fatalf("unexpected fail-closed result: %#v", result)
	}
}

func TestHetznerPowerRejectsInspectableRelayTransportBeforeReadingCredential(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Yaver-Via-Relay", "1")
	result := opsHetznerPowerHandler(OpsContext{RequestHeaders: headers}, []byte(`{"action":"list"}`))
	if result.OK || result.Code != "secure_transport_required" {
		t.Fatalf("inspectable relay transport was not rejected: %#v", result)
	}
}

func TestHetznerPowerRejectsBrowserSurfaceBeforeReadingCredential(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Yaver-Surface", "web")
	result := opsHetznerPowerHandler(OpsContext{RequestHeaders: headers}, []byte(`{"action":"list"}`))
	if result.OK || result.Code != "unsupported_surface" {
		t.Fatalf("browser surface was not rejected: %#v", result)
	}
}
