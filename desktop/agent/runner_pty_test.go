package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// fakeAgent serves /runner-auth/status from a mutable row so a test can
	// model the real contract: a mirror only counts if the NEXT status read
	// says the runner is authenticated.
	type fakeAgent struct {
		srv         *httptest.Server
		row         *runnerAuthStatusRow
		mirrorHits  int
		deviceAuths int
	}
	newFakeAgent := func(row runnerAuthStatusRow, authAfterMirror bool) *fakeAgent {
		fa := &fakeAgent{row: &row}
		var mu sync.Mutex
		fa.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			switch r.URL.Path {
			case "/runner-auth/status":
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "runners": []runnerAuthStatusRow{*fa.row}})
			case "/runner/auth/mirror/accept":
				fa.mirrorHits++
				if authAfterMirror {
					fa.row.AuthConfigured = true
					fa.row.AuthVerified = true
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "runner": fa.row.ID})
			case "/runner-auth/browser/start":
				fa.deviceAuths++
				// No session id → preflight reports the flow could not start,
				// which is enough to assert that we *reached* device-auth.
				json.NewEncoder(w).Encode(map[string]any{"ok": true})
			default:
				http.NotFound(w, r)
			}
		}))
		return fa
	}

	t.Run("authed proceeds silently", func(t *testing.T) {
		fa := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, Ready: true, AuthConfigured: true, AuthVerified: true}, false)
		defer fa.srv.Close()
		repaired, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if repaired {
			t.Fatal("nothing was repaired; repaired must be false")
		}
	})

	t.Run("not installed fails fast with npm hint", func(t *testing.T) {
		fa := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: false}, false)
		defer fa.srv.Close()
		_, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err == nil || !strings.Contains(err.Error(), "@openai/codex") {
			t.Fatalf("expected npm hint error, got %v", err)
		}
	})

	t.Run("sandbox blocked fails fast with sysctl hint", func(t *testing.T) {
		fa := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, Error: "kernel settings are blocking the sandbox"}, false)
		defer fa.srv.Close()
		_, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err == nil || !strings.Contains(err.Error(), "sysctl") {
			t.Fatalf("expected sysctl hint error, got %v", err)
		}
	})

	t.Run("auth file present but rejected fails before tui", func(t *testing.T) {
		fa := newFakeAgent(runnerAuthStatusRow{
			ID:             "claude",
			Installed:      true,
			Ready:          false,
			AuthConfigured: true,
			Error:          "Failed to authenticate. API Error: 401 Invalid authentication credentials",
		}, false)
		defer fa.srv.Close()
		_, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "claude", true)
		if err == nil || !strings.Contains(err.Error(), "not usable") || !strings.Contains(err.Error(), "yaver primary auth claude") {
			t.Fatalf("expected actionable auth error, got %v", err)
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
		fa := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, Ready: true, AuthConfigured: false}, true)
		defer fa.srv.Close()
		repaired, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err != nil {
			t.Fatalf("expected mirror to satisfy preflight, got %v", err)
		}
		if fa.mirrorHits != 1 {
			t.Fatalf("mirror accept hits = %d, want 1", fa.mirrorHits)
		}
		if !repaired {
			t.Fatal("a successful mirror must report repaired=true so the caller starts a fresh session")
		}
	})

	// The bug this whole change exists for: a mirror that transfers bytes but
	// not a login must NOT be treated as success. Before the fix the preflight
	// returned nil here and dropped the user into a TUI showing a browser
	// login screen on a machine with no browser.
	t.Run("mirror that does not authenticate falls through to device-auth", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"oauth":{"expiresAt":9999999999999}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		fa := newFakeAgent(runnerAuthStatusRow{ID: "codex", Installed: true, Ready: true, AuthConfigured: false}, false)
		defer fa.srv.Close()
		_, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err == nil {
			t.Fatal("expected preflight to fail rather than open a TUI onto a login screen")
		}
		if fa.mirrorHits != 1 {
			t.Fatalf("mirror accept hits = %d, want 1", fa.mirrorHits)
		}
		if fa.deviceAuths != 1 {
			t.Fatalf("device-auth starts = %d, want 1 (must fall through after a useless mirror)", fa.deviceAuths)
		}
	})

	// A signed-out runner is repairable, not fatal: the preflight must reach
	// the headless sign-in instead of erroring on `ready:false`.
	t.Run("signed out runner reaches headless device auth", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir()) // no mirrorable credential
		fa := newFakeAgent(runnerAuthStatusRow{
			ID:             "codex",
			Installed:      true,
			Ready:          false,
			AuthConfigured: false,
			Error:          "Codex is installed but no credentials were found.",
		}, false)
		defer fa.srv.Close()
		_, err := preflightRemoteRunnerAuth(fa.srv.URL, "tok", http.Header{}, "box", "codex", true)
		if err == nil {
			t.Fatal("expected the stubbed device-auth start to surface an error")
		}
		if fa.deviceAuths != 1 {
			t.Fatalf("device-auth starts = %d, want 1", fa.deviceAuths)
		}
	})

	t.Run("old agent without endpoint proceeds with warning", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
		defer srv.Close()
		if _, err := preflightRemoteRunnerAuth(srv.URL, "tok", http.Header{}, "box", "codex", true); err != nil {
			t.Fatalf("expected nil for old agent, got %v", err)
		}
	})
}

