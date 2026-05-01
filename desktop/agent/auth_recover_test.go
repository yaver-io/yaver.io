package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setRequirePrivateRecoveryForTest(t *testing.T, enabled bool) {
	t.Helper()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	prev := cfg.RequirePrivateRecoveryTransport
	cfg.RequirePrivateRecoveryTransport = enabled
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	t.Cleanup(func() {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig cleanup: %v", err)
		}
		cfg.RequirePrivateRecoveryTransport = prev
		if err := SaveConfig(cfg); err != nil {
			t.Fatalf("SaveConfig cleanup: %v", err)
		}
	})
}

func TestAuthRecoverRequiresProof(t *testing.T) {
	recoveryLimiter.reset()
	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"mode":"pair"}`))
	req.RemoteAddr = "192.168.1.10:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthRecoverRejectedWhenAgentHealthy(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("healthy-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	srv := &HTTPServer{token: "live-token"}
	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"healthy-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.20:40000"
	rec := httptest.NewRecorder()

	srv.handleAuthRecover(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestAuthRecoverSecretCannotStartDeviceCode(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-123"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-123","mode":"device-code"}`))
	req.RemoteAddr = "192.168.1.11:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestAuthRecoverHostTokenCanStartDeviceCode(t *testing.T) {
	recoveryLimiter.reset()
	oldVerify := verifyHostTokenFn
	oldRequest := requestDeviceCodeFn
	verifyHostTokenFn = func(bearer string) (bool, error) {
		return bearer == "host-token", nil
	}
	requestDeviceCodeFn = func(convexURL string) (*deviceCodeResponse, error) {
		return &deviceCodeResponse{
			DeviceCode: "dev-code",
			UserCode:   "user-code",
			ExpiresAt:  1735689600000,
		}, nil
	}
	defer func() {
		verifyHostTokenFn = oldVerify
		requestDeviceCodeFn = oldRequest
	}()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"mode":"device-code"}`))
	req.RemoteAddr = "192.168.1.12:40000"
	req.Header.Set("Authorization", "Bearer host-token")
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"mode":"device-code"`) {
		t.Fatalf("expected device-code response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"recovery_id"`) || !strings.Contains(rec.Body.String(), `"wait_token"`) {
		t.Fatalf("expected recovery session fields, got %s", rec.Body.String())
	}
}

func TestAuthRecoverPairWorksWithBootstrapSecret(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-456"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-456","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.13:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"pairCode"`) {
		t.Fatalf("expected pairCode in response, got %s", rec.Body.String())
	}
}

func TestAuthRecoverDirectRejectsBootstrapSecret(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-nope"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-nope","mode":"direct"}`))
	req.RemoteAddr = "192.168.1.40:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRecoverDirectAppliesHostTokenImmediately(t *testing.T) {
	withTempHome(t)
	recoveryLimiter.reset()
	oldVerify := verifyHostTokenFn
	oldReport := reportRecoveryEventFn
	oldValidate := validateRecoveredTokenFn
	verifyHostTokenFn = func(bearer string) (bool, error) {
		return bearer == "owner-token", nil
	}
	reportRecoveryEventFn = func(*HTTPServer, string, map[string]interface{}) {}
	validateRecoveredTokenFn = func(_, token string) (string, error) {
		if token == "owner-token" {
			return "test-user-id", nil
		}
		return "", nil
	}
	defer func() {
		verifyHostTokenFn = oldVerify
		reportRecoveryEventFn = oldReport
		validateRecoveredTokenFn = oldValidate
	}()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"mode":"direct"}`))
	req.RemoteAddr = "192.168.1.41:40000"
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()

	srv := &HTTPServer{}
	srv.authExpired.Store(true)
	srv.handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"mode":"direct"`) {
		t.Fatalf("expected direct response, got %s", rec.Body.String())
	}
	if srv.token != "owner-token" {
		t.Fatalf("expected srv.token to be updated to new bearer, got %q", srv.token)
	}
	if srv.authExpired.Load() {
		t.Fatalf("expected authExpired cleared after direct recovery")
	}
}

func TestAuthRecoverPairReusesExistingWindow(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-reuse"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	first := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-reuse","mode":"pair"}`))
	first.RemoteAddr = "192.168.1.30:40000"
	firstRec := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", firstRec.Code)
	}
	if activePairing == nil {
		t.Fatalf("expected active pairing session")
	}
	code := activePairing.Code

	recoveryLimiter.reset()
	second := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-reuse","mode":"pair"}`))
	second.RemoteAddr = "192.168.1.31:40000"
	secondRec := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(secondRec, second)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("expected second request 200, got %d", secondRec.Code)
	}
	if activePairing == nil || activePairing.Code != code {
		t.Fatalf("expected recovery to reuse existing pair window")
	}
	if !strings.Contains(secondRec.Body.String(), code) {
		t.Fatalf("expected reused code in second response, got %s", secondRec.Body.String())
	}
}

