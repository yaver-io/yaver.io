package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// startTestServerWithSDK starts a test server and pre-populates the token cache
// with an SDK token entry (simulating Convex validation).
func startTestServerWithSDK(t *testing.T, agentToken string, sdkToken string, sdkScopes []string, sdkAllowedCIDRs []string) (string, context.CancelFunc, *HTTPServer) {
	t.Helper()
	port := getFreePort(t)

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	srv := NewHTTPServer(port, agentToken, "test-user-id", "", "test-host", tm)
	srv.execMgr = NewExecManager(tm.workDir, nil)

	// Pre-populate SDK token in cache (simulates successful Convex validation)
	if sdkToken != "" {
		srv.tokenCache.Store(sdkToken, &cachedTokenInfo{
			userID:       "test-user-id",
			isSdk:        true,
			scopes:       sdkScopes,
			allowedCIDRs: sdkAllowedCIDRs,
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { srv.Start(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return baseURL, cancel, srv
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	t.Fatalf("server did not start within 3s")
	return "", nil, nil
}

// ---------------------------------------------------------------------------
// Phase 1: Scope Restriction Tests
// ---------------------------------------------------------------------------

func TestSDKTokenScopeAllowsFeedback(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	// SDK token can access /feedback (POST feedback endpoint)
	status, _ := doRequest(t, "POST", baseURL+"/feedback", "sdk-tok", `{"metadata":{}}`)
	// 400 is expected (bad multipart) — but NOT 403 (auth passed)
	if status == 403 || status == 401 {
		t.Fatalf("SDK token should be allowed on /feedback, got %d", status)
	}
}

func TestSDKTokenScopeBlocksTasks(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	// SDK token CANNOT access /tasks (full-access only)
	status, body := doRequest(t, "GET", baseURL+"/tasks", "sdk-tok", "")
	if status != 403 {
		t.Fatalf("expected 403 for SDK token on /tasks, got %d", status)
	}
	if errMsg, ok := body["error"].(string); ok {
		if errMsg != "SDK tokens cannot access this endpoint" {
			t.Logf("error message: %s", errMsg)
		}
	}
}

func TestSDKTokenScopeBlocksExec(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback"},
		nil,
	)
	defer cancel()

	// SDK token CANNOT access /exec
	status, _ := doRequest(t, "POST", baseURL+"/exec", "sdk-tok", `{"command":"ls"}`)
	if status != 403 {
		t.Fatalf("expected 403 for SDK token on /exec, got %d", status)
	}
}

func TestSDKTokenScopeBlocksVault(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback", "blackbox"},
		nil,
	)
	defer cancel()

	// SDK token CANNOT access /vault/*
	status, _ := doRequest(t, "GET", baseURL+"/vault/list", "sdk-tok", "")
	if status != 403 {
		t.Fatalf("expected 403 for SDK token on /vault/list, got %d", status)
	}
}

func TestSDKTokenScopeBlocksAgentShutdown(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	// SDK token CANNOT shut down the agent
	status, _ := doRequest(t, "POST", baseURL+"/agent/shutdown", "sdk-tok", "")
	if status != 403 {
		t.Fatalf("expected 403 for SDK token on /agent/shutdown, got %d", status)
	}
}

func TestSDKTokenNarrowScope(t *testing.T) {
	// Token with only "feedback" scope — cannot access voice or builds
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"narrow-sdk",
		[]string{"feedback"},
		nil,
	)
	defer cancel()

	// /feedback allowed
	status, _ := doRequest(t, "POST", baseURL+"/feedback", "narrow-sdk", `{}`)
	if status == 403 {
		t.Fatal("narrow SDK token should access /feedback")
	}

	// /voice/status NOT allowed (no "voice" scope)
	status, _ = doRequest(t, "GET", baseURL+"/voice/status", "narrow-sdk", "")
	if status != 403 {
		t.Fatalf("expected 403 for narrow SDK on /voice/status, got %d", status)
	}

	// /builds NOT allowed (no "builds" scope)
	status, _ = doRequest(t, "GET", baseURL+"/builds", "narrow-sdk", "")
	if status != 403 {
		t.Fatalf("expected 403 for narrow SDK on /builds, got %d", status)
	}
}

func TestAgentTokenBypassesScopes(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback"},
		nil,
	)
	defer cancel()

	// Agent's own token can access everything
	status, _ := doRequest(t, "GET", baseURL+"/tasks", "agent-tok", "")
	if status != 200 {
		t.Fatalf("agent token should access /tasks, got %d", status)
	}

	status, _ = doRequest(t, "GET", baseURL+"/agent/status", "agent-tok", "")
	if status != 200 {
		t.Fatalf("agent token should access /agent/status, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// Phase 2: IP Allowlist Tests
// ---------------------------------------------------------------------------

func TestIPAllowlistEmpty(t *testing.T) {
	// Empty allowlist = all IPs allowed
	baseURL, cancel, _ := startTestServerWithSDK(t, "tok", "", nil, nil)
	defer cancel()

	status, _ := doRequest(t, "GET", baseURL+"/health", "", "")
	if status != 200 {
		t.Fatalf("empty allowlist should allow all, got %d", status)
	}
}

func TestIPAllowlistBlocks(t *testing.T) {
	port := getFreePort(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	srv := NewHTTPServer(port, "tok", "test-user-id", "", "test-host", tm)
	srv.execMgr = NewExecManager(tm.workDir, nil)

	// Only allow 10.0.0.0/8 — localhost (127.0.0.1) NOT in range
	srv.allowedCIDRs = parseCIDRs([]string{"10.0.0.0/8"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { srv.Start(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			// Should get 403 since 127.0.0.1 is not in 10.0.0.0/8
			if resp.StatusCode == 403 {
				return // pass
			}
			if resp.StatusCode == 200 {
				t.Fatal("IP allowlist should block 127.0.0.1 when only 10.0.0.0/8 is allowed")
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not start within 3s")
}

func TestIPAllowlistAllowsLocalhost(t *testing.T) {
	port := getFreePort(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	srv := NewHTTPServer(port, "tok", "test-user-id", "", "test-host", tm)
	srv.execMgr = NewExecManager(tm.workDir, nil)

	// Allow localhost
	srv.allowedCIDRs = parseCIDRs([]string{"127.0.0.0/8"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { srv.Start(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return // pass
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("IP allowlist should allow 127.0.0.1 when 127.0.0.0/8 is configured")
}

// ---------------------------------------------------------------------------
// Phase 3: IP Binding on SDK Tokens
// ---------------------------------------------------------------------------

func TestSDKTokenIPBindingAllows(t *testing.T) {
	// SDK token bound to 127.0.0.0/8 — localhost requests should pass
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"bound-sdk",
		[]string{"feedback", "blackbox", "voice", "builds"},
		[]string{"127.0.0.0/8"},
	)
	defer cancel()

	// Request from localhost → allowed
	status, _ := doRequest(t, "POST", baseURL+"/feedback", "bound-sdk", `{}`)
	if status == 403 {
		t.Fatal("SDK token bound to 127.0.0.0/8 should allow localhost requests")
	}
}

func TestSDKTokenIPBindingBlocks(t *testing.T) {
	// SDK token bound to 10.0.0.0/8 — localhost requests should be blocked
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"restricted-sdk",
		[]string{"feedback", "blackbox", "voice", "builds"},
		[]string{"10.0.0.0/8"},
	)
	defer cancel()

	// Request from localhost (127.0.0.1) → blocked
	status, body := doRequest(t, "POST", baseURL+"/feedback", "restricted-sdk", `{}`)
	if status != 403 {
		t.Fatalf("expected 403 for SDK token bound to 10.0.0.0/8, got %d (body: %v)", status, body)
	}
}

func TestSDKTokenNoIPBinding(t *testing.T) {
	// SDK token with no IP binding — all IPs allowed
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"unbound-sdk",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	status, _ := doRequest(t, "POST", baseURL+"/feedback", "unbound-sdk", `{}`)
	if status == 403 {
		t.Fatal("SDK token with no IP binding should allow all IPs")
	}
}

// ---------------------------------------------------------------------------
// Phase 5: New Device Notification (seenIPs tracking)
// ---------------------------------------------------------------------------

func TestNewDeviceIPTracking(t *testing.T) {
	baseURL, cancel, srv := startTestServerWithSDK(t,
		"agent-tok",
		"track-sdk",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	// First request from localhost
	doRequest(t, "POST", baseURL+"/feedback", "track-sdk", `{}`)

	// Check seenIPs was populated
	key := "track-sd_127.0.0.1"
	if _, ok := srv.seenIPs.Load(key); !ok {
		t.Fatal("expected seenIPs to track the first request IP")
	}

	// Second request shouldn't add a new entry (same IP)
	countBefore := 0
	srv.seenIPs.Range(func(_, _ interface{}) bool { countBefore++; return true })

	doRequest(t, "POST", baseURL+"/feedback", "track-sdk", `{}`)

	countAfter := 0
	srv.seenIPs.Range(func(_, _ interface{}) bool { countAfter++; return true })

	if countAfter != countBefore {
		t.Fatalf("seenIPs should not grow for same IP; before=%d, after=%d", countBefore, countAfter)
	}
}

// ---------------------------------------------------------------------------
// Phase 6: TLS Certificate Generation
// ---------------------------------------------------------------------------

func TestTLSCertGeneration(t *testing.T) {
	// Use temp dir to avoid polluting ~/.yaver/tls/
	tmpDir := t.TempDir()
	certPath := tmpDir + "/server.pem"
	keyPath := tmpDir + "/server-key.pem"

	cert, fingerprint, err := generateTLSCert(tmpDir, certPath, keyPath)
	if err != nil {
		t.Fatalf("generateTLSCert failed: %v", err)
	}

	if fingerprint == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if len(fingerprint) != 64 { // SHA256 hex = 64 chars
		t.Fatalf("expected 64-char fingerprint, got %d chars", len(fingerprint))
	}

	// Verify it's a valid tls.Certificate
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate in chain")
	}

	// Verify cert files were written
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("cert file not created: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file not created: %v", err)
	}
}

func TestTLSHealthIncludesFingerprint(t *testing.T) {
	port := getFreePort(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	srv := NewHTTPServer(port, "tok", "test-user-id", "", "test-host", tm)
	srv.execMgr = NewExecManager(tm.workDir, nil)
	srv.tlsFingerprint = "abc123def456"
	srv.tlsPort = 18443

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { srv.Start(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	status, body := doRequest(t, "GET", baseURL+"/health", "", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["tlsFingerprint"] != "abc123def456" {
		t.Fatalf("expected tlsFingerprint in health, got %v", body["tlsFingerprint"])
	}
	if body["tlsPort"] != float64(18443) {
		t.Fatalf("expected tlsPort=18443, got %v", body["tlsPort"])
	}
}

func TestTLSServerStarts(t *testing.T) {
	tmpDir := t.TempDir()
	cert, fingerprint, err := generateTLSCert(tmpDir, tmpDir+"/server.pem", tmpDir+"/server-key.pem")
	if err != nil {
		t.Fatalf("generateTLSCert: %v", err)
	}

	port := getFreePort(t)
	tlsPort := getFreePort(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	srv := NewHTTPServer(port, "tok", "test-user-id", "", "test-host", tm)
	srv.execMgr = NewExecManager(tm.workDir, nil)
	srv.tlsCert = cert
	srv.tlsFingerprint = fingerprint
	srv.tlsPort = tlsPort

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { srv.Start(ctx) }()

	// Wait for HTTP server
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Try HTTPS (skip cert verification since it's self-signed)
	tlsClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	tlsURL := fmt.Sprintf("https://127.0.0.1:%d/health", tlsPort)

	var tlsResp *http.Response
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tlsResp, err = tlsClient.Get(tlsURL)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("HTTPS health check failed: %v", err)
	}
	defer tlsResp.Body.Close()

	if tlsResp.StatusCode != 200 {
		t.Fatalf("expected HTTPS 200, got %d", tlsResp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Utility function tests
// ---------------------------------------------------------------------------

func TestParseCIDRs(t *testing.T) {
	tests := []struct {
		input    []string
		expected int
	}{
		{nil, 0},
		{[]string{}, 0},
		{[]string{"192.168.1.0/24"}, 1},
		{[]string{"10.0.0.0/8", "172.16.0.0/12"}, 2},
		{[]string{"192.168.1.100"}, 1},       // plain IP → /32
		{[]string{"invalid"}, 0},               // invalid → skipped
		{[]string{"192.168.1.0/24", ""}, 1},   // empty string → skipped
	}

	for _, tt := range tests {
		result := parseCIDRs(tt.input)
		if len(result) != tt.expected {
			t.Errorf("parseCIDRs(%v) = %d CIDRs, want %d", tt.input, len(result), tt.expected)
		}
	}
}

func TestIPMatchesCIDRs(t *testing.T) {
	cidrs := parseCIDRs([]string{"192.168.1.0/24", "10.0.0.0/8"})

	tests := []struct {
		ip       string
		expected bool
	}{
		{"192.168.1.1", true},
		{"192.168.1.254", true},
		{"192.168.2.1", false},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", false},
		{"127.0.0.1", false},
	}

	for _, tt := range tests {
		result := ipMatchesCIDRs(tt.ip, cidrs)
		if result != tt.expected {
			t.Errorf("ipMatchesCIDRs(%s) = %v, want %v", tt.ip, result, tt.expected)
		}
	}
}

func TestPathAllowedByScopes(t *testing.T) {
	tests := []struct {
		path     string
		scopes   []string
		expected bool
	}{
		{"/feedback", []string{"feedback"}, true},
		{"/feedback/stream", []string{"feedback"}, true},
		{"/feedback/abc-123", []string{"feedback"}, true},
		{"/blackbox/events", []string{"blackbox"}, true},
		{"/blackbox/subscribe", []string{"blackbox"}, true},
		{"/voice/status", []string{"voice"}, true},
		{"/voice/transcribe", []string{"voice"}, true},
		{"/builds", []string{"builds"}, true},
		{"/builds/register", []string{"builds"}, true},
		{"/tasks", []string{"feedback"}, false},
		{"/exec", []string{"feedback", "blackbox"}, false},
		{"/vault/list", []string{"feedback", "voice"}, false},
		{"/agent/shutdown", []string{"feedback", "blackbox", "voice", "builds"}, false},
		{"/voice/status", []string{"feedback"}, false},  // no voice scope
		{"/builds", []string{"feedback", "voice"}, false}, // no builds scope
	}

	for _, tt := range tests {
		result := pathAllowedByScopes(tt.path, tt.scopes)
		if result != tt.expected {
			t.Errorf("pathAllowedByScopes(%q, %v) = %v, want %v", tt.path, tt.scopes, result, tt.expected)
		}
	}
}

func TestClientIPExtraction(t *testing.T) {
	// Standard request
	r := &http.Request{RemoteAddr: "192.168.1.10:54321"}
	if ip := clientIP(r); ip != "192.168.1.10" {
		t.Errorf("clientIP() = %s, want 192.168.1.10", ip)
	}

	// With X-Forwarded-For
	r = &http.Request{
		RemoteAddr: "10.0.0.1:12345",
		Header:     http.Header{"X-Forwarded-For": {"203.0.113.50, 70.41.3.18"}},
	}
	if ip := clientIP(r); ip != "203.0.113.50" {
		t.Errorf("clientIP() with XFF = %s, want 203.0.113.50", ip)
	}
}

func TestCollectLocalIPs(t *testing.T) {
	ips := collectLocalIPs()
	// Should find at least one IP (unless running in a very stripped container)
	if len(ips) == 0 {
		t.Log("Warning: no local IPs found (may be expected in CI)")
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			t.Errorf("collectLocalIPs() should not include loopback, got %s", ip)
		}
	}
}

// ---------------------------------------------------------------------------
// Cross-token isolation (SDK vs agent tokens)
// ---------------------------------------------------------------------------

func TestSDKTokenCannotAccessFullEndpoints(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	// List of full-access-only endpoints
	fullAccessEndpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/tasks"},
		{"POST", "/exec"},
		{"GET", "/agent/status"},
		{"POST", "/agent/shutdown"},
		{"GET", "/vault/list"},
		{"GET", "/session/list"},
		{"GET", "/tmux/sessions"},
		{"GET", "/schedules"},
		{"GET", "/notifications/config"},
	}

	for _, ep := range fullAccessEndpoints {
		status, _ := doRequest(t, ep.method, baseURL+ep.path, "sdk-tok", "")
		if status != 403 {
			t.Errorf("SDK token should be blocked on %s %s, got %d", ep.method, ep.path, status)
		}
	}
}

func TestSDKTokenCanAccessSDKEndpoints(t *testing.T) {
	baseURL, cancel, _ := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback", "blackbox", "voice", "builds"},
		nil,
	)
	defer cancel()

	// SDK-accessible endpoints (should NOT return 403)
	sdkEndpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/feedback"},
		{"POST", "/feedback/stream"},
		{"POST", "/blackbox/events"},
		{"GET", "/blackbox/logs"},
		{"GET", "/voice/status"},
		{"GET", "/builds"},
	}

	for _, ep := range sdkEndpoints {
		status, _ := doRequest(t, ep.method, baseURL+ep.path, "sdk-tok", "")
		if status == 403 || status == 401 {
			t.Errorf("SDK token should be allowed on %s %s, got %d", ep.method, ep.path, status)
		}
	}
}

// ---------------------------------------------------------------------------
// Token cache struct tests
// ---------------------------------------------------------------------------

func TestTokenCacheDistinguishesSDKAndSession(t *testing.T) {
	baseURL, cancel, srv := startTestServerWithSDK(t,
		"agent-tok",
		"sdk-tok",
		[]string{"feedback"},
		nil,
	)
	defer cancel()

	// Also add a session token to cache
	srv.tokenCache.Store("session-tok", &cachedTokenInfo{
		userID: "test-user-id",
		isSdk:  false,
	})

	// Session token can access /tasks
	status, _ := doRequest(t, "GET", baseURL+"/tasks", "session-tok", "")
	if status != 200 {
		t.Fatalf("session token should access /tasks, got %d", status)
	}

	// SDK token cannot access /tasks
	status, _ = doRequest(t, "GET", baseURL+"/tasks", "sdk-tok", "")
	if status != 403 {
		t.Fatalf("SDK token should NOT access /tasks, got %d", status)
	}
}

func TestDifferentUserSDKTokenRejected(t *testing.T) {
	baseURL, cancel, srv := startTestServerWithSDK(t,
		"agent-tok",
		"",
		nil,
		nil,
	)
	defer cancel()

	// Add an SDK token for a different user
	srv.tokenCache.Store("other-user-sdk", &cachedTokenInfo{
		userID:       "different-user-id",
		isSdk:        true,
		scopes:       []string{"feedback"},
		allowedCIDRs: nil,
	})

	status, _ := doRequest(t, "POST", baseURL+"/feedback", "other-user-sdk", `{}`)
	if status != 403 {
		t.Fatalf("expected 403 for different user's SDK token, got %d", status)
	}
}

// ---------------------------------------------------------------------------
// Beacon payload tests
// ---------------------------------------------------------------------------

func TestBeaconPayloadTLSFields(t *testing.T) {
	bp := beaconPayload{
		Version:        1,
		DeviceID:       "abcd1234",
		Port:           18080,
		Name:           "test",
		TLSFingerprint: "deadbeef",
		TLSPort:        18443,
	}

	if bp.TLSFingerprint != "deadbeef" {
		t.Errorf("expected TLSFingerprint=deadbeef, got %s", bp.TLSFingerprint)
	}
	if bp.TLSPort != 18443 {
		t.Errorf("expected TLSPort=18443, got %d", bp.TLSPort)
	}
}

// Verify net import is used (compile check for parseCIDRs, ipMatchesCIDRs)
var _ = net.ParseIP
