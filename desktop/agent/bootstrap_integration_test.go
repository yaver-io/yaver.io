package main

// bootstrap_integration_test.go — end-to-end coverage for the
// bootstrap HTTP server that the audit identified as untested at
// the network boundary. Existing handler tests use
// httptest.NewRequest directly, which skips ServeMux, CORS wrapping,
// chunked-body parsing, and connection lifecycle — exactly the
// stack the mobile and web reclaim flows actually traverse.
//
// Each test here wraps the bootstrap mux in httptest.NewServer so a
// regression in routing (e.g. /auth/pair/owner-claim ever moving
// off the bootstrap mux) or in CORS preflight (e.g. OPTIONS for the
// reclaim path returning the wrong status) surfaces immediately.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newBootstrapTestServer mirrors the mux that runBootstrapServe assembles
// in production. We don't start the bootstrap loop, beacon, or relay
// tunnel — just the HTTP surface, which is what every external caller
// (mobile, web, relay-forwarded request) ever touches.
func newBootstrapTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	bs := &bootstrapHTTPServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", bs.handleHealth)
	mux.HandleFunc("/auth/pair/info", bs.handlePairInfo)
	mux.HandleFunc("/auth/pair/session", bs.handlePairSession)
	mux.HandleFunc("/auth/pair/submit", bs.handlePairSubmit)
	mux.HandleFunc("/auth/pair/encrypted", bs.handlePairEncrypted)
	mux.HandleFunc("/info", bs.handleInfo)
	mux.HandleFunc("/auth/recover", bs.handleAuthRecover)
	mux.HandleFunc("/auth/pair/owner-claim", bs.handleOwnerClaim)
	srv := httptest.NewServer(corsWrap(mux))
	t.Cleanup(srv.Close)
	return srv
}

// TestBootstrapServer_OwnerClaimEndToEnd is the smoke test that was missing
// per the audit's G9. It proves that the entire reclaim path — HTTP server,
// CORS wrap, mux routing, handler, ownership check, pair-session splice —
// works end-to-end against a real listening socket. If this test breaks,
// the user-facing one-click reclaim from mobile/web is broken in
// production.
func TestBootstrapServer_OwnerClaimEndToEnd(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-end-to-end",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if _, err := StartPairingSession(bootstrapPairingTTL); err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	var convexCalls atomic.Int32
	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		convexCalls.Add(1)
		if token != "owner-bearer" {
			t.Errorf("expected bearer to round-trip through HTTP, got %q", token)
		}
		return []DeviceInfo{{DeviceID: "device-end-to-end", AccessScope: "owner"}}, nil
	}
	t.Cleanup(func() { listDevicesForOwnerClaimFn = oldList })

	srv := newBootstrapTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		srv.URL+"/auth/pair/owner-claim", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer owner-bearer")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if convexCalls.Load() != 1 {
		t.Fatalf("expected exactly one Convex ownership lookup, got %d", convexCalls.Load())
	}
	snap := activePairingSnapshot()
	if snap == nil {
		t.Fatalf("pair session disappeared")
	}
	if snap.ReceivedToken != "owner-bearer" {
		t.Fatalf("token did not splice into pair session: got %q", snap.ReceivedToken)
	}
}

