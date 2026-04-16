package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGroupTwoFactorSecretReadsIn4CharGroups(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"JBSWY3DP":            "JBSW Y3DP",
		"JBSWY3DPEHPK3PXP":    "JBSW Y3DP EHPK 3PXP",
		"jbswy3dpehpk3pxp":    "JBSW Y3DP EHPK 3PXP",
		"ABC":                 "ABC",
	}
	for in, want := range cases {
		if got := groupTwoFactorSecret(in); got != want {
			t.Errorf("groupTwoFactorSecret(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTwoFactorErrorMapsInvalidCodeToHumanMessage(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"error": "INVALID_CODE"})
	err := parseTwoFactorError(400, raw)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did not match") {
		t.Fatalf("expected human-readable invalid-code message, got %q", msg)
	}
	if strings.Contains(msg, "INVALID_CODE") {
		t.Fatalf("raw sentinel should be suppressed, got %q", msg)
	}
}

func TestParseTwoFactorErrorFallsBackToHTTPStatus(t *testing.T) {
	err := parseTwoFactorError(500, []byte{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP code in message, got %q", err.Error())
	}
}

func TestTwoFactorConvexCallSendsBearerAndDecodesResponse(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"enabled":true,"recoveryCodesRemaining":4}`))
	}))
	defer server.Close()

	cfg := &Config{AuthToken: "abc123", ConvexSiteURL: server.URL}
	var out struct {
		Enabled                bool `json:"enabled"`
		RecoveryCodesRemaining int  `json:"recoveryCodesRemaining"`
	}
	if err := twoFactorConvexCall(cfg, http.MethodGet, "/auth/totp/status", nil, &out); err != nil {
		t.Fatalf("twoFactorConvexCall: %v", err)
	}
	if gotPath != "/auth/totp/status" {
		t.Errorf("path = %q", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q", gotMethod)
	}
	if gotAuth != "Bearer abc123" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !out.Enabled || out.RecoveryCodesRemaining != 4 {
		t.Errorf("decoded %+v", out)
	}
}

func TestTwoFactorConvexCallSurfacesConvexError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"INVALID_CODE"}`))
	}))
	defer server.Close()

	cfg := &Config{AuthToken: "t", ConvexSiteURL: server.URL}
	err := twoFactorConvexCall(cfg, http.MethodPost, "/auth/totp/enable", map[string]string{"code": "000000"}, nil)
	if err == nil {
		t.Fatal("expected error from invalid code")
	}
	if !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("expected invalid-code friendly message, got %q", err.Error())
	}
}
