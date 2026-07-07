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

func TestPreflightRemoteRunnerAuth(t *testing.T) {
	newFakeAgent := func(row runnerAuthStatusRow, mirrorHits *int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/runner-auth/status":
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "runners": []runnerAuthStatusRow{row}})
			case r.URL.Path == "/runner/auth/mirror/accept":
				if mirrorHits != nil {
					*mirrorHits++
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "runner": row.ID})
			default:
				http.NotFound(w, r)
			}
		}))
	}

	t.Run("authed proceeds silently", func(t *testing.T) {
		srv := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, AuthConfigured: true}, nil)
		defer srv.Close()
		if err := preflightRemoteRunnerAuth(srv.URL, "tok", http.Header{}, "box", "codex", true); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("not installed fails fast with npm hint", func(t *testing.T) {
		srv := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: false}, nil)
		defer srv.Close()
		err := preflightRemoteRunnerAuth(srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err == nil || !strings.Contains(err.Error(), "@openai/codex") {
			t.Fatalf("expected npm hint error, got %v", err)
		}
	})

	t.Run("sandbox blocked fails fast with sysctl hint", func(t *testing.T) {
		srv := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, Error: "kernel settings are blocking the sandbox"}, nil)
		defer srv.Close()
		err := preflightRemoteRunnerAuth(srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err == nil || !strings.Contains(err.Error(), "sysctl") {
			t.Fatalf("expected sysctl hint error, got %v", err)
		}
	})

	t.Run("unauthed with local credential mirrors and proceeds", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"oauth":{"expiresAt":9999999999999}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		hits := 0
		srv := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, AuthConfigured: false}, &hits)
		defer srv.Close()
		if err := preflightRemoteRunnerAuth(srv.URL, "tok", http.Header{}, "box", "codex", true); err != nil {
			t.Fatalf("expected mirror to satisfy preflight, got %v", err)
		}
		if hits != 1 {
			t.Fatalf("mirror accept hits = %d, want 1", hits)
		}
	})

	t.Run("old agent without endpoint proceeds with warning", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
		defer srv.Close()
		if err := preflightRemoteRunnerAuth(srv.URL, "tok", http.Header{}, "box", "codex", true); err != nil {
			t.Fatalf("expected nil for old agent, got %v", err)
		}
	})
}

