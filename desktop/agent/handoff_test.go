package main

// handoff_test.go — unit tests for the "pass session to yaver" feature.
// Covers the runner/engine resolver (claude / hybrid / aider+ollama+qwen
// / arbitrary runner ids), the prompt builder (resume + autodev modes),
// and one end-to-end RunHandoff smoke test that verifies the loop spec,
// task import, and sentinel file land on disk with the right runner
// wired through.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveHandoffRunner_EngineMapping locks in the contract that the
// CLI/MCP "engine" knob maps to concrete loop runner ids. Includes the
// aider + ollama + qwen flow the user explicitly asked us to support.
func TestResolveHandoffRunner_EngineMapping(t *testing.T) {
	cases := []struct {
		name       string
		engine     string
		runner     string
		wantRunner string
		wantErr    bool
	}{
		{"empty defaults to claude", "", "", "claude-code", false},
		{"claude alias", "claude", "", "claude-code", false},
		{"claude-code alias", "claude-code", "", "claude-code", false},
		{"hybrid", "hybrid", "", "hybrid", false},
		{"runner=aider", "runner", "aider", "aider", false},
		{"runner=codex", "runner", "codex", "codex", false},
		{"runner=ollama qwen2.5-coder:14b", "runner", "ollama:qwen2.5-coder:14b", "ollama:qwen2.5-coder:14b", false},
		{"runner=aider-ollama (hybrid implementer alone)", "runner", "aider-ollama", "aider-ollama", false},
		{"runner without --runner is rejected", "runner", "", "", true},
		{"unknown engine passes through (forward-compat)", "future-runner-id", "", "future-runner-id", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveHandoffRunner(c.engine, c.runner)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got runner=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.wantRunner {
				t.Errorf("engine=%q runner=%q → got %q, want %q", c.engine, c.runner, got, c.wantRunner)
			}
		})
	}
}

// TestBuildHandoffPrompt_ResumeMode checks the baseline (non-autodev)
// prompt: resume the previous agent, mention bundle context, append
// extra instructions, and DO NOT add the autodev "be proactive" block.
func TestBuildHandoffPrompt_ResumeMode(t *testing.T) {
	bundle := &TransferBundle{
		AgentType: "claude",
		Task: TransferTask{
			Title: "Wire up SSO",
			Turns: make([]ConversationTurn, 12),
		},
	}
	p := buildHandoffPrompt(bundle, "focus on the failing tests first", false, nil)

	mustHave := []string{
		"resuming a session",
		"Continue the in-progress work",
		"Original task: Wire up SSO",
		"Previous agent: claude",
		"12 turns",
		"focus on the failing tests first",
	}
	for _, s := range mustHave {
		if !strings.Contains(p, s) {
			t.Errorf("prompt missing %q\n---\n%s", s, p)
		}
	}
	if strings.Contains(p, "AUTODEV MODE") {
		t.Error("resume-mode prompt unexpectedly contains AUTODEV MODE block")
	}
}

// TestBuildHandoffPrompt_AutodevMode verifies the autodev sub-mode adds
// the "mine the session for new ideas + add tests + propose follow-ups"
// instructions on top of the resume prompt. This is the contract the
// `yaver handoff autodev` user-facing command relies on.
func TestBuildHandoffPrompt_AutodevMode(t *testing.T) {
	bundle := &TransferBundle{AgentType: "aider", Task: TransferTask{Title: "Refactor X"}}
	p := buildHandoffPrompt(bundle, "", true, nil)

	mustHave := []string{
		"AUTODEV MODE",
		"finish every uncompleted item",
		"mine the imported session",
		"discussed but never did",
		"add or update tests",
		"Propose 1-3 new improvements",
	}
	for _, s := range mustHave {
		if !strings.Contains(p, s) {
			t.Errorf("autodev prompt missing %q\n---\n%s", s, p)
		}
	}
}

