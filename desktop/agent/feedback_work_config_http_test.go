package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeedbackWorkConfigRouteStaysOwnerAuthenticated(t *testing.T) {
	src := readSourceFile(t, "httpserver.go")
	want := `mux.HandleFunc("/feedback-work/config", s.auth(s.handleFeedbackWorkConfig))`
	if !strings.Contains(src, want) {
		t.Fatalf("/feedback-work/config must stay behind owner auth; missing registration %q", want)
	}
}

func TestHandleFeedbackWorkConfigGetDefaults(t *testing.T) {
	_ = withTempHome(t)
	if err := SaveConfig(&Config{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/feedback-work/config", nil)
	w := httptest.NewRecorder()
	s.handleFeedbackWorkConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got feedbackWorkConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || got.Enabled || got.Running || got.CreateProviderIssues {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestHandleFeedbackWorkConfigPatchPersistsProviderIssueGateButDoesNotRunWithoutBackendAuth(t *testing.T) {
	_ = withTempHome(t)
	if err := SaveConfig(&Config{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	s := &HTTPServer{}
	body := strings.NewReader(`{"enabled":true,"createProviderIssues":true,"intervalSeconds":15,"projectSlug":"demo"}`)
	req := httptest.NewRequest(http.MethodPatch, "/feedback-work/config", body)
	w := httptest.NewRecorder()
	s.handleFeedbackWorkConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got feedbackWorkConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || !got.Enabled || got.Running || got.RuntimeReason != "backend auth unavailable" {
		t.Fatalf("unexpected response: %+v", got)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FeedbackWorkWorker == nil {
		t.Fatal("FeedbackWorkWorker config missing")
	}
	if !cfg.FeedbackWorkWorker.Enabled || !cfg.FeedbackWorkWorker.CreateProviderIssues {
		t.Fatalf("flags not persisted: %+v", cfg.FeedbackWorkWorker)
	}
	if cfg.FeedbackWorkWorker.IntervalSeconds != 15 || cfg.FeedbackWorkWorker.ProjectSlug != "demo" {
		t.Fatalf("fields not persisted: %+v", cfg.FeedbackWorkWorker)
	}
}

func TestHandleFeedbackWorkConfigPatchStartsAndStopsLiveWorker(t *testing.T) {
	_ = withTempHome(t)
	if err := SaveConfig(&Config{}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &HTTPServer{token: "tok", convexURL: "http://127.0.0.1:9", deviceID: "device-1"}
	s.feedbackWorkWorkerRootCtx = ctx

	req := httptest.NewRequest(http.MethodPatch, "/feedback-work/config", strings.NewReader(`{"enabled":true,"intervalSeconds":15,"projectSlug":"demo"}`))
	w := httptest.NewRecorder()
	s.handleFeedbackWorkConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var enabled feedbackWorkConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &enabled); err != nil {
		t.Fatalf("decode enabled: %v", err)
	}
	if !enabled.OK || !enabled.Enabled || !enabled.Running {
		t.Fatalf("worker did not start: %+v", enabled)
	}
	if status := s.feedbackWorkWorkerRuntimeStatus(); !status.Running {
		t.Fatalf("server runtime not running: %+v", status)
	}

	req = httptest.NewRequest(http.MethodPatch, "/feedback-work/config", strings.NewReader(`{"enabled":false}`))
	w = httptest.NewRecorder()
	s.handleFeedbackWorkConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", w.Code, w.Body.String())
	}
	var disabled feedbackWorkConfigResponse
	if err := json.Unmarshal(w.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disabled: %v", err)
	}
	if !disabled.OK || disabled.Enabled || disabled.Running || disabled.RuntimeReason != "disabled" {
		t.Fatalf("worker did not stop: %+v", disabled)
	}
	if status := s.feedbackWorkWorkerRuntimeStatus(); status.Running {
		t.Fatalf("server runtime still running: %+v", status)
	}
}
