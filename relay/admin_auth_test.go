package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// chdirTemp moves the test into t.TempDir for the duration of the
// test. Used by tests that exercise handleSetPassword, which writes
// .relay-password to cwd; without this the test pollutes the repo
// with a stray credential file.
func chdirTemp(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// newRelayWithAdminAuth wires up a relay with an HTTP test server
// covering every endpoint touched by the C-9 / H-14 fixes.
func newRelayWithAdminAuth(t *testing.T, password, adminToken string) (*RelayServer, *httptest.Server) {
	t.Helper()
	rs := NewRelayServer(0, 0, password, "", "")
	// Inject the admin token directly. main.go normally reads
	// RELAY_ADMIN_TOKEN at process start; tests bypass the env.
	rs.adminToken = adminToken

	mux := http.NewServeMux()
	mux.HandleFunc("/health", rs.handleHealth)
	mux.HandleFunc("/tunnels", rs.handleListTunnels)
	mux.HandleFunc("/presence", rs.handlePresence)
	mux.HandleFunc("/admin/set-password", rs.handleSetPassword)
	mux.HandleFunc("/admin/status", rs.handleAdminStatus)
	mux.HandleFunc("/admin/bandwidth", rs.handleBandwidthStats)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return rs, srv
}

// H-14: /health must be slim — only ok + version. No tunnel counts,
// no bandwidth, no activeDevices.
func TestHealth_IsSlim(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "")
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health expected 200, got %d", resp.StatusCode)
	}
	var got map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	for _, leakedKey := range []string{
		"tunnels", "exposeRoutes", "activeDevices",
		"loadPercent", "limitsRelaxed", "bandwidthMultiplier",
	} {
		if _, present := got[leakedKey]; present {
			t.Errorf("health response leaks %q (full body: %v)", leakedKey, got)
		}
	}
	if got["ok"] != true {
		t.Errorf("expected ok:true, got %v", got["ok"])
	}
	if got["version"] == nil {
		t.Errorf("expected version field, got nil")
	}
}

// H-14: /tunnels must require admin auth.
func TestTunnels_RequiresAuth(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "")

	// No auth → 401.
	resp, _ := http.Get(srv.URL + "/tunnels")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong password → 401.
	req, _ := http.NewRequest("GET", srv.URL+"/tunnels", nil)
	req.Header.Set("X-Relay-Password", "wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Correct password → 200.
	req, _ = http.NewRequest("GET", srv.URL+"/tunnels", nil)
	req.Header.Set("X-Relay-Password", "test-pw")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with correct password, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// H-14: /presence must require admin auth and cap ids list at 50.
func TestPresence_RequiresAuth(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "")

	resp, _ := http.Get(srv.URL + "/presence?id=device-x")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/presence?id=device-x", nil)
	req.Header.Set("X-Relay-Password", "test-pw")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with auth, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPresence_AcceptsAdminTokenBearer(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "ADMIN-SECRET-123")

	req, _ := http.NewRequest("GET", srv.URL+"/presence?id=device-x", nil)
	req.Header.Set("Authorization", "Bearer ADMIN-SECRET-123")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with admin bearer, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	req, _ = http.NewRequest("GET", srv.URL+"/presence?id=device-x", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong bearer, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestPresence_CapsIdsListAt50(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "")

	// Exactly 50 — should pass.
	ids50 := strings.Repeat("a,", 49) + "a"
	req, _ := http.NewRequest("GET", srv.URL+"/presence?ids="+ids50, nil)
	req.Header.Set("X-Relay-Password", "test-pw")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 for 50 ids, got %d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// 51 entries — should reject with 400.
	ids51 := strings.Repeat("a,", 50) + "a"
	req, _ = http.NewRequest("GET", srv.URL+"/presence?ids="+ids51, nil)
	req.Header.Set("X-Relay-Password", "test-pw")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 for 51 ids, got %d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

// H-14: /admin/status must require auth.
func TestAdminStatus_RequiresAuth(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "")
	resp, _ := http.Get(srv.URL + "/admin/status")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// H-14: /admin/bandwidth must require auth.
func TestAdminBandwidth_RequiresAuth(t *testing.T) {
	_, srv := newRelayWithAdminAuth(t, "test-pw", "")
	resp, _ := http.Get(srv.URL + "/admin/bandwidth")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// C-9: when RELAY_ADMIN_TOKEN is set, /admin/set-password requires it
// even if a password is currently set (so an attacker who guesses or
// steals the password can't pivot to permanent admin control).
func TestSetPassword_RequiresAdminTokenWhenConfigured(t *testing.T) {
	// handleSetPassword persists to .relay-password in cwd. Run from
	// t.TempDir so the test's side effect doesn't pollute the repo.
	chdirTemp(t)
	rs, srv := newRelayWithAdminAuth(t, "current-pw", "ADMIN-XYZ")

	// Without admin token (but with current_password matching) → 401.
	body, _ := json.Marshal(map[string]string{
		"password":         "new-pw",
		"current_password": "current-pw",
	})
	req, _ := http.NewRequest("POST", srv.URL+"/admin/set-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without admin token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Password should not have changed.
	if rs.getPassword() != "current-pw" {
		t.Fatalf("password changed without admin token: %q", rs.getPassword())
	}

	// With admin token → 200, password updates.
	req, _ = http.NewRequest("POST", srv.URL+"/admin/set-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ADMIN-XYZ")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 with admin token, got %d body=%s", resp.StatusCode, buf)
	}
	resp.Body.Close()
	if rs.getPassword() != "new-pw" {
		t.Fatalf("password should have rotated to new-pw, got %q", rs.getPassword())
	}
}

// C-9: when no password AND no admin token, /admin/set-password must
// refuse (no "first write wins" footgun).
func TestSetPassword_RefusesFirstWriteWinsWithoutAdminToken(t *testing.T) {
	rs, srv := newRelayWithAdminAuth(t, "", "")

	body, _ := json.Marshal(map[string]string{
		"password": "attacker-pw",
	})
	req, _ := http.NewRequest("POST", srv.URL+"/admin/set-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 (no first-write-wins), got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if rs.getPassword() != "" {
		t.Fatalf("expected empty password to remain unchanged, got %q", rs.getPassword())
	}
}