// TestClaudeCredentialFileHasOAuth pins the exact false positive that shipped
// the bug: ~/.claude/.credentials.json also stores MCP plugin OAuth, so on a
// Mac whose subscription token lives in the Keychain the file exists while
// Claude is signed out of it. Mirroring it made a headless box claim it was
// signed in and then demand a browser login.
func TestClaudeCredentialFileHasOAuth(t *testing.T) {
	future := time.Now().Add(time.Hour).UnixMilli()
	past := time.Now().Add(-time.Hour).UnixMilli()
	cases := map[string]struct {
		body string
		want bool
	}{
		"mcp plugin tokens only":     {`{"mcpOAuth":{"vercel":{"accessToken":"tok"}}}`, false},
		"empty object":               {`{}`, false},
		"not json":                   {`nope`, false},
		"blank access token":         {`{"claudeAiOauth":{"accessToken":""}}`, false},
		"live access token": {fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"a","expiresAt":%d}}`, future), true},
		// An expired token is NOT signed in, refresh token or not. This case
		// used to expect true on the theory that Claude would refresh itself.
		// A real Mac mini disproved it: expiresAt was 2026-05-11, a refresh
		// token was present, the refresh never fired, and this function called
		// it signed in for two months — so every picker showed "Claude Code
		// ready" in green and every task sent there died on a 30s timeout.
		// A maybe must not be reported as a yes; if Claude CAN still refresh,
		// probeClaudeAuthStatus() asks the binary directly and overrides this.
		"expired even with refresh token": {fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":%d}}`, past), false},
		"expired without refresh":         {fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"a","expiresAt":%d}}`, past), false},
		"live token with refresh":         {fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":%d}}`, future), true},
		"no expiry recorded":              {`{"claudeAiOauth":{"accessToken":"a"}}`, true},
		"mcp tokens plus real oauth": {fmt.Sprintf(`{"mcpOAuth":{"v":{"accessToken":"t"}},"claudeAiOauth":{"accessToken":"a","expiresAt":%d}}`, future), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".credentials.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := claudeCredentialFileHasOAuth(path); got != tc.want {
				t.Errorf("claudeCredentialFileHasOAuth(%s) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if claudeCredentialFileHasOAuth(filepath.Join(t.TempDir(), "absent.json")) {
			t.Error("a missing file must not read as authenticated")
		}
	})
}