func TestAuthRecoverPairReturnsRecoverySessionAndStatus(t *testing.T) {
	withTempHome(t)
	recoveryLimiter.reset()
	EndPairingSession()
	if err := SetBootstrapSecret("session-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"session-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.32:40000"
	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var start map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	recoveryID := strings.TrimSpace(anyString(start["recovery_id"]))
	waitToken := strings.TrimSpace(anyString(start["wait_token"]))
	pairCode := strings.TrimSpace(anyString(start["pairCode"]))
	if recoveryID == "" || waitToken == "" || pairCode == "" {
		t.Fatalf("expected recovery_id, wait_token, pairCode in response, got %#v", start)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/auth/recover/session?id="+recoveryID+"&wait_token="+waitToken, nil)
	statusRec := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecoverSession(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status before submit: expected 200, got %d: %s", statusRec.Code, statusRec.Body.String())
	}
	if !strings.Contains(statusRec.Body.String(), `"state":"awaiting_pair_submit"`) {
		t.Fatalf("expected awaiting_pair_submit, got %s", statusRec.Body.String())
	}

	submitBody := bytes.NewReader([]byte(`{"token":"recovered-token","convexSiteUrl":"https://example.convex.cloud"}`))
	submitReq := httptest.NewRequest(http.MethodPost, "/auth/pair/submit?code="+pairCode, submitBody)
	submitRec := httptest.NewRecorder()
	(&HTTPServer{}).handlePairSubmit(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("submit failed: %d %s", submitRec.Code, submitRec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		finalReq := httptest.NewRequest(http.MethodGet, "/auth/recover/session?id="+recoveryID+"&wait_token="+waitToken, nil)
		finalRec := httptest.NewRecorder()
		(&HTTPServer{}).handleAuthRecoverSession(finalRec, finalReq)
		if finalRec.Code != http.StatusOK {
			t.Fatalf("status after submit: expected 200, got %d: %s", finalRec.Code, finalRec.Body.String())
		}
		if strings.Contains(finalRec.Body.String(), `"state":"recovered"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected recovered state, got %s", finalRec.Body.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestAuthRecoverPairAllowedWhenAuthExpired(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("expired-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	srv := &HTTPServer{token: "stale-token"}
	srv.authExpired.Store(true)

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"expired-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.21:40000"
	rec := httptest.NewRecorder()

	srv.handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRecoverRejectsDirectPublicIngress(t *testing.T) {
	recoveryLimiter.reset()
	setRequirePrivateRecoveryForTest(t, true)
	if err := SetBootstrapSecret("public-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"public-secret","mode":"pair"}`))
	req.RemoteAddr = "203.0.113.9:40000"
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "direct public HTTP") {
		t.Fatalf("expected direct-public-http message, got %s", rec.Body.String())
	}
}

func TestAuthRecoverAllowsRelayIngress(t *testing.T) {
	recoveryLimiter.reset()
	setRequirePrivateRecoveryForTest(t, true)
	if err := SetBootstrapSecret("relay-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"relay-secret","mode":"pair"}`))
	req.RemoteAddr = "203.0.113.9:40000"
	req.Header.Set("X-Relay-Password", "relay-password")
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRecoverRateLimitsPerIP(t *testing.T) {
	recoveryLimiter.reset()
	if err := SetBootstrapSecret("secret-789"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() { _ = SetBootstrapSecret("") }()

	mkReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(`{"secret":"secret-789","mode":"pair"}`))
		req.RemoteAddr = "192.168.1.14:40000"
		return req
	}

	first := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(first, mkReq())
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request 200, got %d", first.Code)
	}

	second := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(second, mkReq())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", second.Code)
	}
}

// TestAuthRecoverPairUnblocksBootstrapLoopWaiter is the regression test for
// the audit's G6: the bootstrap-loop in runBootstrapServe holds a local
// pointer to its pair session and blocks on session.done. /auth/recover
// mode=pair, when no fresh-bootstrap session exists, falls into one of two
// shapes:
//
//  1. Reuse: an active session already exists with no token →
//     handleAuthRecover returns its code. /auth/pair/submit fills it
//     and closes session.done. The bootstrap loop unblocks and sees
//     ReceivedToken set → re-execs into authenticated `yaver serve`.
//
//  2. Replace: no active session (or expired) → handleAuthRecover starts
//     a NEW session via StartPairingSession, which closes the previous
//     session's done as a side effect. The bootstrap loop's old pointer
//     unblocks, sees ReceivedToken empty, and rotates by calling
//     StartPairingSession itself — which would replace the recovery's
//     brand-new session. That rotation closes the recovery session's
//     done. completePairRecoveryInBackground checks ReceivedToken AT
//     THAT POINT — and if no submit landed first, the recovery is
//     effectively dropped.
//
// We test the safe path (#1, the reuse case) here. The unsafe path (#2,
// the rotation race) is covered by the next test, which proves it is at
// least observable rather than silent.
func TestAuthRecoverPairUnblocksBootstrapLoopWaiter(t *testing.T) {
	withTempHome(t)
	recoveryLimiter.reset()
	EndPairingSession()
	if err := SetBootstrapSecret("loop-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	// Stand in for the bootstrap loop: open a session, block on its done.
	loopSession, err := StartPairingSession(bootstrapPairingTTL)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}

	// Recovery comes in: should REUSE loopSession because it's active and
	// has no token yet.
	req := httptest.NewRequest(http.MethodPost, "/auth/recover",
		strings.NewReader(`{"secret":"loop-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.50:40000"
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/recover, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), loopSession.Code) {
		t.Fatalf("recovery did not reuse bootstrap-loop session: body=%s want code=%s",
			rec.Body.String(), loopSession.Code)
	}

	// Now simulate the user's pair-submit landing on the loop session.
	submitBody := bytes.NewReader([]byte(`{"token":"recovered-token","convexSiteUrl":"https://example.convex.cloud"}`))
	submitReq := httptest.NewRequest(http.MethodPost,
		"/auth/pair/submit?code="+loopSession.Code, submitBody)
	submitRec := httptest.NewRecorder()
	(&HTTPServer{}).handlePairSubmit(submitRec, submitReq)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("submit failed: %d %s", submitRec.Code, submitRec.Body.String())
	}

	// The bootstrap loop's waiter must wake.
	select {
	case <-loopSession.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("bootstrap-loop session.done never closed after recovery submit")
	}
	if loopSession.ReceivedToken != "recovered-token" {
		t.Fatalf("bootstrap-loop session did not receive token: got %q", loopSession.ReceivedToken)
	}
}

// TestAuthRecoverPairRotatesWhenActiveExpired pins down the documented but
// previously untested behavior that recovery falls back to starting a new
// session when activePairingSnapshot returns nil due to expiry. Anyone
// holding a pointer to the expired session must see their done channel
// closed — otherwise CLI callers (the bootstrap loop, `yaver auth pair`)
// will hang indefinitely.
func TestAuthRecoverPairRotatesWhenActiveExpired(t *testing.T) {
	withTempHome(t)
	recoveryLimiter.reset()
	EndPairingSession()
	if err := SetBootstrapSecret("rotation-secret"); err != nil {
		t.Fatalf("SetBootstrapSecret: %v", err)
	}
	defer func() {
		_ = SetBootstrapSecret("")
		EndPairingSession()
	}()

	// Open an expired session — TTL of 0 expires immediately.
	expired, err := StartPairingSession(0)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	// Wait one Unix-time tick to push the wall clock past ExpiresAt.
	time.Sleep(2 * time.Millisecond)
	if activePairingSnapshot() != nil {
		t.Fatalf("snapshot should be nil for an expired session")
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/recover",
		strings.NewReader(`{"secret":"rotation-secret","mode":"pair"}`))
	req.RemoteAddr = "192.168.1.51:40000"
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleAuthRecover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// The rotation must have closed the expired session's done so that
	// any waiter (the bootstrap loop) is unblocked and can reschedule.
	select {
	case <-expired.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expired session.done not closed after rotation; loop callers will hang")
	}

	// And a fresh session must be active.
	snap := activePairingSnapshot()
	if snap == nil {
		t.Fatalf("expected a fresh active pairing session after rotation")
	}
	if snap.Code == expired.Code {
		t.Fatalf("rotation produced identical code — generatePairCode collision or no rotation")
	}
}