// TestRunHandoff_LocalSmoke is the end-to-end test: no source bundle,
// pure ad-hoc handoff. Verifies (a) a develop-mode loop is persisted with
// the right runner wired through, (b) a TaskManager task was imported,
// (c) the sentinel file landed on disk with the correct loop name.
//
// Runs three sub-cases covering claude / hybrid / runner=aider so the
// loop's runner field is exercised across the full engine matrix the
// handoff feature promises.
func TestRunHandoff_LocalSmoke(t *testing.T) {
	cases := []struct {
		name       string
		engine     string
		runner     string
		wantRunner string
	}{
		{"claude default", "", "", "claude-code"},
		{"hybrid", "hybrid", "", "hybrid"},
		{"runner=aider+ollama+qwen", "runner", "ollama:qwen2.5-coder:14b", "ollama:qwen2.5-coder:14b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			workDir := t.TempDir()
			tm := NewTaskManager(workDir, nil, defaultTestRunner())
			srv := &HTTPServer{taskMgr: tm}

			res, err := RunHandoff(srv, HandoffSpec{
				Engine:          c.engine,
				Runner:          c.runner,
				WorkDir:         workDir,
				MaxKicks:        3,
				ExtraPrompt:     "smoke test",
				StopSource:      false,
				SkipInitialKick: true, // keep the test hermetic
			})
			if err != nil {
				t.Fatalf("RunHandoff: %v", err)
			}
			if !res.OK || res.LocalTaskID == "" || res.LoopName == "" {
				t.Fatalf("bad result: %+v", res)
			}
			if !res.ExitNow {
				t.Error("ExitNow should be true so the source agent self-terminates")
			}
			if res.Runner != c.wantRunner {
				t.Errorf("runner: want %q got %q", c.wantRunner, res.Runner)
			}

			// Loop spec must have been persisted with the resolved runner.
			loopsPath := filepath.Join(home, ".yaver", "loops", "loops.json")
			data, err := os.ReadFile(loopsPath)
			if err != nil {
				t.Fatalf("read loops.json: %v", err)
			}
			var loops map[string]*LoopState
			if err := json.Unmarshal(data, &loops); err != nil {
				t.Fatalf("parse loops.json: %v", err)
			}
			ls, ok := loops[res.LoopName]
			if !ok {
				t.Fatalf("loop %q not in loops.json (have %v)", res.LoopName, keys(loops))
			}
			if ls.Spec.Think.Runner != c.wantRunner {
				t.Errorf("loop runner: want %q got %q", c.wantRunner, ls.Spec.Think.Runner)
			}
			if ls.Spec.Mode != LoopModeDevelop {
				t.Errorf("loop mode: want develop got %q", ls.Spec.Mode)
			}
			if !strings.Contains(ls.PromptInline, "smoke test") {
				t.Errorf("loop PromptInline missing extra prompt: %q", ls.PromptInline)
			}

			// Sentinel file must exist and reference the loop.
			if res.SentinelFile == "" {
				t.Fatal("expected sentinel file path on result")
			}
			sb, err := os.ReadFile(res.SentinelFile)
			if err != nil {
				t.Fatalf("read sentinel: %v", err)
			}
			var sentinel HandoffSentinel
			if err := json.Unmarshal(sb, &sentinel); err != nil {
				t.Fatalf("parse sentinel: %v", err)
			}
			if sentinel.LoopName != res.LoopName {
				t.Errorf("sentinel loopName mismatch: %q vs %q", sentinel.LoopName, res.LoopName)
			}
			if sentinel.Runner != c.wantRunner {
				t.Errorf("sentinel runner: want %q got %q", c.wantRunner, sentinel.Runner)
			}
			// The stable "latest.json" pointer must also be written so
			// poll-based external agents can find the most recent
			// handoff without knowing the loop name.
			latest := filepath.Join(filepath.Dir(res.SentinelFile), "latest.json")
			if _, err := os.Stat(latest); err != nil {
				t.Errorf("latest.json sentinel missing: %v", err)
			}

			// Imported task must be in TaskManager.
			if _, ok := tm.GetTask(res.LocalTaskID); !ok {
				t.Errorf("task %q not in TaskManager", res.LocalTaskID)
			}
		})
	}
}

// TestRunHandoff_AutodevPropagatesToLoopPrompt confirms that the autodev
// flag flows all the way through to the persisted loop's PromptInline.
// If this regresses, `yaver handoff autodev` becomes a synonym for plain
// `yaver handoff` — silently downgrading the user's request.
func TestRunHandoff_AutodevPropagatesToLoopPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	srv := &HTTPServer{taskMgr: tm}

	res, err := RunHandoff(srv, HandoffSpec{
		Engine: "hybrid", WorkDir: workDir,
		Autodev: true, SkipInitialKick: true,
	})
	if err != nil {
		t.Fatalf("RunHandoff: %v", err)
	}
	if !strings.Contains(res.Message, "Yaver has taken over") {
		t.Errorf("unexpected message: %q", res.Message)
	}

	loopsPath := filepath.Join(os.Getenv("HOME"), ".yaver", "loops", "loops.json")
	data, _ := os.ReadFile(loopsPath)
	var loops map[string]*LoopState
	json.Unmarshal(data, &loops)
	ls := loops[res.LoopName]
	if ls == nil {
		t.Fatal("loop missing")
	}
	if !strings.Contains(ls.PromptInline, "AUTODEV MODE") {
		t.Errorf("autodev prompt did not propagate to LoopState.PromptInline")
	}
}

// TestRunHandoff_RejectsBadEngineRunnerCombo is the negative case: asking
// for engine=runner without a --runner must be a hard error so the user
// gets a clear message instead of a half-spawned loop.
func TestRunHandoff_RejectsBadEngineRunnerCombo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	srv := &HTTPServer{taskMgr: tm}

	_, err := RunHandoff(srv, HandoffSpec{
		Engine: "runner", WorkDir: t.TempDir(), SkipInitialKick: true,
	})
	if err == nil {
		t.Fatal("expected error for engine=runner with no Runner")
	}
	if !strings.Contains(err.Error(), "runner") {
		t.Errorf("error should mention runner, got: %v", err)
	}
}

func keys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
