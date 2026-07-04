package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newWhatsAppIngressTestServer(t *testing.T) *HTTPServer {
	t.Helper()
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	return NewHTTPServer(0, "owner-token", "owner-user", "device-1", "", "host", tm)
}

func TestWhatsAppIngressDisabledByDefault(t *testing.T) {
	srv := newWhatsAppIngressTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/integrations/whatsapp/command", strings.NewReader(`{"action":"status"}`))
	rec := httptest.NewRecorder()
	srv.handleWhatsAppCommand(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestWhatsAppIngressRejectsBadSecret(t *testing.T) {
	t.Setenv("YAVER_WHATSAPP_INGRESS_SECRET", "good")
	srv := newWhatsAppIngressTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/integrations/whatsapp/command", strings.NewReader(`{"action":"status"}`))
	req.Header.Set("X-Yaver-WhatsApp-Secret", "bad")
	rec := httptest.NewRecorder()
	srv.handleWhatsAppCommand(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWhatsAppIngressStatusWithSecret(t *testing.T) {
	t.Setenv("YAVER_WHATSAPP_INGRESS_SECRET", "good")
	srv := newWhatsAppIngressTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/integrations/whatsapp/command", bytes.NewReader([]byte(`{"action":"status"}`)))
	req.Header.Set("X-Yaver-WhatsApp-Secret", "good")
	rec := httptest.NewRecorder()
	srv.handleWhatsAppCommand(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok response, got %s", rec.Body.String())
	}
}
