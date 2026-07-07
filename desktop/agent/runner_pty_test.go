package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// No real-runner spawn test on purpose: the dev machine has claude/codex
// installed and spawning their real TUIs from a unit test means Keychain
// prompts + live sessions. The PTY/attach/replay mechanics are covered by
// terminal_session_test.go over the same terminalSession infra.

func TestRunnerPTYRejectsGuests(t *testing.T) {
	srv := &HTTPServer{token: "owner-token", ownerUserID: "owner-user"}
	server := httptest.NewServer(http.HandlerFunc(srv.auth(srv.handleRunnerPTYWS)))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/runner?runner=codex"
	header := http.Header{}
	header.Set("Authorization", "Bearer owner-token")
	header.Set("X-Yaver-Guest", "true")

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("expected guest dial to be rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("guest dial status = %d, want 403", status)
	}
}

func TestRunnerPTYRejectsUnsupportedRunner(t *testing.T) {
	srv := &HTTPServer{token: "owner-token", ownerUserID: "owner-user"}
	server := httptest.NewServer(http.HandlerFunc(srv.auth(srv.handleRunnerPTYWS)))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/runner?runner=definitely-not-a-runner"
	header := http.Header{}
	header.Set("Authorization", "Bearer owner-token")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read error frame: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("frame type = %d, want text", mt)
	}
	var frame struct {
		Type  string `json:"type"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("unmarshal error frame: %v (%s)", err, data)
	}
	if frame.Type != "runner_pty_error" || !strings.Contains(frame.Error, "unsupported runner") {
		t.Fatalf("unexpected error frame: %+v", frame)
	}
}

// TestRunnerPTYSpawnsStubRunner exercises the real spawn + I/O path with a
// stub `codex` on a restricted PATH (no tmux on PATH → direct PTY spawn, so
// the test leaves no tmux sessions behind and never touches a real runner).
func TestRunnerPTYSpawnsStubRunner(t *testing.T) {
	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "codex")
	// Absolute /bin/cat — the restricted PATH has no coreutils.
	script := "#!/bin/sh\necho CODEX_STUB_READY\nexec /bin/cat\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir)

	srv := &HTTPServer{token: "owner-token", ownerUserID: "owner-user"}
	server := httptest.NewServer(http.HandlerFunc(srv.auth(srv.handleRunnerPTYWS)))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/runner?runner=codex&arg=--flag-passthrough"
	header := http.Header{}
	header.Set("Authorization", "Bearer owner-token")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sawReady := false
	sawEcho := false
	wroteInput := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !sawEcho {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		mt, data, rerr := conn.ReadMessage()
		if rerr != nil {
			t.Fatalf("read: %v (sawReady=%v)", rerr, sawReady)
		}
		if mt == websocket.TextMessage {
			var frame struct {
				Type  string `json:"type"`
				Error string `json:"error"`
			}
			_ = json.Unmarshal(data, &frame)
			if frame.Type == "runner_pty_error" {
				t.Fatalf("unexpected error frame: %s", frame.Error)
			}
			continue
		}
		out := string(data)
		if strings.Contains(out, "CODEX_STUB_READY") {
			sawReady = true
			if !wroteInput {
				wroteInput = true
				if werr := conn.WriteMessage(websocket.BinaryMessage, []byte("hello-pty\n")); werr != nil {
					t.Fatalf("write input: %v", werr)
				}
			}
		}
		if sawReady && strings.Contains(out, "hello-pty") {
			sawEcho = true
		}
	}
	if !sawEcho {
		t.Fatalf("never saw echoed input from the stub runner PTY")
	}
}

func TestSanitizeTmuxSessionName(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"yaver-codex":   "yaver-codex",
		"My_Session-1":  "My_Session-1",
		"bad;rm -rf /":  "",
		"has space":     "",
		"dots.dots":     "",
		"quote'inject":  "",
		"unicode-ğüş":   "",
		"UPPER_lower-9": "UPPER_lower-9",
	}
	for in, want := range cases {
		if got := sanitizeTmuxSessionName(in); got != want {
			t.Errorf("sanitizeTmuxSessionName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 100)
	if got := sanitizeTmuxSessionName(long); len(got) > 48 {
		t.Errorf("sanitizeTmuxSessionName(long) length = %d, want <= 48", len(got))
	}
}

func TestCodexHomeDirHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	if got := codexHomeDir(); got != dir {
		t.Fatalf("codexHomeDir() = %q, want %q", got, dir)
	}
	t.Setenv("CODEX_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	if got := codexHomeDir(); got != filepath.Join(home, ".codex") {
		t.Fatalf("codexHomeDir() = %q, want %q", got, filepath.Join(home, ".codex"))
	}
}

func TestCollectCodexFilesPicksUpHomeSessions(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	sessDir := filepath.Join(codexHome, "sessions", "2026", "07", "07")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessDir, "rollout-abc.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"session_id":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	files := map[string]string{}
	collectCodexFiles(workDir, files)

	want := "codex-home/sessions/2026/07/07/rollout-abc.jsonl"
	if _, ok := files[want]; !ok {
		keys := make([]string, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		t.Fatalf("expected %s in collected files, got %v", want, keys)
	}
}
