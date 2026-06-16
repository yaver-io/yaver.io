package main

import (
	"strings"
	"testing"
)

func TestResumeTransform_ClaudeAndGLM(t *testing.T) {
	for _, id := range []string{"claude", "glm"} {
		runner := RunnerConfig{RunnerID: id, Command: "claude"}
		base := []string{"-p", "{prompt}", "--no-session-persistence", "--tools", "Bash"}

		// With a session id → --resume appended, --no-session-persistence stripped.
		out, ok := resumeTransform(runner, base, "go on", "/w", "sess-123")
		if !ok {
			t.Fatalf("%s: expected resume ok with session id", id)
		}
		joined := strings.Join(out, " ")
		if !strings.Contains(joined, "--resume sess-123") {
			t.Errorf("%s: missing --resume, got %v", id, out)
		}
		if strings.Contains(joined, "--no-session-persistence") {
			t.Errorf("%s: --no-session-persistence must be stripped, got %v", id, out)
		}

		// Without a session id → cannot resume.
		if _, ok := resumeTransform(runner, base, "go on", "/w", ""); ok {
			t.Errorf("%s: must not resume without a session id", id)
		}
	}
}

func TestResumeTransform_Opencode(t *testing.T) {
	runner := RunnerConfig{RunnerID: "opencode", Command: "opencode"}
	base := []string{"run", "--dangerously-skip-permissions", "do it"}

	// opencode resumes via --continue, no id needed.
	out, ok := resumeTransform(runner, base, "do it", "/w", "")
	if !ok {
		t.Fatal("opencode should resume via --continue without an id")
	}
	if out[len(out)-1] != "--continue" {
		t.Errorf("expected --continue appended, got %v", out)
	}
	// base args preserved (not mutated).
	if base[0] != "run" || len(base) != 3 {
		t.Errorf("base args mutated: %v", base)
	}
}

func TestResumeTransform_Codex(t *testing.T) {
	runner := RunnerConfig{RunnerID: "codex", Command: "codex"}
	base := []string{"exec", "--full-auto", "the prompt"}

	// No id → cannot reconstruct blind.
	if _, ok := resumeTransform(runner, base, "the prompt", "/proj", ""); ok {
		t.Fatal("codex must not resume without a session id")
	}

	// With id → rebuilt as `exec resume <id>` with sandbox/approval globals.
	out, ok := resumeTransform(runner, base, "the prompt", "/proj", "uuid-9")
	if !ok {
		t.Fatal("codex should resume with a session id")
	}
	joined := strings.Join(out, " ")
	for _, want := range []string{"--sandbox workspace-write", "--ask-for-approval on-failure", "-C /proj", "exec resume uuid-9", "the prompt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("codex resume args missing %q, got %v", want, out)
		}
	}
	if strings.Contains(joined, "--full-auto") {
		t.Errorf("codex `exec resume` must not carry --full-auto (it is rejected), got %v", out)
	}
}

func TestParseRawSessionID(t *testing.T) {
	cases := []struct {
		runner, text, want string
	}{
		{"codex", "session_id: 0199aaaa-bbbb-cccc-dddd-eeeeeeeeeeee done", "0199aaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"codex", "wrote ~/.codex/sessions/2026/06/rollout-0199aaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl", "0199aaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"codex", "no id here", ""},
		{"opencode", "share: https://opencode.ai/s/abc123XYZ", "abc123XYZ"},
		{"opencode", "started session ses_01HZZZ0000aaaa", "ses_01HZZZ0000aaaa"},
		{"opencode", "nothing", ""},
		{"claude", "session_id: 0199aaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ""}, // stream-json runner: never parsed here
	}
	for _, c := range cases {
		if got := parseRawSessionID(c.runner, c.text); got != c.want {
			t.Errorf("parseRawSessionID(%q, %q) = %q, want %q", c.runner, c.text, got, c.want)
		}
	}
}
