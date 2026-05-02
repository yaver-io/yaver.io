package main

// vault_http_integration_test.go — end-to-end /vault/* CRUD over HTTP.
// Proves the encrypted-on-host vault actually works as a unit, not
// just that the Go code compiles. Spins up a real HTTPServer on a
// random port, points HOME at a temp dir so vault.enc lands in the
// sandbox, and walks the full set → get → list → delete cycle.

import (
	"testing"
)

func TestVaultHTTPCRUD(t *testing.T) {
	// Redirect ~/.yaver so vault.enc is created inside a sandbox and
	// doesn't clobber the dev's real vault.
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// Initialise the vault store on the server. main.go does this at
	// startup; tests have to wire it in manually.
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	currentTestHTTPServer.vaultStore = vs

	// 1. Initially empty.
	status, body := doRequest(t, "GET", baseURL+"/vault/list", "owner-tok", "")
	if status != 200 {
		t.Fatalf("list: got %d, body=%v", status, body)
	}

	// 2. Set entry.
	status, _ = doRequest(t, "POST", baseURL+"/vault/set", "owner-tok",
		`{"name":"OPENAI_API_KEY","category":"api-key","value":"sk-fake-test-1234","notes":"test key"}`)
	if status != 200 {
		t.Fatalf("set: got %d", status)
	}

	// 3. List should now include it — without the value. The response
	// shape is {"entries": [...], "projects": [...]}.
	_, body = doRequest(t, "GET", baseURL+"/vault/list", "owner-tok", "")
	entriesIface, ok := body["entries"].([]interface{})
	if !ok {
		t.Fatalf("expected 'entries' array in /vault/list response, got %v", body)
	}
	if len(entriesIface) != 1 {
		t.Fatalf("expected 1 entry after set, got %d (%v)", len(entriesIface), entriesIface)
	}
	first := entriesIface[0].(map[string]interface{})
	if first["name"] != "OPENAI_API_KEY" {
		t.Errorf("wrong name: %v", first["name"])
	}
	if _, hasValue := first["value"]; hasValue {
		t.Error("vault list leaked the value field — must never be in summaries")
	}

	// 4. Get returns the plaintext.
	status, getBody := doRequest(t, "GET", baseURL+"/vault/get?name=OPENAI_API_KEY", "owner-tok", "")
	if status != 200 {
		t.Fatalf("get: got %d", status)
	}
	if getBody["value"] != "sk-fake-test-1234" {
		t.Errorf("wrong value returned: %v", getBody["value"])
	}

	// 5. Get missing → 404.
	status, _ = doRequest(t, "GET", baseURL+"/vault/get?name=NOPE", "owner-tok", "")
	if status != 404 {
		t.Errorf("missing entry: expected 404, got %d", status)
	}

	// 6. Set without a value is rejected.
	status, _ = doRequest(t, "POST", baseURL+"/vault/set", "owner-tok",
		`{"name":"EMPTY","category":"api-key","value":""}`)
	if status != 400 {
		t.Errorf("empty value: expected 400, got %d", status)
	}

	// 7. Wrong token is forbidden.
	status, _ = doRequest(t, "GET", baseURL+"/vault/list", "wrong-token", "")
	if status != 401 && status != 403 {
		t.Errorf("wrong token on /vault/list: expected 401/403, got %d", status)
	}

	// 8. An active support bearer MUST still be blocked.
	resetSupport(t)
	sess := StartSupportSession(SupportStartOptions{Label: "test"})
	defer StopSupportSession()
	status, _ = doRequest(t, "GET", baseURL+"/vault/list", sess.Token, "")
	if status == 200 {
		t.Error("support bearer should NEVER open /vault/list")
	}

	// 9. Delete → then 404 on next get.
	status, _ = doRequest(t, "DELETE", baseURL+"/vault/delete?name=OPENAI_API_KEY", "owner-tok", "")
	if status != 200 {
		t.Errorf("delete: got %d", status)
	}
	status, _ = doRequest(t, "GET", baseURL+"/vault/get?name=OPENAI_API_KEY", "owner-tok", "")
	if status != 404 {
		t.Errorf("after delete: expected 404, got %d", status)
	}
}

