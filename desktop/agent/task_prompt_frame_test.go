package main

import (
	"fmt"
	"strings"
	"testing"
)

// task_prompt_frame_test.go — the guards for "preamble on the first message of
// a runner session, the user's own words for every turn after".
//
// The composer is a pure seam on purpose: it takes a Task and a policy and
// returns bytes. Everything asserted here is therefore checkable without
// spawning a runner, which is the only way a rule about *what a runner reads*
// can be regression-proof.

func framedTestManager(t *testing.T) *TaskManager {
	t.Helper()
	return &TaskManager{workDir: t.TempDir()}
}

func framedMobileTask(tm *TaskManager) *Task {
	return &Task{
		ID:       "t-frame",
		Source:   "mobile",
		WorkDir:  tm.workDir,
		RunnerID: "claude",
		runner:   RunnerConfig{RunnerID: "claude", Command: "claude"},
	}
}

// --- 1. the first message carries the briefing ------------------------------

func TestFirstMessageCarriesThePreamble(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)

	got := tm.composeTurnPrompt(task, "add a settings screen", promptFramePolicy{ArmPreamble: true})

	for _, want := range []string{
		"Yaver orchestration",               // yaver-action sentinel instructions (prefix)
		"[Yaver task]",                      // compact agent-owned stream contract
		"[Yaver run policy]",                // no-questions policy
		"[Yaver — recurring / future work]", // scheduling contract
		"[Yaver Agent Context]",             // dev-server transport rules
		"add a settings screen",             // and the user's actual ask
	} {
		if !strings.Contains(got, want) {
			t.Errorf("first message is missing %q — a cold runner would not know it is inside Yaver", want)
		}
	}
	if !strings.HasPrefix(got, YaverActionSystemPrompt) {
		t.Error("the yaver-action instructions must LEAD the first message; codex/opencode have no --append-system-prompt")
	}
}

func TestFirstMessageCarriesSelectedProjectFrame(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)
	task.ProjectName = "medici.ai"

	got := tm.composeTurnPrompt(task, "run text-to-text tests", promptFramePolicy{ArmPreamble: true})

	for _, want := range []string{
		"[Yaver selected project]",
		"Project: medici.ai",
		"Runner working directory: " + tm.workDir,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("selected project frame missing %q in:\n%s", want, got)
		}
	}
}

func TestOpenCodeDeepSeekGetsAutonomyLadder(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)
	task.RunnerID = "opencode"
	task.Model = "deepseek/deepseek-v4.1-flash"
	task.runner = RunnerConfig{RunnerID: "opencode", Model: task.Model}

	got := tm.composeTurnPrompt(task, "build the feature", promptFramePolicy{ArmPreamble: true})
	if !strings.Contains(got, "[OpenCode + DeepSeek autonomy ladder]") {
		t.Fatal("OpenCode with DeepSeek must receive the ambiguity decision ladder")
	}
}

func TestAutonomyLadderDoesNotAlterCodexOrOtherOpenCodeModels(t *testing.T) {
	tm := framedTestManager(t)
	for _, task := range []*Task{
		{Source: "mobile", RunnerID: "codex", Model: "deepseek/deepseek-v4.1-flash", WorkDir: tm.workDir, runner: RunnerConfig{RunnerID: "codex"}},
		{Source: "mobile", RunnerID: "opencode", Model: "openai/gpt-5", WorkDir: tm.workDir, runner: RunnerConfig{RunnerID: "opencode"}},
	} {
		got := tm.composeTurnPrompt(task, "build the feature", promptFramePolicy{ArmPreamble: true})
		if strings.Contains(got, "[OpenCode + DeepSeek autonomy ladder]") {
			t.Fatalf("autonomy ladder leaked to runner=%q model=%q", task.RunnerID, task.Model)
		}
	}
}

// --- 2. every later turn is the user's words --------------------------------

func TestFollowUpIsTheUsersWordsVerbatim(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)
	task.SessionID = "sess-1"

	const userText = "now make it red"
	got := tm.composeTurnPrompt(task, userText, promptFramePolicy{ArmPreamble: false})

	// The ONLY thing allowed to ride a follow-up is the echo boundary.
	want := userText + "\n\n" + promptEchoSentinel + "\n"
	if got != want {
		t.Fatalf("follow-up is not the user's words.\n got: %q\nwant: %q", got, want)
	}

	// Belt-and-braces: name the blocks explicitly, so a future block added to
	// the armed frame cannot leak into a follow-up without failing here.
	for _, banned := range []string{
		"Yaver orchestration",
		"[Yaver task]",
		"[Yaver run policy]",
		"[Yaver — recurring / future work]",
		"[Yaver wrapper capabilities]",
		"[Yaver Agent Context]",
	} {
		if strings.Contains(got, banned) {
			t.Errorf("follow-up re-sent %q — the runner already read it on turn 1", banned)
		}
	}
}

// --- 3. a NEW session re-arms ----------------------------------------------

