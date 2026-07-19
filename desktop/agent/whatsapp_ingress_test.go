package main

import (
	"bytes"
	"encoding/json"
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

func TestWhatsAppTaskDefersCloudPlacementInsteadOfRunningLocal(t *testing.T) {
	t.Setenv("YAVER_WHATSAPP_INGRESS_SECRET", "good")
	var seen []string
	var metadataPayloads []map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch r.URL.Path {
		case "/tasks/placement/preview":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-preview",
				"lane":           "cloud_standard",
				"resourceClass":  "standard",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-1",
				"lane":           "cloud_standard",
				"resourceClass":  "standard",
				"targetDeviceId": "cloud-dev",
				"wakeRequired":   true,
			})
		case "/tasks/placement/activate":
			metadataPayloads = append(metadataPayloads, body)
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":             false,
				"action":         "runner_auth_required",
				"targetDeviceId": "cloud-dev",
				"reason":         "Cloud Workspace is awake but Codex needs sign-in before WhatsApp tasks can run.",
			})
		case "/tasks/dispatch-intents":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      "queued",
			})
		case "/tasks/dispatch-intents/status":
			metadataPayloads = append(metadataPayloads, body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "intent-1",
				"localTaskId": body["localTaskId"],
				"status":      body["status"],
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{ConvexSiteURL: backend.URL, AuthToken: "owner-token"}); err != nil {
		t.Fatal(err)
	}
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	tm.DummyMode = true
	srv := NewHTTPServer(0, "owner-token", "owner-user", "local-dev", backend.URL, "host", tm)

	req := httptest.NewRequest(http.MethodPost, "/integrations/whatsapp/command", bytes.NewReader([]byte(`{
		"action":"task",
		"projectSlug":"demo",
		"commandText":"build apk with private prompt"
	}`)))
	req.Header.Set("X-Yaver-WhatsApp-Secret", "good")
	rec := httptest.NewRecorder()
	srv.handleWhatsAppCommand(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if tasks := tm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("expected no local task, got %d", len(tasks))
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["action"] != "cloud_workspace_required" || resp["pendingTaskId"] == "" {
		t.Fatalf("response = %#v", resp)
	}
	for i, payload := range metadataPayloads {
		for _, forbidden := range []string{"title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "commandText"} {
			if _, ok := payload[forbidden]; ok {
				t.Fatalf("metadata payload %d leaked %q: %#v", i, forbidden, payload)
			}
		}
	}
	if len(seen) < 5 || seen[0] != "/tasks/placement/preview" || seen[1] != "/tasks/placement/record" || seen[2] != "/tasks/placement/activate" {
		t.Fatalf("backend paths = %#v", seen)
	}
}