func TestRunnerFromStartCommand(t *testing.T) {
	cases := map[string]string{
		"codex --dangerously-bypass-approvals-and-sandbox": "codex",
		"/usr/local/bin/claude --dangerously-skip-permissions": "claude",
		"opencode":            "opencode",
		"/root/.opencode/bin/opencode": "opencode",
		"bash":                "",
		"":                    "",
		"vim file.go":         "",
	}
	for in, want := range cases {
		if got := runnerFromStartCommand(in); got != want {
			t.Errorf("runnerFromStartCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunnerSessionsEndpointOwnerOnly(t *testing.T) {
	srv := &HTTPServer{token: "owner-token", ownerUserID: "owner-user"}
	server := httptest.NewServer(http.HandlerFunc(srv.auth(srv.handleRunnerSessions)))
	defer server.Close()

	// Guest is rejected.
	req, _ := http.NewRequest("GET", server.URL+"/runner/sessions", nil)
	req.Header.Set("Authorization", "Bearer owner-token")
	req.Header.Set("X-Yaver-Guest", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("guest status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	// Owner gets a well-formed (possibly empty) list.
	req2, _ := http.NewRequest("GET", server.URL+"/runner/sessions", nil)
	req2.Header.Set("Authorization", "Bearer owner-token")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("owner status = %d, want 200", resp2.StatusCode)
	}
	var out struct {
		OK       bool               `json:"ok"`
		Sessions []RunnerPTYSession `json:"sessions"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestFetchRunnerSessionsFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"sessions": []RunnerPTYSession{
				{Name: "yaver-codex", Runner: "codex"},
				{Name: "yaver-claude", Runner: "claude"},
			},
		})
	}))
	defer server.Close()
	got, err := fetchRunnerSessions(server.URL, "tok", http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
}

func TestApplyRunnerYoloDefaults(t *testing.T) {
	cases := []struct {
		runner string
		in     []string
		want   []string
	}{
		// The headline behavior: bare invocation gets the yolo flag.
		{"codex", nil, []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{"claude", nil, []string{"--dangerously-skip-permissions"}},
		{"glm", nil, []string{"--dangerously-skip-permissions"}},
		// opencode's TUI has no permission flag — untouched.
		{"opencode", nil, nil},
		// A prompt positional still gets the flag prepended.
		{"claude", []string{"fix the failing tests"}, []string{"--dangerously-skip-permissions", "fix the failing tests"}},
		// Already carrying a stance → untouched.
		{"codex", []string{"--full-auto"}, []string{"--full-auto"}},
		{"codex", []string{"--sandbox=workspace-write"}, []string{"--sandbox=workspace-write"}},
		{"claude", []string{"--permission-mode", "plan"}, []string{"--permission-mode", "plan"}},
		{"claude", []string{"--dangerously-skip-permissions"}, []string{"--dangerously-skip-permissions"}},
		// Management subcommands must not get a root flag shoved in front.
		{"codex", []string{"login", "--device-auth"}, []string{"login", "--device-auth"}},
		{"codex", []string{"resume", "abc123"}, []string{"resume", "abc123"}},
		{"claude", []string{"mcp", "list"}, []string{"mcp", "list"}},
		// Flags-first invocations still get it.
		{"codex", []string{"--model", "gpt-5.4"}, []string{"--dangerously-bypass-approvals-and-sandbox", "--model", "gpt-5.4"}},
	}
	for _, c := range cases {
		got := applyRunnerYoloDefaults(c.runner, c.in)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("applyRunnerYoloDefaults(%s, %v) = %v, want %v", c.runner, c.in, got, c.want)
		}
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

func TestParseRunnerPassthroughRemoteSugar(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantMachine string
		wantPass    []string
	}{
		{"bare remote → primary", []string{"remote"}, "primary", []string{}},
		{"remote then args", []string{"remote", "exec", "hi"}, "primary", []string{"exec", "hi"}},
		{"explicit machine wins over remote", []string{"remote", "--machine=linux-3"}, "linux-3", []string{}},
		{"remote only counts as first token", []string{"exec", "remote"}, "", []string{"exec", "remote"}},
		{"machine flag still works", []string{"--machine", "mypi", "hello"}, "mypi", []string{"hello"}},
		{"no machine → local", []string{"hello world"}, "", []string{"hello world"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRunnerPassthrough(tc.args)
			if got.machine != tc.wantMachine {
				t.Errorf("machine = %q, want %q", got.machine, tc.wantMachine)
			}
			if strings.Join(got.passthrough, "\x00") != strings.Join(tc.wantPass, "\x00") {
				t.Errorf("passthrough = %v, want %v", got.passthrough, tc.wantPass)
			}
		})
	}
}

func TestNormalizeGitURLToHTTPS(t *testing.T) {
	cases := map[string]string{
		"git@github.com:kivanccakmak/yaver.io.git":     "https://github.com/kivanccakmak/yaver.io.git",
		"git@gitlab.com:group/sub/proj.git":            "https://gitlab.com/group/sub/proj.git",
		"ssh://git@github.com/owner/repo.git":          "https://github.com/owner/repo.git",
		"https://github.com/owner/repo.git":            "https://github.com/owner/repo.git",
		"":                                             "",
	}
	for in, want := range cases {
		if got := normalizeGitURLToHTTPS(in); got != want {
			t.Errorf("normalizeGitURLToHTTPS(%q) = %q, want %q", in, got, want)
		}
	}
}