func TestNewSessionReArmsThePreamble(t *testing.T) {
	// The decision is made from the runner + what we captured, never from a
	// UI-level "is this the first message" guess.
	cases := []struct {
		name      string
		runner    RunnerConfig
		sessionID string
		carries   bool
	}{
		{"claude with a captured session", RunnerConfig{RunnerID: "claude"}, "abc-123", true},
		{"claude with nothing to resume", RunnerConfig{RunnerID: "claude"}, "", false},
		{"codex with a captured session", RunnerConfig{RunnerID: "codex"}, "abc-123", true},
		{"codex with nothing to resume", RunnerConfig{RunnerID: "codex"}, "", false},
		{"opencode with exact session", RunnerConfig{RunnerID: "opencode"}, "ses_exact", true},
		{"opencode with nothing to resume", RunnerConfig{RunnerID: "opencode"}, "", false},
		{"custom runner with a template", RunnerConfig{RunnerID: "glm", ResumeArgs: []string{"--resume", "{sessionId}"}}, "abc", true},
		{"custom runner that cannot resume", RunnerConfig{RunnerID: "glm"}, "abc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resumeCanCarryContext(tc.runner, tc.sessionID); got != tc.carries {
				t.Fatalf("resumeCanCarryContext = %v, want %v", got, tc.carries)
			}
			// The argv builder and the preamble decision MUST agree — that is
			// the whole reason they share this oracle. A resume that builds
			// resume-args but skips the preamble, or vice versa, is the bug.
			_, transformed := resumeTransform(tc.runner, []string{"-p", "hi"}, "hi", "/tmp", tc.sessionID)
			if transformed != tc.carries {
				t.Fatalf("resumeTransform ok = %v but resumeCanCarryContext = %v — the two decisions drifted", transformed, tc.carries)
			}
		})
	}
}

// --- 4. a follow-up after a runner restart re-arms --------------------------

func TestFollowUpAfterRunnerRestartReArms(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)

	// The runner died / was switched, so nothing was captured to resume.
	task.SessionID = ""
	carries := resumeCanCarryContext(task.runner, task.SessionID)
	if carries {
		t.Fatal("a claude follow-up with no session id cannot carry context")
	}

	got := tm.composeTurnPrompt(task, "now make it red", promptFramePolicy{ArmPreamble: !carries})
	if !strings.Contains(got, "[Yaver run policy]") {
		t.Error("a cold restart must be briefed like a first message — otherwise the runner answers a phone with desktop-shaped markdown and cannot drive the dev server")
	}
	if !strings.Contains(got, "now make it red") {
		t.Error("the user's words must still be in there")
	}
}

// --- 5. runner-native commands keep their exact bytes -----------------------

func TestRawRunnerCommandGetsNoFrameAtAll(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)

	got := tm.composeTurnPrompt(task, "  /exit", promptFramePolicy{ArmPreamble: true, RawRunnerCommand: true})
	if got != "/exit" {
		t.Fatalf("runner-native command was rewritten: %q — even the boundary sentinel changes what /exit means", got)
	}
}

// --- 6. per-turn context rides EVERY turn -----------------------------------

func TestPerTurnContextRidesEveryTurn(t *testing.T) {
	// The seam the screen-context work plugs into: context that is true only of
	// THIS turn cannot live in the armed frame, because the screen the user is
	// looking at changes between two messages of one conversation.
	saved := perTurnContextHooks
	t.Cleanup(func() { perTurnContextHooks = saved })
	perTurnContextHooks = nil
	registerPerTurnContext(func(task *Task, contextDir string) string {
		return "\n\n" + promptEchoSentinel + "\n[Screen the user is looking at] route: /settings\n" + promptEchoSentinel + "\n"
	})

	tm := framedTestManager(t)
	task := framedMobileTask(tm)

	for _, armed := range []bool{true, false} {
		got := tm.composeTurnPrompt(task, "make this red", promptFramePolicy{ArmPreamble: armed})
		if !strings.Contains(got, "[Screen the user is looking at]") {
			t.Errorf("armed=%v: per-turn context was dropped — a follow-up is exactly when 'this screen' means a NEW screen", armed)
		}
	}
}

// --- 7. attachments are data, so they ride every turn -----------------------

func TestAttachmentsRideEveryTurn(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)
	task.ImagePaths = []string{"/var/yaver/shot.png"}

	got := tm.composeTurnPrompt(task, "what is wrong here", promptFramePolicy{ArmPreamble: false})
	if !strings.Contains(got, "/var/yaver/shot.png") {
		t.Fatal("a follow-up's attachment path must survive — the runner cannot discover it on its own")
	}
}

// --- 8. embedded chat never sees coding-agent framing -----------------------

func TestChatModeNeverSeesCodingAgentFraming(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)
	task.Source = "chat"

	got := tm.composeTurnPrompt(task, "kaç fatura var", promptFramePolicy{ArmPreamble: true, ChatMode: "chat:whatsapp"})
	if strings.Contains(got, "[Yaver run policy]") || strings.Contains(got, "[Yaver Agent Context]") {
		t.Fatal("embedded chat leaked coding-agent framing to a non-technical end user")
	}
	if !strings.Contains(got, "WhatsApp") {
		t.Fatal("embedded chat lost its surface contract")
	}
}

// --- 9. the measurement -----------------------------------------------------

func TestFollowUpSavesMostOfThePrompt(t *testing.T) {
	tm := framedTestManager(t)
	task := framedMobileTask(tm)

	const userText = "now make it red"
	first := tm.composeTurnPrompt(task, userText, promptFramePolicy{ArmPreamble: true})
	follow := tm.composeTurnPrompt(task, userText, promptFramePolicy{ArmPreamble: false})

	saved := len(first) - len(follow)
	pct := float64(saved) / float64(len(first)) * 100
	// ~4 bytes/token is the usual English estimate; good enough to state an
	// order of magnitude in the test log rather than in a commit message.
	fmt.Printf("[prompt frame] armed=%d bytes (~%d tokens) · follow-up=%d bytes (~%d tokens) · saved %d bytes (%.1f%%) per follow-up turn\n",
		len(first), len(first)/4, len(follow), len(follow)/4, saved, pct)

	if pct < 95 {
		t.Fatalf("a follow-up still carries %.1f%% of the first-turn prompt — the preamble is leaking into follow-ups", 100-pct)
	}
	if len(follow) > len(userText)+64 {
		t.Fatalf("follow-up is %d bytes for a %d-byte message; only the boundary sentinel may ride along", len(follow), len(userText))
	}
}
