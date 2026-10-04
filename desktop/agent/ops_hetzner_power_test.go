package main

import (
	"encoding/json"
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

func TestHetznerRenameUsesExactBoundedProviderUpdate(t *testing.T) {
	originalBase := hetznerAPIBase
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/servers/123" {
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("provider credential was not confined to the authorization header")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["name"] != "mn71pn9c.cloud.yaver.io" {
			t.Fatalf("unexpected rename body: %#v (%v)", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server":{"id":123,"name":"mn71pn9c.cloud.yaver.io"}}`))
	}))
	hetznerAPIBase = server.URL
	t.Cleanup(func() { hetznerAPIBase = originalBase; server.Close() })

	if err := hetznerServerRename("test-token", "123", "mn71pn9c.cloud.yaver.io"); err != nil {
		t.Fatal(err)
	}
}

func TestHetznerActivityIsReadOnlyAndRedactsProviderMessage(t *testing.T) {
	originalBase := hetznerAPIBase
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/servers/123/actions" || r.URL.Query().Get("sort") != "started:desc" {
			t.Fatalf("unexpected provider request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"actions":[{"id":91,"command":"shutdown_server","status":"error","started":"2026-10-04T20:00:00Z","progress":23,"error":{"code":"action_failed","message":"private server name"}}]}`))
	}))
	hetznerAPIBase = server.URL
	t.Cleanup(func() { hetznerAPIBase = originalBase; server.Close() })

	actions, err := hetznerServerActions("test-token", "123")
	if err != nil || len(actions) != 1 {
		t.Fatalf("activity lookup failed: %#v (%v)", actions, err)
	}
	encoded, _ := json.Marshal(actions)
	if string(encoded) == "" || actions[0].ErrorCode != "action_failed" {
		t.Fatalf("stable activity fields missing: %s", encoded)
	}
	if string(encoded) != "[{\"id\":91,\"command\":\"shutdown_server\",\"status\":\"error\",\"started\":\"2026-10-04T20:00:00Z\",\"progress\":23,\"errorCode\":\"action_failed\"}]" {
		t.Fatalf("provider message leaked or activity shape drifted: %s", encoded)
	}
}

func TestHetznerPowerVerbIsCompanionDiscoverableAndFailsClosedWithoutAccount(t *testing.T) {
	originalManager := globalAccountsManager
	globalAccountsManager = &AccountsManager{baseDir: t.TempDir()}
	t.Cleanup(func() { globalAccountsManager = originalManager })
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
