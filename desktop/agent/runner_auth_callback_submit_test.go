package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateRunnerBrowserAuthCallbackURLRequiresObservedLocalPort(t *testing.T) {
	target, err := validateRunnerBrowserAuthCallbackURL("http://localhost:46765/callback?code=abc&state=def", 46765)
	if err != nil {
		t.Fatalf("validate localhost callback: %v", err)
	}
	if !strings.HasPrefix(target, "http://127.0.0.1:46765/callback?") {
		t.Fatalf("target should be normalized to loopback, got %q", target)
	}

	for name, raw := range map[string]string{
		"https":     "https://localhost:46765/callback?code=abc",
		"nonlocal":  "http://example.com:46765/callback?code=abc",
		"wrongPort": "http://localhost:46766/callback?code=abc",
	} {
		if _, err := validateRunnerBrowserAuthCallbackURL(raw, 46765); err == nil {
			t.Fatalf("%s callback should be rejected", name)
		}
	}
}

func TestHandleRunnerBrowserAuthSubmitCallbackReplaysToLocalRunner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	seen := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
	})}
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	sessionID := "codex-test-callback"
	runnerBrowserAuthSessions.Store(sessionID, &runnerBrowserAuthSessionState{
		runnerBrowserAuthSession: runnerBrowserAuthSession{
			ID:           sessionID,
			Runner:       "codex",
			Method:       "oauth",
			Status:       "awaiting_browser",
			CallbackPort: port,
			StartedAt:    time.Now().UnixMilli(),
			UpdatedAt:    time.Now().UnixMilli(),
		},
	})
	defer runnerBrowserAuthSessions.Delete(sessionID)

	body, _ := json.Marshal(map[string]string{
		"callback_url": "http://localhost:" + strconv.Itoa(port) + "/callback?code=abc&state=def",
	})
	req := httptest.NewRequest(http.MethodPost, "/runner-auth/browser/submit-callback?id="+sessionID, bytes.NewReader(body))
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleRunnerBrowserAuthSubmitCallback(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	select {
	case uri := <-seen:
		if uri != "/callback?code=abc&state=def" {
			t.Fatalf("callback URI = %q", uri)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback was not replayed to local runner listener")
	}
}

func TestHandleRunnerBrowserAuthSubmitCodeRejectsCodex(t *testing.T) {
	sessionID := "codex-test-submit-code"
	runnerBrowserAuthSessions.Store(sessionID, &runnerBrowserAuthSessionState{
		runnerBrowserAuthSession: runnerBrowserAuthSession{
			ID:        sessionID,
			Runner:    "codex",
			Method:    "oauth",
			Status:    "awaiting_browser",
			StartedAt: time.Now().UnixMilli(),
			UpdatedAt: time.Now().UnixMilli(),
		},
		stdin: nopWriteCloser{Writer: &bytes.Buffer{}},
	})
	defer runnerBrowserAuthSessions.Delete(sessionID)

	body, _ := json.Marshal(map[string]string{
		"code": "claude-code-token",
	})
	req := httptest.NewRequest(http.MethodPost, "/runner-auth/browser/submit-code?id="+sessionID, bytes.NewReader(body))
	rec := httptest.NewRecorder()

	(&HTTPServer{}).handleRunnerBrowserAuthSubmitCode(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "only supported for Claude Code") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error { return nil }

func TestRunnerAuthBrowserSubmitCallbackMCPToolRemoved(t *testing.T) {
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatal("getMCPToolsList did not return a map wrapper")
	}
	tools, ok := wrapper["tools"].([]map[string]interface{})
	if !ok {
		t.Fatal("tools list missing")
	}
	for _, tool := range tools {
		if tool["name"] == "runner_auth_browser_submit_callback" {
			t.Fatal("runner OAuth callback transport must not be advertised")
		}
	}
}

func TestOpsRunnerAuthTransportRemoved(t *testing.T) {
	for name, payload := range map[string]string{
		"missingSession":  `{"op":"submit_callback","callbackUrl":"http://localhost:1234/callback?code=x"}`,
		"missingCallback": `{"op":"submit_callback","sessionId":"claude-123"}`,
	} {
		res := opsRunnerAuthHandler(OpsContext{}, json.RawMessage(payload))
		if res.OK || res.Code != "removed" {
			t.Errorf("%s: expected removed rejection, got ok=%v code=%q", name, res.OK, res.Code)
		}
		if !strings.Contains(res.Error, "native CLI") {
			t.Errorf("%s: error must direct users to native CLI auth, got %q", name, res.Error)
		}
	}
}
