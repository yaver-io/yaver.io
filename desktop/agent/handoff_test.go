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

func TestNormalizeSessionCompleteSpec_DefaultsAndPrompt(t *testing.T) {
	spec := normalizeSessionCompleteSpec(HandoffSpec{})

	if spec.Engine != "claude" {
		t.Fatalf("Engine default = %q, want claude", spec.Engine)
	}
	if spec.Load != "lite" {
		t.Fatalf("Load default = %q, want lite", spec.Load)
	}
	if spec.Hours != "infinite" {
		t.Fatalf("Hours default = %q, want infinite", spec.Hours)
	}
	if spec.MaxKicks != 200 {
		t.Fatalf("MaxKicks default = %d, want 200", spec.MaxKicks)
	}
	if spec.AutoIdeas != -1 {
		t.Fatalf("AutoIdeas default = %d, want -1", spec.AutoIdeas)
	}
	for _, want := range []string{
		"SESSION COMPLETE MODE",
		"Run the relevant tests",
		"Do NOT pivot into market research",
	} {
		if !strings.Contains(spec.ExtraPrompt, want) {
			t.Errorf("session-complete prompt missing %q\n---\n%s", want, spec.ExtraPrompt)
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

// TestResolveCallerPID_PriorityOrder locks the priority chain that
// session_handoff relies on: explicit MCP arg > stdio parent PID >
// HTTP loopback peer-port lookup > 0 (cooperative-only fallback).
func TestResolveCallerPID_PriorityOrder(t *testing.T) {
	// Reset between sub-cases.
	defer setMCPStdioCallerPID(0)

	// 1. Explicit wins over everything.
	setMCPStdioCallerPID(9999)
	if got := resolveCallerPID(12345, "127.0.0.1:65535"); got != 12345 {
		t.Errorf("explicit should win: got %d", got)
	}

	// 2. Stdio parent PID is used when explicit is 0.
	setMCPStdioCallerPID(7777)
	if got := resolveCallerPID(0, ""); got != 7777 {
		t.Errorf("stdio parent should be used: got %d", got)
	}

	// 3. With no explicit and no stdio, an empty addr falls through to 0.
	setMCPStdioCallerPID(0)
	if got := resolveCallerPID(0, ""); got != 0 {
		t.Errorf("no source → want 0, got %d", got)
	}

	// 4. Non-loopback HTTP addr is rejected (security: never SIGKILL
	// across machines).
	if got := resolveCallerPID(0, "10.0.0.5:54321"); got != 0 {
		t.Errorf("non-loopback should be rejected: got %d", got)
	}
}

// TestRunHandoff_LiteAndBurstLoadShapeSchedule verifies the --load knob
// produces the documented schedule defaults: lite stretches kicks to
// 5min and respects session limits; burst tightens to 30s and lifts
// the daily kick cap.
func TestRunHandoff_LiteAndBurstLoadShapeSchedule(t *testing.T) {
	cases := []struct {
		load              string
		wantEvery         string
		wantMaxIter       int
		wantRespectLimits bool
	}{
		{"lite", "5m", 20, true},
		{"", "5m", 20, true}, // empty defaults to lite
		{"burst", "30s", 200, false},
	}
	for _, c := range cases {
		t.Run("load="+c.load, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			workDir := t.TempDir()
			tm := NewTaskManager(workDir, nil, defaultTestRunner())
			srv := &HTTPServer{taskMgr: tm}

			res, err := RunHandoff(srv, HandoffSpec{
				WorkDir: workDir, Load: c.load, SkipInitialKick: true,
			})
			if err != nil {
				t.Fatalf("RunHandoff: %v", err)
			}
			loops, _ := loadLoops()
			ls := loops[res.LoopName]
			if ls.Spec.Schedule.Every != c.wantEvery {
				t.Errorf("Schedule.Every: want %q got %q", c.wantEvery, ls.Spec.Schedule.Every)
			}
			if ls.Spec.Schedule.MaxIterations != c.wantMaxIter {
				t.Errorf("MaxIterations: want %d got %d", c.wantMaxIter, ls.Spec.Schedule.MaxIterations)
			}
			if ls.Spec.Think.RespectSessionLimits == nil || *ls.Spec.Think.RespectSessionLimits != c.wantRespectLimits {
				t.Errorf("RespectSessionLimits: want %v got %v", c.wantRespectLimits, ls.Spec.Think.RespectSessionLimits)
			}
		})
	}
}

// TestRunHandoff_HoursBecomesPerKickTimeout — `--hours 8` must land as
// the loop's Schedule.Timeout so a single kick can't burn the whole
// 8-hour budget.
func TestRunHandoff_HoursBecomesPerKickTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	srv := &HTTPServer{taskMgr: tm}

	res, err := RunHandoff(srv, HandoffSpec{
		WorkDir: workDir, Hours: "8", SkipInitialKick: true,
	})
	if err != nil {
		t.Fatalf("RunHandoff: %v", err)
	}
	loops, _ := loadLoops()
	if got := loops[res.LoopName].Spec.Schedule.Timeout; got != "8h" {
		t.Errorf("Schedule.Timeout: want 8h got %q", got)
	}
}

// TestRunHandoff_AutodevParityKnobs locks in the wiring of the autodev-
// parity flags into the persisted LoopSpec. Each of these would silently
// no-op if the spec→loop translation regresses, so we assert the field
// landed on disk where the daemon's scheduler and runner read it from.
func TestRunHandoff_AutodevParityKnobs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	srv := &HTTPServer{taskMgr: tm}

	res, err := RunHandoff(srv, HandoffSpec{
		WorkDir:         workDir,
		LoopTarget:      "android-emu",
		Branch:          "feature-x",
		Deploy:          "testflight",
		Engine:          "hybrid",
		Prompt:          "ship the import wizard",
		ExtraPrompt:     "respect the existing zod schemas",
		SkipInitialKick: true,
	})
	if err != nil {
		t.Fatalf("RunHandoff: %v", err)
	}
	loops, _ := loadLoops()
	ls := loops[res.LoopName]
	if ls.Spec.Target != "android-emu" {
		t.Errorf("LoopTarget not applied: got %q", ls.Spec.Target)
	}
	if ls.Spec.Ship.Branch != "feature-x" {
		t.Errorf("Branch not applied: got %q", ls.Spec.Ship.Branch)
	}
	if ls.Spec.Ship.Deploy != "testflight" {
		t.Errorf("Deploy not applied: got %q", ls.Spec.Ship.Deploy)
	}
	// Explicit Prompt replaces the auto-resume prompt; the focus
	// string + extra-context block must both be present.
	if !strings.Contains(ls.PromptInline, "ship the import wizard") {
		t.Errorf("explicit Prompt missing from LoopState.PromptInline: %q", ls.PromptInline)
	}
	if !strings.Contains(ls.PromptInline, "respect the existing zod schemas") {
		t.Errorf("ExtraPrompt missing as additional context: %q", ls.PromptInline)
	}
}