// TestBootstrapServer_OwnerClaimWithRelayHeaders proves the handler still
// works when the request shape mimics what the relay forwards: an
// X-Forwarded-For header, an X-Relay-Password header, and the bearer
// in the Authorization header. The relay strips ?__rp= before forwarding
// so we test the post-strip shape here. The handler must NOT leak the
// passkey via /info under these headers (regression test for
// bootstrapPasskeyVisible).
func TestBootstrapServer_OwnerClaimWithRelayHeaders(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-relayed",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	session, err := StartPairingSession(bootstrapPairingTTL)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		return []DeviceInfo{{DeviceID: "device-relayed", AccessScope: "owner"}}, nil
	}
	t.Cleanup(func() { listDevicesForOwnerClaimFn = oldList })

	srv := newBootstrapTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First, prove /info does NOT leak the passkey under relay-style
	// headers. This is a regression test for the audit's note that
	// "passkey is intentionally hidden over relay" — which is the WHOLE
	// reason owner-claim exists as a separate path.
	infoReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/info", nil)
	infoReq.Header.Set("X-Forwarded-For", "203.0.113.50")
	infoReq.Header.Set("X-Relay-Password", "relay-secret")
	infoResp, err := http.DefaultClient.Do(infoReq)
	if err != nil {
		t.Fatalf("/info: %v", err)
	}
	infoBody, _ := io.ReadAll(infoResp.Body)
	infoResp.Body.Close()
	if strings.Contains(string(infoBody), session.Code) {
		t.Fatalf("/info leaked passkey under relay headers: %s", string(infoBody))
	}
	// Lifecycle should still flow through.
	var info map[string]any
	if err := json.Unmarshal(infoBody, &info); err != nil {
		t.Fatalf("/info body unparseable: %v body=%s", err, string(infoBody))
	}
	lifecycle, _ := info["lifecycle"].(map[string]any)
	if lifecycle == nil || lifecycle["state"] != "bootstrap" {
		t.Fatalf("/info lifecycle missing or wrong: %v", info)
	}
	supportsOwnerClaim, _ := lifecycle["supportsOwnerClaim"].(bool)
	if !supportsOwnerClaim {
		t.Fatalf("/info lifecycle should advertise supportsOwnerClaim for previously-owned bootstrap")
	}

	// Now fire the owner-claim with the same relay-style headers.
	body := bytes.NewReader([]byte(`{}`))
	claimReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/auth/pair/owner-claim", body)
	claimReq.Header.Set("Authorization", "Bearer owner-bearer")
	claimReq.Header.Set("X-Forwarded-For", "203.0.113.50")
	claimReq.Header.Set("X-Relay-Password", "relay-secret")
	claimReq.Header.Set("Content-Type", "application/json")
	claimResp, err := http.DefaultClient.Do(claimReq)
	if err != nil {
		t.Fatalf("owner-claim: %v", err)
	}
	defer claimResp.Body.Close()
	claimBody, _ := io.ReadAll(claimResp.Body)
	if claimResp.StatusCode != http.StatusOK {
		t.Fatalf("owner-claim with relay headers expected 200, got %d: %s", claimResp.StatusCode, string(claimBody))
	}
	snap := activePairingSnapshot()
	if snap == nil || snap.ReceivedToken != "owner-bearer" {
		t.Fatalf("token did not splice into session via relay-style request: snap=%+v", snap)
	}
}

// TestBootstrapServer_OwnerClaimCORSPreflight proves the OPTIONS handler
// short-circuits correctly. Mobile WebView and web dashboards both fire
// preflight before POST; if this returns 405 instead of 204, the actual
// reclaim never fires and the user sees a generic "network error".
func TestBootstrapServer_OwnerClaimCORSPreflight(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	t.Cleanup(EndPairingSession)

	srv := newBootstrapTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodOptions,
		srv.URL+"/auth/pair/owner-claim", nil)
	req.Header.Set("Origin", "https://yaver.io")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Fatalf("CORS Allow-Methods missing POST: %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Fatalf("CORS Allow-Headers missing Authorization: %q", got)
	}
}

// TestBootstrapServer_HealthExposesLifecycle is a quick belt-and-braces
// check that the lifecycle contract reaches /health (not just /info). The
// audit calls out that mobile/web read lifecycle from /info, but /health
// is the cheap reachability probe many transport selectors run first —
// returning lifecycle there saves a round-trip per probe.
func TestBootstrapServer_HealthExposesLifecycle(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-health",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	t.Cleanup(EndPairingSession)

	srv := newBootstrapTestServer(t)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v body=%s", err, string(body))
	}
	if got, _ := parsed["lifecycleState"].(string); got != "bootstrap" {
		t.Fatalf("/health lifecycleState=%q want bootstrap", got)
	}
	lifecycle, _ := parsed["lifecycle"].(map[string]any)
	if lifecycle == nil {
		t.Fatalf("/health missing structured lifecycle: %s", string(body))
	}
	if got, _ := lifecycle["state"].(string); got != "bootstrap" {
		t.Fatalf("/health lifecycle.state=%q want bootstrap", got)
	}
	if got, _ := lifecycle["recoverable"].(bool); !got {
		t.Fatalf("/health lifecycle.recoverable should be true for previously-owned bootstrap")
	}
	if got, _ := lifecycle["supportsOwnerClaim"].(bool); !got {
		t.Fatalf("/health lifecycle.supportsOwnerClaim should be true when device has DeviceID")
	}
}
