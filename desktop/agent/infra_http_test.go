package main

import (
	"testing"
)

func TestInfraSummaryEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "infra-token", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/infra/summary", "infra-token", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	machine, ok := body["machine"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected machine payload, got %#v", body["machine"])
	}
	if machine["deviceId"] != "test-device-id" {
		t.Fatalf("deviceId = %v, want test-device-id", machine["deviceId"])
	}
	if _, ok := body["capabilities"].(map[string]interface{}); !ok {
		t.Fatalf("expected capabilities payload")
	}
	if _, ok := body["sharing"].(map[string]interface{}); !ok {
		t.Fatalf("expected sharing payload")
	}
}

func TestInfraPowerRequiresConfirm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "infra-token", tm)
	defer cancel()

	status, body := doRequest(t, "POST", baseURL+"/infra/power", "infra-token", `{"action":"agent_shutdown"}`)
	if status != 400 {
		t.Fatalf("expected 400, got %d", status)
	}
	if body["error"] != "confirm=true required" {
		t.Fatalf("unexpected error: %#v", body["error"])
	}
}