// TestRunHandoff_AutoBranchSetsDatedBranch confirms --auto-branch turns
// into "autodev/<loop>-<YYYYMMDD>" so overnight runs get a PR-reviewable
// branch out of the box.
func TestRunHandoff_AutoBranchSetsDatedBranch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	srv := &HTTPServer{taskMgr: tm}

	res, err := RunHandoff(srv, HandoffSpec{
		WorkDir: workDir, AutoBranch: true, SkipInitialKick: true,
	})
	if err != nil {
		t.Fatalf("RunHandoff: %v", err)
	}
	loops, _ := loadLoops()
	br := loops[res.LoopName].Spec.Ship.Branch
	if !strings.HasPrefix(br, "autodev/handoff-") {
		t.Errorf("AutoBranch should produce an autodev/handoff-* branch, got %q", br)
	}
}

// TestOperatingDirectives_RendersWhenAnySet — flag fan-in test. Nothing
// set → empty block; any one set → the corresponding line appears so
// the runner sees its instruction.
func TestOperatingDirectives_RendersWhenAnySet(t *testing.T) {
	if got := operatingDirectives(HandoffSpec{}); got != "" {
		t.Errorf("empty spec should render no directives, got %q", got)
	}

	s := HandoffSpec{
		NoAutotest:   true,
		AutoIdeas:    3,
		RemainedFile: "/tmp/remained.md",
		Notify:       true,
	}
	got := operatingDirectives(s)
	for _, want := range []string{
		"Do NOT run the autotest",
		"up to 3 fresh batches of ideas",
		"/tmp/remained.md",
		"mobile notification",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("directives missing %q\n---\n%s", want, got)
		}
	}

	// AutoIdeas=-1 is the explicit "stop on empty" sentinel.
	if got := operatingDirectives(HandoffSpec{AutoIdeas: -1}); !strings.Contains(got, "Do not auto-generate") {
		t.Errorf("AutoIdeas=-1 should render stop-on-empty directive, got %q", got)
	}
}

