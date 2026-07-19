package main

// runner_phase2_test.go — Phase 2 coverage for the sandbox manager
// and the agent-session manager (RUNNER_DEV.md). Pinned to the same
// patterns Phase 1 uses: tempdir HOME, no Docker / TaskManager
// requirements for the unit-level tests, integration-shape tests
// skip cleanly when their backing infra isn't available.
//
// What's covered:
//
//   - shellEscape: single-quote round-trip, embedded quotes, empty.
//   - capWriter: bounded writes silently truncate, exact-cap edge case.
//   - SandboxManager.NewSandboxManager(nil) returns nil safely.
//   - SandboxManager.Snapshot on a nil manager returns the empty shape.
//   - SandboxManager.reapIdle without any sessions is a no-op.
//   - resolveAgentSessionRunner: aliases (claude/codex/aider/hybrid)
//     map to canonical runner IDs; explicit runner overrides engine;
//     unknown engine passes through.
//   - composeAgentSessionPrompt: includes title + workdir + project
//     context; truncates message history past 10 turns.
//   - firstLine: empty input gracefully degrades; long lines truncate
//     at 80 chars with the "..." suffix.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShellEscape(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "''"},
		{"hello", "'hello'"},
		{"with space", "'with space'"},
		{"o'brien", `'o'\''brien'`},
		{"a'b'c", `'a'\''b'\''c'`},
		{`semi;rm -rf /`, "'semi;rm -rf /'"},
	} {
		if got := shellEscape(tc.in); got != tc.want {
			t.Errorf("shellEscape(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestCapWriter(t *testing.T) {
	var sink strings.Builder
	cw := &capWriter{w: &sink, max: 10}

	// First write fits.
	if n, err := cw.Write([]byte("12345")); err != nil || n != 5 {
		t.Fatalf("first write: n=%d err=%v", n, err)
	}
	// Second write straddles the cap.
	if n, err := cw.Write([]byte("6789ABCD")); err != nil || n != 8 {
		t.Fatalf("second write: n=%d err=%v", n, err)
	}
	if got := sink.String(); got != "1234567890" {
		t.Errorf("capWriter sink = %q; want %q (cap=10)", got, "1234567890")
	}

	// Further writes are silently dropped — n still reports the
	// caller's input length so they don't see partial-write errors.
	if n, err := cw.Write([]byte("EFGH")); err != nil || n != 4 {
		t.Errorf("post-cap write: n=%d err=%v (expected n=4 err=nil)", n, err)
	}
	if got := sink.String(); got != "1234567890" {
		t.Errorf("sink mutated past cap: %q", got)
	}
}

func TestSandboxManagerNilSafety(t *testing.T) {
	if got := NewSandboxManager(nil); got != nil {
		t.Errorf("NewSandboxManager(nil) should return nil, got %#v", got)
	}
	var nilMgr *SandboxManager
	if rep := nilMgr.Snapshot(""); rep.Count != 0 || rep.Available {
		t.Errorf("nil manager Snapshot non-empty: %#v", rep)
	}
	if got := nilMgr.List(""); got != nil {
		t.Errorf("nil manager List should return nil, got %#v", got)
	}
	// Reaper on nil manager should not panic.
	nilMgr.reapIdle()
	// Stop on nil manager should not panic.
	nilMgr.Stop()
}

func TestResolveAgentSessionRunner(t *testing.T) {
	for _, tc := range []struct {
		engine, runner, want string
	}{
		{"", "", "claude-code"},
		{"claude", "", "claude-code"},
		{"claude-code", "", "claude-code"},
		{"codex", "", "codex"},
		{"aider", "", "aider"},
		{"hybrid", "", "hybrid"},
		{"some-future-engine", "", "some-future-engine"},
		{"", "claude-code", "claude-code"},
		{"codex", "aider-ollama", "aider-ollama"}, // explicit runner wins
		{"claude", "ollama:qwen2.5-coder:14b", "ollama:qwen2.5-coder:14b"},
	} {
		if got := resolveAgentSessionRunner(tc.engine, tc.runner); got != tc.want {
			t.Errorf("resolveAgentSessionRunner(%q, %q) = %q; want %q", tc.engine, tc.runner, got, tc.want)
		}
	}
}

func TestComposeAgentSessionPromptIncludesContext(t *testing.T) {
	s := &AgentSession{
		Title:   "fix the auth bug",
		WorkDir: "/home/dev/yaver",
		Project: "yaver",
		Messages: []AgentSessionMessage{
			{Direction: "user", Text: "investigate"},
			{Direction: "agent", Text: "found issue in auth.go"},
		},
	}
	prompt := composeAgentSessionPrompt(s, "now write the fix")
	for _, want := range []string{
		"now write the fix",
		"Title: fix the auth bug",
		"Workdir: /home/dev/yaver",
		"Project: yaver",
		"[user] investigate",
		"[agent] found issue in auth.go",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n--- prompt ---\n%s", want, prompt)
		}
	}
}

func TestComposeAgentSessionPromptCapsHistory(t *testing.T) {
	s := &AgentSession{Title: "long thread"}
	for i := 0; i < 30; i++ {
		dir := "user"
		if i%2 == 1 {
			dir = "agent"
		}
		s.Messages = append(s.Messages, AgentSessionMessage{Direction: dir, Text: "msg-" + onlyDigits(i)})
	}
	prompt := composeAgentSessionPrompt(s, "next step")
	// Older messages must NOT appear; only the last 10 should.
	if strings.Contains(prompt, "msg-0\n") || strings.Contains(prompt, "msg-1\n") {
		t.Errorf("prompt should not contain oldest messages:\n%s", prompt)
	}
	// Trailing messages should be present.
	if !strings.Contains(prompt, "msg-29") {
		t.Errorf("prompt should contain the most recent message:\n%s", prompt)
	}
}

func TestAgentSessionDefersTaskWhenCloudPlacementSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const initialPrompt = "fix the login bug in auth.ts"
	const followupPrompt = "now add a regression test for the login bug"
	var seen []string
	var bodies []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tasks/placement/preview":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-preview",
				"lane":           "cloud_wake",
				"targetDeviceId": "cloud-device",
				"wakeRequired":   true,
			})
		case "/tasks/placement/record":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":             "placement-recorded",
				"lane":           "cloud_wake",
				"targetDeviceId": "cloud-device",
				"wakeRequired":   true,
			})
		case "/tasks/placement/activate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":         true,
				"activation": map[string]any{"status": "queued"},
			})
		default:
			t.Fatalf("unexpected backend path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	mgr := NewAgentSessionManager(tm)
	mgr.SetPlacementConfig(TaskIngressPlacementConfig{
		ConvexURL:     backend.URL,
		Token:         "owner-token",
		LocalDeviceID: "relay-device",
		WorkDir:       t.TempDir(),
	})

	sess, err := mgr.Create(AgentSessionStartOpts{
		Prompt: initialPrompt,
		Runner: "codex",
	}, "owner")
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if sess.Status != AgentSessionAwaiting {
		t.Fatalf("create status = %s, want %s", sess.Status, AgentSessionAwaiting)
	}
	if !strings.HasPrefix(sess.CurrentTask, "pending-cloud:") {
		t.Fatalf("create current task = %q, want pending-cloud id", sess.CurrentTask)
	}
	if got := len(tm.ListTasks()); got != 0 {
		t.Fatalf("local tasks after create = %d, want 0", got)
	}

	sess, err = mgr.Message(sess.ID, followupPrompt, "owner")
	if err != nil {
		t.Fatalf("Message error: %v", err)
	}
	if sess.Status != AgentSessionAwaiting {
		t.Fatalf("message status = %s, want %s", sess.Status, AgentSessionAwaiting)
	}
	if got := len(tm.ListTasks()); got != 0 {
		t.Fatalf("local tasks after message = %d, want 0", got)
	}
	if len(seen) != 6 {
		t.Fatalf("backend calls = %v, want 6 placement calls", seen)
	}
	for _, body := range bodies {
		for _, forbidden := range []string{initialPrompt, followupPrompt, "auth.ts", "login bug", "regression test"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("placement body leaked prompt fragment %q: %s", forbidden, body)
			}
		}
	}
}

func TestFirstLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "agent session"},
		{"\n\n  \n", "agent session"},
		{"hello", "hello"},
		{"  trimmed  ", "trimmed"},
		{"line1\nline2", "line1"},
		{strings.Repeat("a", 100), strings.Repeat("a", 77) + "..."},
	} {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// onlyDigits is a tiny stringy helper so the cap-history test reads
// cleanly without dragging in fmt or strconv.
func onlyDigits(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
