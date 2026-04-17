package main

// apikeys_integration_test.go — exercises the /apikeys HTTP surface
// end-to-end *without* hitting Convex. POST creates an SDK token via
// the Convex backend (so we skip that flow here); GET lists the local
// registry and DELETE disables. RegisterAPIKey seeds the registry
// directly so the list/disable path is testable in isolation.

import (
	"encoding/json"
	"testing"
)

func TestAPIKeysListAndDisable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// Seed two keys directly into the on-disk registry (bypassing
	// Convex). The HTTP handler reads this registry for GET + DELETE.
	if err := RegisterAPIKey("raw-token-alpha", "alpha-app", []string{"feedback"}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	if err := RegisterAPIKey("raw-token-bravo", "bravo-app", []string{"feedback", "voice"}); err != nil {
		t.Fatalf("register bravo: %v", err)
	}

	// GET lists both.
	status, body := doRequest(t, "GET", baseURL+"/apikeys", "owner-tok", "")
	if status != 200 {
		t.Fatalf("list: HTTP %d", status)
	}
	rawKeys, _ := body["keys"].([]interface{})
	if len(rawKeys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(rawKeys))
	}

	// Each entry must have a tokenHash and label — and must NOT have
	// the raw token (that'd be a leak).
	for _, k := range rawKeys {
		m := k.(map[string]interface{})
		if m["tokenHash"] == "" {
			t.Error("entry missing tokenHash")
		}
		if _, ok := m["token"]; ok {
			t.Error("LEAK: /apikeys response carried a raw `token` field")
		}
	}

	// DELETE by label disables.
	status, _ = doRequest(t, "DELETE", baseURL+"/apikeys?id=alpha-app", "owner-tok", "")
	if status != 200 {
		t.Fatalf("delete: HTTP %d", status)
	}
	if !IsAPIKeyDisabled("raw-token-alpha") {
		t.Error("IsAPIKeyDisabled should now return true for alpha-app")
	}
	if IsAPIKeyDisabled("raw-token-bravo") {
		t.Error("bravo should still be enabled")
	}

	// A second GET reflects the disabled state.
	status, body = doRequest(t, "GET", baseURL+"/apikeys", "owner-tok", "")
	keys2, _ := body["keys"].([]interface{})
	disabledFound := false
	for _, k := range keys2 {
		m := k.(map[string]interface{})
		if m["label"] == "alpha-app" && m["disabled"] == true {
			disabledFound = true
		}
	}
	if !disabledFound {
		b, _ := json.Marshal(keys2)
		t.Errorf("alpha-app should show disabled=true; got %s", string(b))
	}

	// Missing id is 400.
	status, _ = doRequest(t, "DELETE", baseURL+"/apikeys", "owner-tok", "")
	if status != 400 {
		t.Errorf("DELETE without id: expected 400, got %d", status)
	}

	// Unknown id is 404.
	status, _ = doRequest(t, "DELETE", baseURL+"/apikeys?id=ghost", "owner-tok", "")
	if status != 404 {
		t.Errorf("DELETE unknown id: expected 404, got %d", status)
	}
}

func TestAPIKeysPOSTRequiresLabel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// Missing label → 400 (this path doesn't hit Convex).
	status, _ := doRequest(t, "POST", baseURL+"/apikeys", "owner-tok", `{"scopes":["feedback"]}`)
	if status != 400 {
		t.Errorf("empty label: expected 400, got %d", status)
	}

	// > 80 char label → 400 (label cap rule).
	longLabel := ""
	for i := 0; i < 100; i++ {
		longLabel += "x"
	}
	status, _ = doRequest(t, "POST", baseURL+"/apikeys", "owner-tok",
		`{"label":"`+longLabel+`"}`)
	if status != 400 {
		t.Errorf("long label: expected 400, got %d", status)
	}
}
