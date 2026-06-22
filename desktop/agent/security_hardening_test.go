package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSecurityCORSRejectsUntrustedBrowserOrigin(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden preflight for untrusted origin, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("untrusted origin received CORS allow header %q", got)
	}
}

func TestSecurityCORSAllowsFirstPartyAndNoOriginClients(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://dashboard.yaver.io")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected first-party preflight to pass, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.yaver.io" {
		t.Fatalf("expected echoed first-party origin, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	resp = httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected wildcard for no-Origin compatibility clients, got %q", got)
	}
}

func TestSecurityPairedTokensNeverPersistBearer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const bearer = "raw-secret-bearer-token"

	if err := AddPairedToken(bearer, "phone", "https://convex.example", "iphone"); err != nil {
		t.Fatalf("AddPairedToken failed: %v", err)
	}
	path, err := pairedTokensPath()
	if err != nil {
		t.Fatalf("pairedTokensPath failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read paired tokens: %v", err)
	}
	if strings.Contains(string(data), bearer) {
		t.Fatalf("paired token ledger persisted raw bearer token: %s", string(data))
	}
	if !strings.Contains(string(data), pairedTokenFingerprint(bearer)) {
		t.Fatalf("paired token ledger did not persist token fingerprint: %s", string(data))
	}
}

func TestSecurityPairedTokensScrubLegacyBearerOnSave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const bearer = "legacy-raw-bearer-token"
	path, err := pairedTokensPath()
	if err != nil {
		t.Fatalf("pairedTokensPath failed: %v", err)
	}
	payload := map[string]any{
		"tokens": []PairedToken{{
			TokenHash: pairedTokenFingerprint(bearer),
			Token:     bearer,
			Label:     "legacy",
			AddedAt:   "2026-01-01T00:00:00Z",
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal legacy ledger: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write legacy ledger: %v", err)
	}

	if err := AddPairedToken(bearer, "legacy", "", ""); err != nil {
		t.Fatalf("AddPairedToken update failed: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scrubbed ledger: %v", err)
	}
	if strings.Contains(string(data), bearer) {
		t.Fatalf("legacy raw bearer token was not scrubbed: %s", string(data))
	}
}
