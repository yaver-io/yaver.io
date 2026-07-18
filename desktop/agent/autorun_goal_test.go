package main

import (
	"context"
	"strings"
	"testing"
)

// /goal is a claude-binary feature. Sending it to codex or opencode would land
// as literal prompt text at the top of the task, silently corrupting it.
func TestAutorunGoalOnlyForClaudeFamily(t *testing.T) {
	// `glm` was in this list because it drove the claude binary against z.ai —
	// which is exactly why it was retired: subscription-OAuth tooling pointed at
	// an API key. It is no longer a runner, so it is no longer claude-family.
	// GLM itself remains available through opencode's zai-coding-plan provider,
	// and opencode has no /goal, which the second loop already asserts.
	for _, id := range []string{"claude"} {
		if !autorunRunsClaudeBinary(GetRunnerConfig(id)) {
			t.Errorf("%s drives the claude binary and must accept /goal", id)
		}
	}
	// A retired runner must not resolve to a config that quietly behaves like
	// claude. Failing loudly at the boundary is the whole point of retiring it.
	if autorunRunsClaudeBinary(GetRunnerConfig("glm")) {
		t.Error("glm is retired: it must not resolve to a claude-binary config, or the compliance boundary it was retired for is still crossable")
	}
	for _, id := range []string{"codex", "opencode"} {
		if autorunRunsClaudeBinary(GetRunnerConfig(id)) {
			t.Errorf("%s has no /goal and must never be sent one", id)
		}
	}
}

// A non-claude runner must not cost a single tmux call for /goal.
func TestAutorunTmuxSetGoalNoOpsForNonClaude(t *testing.T) {
	restore := autorunExec
	defer func() { autorunExec = restore }()
	called := false
	autorunExec = func(_ context.Context, name string, _ []string, _ string) autorunCommandResult {
		called = true
		return autorunCommandResult{}
	}
	res := autorunTmuxSetGoal(context.Background(), "sess", "all tests pass", GetRunnerConfig("codex"), t.TempDir())
	if res.Err != nil {
		t.Errorf("no-op path returned an error: %v", res.Err)
	}
	if called {
		t.Error("a non-claude runner must not trigger any tmux call for /goal")
	}
}

// An empty --goal is the default, and must stay free.
func TestAutorunTmuxSetGoalNoOpsWhenGoalEmpty(t *testing.T) {
	restore := autorunExec
	defer func() { autorunExec = restore }()
	called := false
	autorunExec = func(_ context.Context, _ string, _ []string, _ string) autorunCommandResult {
		called = true
		return autorunCommandResult{}
	}
	res := autorunTmuxSetGoal(context.Background(), "sess", "   ", GetRunnerConfig("claude"), t.TempDir())
	if res.Err != nil {
		t.Errorf("empty goal returned an error: %v", res.Err)
	}
	if called {
		t.Error("an empty goal must not trigger any tmux call")
	}
}

// The goal must be typed into the composer as a slash command, literally, and
// submitted with a SEPARATE Enter — a combined send leaves it unsubmitted.
func TestAutorunTmuxSetGoalTypesSlashCommandAndSubmits(t *testing.T) {
	restore := autorunExec
	defer func() { autorunExec = restore }()
	var sent [][]string
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		sent = append(sent, append([]string{name}, args...))
		// Report a ready composer so the readiness wait passes immediately.
		if len(args) > 0 && args[0] == "capture-pane" {
			return autorunCommandResult{Output: "> "}
		}
		return autorunCommandResult{}
	}
	res := autorunTmuxSetGoal(context.Background(), "sess", "all tests pass", GetRunnerConfig("claude"), t.TempDir())
	if res.Err != nil {
		t.Fatalf("set goal: %v", res.Err)
	}

	var literal, enter bool
	for _, call := range sent {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "-l /goal all tests pass") {
			literal = true
		}
		if strings.HasSuffix(joined, "Enter") {
			enter = true
		}
	}
	if !literal {
		t.Errorf("expected the goal to be typed literally as a slash command, got %v", sent)
	}
	if !enter {
		t.Errorf("expected a separate Enter to submit the goal, got %v", sent)
	}
}

// If the TUI never shows a composer, the goal was swallowed. That must surface
// as an error, not pass silently — an unarmed goal is invisible until the run
// overshoots.
func TestAutorunTmuxSetGoalFailsWhenComposerNeverReady(t *testing.T) {
	restore := autorunExec
	defer func() { autorunExec = restore }()
	autorunExec = func(_ context.Context, _ string, args []string, _ string) autorunCommandResult {
		if len(args) > 0 && args[0] == "capture-pane" {
			return autorunCommandResult{Output: "still booting"}
		}
		return autorunCommandResult{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the readiness loop must bail out rather than block the test
	res := autorunTmuxSetGoal(ctx, "sess", "all tests pass", GetRunnerConfig("claude"), t.TempDir())
	if res.Err == nil {
		t.Error("a TUI that never shows a composer must fail loudly, not swallow the goal")
	}
}
