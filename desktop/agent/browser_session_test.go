package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBrowserSessionScopedToPath(t *testing.T) {
	s := &HTTPServer{}
	token, _, err := s.issueBrowserSession("/ws/terminal", time.Minute)
	if err != nil {
		t.Fatalf("issueBrowserSession: %v", err)
	}
	if !s.validateBrowserSession(token, "/ws/terminal") {
		t.Fatalf("expected browser session to allow exact path")
	}
	if s.validateBrowserSession(token, "/ws/logs") {
		t.Fatalf("expected browser session to reject different path")
	}
}

func TestHandleBrowserSessionRejectsUnsafePaths(t *testing.T) {
	s := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/auth/browser-session", strings.NewReader(`{"pathPrefix":"/tasks"}`))
	rec := httptest.NewRecorder()
	s.handleBrowserSession(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
