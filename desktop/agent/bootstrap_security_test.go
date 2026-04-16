package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBootstrapInfoHidesPasskeyBehindProxy(t *testing.T) {
	session, err := StartPairingSession(10 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()

	bs := &bootstrapHTTPServer{}

	directReq := httptest.NewRequest(http.MethodGet, "/info", nil)
	directRec := httptest.NewRecorder()
	bs.handleInfo(directRec, directReq)
	if !strings.Contains(directRec.Body.String(), session.Code) {
		t.Fatalf("expected direct bootstrap info to include passkey")
	}

	proxyReq := httptest.NewRequest(http.MethodGet, "/info", nil)
	proxyReq.Header.Set("X-Forwarded-For", "203.0.113.5")
	proxyRec := httptest.NewRecorder()
	bs.handleInfo(proxyRec, proxyReq)
	if strings.Contains(proxyRec.Body.String(), session.Code) {
		t.Fatalf("expected proxied bootstrap info to hide passkey")
	}
}

func TestBootstrapEncryptedPairRequiresPairCode(t *testing.T) {
	session, err := StartPairingSession(10 * time.Minute)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	defer EndPairingSession()

	bs := &bootstrapHTTPServer{}

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/encrypted", strings.NewReader(`{"encrypted":"abc","senderPublicKey":"def"}`))
	rec := httptest.NewRecorder()
	bs.handlePairEncrypted(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing code, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/auth/pair/encrypted", strings.NewReader(`{"code":"WRONG","encrypted":"abc","senderPublicKey":"def"}`))
	rec = httptest.NewRecorder()
	bs.handlePairEncrypted(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong code, got %d", rec.Code)
	}

	if activePairing == nil || activePairing.Code != session.Code {
		t.Fatalf("expected active pairing session to remain intact")
	}
}
