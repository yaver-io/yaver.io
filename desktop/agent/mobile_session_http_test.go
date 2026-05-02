package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// /mobile/sessions + /mobile/insert are the surface that lets a
// remote operator (CLI on yaver-test-ephemeral) trigger an
// "Open in Yaver" on a paired phone. We exercise the handlers
// directly so the test runs without a live BlackBox client.

func newMobileTestServer(t *testing.T) *HTTPServer {
	t.Helper()
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	return &HTTPServer{blackboxMgr: mgr}
}

func TestMobileSessionsEmpty(t *testing.T) {
	s := newMobileTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/mobile/sessions", nil)
	rr := httptest.NewRecorder()
	s.handleMobileSessions(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var resp struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad json: %v\n%s", err, rr.Body.String())
	}
	if len(resp.Sessions) != 0 {
		t.Fatalf("sessions: got %d, want 0", len(resp.Sessions))
	}
}

func TestMobileSessionsLists(t *testing.T) {
	s := newMobileTestServer(t)
	s.blackboxMgr.GetOrCreateSession("dev-1", "ios", "yaver-mobile")
	s.blackboxMgr.GetOrCreateSession("dev-2", "android", "yaver-mobile")

	req := httptest.NewRequest(http.MethodGet, "/mobile/sessions", nil)
	rr := httptest.NewRecorder()
	s.handleMobileSessions(rr, req)

	var resp struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("sessions: got %d, want 2", len(resp.Sessions))
	}
}

func TestMobileInsertRejectsEmptyApp(t *testing.T) {
	s := newMobileTestServer(t)
	body := bytes.NewReader([]byte(`{"app":""}`))
	req := httptest.NewRequest(http.MethodPost, "/mobile/insert", body)
	rr := httptest.NewRecorder()
	s.handleMobileInsert(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400. body=%s", rr.Code, rr.Body.String())
	}
}

func TestMobileInsertNoSessions(t *testing.T) {
	s := newMobileTestServer(t)
	body := bytes.NewReader([]byte(`{"app":"sfmg"}`))
	req := httptest.NewRequest(http.MethodPost, "/mobile/insert", body)
	rr := httptest.NewRecorder()
	s.handleMobileInsert(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503. body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no mobile sessions") {
		t.Fatalf("body should mention no sessions: %s", rr.Body.String())
	}
}

func TestMobileInsertBroadcasts(t *testing.T) {
	s := newMobileTestServer(t)
	sess := s.blackboxMgr.GetOrCreateSession("dev-1", "ios", "yaver-mobile")
	ch := sess.SubscribeCommands()

	body := bytes.NewReader([]byte(`{"app":"sfmg"}`))
	req := httptest.NewRequest(http.MethodPost, "/mobile/insert", body)
	rr := httptest.NewRecorder()
	s.handleMobileInsert(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200. body=%s", rr.Code, rr.Body.String())
	}
	select {
	case cmd := <-ch:
		if cmd.Command != "open_app" {
			t.Fatalf("command: got %q", cmd.Command)
		}
		if app, _ := cmd.Data["app"].(string); app != "sfmg" {
			t.Fatalf("data.app: got %v", cmd.Data["app"])
		}
	default:
		t.Fatal("expected command on the subscribed channel")
	}
}

func TestMobileInsertTargetsSpecificDevice(t *testing.T) {
	s := newMobileTestServer(t)
	a := s.blackboxMgr.GetOrCreateSession("dev-A", "ios", "yaver-mobile")
	b := s.blackboxMgr.GetOrCreateSession("dev-B", "ios", "yaver-mobile")
	chA := a.SubscribeCommands()
	chB := b.SubscribeCommands()

	body := bytes.NewReader([]byte(`{"app":"sfmg","deviceId":"dev-B"}`))
	req := httptest.NewRequest(http.MethodPost, "/mobile/insert", body)
	rr := httptest.NewRecorder()
	s.handleMobileInsert(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rr.Code, rr.Body.String())
	}

	// dev-A must NOT have received it.
	select {
	case cmd := <-chA:
		t.Fatalf("dev-A unexpectedly received %v", cmd)
	default:
	}
	select {
	case cmd := <-chB:
		if cmd.Command != "open_app" {
			t.Fatalf("dev-B got wrong command: %q", cmd.Command)
		}
	default:
		t.Fatal("dev-B did not receive open_app")
	}
}

func TestMobileInsertUnknownDeviceReturns404(t *testing.T) {
	s := newMobileTestServer(t)
	s.blackboxMgr.GetOrCreateSession("dev-A", "ios", "yaver-mobile")

	body := bytes.NewReader([]byte(`{"app":"sfmg","deviceId":"does-not-exist"}`))
	req := httptest.NewRequest(http.MethodPost, "/mobile/insert", body)
	rr := httptest.NewRecorder()
	s.handleMobileInsert(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404. body=%s", rr.Code, rr.Body.String())
	}
}
