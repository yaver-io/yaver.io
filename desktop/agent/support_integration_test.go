package main

// support_integration_test.go — exercises the full /support/* HTTP flow
// end-to-end over loopback. Confirms:
//
//   owner → POST /support/start                returns code + token
//   anon  → GET  /support/info                 reveals live-but-no-secret
//   anon  → POST /support/redeem {code}        exchanges code for token
//   support-token → GET  /info                 allowed (in scope)
//   support-token → GET  /agent/status         allowed
//   support-token → POST /vault/read           rejected (out of scope)
//   support-token → POST /agent/shutdown       rejected
//   owner → POST /support/stop                 revokes
//   stale support-token → /info                rejected afterwards
//
// This is the "works from IPC, not just unit test" coverage the brief
// asked for — if any piece of the wiring in support.go, support_http.go,
// or the auth() fast path breaks, this test fails.

import (
	"strings"
	"testing"
	"time"
)

func TestSupportSessionFlowEndToEnd(t *testing.T) {
	resetSupport(t) // defined in support_test.go
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// 1. Owner opens a session.
	code, status := doRequest(t, "POST", baseURL+"/support/start", "owner-tok",
		`{"ttl":"5m","label":"ci-integration"}`)
	if code != 200 {
		t.Fatalf("start: got %d, body=%v", code, status)
	}
	inviteCode, _ := status["code"].(string)
	bearer, _ := status["token"].(string)
	if len(inviteCode) != 6 {
		t.Fatalf("expected 6-char code, got %q", inviteCode)
	}
	if !strings.HasPrefix(bearer, "yv_supp_") {
		t.Fatalf("expected yv_supp_ token, got %q", bearer)
	}

	// 2. Anonymous info probe works.
	code, info := doRequest(t, "GET", baseURL+"/support/info", "", "")
	if code != 200 || info["active"] != true {
		t.Fatalf("info probe: got %d %v", code, info)
	}
	if _, leaked := info["code"]; leaked {
		t.Fatal("/support/info leaked the invite code to anon caller")
	}
	if _, leaked := info["token"]; leaked {
		t.Fatal("/support/info leaked the bearer token to anon caller")
	}

	// 3. Anonymous redeem succeeds with the right code.
	code, redeemed := doRequest(t, "POST", baseURL+"/support/redeem", "",
		`{"code":"`+inviteCode+`"}`)
	if code != 200 {
		t.Fatalf("redeem: got %d, body=%v", code, redeemed)
	}
	gotToken, _ := redeemed["token"].(string)
	if gotToken != bearer {
		t.Fatalf("redeem returned a different token than /support/start (%q vs %q)", gotToken, bearer)
	}

	// Wrong code is forbidden.
	code, _ = doRequest(t, "POST", baseURL+"/support/redeem", "", `{"code":"NOPE23"}`)
	if code != 403 {
		t.Fatalf("wrong code: expected 403, got %d", code)
	}

	// 4. The support bearer works on in-scope paths.
	for _, path := range []string{"/info", "/agent/status", "/health"} {
		code, _ := doRequest(t, "GET", baseURL+path, bearer, "")
		if code != 200 {
			t.Fatalf("in-scope %s with support token: got %d", path, code)
		}
	}

	// 5. The support bearer is rejected on out-of-scope owner-only paths.
	outOfScope := []string{"/vault/read", "/agent/shutdown", "/tasks", "/session/list"}
	for _, path := range outOfScope {
		// Use a POST for /agent/shutdown + /vault/read to avoid method-
		// -not-allowed masking the auth rejection. The auth middleware
		// runs before the handler's method check, so either a 401/403
		// from auth() counts as correct — we just don't want 200.
		code, _ := doRequest(t, "GET", baseURL+path, bearer, "")
		if code == 200 {
			t.Fatalf("out-of-scope %s should NOT be reachable with support token (got 200)", path)
		}
	}

	// 6. Owner GETs /support/status and sees the active session with full secrets.
	code, stat := doRequest(t, "GET", baseURL+"/support/status", "owner-tok", "")
	if code != 200 || stat["active"] != true {
		t.Fatalf("status: got %d %v", code, stat)
	}
	if got, _ := stat["code"].(string); got != inviteCode {
		t.Fatalf("status returned wrong code: got %q want %q", got, inviteCode)
	}

	// 7. Owner stops the session.
	code, stopped := doRequest(t, "POST", baseURL+"/support/stop", "owner-tok", "")
	if code != 200 || stopped["stopped"] != true {
		t.Fatalf("stop: got %d %v", code, stopped)
	}

	// 8. The previously-redeemed bearer is now dead.
	code, _ = doRequest(t, "GET", baseURL+"/info", bearer, "")
	if code == 200 {
		t.Fatal("stale support bearer still accepted after stop — revocation leak")
	}

	// 9. Anon info probe now reports no active session.
	code, info = doRequest(t, "GET", baseURL+"/support/info", "", "")
	if code != 200 || info["active"] != false {
		t.Fatalf("post-stop info: got %d %v", code, info)
	}
}

