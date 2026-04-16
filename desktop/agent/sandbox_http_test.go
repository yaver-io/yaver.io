package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSandboxStatusIncludesEnabledModeAndDefaults(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/sandbox/status", "tok", "")
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["enabledMode"] != "off" {
		t.Fatalf("expected enabledMode=off, got %v", body["enabledMode"])
	}
	if body["networkMode"] != "host" {
		t.Fatalf("expected networkMode=host, got %v", body["networkMode"])
	}
}

func TestSandboxQuickstartRejectsMissingDocker(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	reqBody := `{"mode":"guests","buildImage":false}`
	status, body := doRequest(t, "POST", baseURL+"/sandbox/quickstart", "tok", reqBody)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 when docker missing, got %d body=%v", status, body)
	}
	if errText, _ := body["error"].(string); !strings.Contains(strings.ToLower(errText), "docker") {
		t.Fatalf("expected docker error, got %v", body["error"])
	}
}

func TestApplySandboxQuickstartConfiguresGuestIsolation(t *testing.T) {
	srv := NewHTTPServer(0, "tok", "user", "device", "", "host", NewTaskManager(t.TempDir(), nil, defaultRunner))
	srv.containerRunner = &ContainerRunner{}
	summary, message, err := srv.applySandboxQuickstart("guests", false)
	if err != nil {
		t.Fatalf("applySandboxQuickstart() error = %v", err)
	}
	if !srv.containerizeGuests || srv.containerizeHost {
		t.Fatalf("unexpected flags guests=%v host=%v", srv.containerizeGuests, srv.containerizeHost)
	}
	if srv.taskMgr.ContainerNetwork != "host" {
		t.Fatalf("expected default network host, got %q", srv.taskMgr.ContainerNetwork)
	}
	if !srv.taskMgr.ContainerReadOnly {
		t.Fatal("expected quickstart to default read-only rootfs on")
	}
	if summary.EnabledMode != "guests" {
		t.Fatalf("expected enabled mode guests, got %q", summary.EnabledMode)
	}
	if message == "" {
		t.Fatal("expected status message")
	}
}

func TestSandboxQuickstartResponseIncludesSandboxSummary(t *testing.T) {
	srv := NewHTTPServer(0, "tok", "user", "device", "", "host", NewTaskManager(t.TempDir(), nil, defaultRunner))
	srv.containerRunner = &ContainerRunner{}

	req := strings.NewReader(`{"mode":"host","buildImage":false}`)
	httpReq, _ := http.NewRequest(http.MethodPost, "/sandbox/quickstart", req)
	rr := httptest.NewRecorder()
	srv.handleSandboxQuickstart(rr, httpReq)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sandbox, ok := body["sandbox"].(map[string]any)
	if !ok {
		t.Fatalf("expected sandbox object, got %T", body["sandbox"])
	}
	if sandbox["enabledMode"] != "host" {
		t.Fatalf("expected host mode, got %v", sandbox["enabledMode"])
	}
}
