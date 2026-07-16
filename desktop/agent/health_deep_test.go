package main

// health_deep_test.go — P8 install + self-healing tests.
// composeDeepHealth is pure enough to drive with a stubbed HTTPServer
// and stubbed clock; the real /health/deep handler is a thin wrap.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newTestKeeperForHealth(t *testing.T) *RunnerKeeper {
	t.Helper()
	tmp := t.TempDir()
	orig, _ := os.LookupEnv("HOME")
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
	k, err := NewRunnerKeeper()
	if err != nil {
		t.Fatalf("NewRunnerKeeper: %v", err)
	}
	return k
}

func TestComposeDeepHealth_MinimalReturnsOK(t *testing.T) {
	report := composeDeepHealth(&HTTPServer{}, time.Now())
	if !report.OK && report.Tmux.Status != "down" {
		// If tmux is available, OK should hold. If tmux isn't installed
		// (containerless CI), the report legitimately fails OK. Both
		// paths are correct.
		t.Fatalf("report=%+v", report)
	}
	if report.Agent.Status != "ok" {
		t.Fatalf("agent status = %q, want ok", report.Agent.Status)
	}
}

func TestComposeDeepHealth_RunnerKeeperSurfacesSessionState(t *testing.T) {
	k := newTestKeeperForHealth(t)
	k.SetMode("s1", KeeperModeAuto)
	k.EnqueuePrompt("s1", "hello", "cli")
	srv := &HTTPServer{runnerKeeper: k}
	report := composeDeepHealth(srv, time.Now())
	if report.RunnerKeeper.Status != "ok" {
		t.Fatalf("keeper status = %q body=%s", report.RunnerKeeper.Status, report.RunnerKeeper.Detail)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].SessionName != "s1" {
		t.Fatalf("sessions = %+v", report.Sessions)
	}
	if report.Sessions[0].QueuedCount != 1 {
		t.Fatalf("queued count = %d, want 1", report.Sessions[0].QueuedCount)
	}
}

func TestComposeDeepHealth_StalledSessionGetsRecoveryHint(t *testing.T) {
	k := newTestKeeperForHealth(t)
	k.SetMode("s1", KeeperModeAuto)
	// Force LastActivity to something 20 min in the past — the composer
	// should flag the session as stalled and surface a recovery hint.
	st := &SessionState{
		SessionName:  "s1",
		Mode:         KeeperModeAuto,
		LastActivity: time.Now().Add(-20 * time.Minute).UTC().Format(time.RFC3339),
	}
	k.mu.Lock()
	k.states["s1"] = st
	k.mu.Unlock()
	srv := &HTTPServer{runnerKeeper: k}
	report := composeDeepHealth(srv, time.Now())
	if len(report.Sessions) != 1 {
		t.Fatalf("sessions = %+v", report.Sessions)
	}
	sess := report.Sessions[0]
	if sess.Status != "stalled" {
		t.Fatalf("session status = %q, want stalled", sess.Status)
	}
	if sess.RecoveryHint == "" {
		t.Fatal("stalled session should carry a recovery hint")
	}
}

func TestHandleHealthDeep_HTTPRoundTrip(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/health/deep", nil)
	rec := httptest.NewRecorder()
	srv.handleHealthDeep(rec, req)
	// 200 when tmux is present, 503 when it isn't — either is correct
	// as long as the response body decodes to a DeepHealthReport.
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body DeepHealthReport
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v raw=%s", err, rec.Body.String())
	}
	if body.Agent.Status == "" {
		t.Fatal("agent status missing")
	}
}

func TestHandleHealthDeep_RejectsNonGET(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/health/deep", nil)
	rec := httptest.NewRecorder()
	srv.handleHealthDeep(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST should 405, got %d", rec.Code)
	}
}
