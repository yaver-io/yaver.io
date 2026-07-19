package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestRepairRelayPasswordForRemoteHTTPSyncsFreshPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var sawRepair bool
	var sawSettings bool
	convex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Fatalf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/settings/repair-relay":
			sawRepair = true
			_, _ = w.Write([]byte(`{"ok":true,"repaired":true}`))
		case "/settings":
			sawSettings = true
			_, _ = w.Write([]byte(`{"settings":{"relayUrl":"https://relay.example.com","relayPassword":"fresh-pw"}}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer convex.Close()

	cfg := &Config{
		ConvexSiteURL:       convex.URL,
		AuthToken:           "token-123",
		RelayPassword:       "old-global",
		CachedRelayPassword: "old-cached",
		RelayServers: []RelayServerConfig{
			{ID: "free", HttpURL: "https://relay.example.com", Password: "old-per-relay"},
			{ID: "custom", HttpURL: "https://custom.example.com", Password: "custom-pw"},
		},
		CachedRelayServers: []RelayServerConfig{
			{ID: "cached-free", HttpURL: "https://relay.example.com", Password: "old-cached-per-relay"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	if !repairRelayPasswordForRemoteHTTP(context.Background()) {
		t.Fatal("repairRelayPasswordForRemoteHTTP() = false")
	}
	if !sawRepair || !sawSettings {
		t.Fatalf("repair=%v settings=%v, want both endpoints called", sawRepair, sawSettings)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.RelayPassword != "fresh-pw" || loaded.CachedRelayPassword != "fresh-pw" {
		t.Fatalf("global relay passwords = %q/%q, want fresh-pw", loaded.RelayPassword, loaded.CachedRelayPassword)
	}
	if got := loaded.RelayServers[0].Password; got != "fresh-pw" {
		t.Fatalf("free relay password = %q, want fresh-pw", got)
	}
	if got := loaded.RelayServers[1].Password; got != "custom-pw" {
		t.Fatalf("custom relay password = %q, want preserved", got)
	}
	if got := loaded.CachedRelayServers[0].Password; got != "fresh-pw" {
		t.Fatalf("cached free relay password = %q, want fresh-pw", got)
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

func TestBuildRemoteAgentCandidatesPrefersLastGoodDirectPath(t *testing.T) {
	cfg := &Config{
		RelayServers: []RelayServerConfig{
			{ID: "relay-1", HttpURL: "https://relay.example.com", Password: "relay-secret", Priority: 1},
		},
	}
	target := &DeviceInfo{
		DeviceID: "dev-1",
		Name:     "mac-mini",
		QuicHost: "100.64.1.2",
		QuicPort: 18080,
		IsOnline: true,
	}
	remoteAgentLastGood.Store(target.DeviceID, "https://relay.example.com/d/dev-1")
	t.Cleanup(func() { remoteAgentLastGood.Delete(target.DeviceID) })

	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		t.Fatalf("buildRemoteAgentCandidates() error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d", len(candidates))
	}
	if candidates[0].BaseURL != "https://relay.example.com/d/dev-1" {
		t.Fatalf("first candidate = %q, want last-good relay first", candidates[0].BaseURL)
	}
	if candidates[1].Kind != "tailscale" {
		t.Fatalf("second candidate kind = %q, want tailscale", candidates[1].Kind)
	}
}

func TestDirectAgentBaseCandidatesAddsDefaultPortFallback(t *testing.T) {
	// A stale device row can advertise a non-default port (the box once ran on
	// --http-port 18090, then restarted on the 18080 default). We must still
	// synthesize an 18080 candidate so the live agent is reachable.
	target := &DeviceInfo{
		DeviceID: "dev-1",
		QuicHost: "100.75.123.78",
		QuicPort: 18090,
	}
	bases := directAgentBaseCandidates(target)
	var has18090, has18080 bool
	for _, b := range bases {
		switch b {
		case "http://100.75.123.78:18090":
			has18090 = true
		case "http://100.75.123.78:18080":
			has18080 = true
		}
	}
	if !has18090 {
		t.Errorf("expected the registered port candidate :18090, got %v", bases)
	}
	if !has18080 {
		t.Errorf("expected the default-port fallback :18080, got %v", bases)
	}
	// Registered port must come first so an unchanged row keeps its ordering.
	if len(bases) < 2 || bases[0] != "http://100.75.123.78:18090" {
		t.Errorf("registered port should be tried first, got %v", bases)
	}
}

func TestDirectAgentBaseCandidatesNoDuplicateWhenDefaultPort(t *testing.T) {
	target := &DeviceInfo{DeviceID: "d", QuicHost: "10.0.0.45", QuicPort: 18080}
	bases := directAgentBaseCandidates(target)
	if len(bases) != 1 || bases[0] != "http://10.0.0.45:18080" {
		t.Fatalf("default-port row should yield exactly one candidate, got %v", bases)
	}
}

func TestDoRemoteAgentRequestFallsBackToSecondCandidate(t *testing.T) {
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// doRemoteAgentRequest now races a GET /health across candidates to find a
		// live leg before sending the real request (a dead LAN leg used to starve
		// the working relay leg out of the budget). A real agent serves /health, so
		// this fake must too — the assertions that matter are below: the REAL
		// request still goes to /info, exactly once, on the second candidate.
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-123" {
			t.Errorf("Authorization header = %q", got)
		}
		if got := r.URL.Path; got != "/info" {
			t.Errorf("request path = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer second.Close()

	candidates := []RemoteAgentCandidate{
		{DeviceID: "dev-1", BaseURL: "http://127.0.0.1:1", Kind: "same-lan"},
		{DeviceID: "dev-1", BaseURL: second.URL, Kind: "relay"},
	}
	chosen, status, raw, err := doRemoteAgentRequest(context.Background(), candidates, "token-123", http.MethodGet, "/info", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("doRemoteAgentRequest() error = %v", err)
	}
	if chosen.BaseURL != second.URL {
		t.Fatalf("chosen candidate = %q, want %q", chosen.BaseURL, second.URL)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(string(raw), `"ok":true`) {
		t.Fatalf("raw body = %q", string(raw))
	}
	if last, _ := remoteAgentLastGood.Load("dev-1"); last != second.URL {
		t.Fatalf("last-good cache = %v, want %q", last, second.URL)
	}
}

// TestDoRemoteAgentRequestFallsThroughOnNonJSON404 covers the
// dead-public-endpoint case: the per-device <id>.yaver.io URL is
// listed before the relay candidate, but Cloudflare answers 404 with
// stock HTML when the wildcard worker is misconfigured. Without the
// "non-JSON 404 == transport failure" check, doRemoteAgentRequest
// pinned to the dead URL on the first hop and downstream features
// (`yaver primary status`, runner-auth lookup) silently lost data.
func TestDoRemoteAgentRequestFallsThroughOnNonJSON404(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>Not found</body></html>`))
	}))
	defer dead.Close()
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"runners":[]}`))
	}))
	defer live.Close()

	candidates := []RemoteAgentCandidate{
		{DeviceID: "dev-fallthrough", BaseURL: dead.URL, Kind: "public"},
		{DeviceID: "dev-fallthrough", BaseURL: live.URL, Kind: "relay"},
	}
	chosen, status, raw, err := doRemoteAgentRequest(context.Background(), candidates, "token-x", http.MethodGet, "/agent/runners", nil, 4*time.Second)
	if err != nil {
		t.Fatalf("doRemoteAgentRequest() error = %v", err)
	}
	if chosen.BaseURL != live.URL {
		t.Fatalf("chosen = %q, want fallthrough to %q", chosen.BaseURL, live.URL)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !strings.Contains(string(raw), `"runners"`) {
		t.Fatalf("raw body = %q, want fallback agent response", string(raw))
	}
}

// TestDoRemoteAgentRequestPreservesYaverJSON404 — when the agent IS
// actually answering with a JSON 404 (e.g. unknown route), don't
// fall through; the caller wants to see that genuine 404.
func TestDoRemoteAgentRequestPreservesYaverJSON404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()
	candidates := []RemoteAgentCandidate{{DeviceID: "dev-json404", BaseURL: srv.URL, Kind: "relay"}}
	_, status, raw, err := doRemoteAgentRequest(context.Background(), candidates, "token-x", http.MethodGet, "/agent/runners", nil, 4*time.Second)
	if err != nil {
		t.Fatalf("doRemoteAgentRequest() error = %v", err)
	}
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 surfaced", status)
	}
	if !strings.Contains(string(raw), `"error"`) {
		t.Fatalf("raw body = %q, want JSON 404 forwarded", string(raw))
	}
}

func TestStaleRelayPasswordHTTP(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		// The relay's actual missing-password message must self-heal — this is
		// the regression: it used to be ignored, so repair-relay never fired.
		{"missing (relay wording)", http.StatusUnauthorized, "relay password missing — sign in again to fetch it", true},
		{"invalid", http.StatusUnauthorized, "invalid relay password", true},
		{"forbidden rejected", http.StatusForbidden, "relay password rejected", true},
		{"denied", http.StatusUnauthorized, "relay password denied", true},
		// Non-relay auth failures must NOT trigger a relay repair.
		{"generic 401", http.StatusUnauthorized, "unauthorized", false},
		{"auth token expired", http.StatusUnauthorized, "session token expired", false},
		// Right wording but wrong status — not a relay-password signal.
		{"500 with wording", http.StatusInternalServerError, "relay password missing", false},
		{"200", http.StatusOK, "relay password missing", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := staleRelayPasswordHTTP(tc.status, []byte(tc.body)); got != tc.want {
				t.Fatalf("staleRelayPasswordHTTP(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

func TestTransportHeadersForBaseIncludesCloudflareAccessHeaders(t *testing.T) {
	cfg := &Config{
		CloudflareTunnels: []CloudflareTunnelConfig{
			{
				URL:                  "https://edge.example.com",
				CFAccessClientId:     "cf-id",
				CFAccessClientSecret: "cf-secret",
			},
		},
	}
	headers, err := transportHeadersForBase(cfg, "https://edge.example.com")
	if err != nil {
		t.Fatalf("transportHeadersForBase() error = %v", err)
	}
	if headers["CF-Access-Client-Id"] != "cf-id" {
		t.Fatalf("CF-Access-Client-Id = %q", headers["CF-Access-Client-Id"])
	}
	if headers["CF-Access-Client-Secret"] != "cf-secret" {
		t.Fatalf("CF-Access-Client-Secret = %q", headers["CF-Access-Client-Secret"])
	}
}

func TestPublicAgentBaseCandidatesIncludesAdvertisedPublicEndpoints(t *testing.T) {
	target := &DeviceInfo{
		DeviceID:        "dev-public-1",
		PublicEndpoints: []string{"https://edge.example.com/", "https://edge.example.com", "https://fallback.example.com"},
	}
	got := publicAgentBaseCandidates(target)
	want := []string{"https://edge.example.com", "https://fallback.example.com"}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildRemoteAgentCandidatesIncludesPublicCloudflareEndpoint(t *testing.T) {
	cfg := &Config{
		CloudflareTunnels: []CloudflareTunnelConfig{
			{
				URL:                  "https://edge.example.com",
				CFAccessClientId:     "cf-id",
				CFAccessClientSecret: "cf-secret",
			},
		},
	}
	target := &DeviceInfo{
		DeviceID:        "dev-public-2",
		Name:            "mac-mini",
		QuicHost:        "100.64.1.2",
		QuicPort:        18080,
		IsOnline:        true,
		PublicEndpoints: []string{"https://edge.example.com"},
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		t.Fatalf("buildRemoteAgentCandidates() error = %v", err)
	}
	found := false
	for _, candidate := range candidates {
		if candidate.BaseURL != "https://edge.example.com" {
			continue
		}
		found = true
		if candidate.Label != "public" {
			t.Fatalf("public candidate label = %q, want public", candidate.Label)
		}
		if candidate.Headers["CF-Access-Client-Id"] != "cf-id" {
			t.Fatalf("CF-Access-Client-Id = %q", candidate.Headers["CF-Access-Client-Id"])
		}
		if candidate.Headers["CF-Access-Client-Secret"] != "cf-secret" {
			t.Fatalf("CF-Access-Client-Secret = %q", candidate.Headers["CF-Access-Client-Secret"])
		}
	}
	if !found {
		t.Fatalf("expected Cloudflare public endpoint candidate, got %v", candidates)
	}
}

func TestDoRemoteAgentRequestReturnsJoinedErrorsWhenAllCandidatesFail(t *testing.T) {
	candidates := []RemoteAgentCandidate{
		{DeviceID: "dev-2", BaseURL: "http://127.0.0.1:1", Kind: "same-lan"},
		{DeviceID: "dev-2", BaseURL: "http://127.0.0.1:2", Kind: "relay"},
	}
	_, _, _, err := doRemoteAgentRequest(context.Background(), candidates, "token-123", http.MethodGet, "/info", nil, 2*time.Second)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("error = %v, want joined candidate failure", err)
	}
}

func TestDirectAgentBaseCandidatesIncludesLocalIPs(t *testing.T) {
	target := &DeviceInfo{
		DeviceID: "dev-3",
		QuicHost: "192.168.1.20",
		LocalIps: []string{"100.64.2.5", "10.0.0.8", "192.168.1.20"},
		QuicPort: 18080,
	}
	got := directAgentBaseCandidates(target)
	want := []string{
		"http://192.168.1.20:18080",
		"http://100.64.2.5:18080",
		"http://10.0.0.8:18080",
	}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOrderRemoteAgentCandidatesDemotesRecentFailures(t *testing.T) {
	deviceID := "dev-health-1"
	failing := "http://10.0.0.1:18080"
	healthy := "http://100.64.1.4:18080"
	remoteAgentHealth.Store(remoteAgentHealthKey(deviceID, failing), &remoteAgentHealthState{
		LastFailure: time.Now(),
		Failures:    3,
	})
	t.Cleanup(func() {
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, failing))
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, healthy))
		remoteAgentLastGood.Delete(deviceID)
	})
	candidates := []RemoteAgentCandidate{
		{DeviceID: deviceID, BaseURL: failing, Kind: "same-lan"},
		{DeviceID: deviceID, BaseURL: healthy, Kind: "tailscale"},
	}
	orderRemoteAgentCandidates(candidates)
	if candidates[0].BaseURL != healthy {
		t.Fatalf("first candidate = %q, want healthy candidate first", candidates[0].BaseURL)
	}
}

func TestOrderRemoteAgentCandidatesPrefersRecentSuccessWithinKind(t *testing.T) {
	deviceID := "dev-health-2"
	first := "http://192.168.1.21:18080"
	second := "http://192.168.1.22:18080"
	remoteAgentHealth.Store(remoteAgentHealthKey(deviceID, second), &remoteAgentHealthState{
		LastSuccess: time.Now(),
		Successes:   2,
	})
	t.Cleanup(func() {
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, first))
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, second))
		remoteAgentLastGood.Delete(deviceID)
	})
	candidates := []RemoteAgentCandidate{
		{DeviceID: deviceID, BaseURL: first, Kind: "same-lan"},
		{DeviceID: deviceID, BaseURL: second, Kind: "same-lan"},
	}
	orderRemoteAgentCandidates(candidates)
	if candidates[0].BaseURL != second {
		t.Fatalf("first candidate = %q, want recent-success candidate first", candidates[0].BaseURL)
	}
}

func TestOrderRemoteAgentCandidatesPrefersHealthyProbe(t *testing.T) {
	deviceID := "dev-probe-1"
	first := "http://10.0.0.10:18080"
	second := "http://10.0.0.11:18080"
	remoteAgentProbe.Store(remoteAgentHealthKey(deviceID, second), &remoteAgentProbeState{
		CheckedAt: time.Now(),
		Healthy:   true,
		Latency:   20 * time.Millisecond,
	})
	t.Cleanup(func() {
		remoteAgentProbe.Delete(remoteAgentHealthKey(deviceID, first))
		remoteAgentProbe.Delete(remoteAgentHealthKey(deviceID, second))
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, first))
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, second))
		remoteAgentLastGood.Delete(deviceID)
	})
	candidates := []RemoteAgentCandidate{
		{DeviceID: deviceID, BaseURL: first, Kind: "same-lan"},
		{DeviceID: deviceID, BaseURL: second, Kind: "same-lan"},
	}
	orderRemoteAgentCandidates(candidates)
	if candidates[0].BaseURL != second {
		t.Fatalf("first candidate = %q, want healthy probed candidate first", candidates[0].BaseURL)
	}
}

func TestOrderRemoteAgentCandidatesDemotesUnhealthyProbe(t *testing.T) {
	deviceID := "dev-probe-2"
	first := "http://10.0.0.20:18080"
	second := "http://10.0.0.21:18080"
	remoteAgentProbe.Store(remoteAgentHealthKey(deviceID, first), &remoteAgentProbeState{
		CheckedAt: time.Now(),
		Healthy:   false,
	})
	t.Cleanup(func() {
		remoteAgentProbe.Delete(remoteAgentHealthKey(deviceID, first))
		remoteAgentProbe.Delete(remoteAgentHealthKey(deviceID, second))
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, first))
		remoteAgentHealth.Delete(remoteAgentHealthKey(deviceID, second))
		remoteAgentLastGood.Delete(deviceID)
	})
	candidates := []RemoteAgentCandidate{
		{DeviceID: deviceID, BaseURL: first, Kind: "same-lan"},
		{DeviceID: deviceID, BaseURL: second, Kind: "same-lan"},
	}
	orderRemoteAgentCandidates(candidates)
	if candidates[0].BaseURL != second {
		t.Fatalf("first candidate = %q, want non-failing candidate first", candidates[0].BaseURL)
	}
}
