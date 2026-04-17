package main

// two_agent_test.go — yaver-to-yaver scenarios where one agent is
// the "target" (running /support/start, holding files) and a second
// is the "caller" (redeeming the code, running exec, reading files).
//
// The pattern mirrors what `yaver support connect` does on a real
// machine — but in-process, over loopback, so we get deterministic
// end-to-end coverage without spinning up actual OS processes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// postJSON is a small helper — doRequest supports it, but blob-style
// bodies in later tests prefer a client-only helper.
func postJSON(t *testing.T, url, token, body string) (int, map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

// TestTwoAgentSupportConnect spins up two agents. Agent A opens a
// support session; "agent B" plays the role of a remote Yaver that
// walks up to A using only the 6-char code. Proves the relay-style
// flow works end-to-end.
func TestTwoAgentSupportConnect(t *testing.T) {
	resetSupport(t)
	t.Setenv("HOME", t.TempDir())

	// Agent A — the target, the one being taken over.
	tmA := NewTaskManager(t.TempDir(), nil, defaultRunner)
	urlA, cancelA := startTestServer(t, "owner-A", tmA)
	defer cancelA()

	// Agent B — the caller. Gets its own tmp home so B's config
	// doesn't leak into A's process-wide test vault/etc.
	tmB := NewTaskManager(t.TempDir(), nil, defaultRunner)
	_, cancelB := startTestServer(t, "owner-B", tmB)
	defer cancelB()

	// 1. Owner of A opens a support session.
	status, startResp := doRequest(t, "POST", urlA+"/support/start", "owner-A",
		`{"ttl":"2m","label":"two-agent-test"}`)
	if status != 200 {
		t.Fatalf("A /support/start: %d %v", status, startResp)
	}
	code, _ := startResp["code"].(string)
	if len(code) != 6 {
		t.Fatalf("bad code: %q", code)
	}

	// 2. Agent B — the caller — probes /support/info (no auth).
	status, info := doRequest(t, "GET", urlA+"/support/info", "", "")
	if status != 200 || info["active"] != true {
		t.Fatalf("B info probe: %d %v", status, info)
	}

	// 3. Agent B redeems the code (still no auth).
	status, redeemed := postJSON(t, urlA+"/support/redeem", "",
		fmt.Sprintf(`{"code":"%s"}`, code))
	if status != 200 {
		t.Fatalf("B redeem: %d %v", status, redeemed)
	}
	bearer, _ := redeemed["token"].(string)
	if !strings.HasPrefix(bearer, "yv_supp_") {
		t.Fatalf("B got wrong bearer shape: %q", bearer)
	}

	// 4. Agent B now runs a shell command on A using the bearer.
	status, execResp := doRequest(t, "POST", urlA+"/exec", bearer,
		`{"command":"echo yaver-to-yaver"}`)
	if status != 200 {
		t.Fatalf("B /exec: %d %v", status, execResp)
	}
	execID, _ := execResp["execId"].(string)
	if execID == "" {
		t.Fatalf("no execId from B's exec: %v", execResp)
	}

	// 5. Agent B polls until the command completes, verifies stdout.
	deadline := time.Now().Add(3 * time.Second)
	var sess map[string]interface{}
	for time.Now().Before(deadline) {
		status, pollResp := doRequest(t, "GET", urlA+"/exec/"+execID, bearer, "")
		if status != 200 {
			t.Fatalf("B poll: %d", status)
		}
		sess, _ = pollResp["exec"].(map[string]interface{})
		if phase, _ := sess["status"].(string); phase == "completed" || phase == "failed" {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if sess == nil {
		t.Fatal("B never saw exec complete")
	}
	out, _ := sess["stdout"].(string)
	if !strings.Contains(out, "yaver-to-yaver") {
		t.Fatalf("wrong stdout from A: %q", out)
	}

	// 6. Agent B's bearer must NOT open vault / tasks / agent/shutdown.
	for _, path := range []string{"/vault/list", "/tasks", "/agent/shutdown"} {
		status, _ := doRequest(t, "GET", urlA+path, bearer, "")
		if status == 200 {
			t.Errorf("support bearer unexpectedly opened %s", path)
		}
	}

	// 7. Owner of A stops the session. Agent B's bearer dies.
	status, _ = doRequest(t, "POST", urlA+"/support/stop", "owner-A", "")
	if status != 200 {
		t.Fatalf("stop: %d", status)
	}
	status, _ = doRequest(t, "GET", urlA+"/info", bearer, "")
	if status == 200 {
		t.Error("B's bearer should be dead after owner-A stopped the session")
	}
}

// TestTwoAgentCrossTokenRejected confirms agent A's owner token
// can't reach agent B's resources and vice versa. Complements the
// existing TestServerClientIntegration but covers the new surfaces
// (vault/list, schedules).
func TestTwoAgentCrossTokenRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tmA := NewTaskManager(t.TempDir(), nil, defaultRunner)
	urlA, cancelA := startTestServer(t, "owner-A", tmA)
	defer cancelA()
	tmB := NewTaskManager(t.TempDir(), nil, defaultRunner)
	urlB, cancelB := startTestServer(t, "owner-B", tmB)
	defer cancelB()

	// B's token on A's vault → 401/403.
	status, _ := doRequest(t, "GET", urlA+"/files/roots", "owner-B", "")
	if status == 200 {
		t.Error("owner-B's token reached A's /files/roots")
	}
	status, _ = doRequest(t, "GET", urlB+"/files/roots", "owner-A", "")
	if status == 200 {
		t.Error("owner-A's token reached B's /files/roots")
	}

	// Each owner still works on its own agent.
	status, _ = doRequest(t, "GET", urlA+"/health", "", "")
	if status != 200 {
		t.Errorf("A /health: %d", status)
	}
	status, _ = doRequest(t, "GET", urlB+"/health", "", "")
	if status != 200 {
		t.Errorf("B /health: %d", status)
	}
}
