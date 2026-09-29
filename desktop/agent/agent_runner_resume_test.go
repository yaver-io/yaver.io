package main

import (
	"strings"
	"testing"
)

func TestResumeTransform_Claude(t *testing.T) {
	runner := RunnerConfig{RunnerID: "claude", Command: "claude"}
	base := []string{"-p", "{prompt}", "--no-session-persistence", "--tools", "Bash"}

	// With a session id → --resume appended, --no-session-persistence stripped.
	out, ok := resumeTransform(runner, base, "go on", "/w", "sess-123")
	if !ok {
		t.Fatalf("claude: expected resume ok with session id")
	}
	joined := strings.Join(out, " ")
	if !strings.Contains(joined, "--resume sess-123") {
		t.Errorf("claude: missing --resume, got %v", out)
	}
	if strings.Contains(joined, "--no-session-persistence") {
		t.Errorf("claude: --no-session-persistence must be stripped, got %v", out)
	}

	// Without a session id → cannot resume.
	if _, ok := resumeTransform(runner, base, "go on", "/w", ""); ok {
		t.Errorf("claude: must not resume without a session id")
	}
}

// TestResumeTransform_RetiredGLMCannotResume pins a red this suite has been
// carrying since 5b0990e0c retired the `glm` runner: that commit dropped "glm"
// from the claude resume case but left the test asserting glm still resumed
// like claude, so the assertion described a runner that no longer exists.
//
// The behaviour is now the correct one and worth stating: an unknown/retired
// runner with no ResumeArgs template CANNOT resume, so a follow-up on it spawns
// a cold process — which is exactly why resumeCanCarryContext must report false
// there and the prompt composer must re-arm the Yaver preamble
// (task_prompt_frame.go). A silent "yes it resumed" here would have shipped a
// briefing-less runner.
func TestResumeTransform_RetiredGLMCannotResume(t *testing.T) {
	runner := RunnerConfig{RunnerID: "glm", Command: "claude"}
	base := []string{"-p", "{prompt}", "--tools", "Bash"}

	if _, ok := resumeTransform(runner, base, "go on", "/w", "sess-123"); ok {
		t.Error("glm is retired and has no ResumeArgs — it must not claim it can resume")
	}
	if resumeCanCarryContext(runner, "sess-123") {
		t.Error("a runner that cannot resume cannot carry prior context; the follow-up must re-arm the preamble")
	}
}

func TestResumeTransform_Opencode(t *testing.T) {
	runner := RunnerConfig{RunnerID: "opencode", Command: "opencode"}
	base := []string{"run", "--dangerously-skip-permissions", "do it"}

	// opencode resumes the exact task session, never "last in cwd".
	out, ok := resumeTransform(runner, base, "do it", "/w", "ses_exact")
	if !ok {
		t.Fatal("opencode should resume with an exact session id")
	}
	joined := strings.Join(out, " ")
	if !strings.Contains(joined, "--session ses_exact") || strings.Contains(joined, "--continue") {
		t.Errorf("expected exact --session and no --continue, got %v", out)
	}
	if _, ok := resumeTransform(runner, base, "do it", "/w", ""); ok {
		t.Fatal("opencode must not resume an ambiguous latest session without an id")
	}
	// base args preserved (not mutated).
	if base[0] != "run" || len(base) != 3 {
		t.Errorf("base args mutated: %v", base)
	}
}

func TestResumeTransform_RemotelessUsesOpenCodeSessionShape(t *testing.T) {
	runner := RunnerConfig{RunnerID: "remoteless", Command: "opencode"}
	base := []string{"run", "next"}
	out, ok := resumeTransform(runner, base, "next", "/w", "ses_hosted")
	if !ok || !strings.Contains(strings.Join(out, " "), "--session ses_hosted") {
		t.Fatalf("OpenCode-backed remoteless resume = %v, ok=%v", out, ok)
	}
	if !resumeCanCarryContext(runner, "ses_hosted") {
		t.Fatal("OpenCode-backed remoteless session must carry its captured context")
	}
}

func TestResumeTransform_Codex(t *testing.T) {
	runner := RunnerConfig{RunnerID: "codex", Command: "codex"}
	base := []string{"exec", "--full-auto", "--output-last-message", "/tmp/yaver-last-message", "the prompt"}

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
	for _, want := range []string{"--dangerously-bypass-approvals-and-sandbox", "-C /proj", "exec resume --output-last-message /tmp/yaver-last-message uuid-9", "the prompt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("codex resume args missing %q, got %v", want, out)
		}
	}
	if strings.Contains(joined, "--full-auto") {
		t.Errorf("codex `exec resume` must not carry --full-auto (it is rejected), got %v", out)
	}
}

func TestApplyResumeRunnerSelectionForwardsTypedModelControl(t *testing.T) {
	tests := []struct {
		name     string
		runnerID string
		args     []string
		model    string
		effort   string
		want     []string
	}{
		{
			name: "codex model and reasoning", runnerID: "codex",
			args:  []string{"--dangerously-bypass-approvals-and-sandbox", "exec", "resume", "session-1", "next"},
			model: "gpt-5.6-sol", effort: "high",
			want: []string{"exec resume --config model_reasoning_effort=\"high\"", "--model gpt-5.6-sol", "session-1 next"},
		},
		{
			name: "claude model", runnerID: "claude",
			args: []string{"-p", "next", "--resume", "session-2"}, model: "claude-opus-4-7",
			want: []string{"--model claude-opus-4-7", "--resume session-2"},
		},
		{
			name: "opencode provider model", runnerID: "opencode",
			args: []string{"run", "next", "--session", "session-3"}, model: "deepseek/deepseek-flash",
			want: []string{"run --model deepseek/deepseek-flash", "--session session-3"},
		},
		{
			name: "remoteless uses opencode provider model", runnerID: "remoteless",
			args: []string{"run", "next", "--session", "session-4"}, model: "deepseek/deepseek-flash",
			want: []string{"run --model deepseek/deepseek-flash", "--session session-4"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := applyResumeRunnerSelection(tc.runnerID, tc.args, tc.model, tc.effort)
			joined := strings.Join(got, " ")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("resume selection missing %q: %v", want, got)
				}
			}
		})
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
		{"remoteless", "started session ses_01HZZZ0000aaaa", "ses_01HZZZ0000aaaa"},
		{"claude", "session_id: 0199aaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ""}, // stream-json runner: never parsed here
	}
	for _, c := range cases {
		if got := parseRawSessionID(c.runner, c.text); got != c.want {
			t.Errorf("parseRawSessionID(%q, %q) = %q, want %q", c.runner, c.text, got, c.want)
		}
	}
}