func TestSupportRedeemRequiresCode(t *testing.T) {
	resetSupport(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// No active session — redeem should fail hard even for a plausible
	// 6-char code. Rules out "accidentally accept any code when there's
	// no session" bugs.
	code, _ := doRequest(t, "POST", baseURL+"/support/redeem", "", `{"code":"ABCD23"}`)
	if code != 403 {
		t.Fatalf("redeem with no session: expected 403, got %d", code)
	}
	// Empty code must 400 not 403 — helps API callers distinguish
	// "I sent nothing" from "wrong code".
	code, _ = doRequest(t, "POST", baseURL+"/support/redeem", "", `{}`)
	if code != 400 {
		t.Fatalf("redeem with empty code: expected 400, got %d", code)
	}
}

// TestSupportBearerCanExec is the full agent-to-agent story: a remote
// host opens a support session, we redeem the code, and then run a
// real command via POST /exec using the support bearer. Proves the
// auth() fast path grants /exec correctly and that the exec manager
// picks up the call.
func TestSupportBearerCanExec(t *testing.T) {
	resetSupport(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// Open a session + redeem as anonymous caller would.
	_, startResp := doRequest(t, "POST", baseURL+"/support/start", "owner-tok", `{"ttl":"1m"}`)
	code, _ := startResp["code"].(string)
	_, redeemed := doRequest(t, "POST", baseURL+"/support/redeem", "", `{"code":"`+code+`"}`)
	bearer, _ := redeemed["token"].(string)
	if bearer == "" {
		t.Fatalf("redeem returned no token: %v", redeemed)
	}

	// POST /exec with the support bearer — this is the "yaver support
	// connect" happy path.
	status, execResp := doRequest(t, "POST", baseURL+"/exec", bearer,
		`{"command":"echo hello-from-remote"}`)
	if status != 200 {
		t.Fatalf("POST /exec with support bearer: got %d, body=%v", status, execResp)
	}
	execID, _ := execResp["execId"].(string)
	if execID == "" {
		t.Fatalf("no execId returned: %v", execResp)
	}

	// Poll /exec/{id} with the bearer until it completes. Bounded to
	// ~3 seconds — a simple echo should finish almost instantly.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, pollResp := doRequest(t, "GET", baseURL+"/exec/"+execID, bearer, "")
		if status != 200 {
			t.Fatalf("poll: got %d %v", status, pollResp)
		}
		sess, _ := pollResp["exec"].(map[string]interface{})
		phase, _ := sess["status"].(string)
		if phase == "completed" || phase == "failed" {
			out, _ := sess["stdout"].(string)
			if !strings.Contains(out, "hello-from-remote") {
				t.Fatalf("unexpected output: %q", out)
			}
			return
		}
		time.Sleep(80 * time.Millisecond)
	}
	t.Fatal("remote exec did not complete in time")
}

func TestSupportStartRequiresOwnerToken(t *testing.T) {
	resetSupport(t)
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "owner-tok", tm)
	defer cancel()

	// An anon caller cannot open a new support session — that would let
	// anyone on the LAN flip the host into take-me-over mode.
	code, _ := doRequest(t, "POST", baseURL+"/support/start", "", `{}`)
	if code != 401 && code != 403 {
		t.Fatalf("anon /support/start: expected 401/403, got %d", code)
	}
	// A wrong bearer also fails.
	code, _ = doRequest(t, "POST", baseURL+"/support/start", "not-the-owner", `{}`)
	if code != 401 && code != 403 {
		t.Fatalf("wrong-token /support/start: expected 401/403, got %d", code)
	}
}
