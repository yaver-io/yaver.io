package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func scopedRunnerAuthRequest(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-GuestUserID", "svc-runner-auth")
	req.Header.Set("X-Yaver-GuestScope", "runner-auth")
	return req
}

func TestScopedRunnerAuthBrowserStartRejectsClaudeCodex(t *testing.T) {
	srv := &HTTPServer{}
	for _, runner := range []string{"claude", "codex"} {
		rec := httptest.NewRecorder()
		req := scopedRunnerAuthRequest("/runner-auth/browser/start", `{"runner":"`+runner+`"}`)
		srv.handleRunnerBrowserAuthStart(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s scoped browser auth status = %d, want 403; body=%s", runner, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "owner-authenticated Yaver machine") {
			t.Fatalf("%s scoped browser auth error should explain owner boundary, got %s", runner, rec.Body.String())
		}
	}
}

func TestScopedRunnerAuthCredentialsImportRejectsClaudeCodex(t *testing.T) {
	srv := &HTTPServer{}
	for _, runner := range []string{"claude", "codex"} {
		rec := httptest.NewRecorder()
		req := scopedRunnerAuthRequest("/runner-auth/credentials/import", `{"runner":"`+runner+`","credentialsJson":"{\"ok\":true}"}`)
		srv.handleRunnerAuthCredentialsImport(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s scoped credentials import status = %d, want 403; body=%s", runner, rec.Code, rec.Body.String())
		}
	}
}

func TestScopedRunnerAuthSetupRejectsClaudeCodex(t *testing.T) {
	srv := &HTTPServer{}
	for _, runner := range []string{"claude", "codex"} {
		rec := httptest.NewRecorder()
		req := scopedRunnerAuthRequest("/runner-auth/setup", `{"runner":"`+runner+`","install_if_missing":false,"allow_install_only":true}`)
		srv.handleRunnerAuthSetup(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s scoped setup status = %d, want 403; body=%s", runner, rec.Code, rec.Body.String())
		}
	}
}

func TestScopedRunnerAuthBoundaryAllowsOpenCodeCredentialImportValidation(t *testing.T) {
	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	req := scopedRunnerAuthRequest("/runner-auth/credentials/import", `{"runner":"opencode","credentialsJson":"not-json"}`)
	srv.handleRunnerAuthCredentialsImport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("opencode should pass the Claude/Codex boundary and fail JSON validation, got %d body=%s", rec.Code, rec.Body.String())
	}
}