// TestNormalizeDeploy_AcceptsTruthyAndFalsyAliases is the contract
// table for the --deploy knob. Truthy / empty / "all" → "both" (ship
// everywhere); falsy / "none" → "none"; named platforms pass through.
func TestNormalizeDeploy_AcceptsTruthyAndFalsyAliases(t *testing.T) {
	cases := map[string]string{
		"":           "both",
		"all":        "both",
		"yes":        "both",
		"true":       "both",
		"1":          "both",
		"on":         "both",
		"auto":       "both",
		"YES":        "both", // case-insensitive
		" all ":      "both", // trim-whitespace
		"no":         "none",
		"false":      "none",
		"0":          "none",
		"off":        "none",
		"none":       "none",
		"disable":    "none",
		"testflight": "testflight",
		"playstore":  "playstore",
		"both":       "both",
		"web":        "web",
		"garbage":    "both", // forward-compat: unknown → ship
	}
	for in, want := range cases {
		if got := normalizeDeploy(in); got != want {
			t.Errorf("normalizeDeploy(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRunHandoff_DeployDefaultsToBoth — handoff loops ship by default
// to every configured platform. Users opt OUT explicitly with
// --deploy false / no / 0 / none. This is the inverse of the previous
// "default-none" rule: handoff is meant to be a takeover, and a
// takeover that doesn't ship would surprise users more than one that
// does.
func TestRunHandoff_DeployDefaultsToBoth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	tm := NewTaskManager(workDir, nil, defaultTestRunner())
	srv := &HTTPServer{taskMgr: tm}

	res, err := RunHandoff(srv, HandoffSpec{WorkDir: workDir, SkipInitialKick: true})
	if err != nil {
		t.Fatalf("RunHandoff: %v", err)
	}
	loops, _ := loadLoops()
	if got := loops[res.LoopName].Spec.Ship.Deploy; got != "both" {
		t.Errorf("Deploy must default to 'both' for handoff, got %q", got)
	}
}

// TestRunHandoff_DeployDisableAliases — every documented falsy value
// must produce Spec.Ship.Deploy="none" so the loop's deploy phase is
// a no-op. If this regresses, `--deploy false` would silently still
// ship and surprise the user.
func TestRunHandoff_DeployDisableAliases(t *testing.T) {
	for _, in := range []string{"false", "no", "0", "none", "off", "disable"} {
		t.Run(in, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			workDir := t.TempDir()
			tm := NewTaskManager(workDir, nil, defaultTestRunner())
			srv := &HTTPServer{taskMgr: tm}

			res, err := RunHandoff(srv, HandoffSpec{
				WorkDir: workDir, Deploy: in, SkipInitialKick: true,
			})
			if err != nil {
				t.Fatalf("RunHandoff: %v", err)
			}
			loops, _ := loadLoops()
			if got := loops[res.LoopName].Spec.Ship.Deploy; got != "none" {
				t.Errorf("Deploy=%q should disable, got Spec.Ship.Deploy=%q", in, got)
			}
		})
	}
}

func keys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