// TestReadLocalRunnerCredentialRefusesClaudeWithoutOAuth guards the mirror
// source: pushing an mcpOAuth-only file is worse than pushing nothing, because
// it makes the target's presence check lie.
func TestReadLocalRunnerCredentialRefusesClaudeWithoutOAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", ".credentials.json")

	if err := os.WriteFile(path, []byte(`{"mcpOAuth":{"vercel":{"accessToken":"tok"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocalRunnerCredential("claude"); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("mcpOAuth-only file must not be mirrorable, got err=%v", err)
	}

	body := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"a","expiresAt":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := ReadLocalRunnerCredential("claude")
	if err != nil {
		t.Fatalf("a real claudeAiOauth credential must be mirrorable, got %v", err)
	}
	if len(cred.FileBytes) == 0 {
		t.Fatal("expected the credential bytes to be carried verbatim")
	}
}

// TestRunnerPTYPaneEnv guards the variables that must reach the runner process
// itself. They used to live only in cmd.Env, which tmux hands to the client,
// not the pane — so on any box whose tmux server predated them they silently
// vanished, taking GLM's z.ai routing and root-claude's IS_SANDBOX with them.
func TestRunnerPTYPaneEnv(t *testing.T) {
	env := runnerPTYPaneEnv("claude", "xterm-256color")
	if len(env) == 0 || !strings.HasPrefix(env[0], "TERM=") {
		t.Fatalf("TERM must lead the pane env, got %v", env)
	}
	if got := runnerPTYPaneEnv("claude", "$(evil)"); !strings.HasPrefix(got[0], "TERM=") ||
		strings.Contains(got[0], "$(") {
		t.Errorf("a hostile TERM must be sanitized, got %q", got[0])
	}
	// Every entry must be a KEY=VALUE assignment: the tmux path splices these
	// straight after `env` on a shell command line.
	for _, e := range runnerPTYPaneEnv("glm", "xterm") {
		if !strings.Contains(e, "=") {
			t.Errorf("pane env entry %q is not a KEY=VALUE assignment", e)
		}
	}
	if os.Geteuid() == 0 {
		var sawSandbox bool
		for _, e := range runnerPTYPaneEnv("claude", "xterm") {
			sawSandbox = sawSandbox || e == "IS_SANDBOX=1"
		}
		if !sawSandbox {
			t.Error("root-owned claude needs IS_SANDBOX=1 to accept --dangerously-skip-permissions")
		}
	}
}

// TestRunnerLoginScreenDetection keeps the stale-session heuristic honest. It
// must fire on the pane a signed-out box actually leaves behind, and stay
// silent on anything a healthy session could render.
func TestRunnerLoginScreenDetection(t *testing.T) {
	matches := func(pane string) bool {
		tail := paneTailLower(pane, runnerLoginScreenTailLines)
		for _, m := range runnerLoginScreenMarkers {
			if strings.Contains(tail, m) {
				return true
			}
		}
		return false
	}

	// Verbatim shape of the pane this bug strands on a headless box.
	stuck := "  ░░░ Claude logo art ░░░\n\n" +
		" OAuth error: Invalid code. Please make sure the full code was copied\n\n\n" +
		" Press Enter to retry.\n"
	if !matches(stuck) {
		t.Error("a pane parked on Claude's OAuth error screen must be detected as stale")
	}
	if !matches("Select login method:\n  1. Claude account with subscription\n") {
		t.Error("Claude's fresh login screen must be detected as stale")
	}

	// A marker scrolled out of the prompt window is just transcript text.
	scrolled := "OAuth error: something the user asked Claude about\n" +
		strings.Repeat("normal transcript line\n", 20) + "> ready for input\n"
	if matches(scrolled) {
		t.Error("a marker in scrollback must not reach the tail window")
	}
	// Generic phrases alone must never kill a live session.
	for _, benign := range []string{
		"request failed.\nPress Enter to retry.\n",
		"the API said you are not logged in\n> \n",
		"Sign in required for that MCP server\n> \n",
	} {
		if matches(benign) {
			t.Errorf("benign pane must not be treated as a login screen: %q", benign)
		}
	}
}

func TestRunnerFromStartCommand(t *testing.T) {
	cases := map[string]string{
		"codex --dangerously-bypass-approvals-and-sandbox":     "codex",
		"/usr/local/bin/claude --dangerously-skip-permissions": "claude",
		"opencode":                     "opencode",
		"/root/.opencode/bin/opencode": "opencode",
		"bash":                         "",
		"":                             "",
		"vim file.go":                  "",
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
		{"yaver finalize mode stripped", []string{"--machine", "mypi", "--yaver-mode", "finalize", "ship it"}, "mypi", []string{"ship it"}},
		{"generic finalize mode sugar stripped", []string{"--mode", "finalize", "ship it"}, "", []string{"ship it"}},
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
			if strings.Contains(tc.name, "finalize") && !got.finalize {
				t.Errorf("finalize = false, want true")
			}
		})
	}
}

func TestNormalizeGitURLToHTTPS(t *testing.T) {
	cases := map[string]string{
		"git@github.com:kivanccakmak/yaver.io.git": "https://github.com/kivanccakmak/yaver.io.git",
		"git@gitlab.com:group/sub/proj.git":        "https://gitlab.com/group/sub/proj.git",
		"ssh://git@github.com/owner/repo.git":      "https://github.com/owner/repo.git",
		"https://github.com/owner/repo.git":        "https://github.com/owner/repo.git",
		"":                                         "",
	}
	for in, want := range cases {
		if got := normalizeGitURLToHTTPS(in); got != want {
			t.Errorf("normalizeGitURLToHTTPS(%q) = %q, want %q", in, got, want)
		}
	}
}
