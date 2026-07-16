package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The master/doer split exists for token economics: the planning seat stays
// cheap because it only ever emits an instruction. That only holds if the two
// seats are actually driven differently, and if either runner can hold either
// seat — nothing here may assume a particular runner plans.

func TestAutorunSeatsFromTaskFrontMatter(t *testing.T) {
	seats := autorunSeatsFromTask("---\nmaster: opencode\ndoer: codex\n---\n\n# Task\n\nDo the thing.\n")
	if seats.Master != "opencode" || seats.Doer != "codex" {
		t.Fatalf("front matter seats not parsed: %+v", seats)
	}
	// `runner:` is the same seat as `doer:` — the CLI calls it --runner.
	if got := autorunSeatsFromTask("---\nrunner: glm\n---\n"); got.Doer != "glm" {
		t.Fatalf("runner: should be a synonym for doer:: %+v", got)
	}
	// A task file is a document first. No front matter is the common case and
	// must not be an error.
	if got := autorunSeatsFromTask("# Task\n\nmaster: not-front-matter\n"); got.Master != "" || got.Doer != "" {
		t.Fatalf("prose must not be read as a seat assignment: %+v", got)
	}
}

// The operator speaking now beats the file speaking whenever it was written.
func TestAutorunExplicitSeatsBeatTaskFile(t *testing.T) {
	workDir := t.TempDir()
	taskPath := filepath.Join(workDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("---\nmaster: opencode\ndoer: codex\n---\n\n# Task\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seats := autorunSeatsFromTask("---\nmaster: opencode\ndoer: codex\n---\n")
	opts := autorunOptions{Master: "glm", Runner: "claude"}
	if strings.TrimSpace(opts.Master) == "" {
		opts.Master = seats.Master
	}
	if r := strings.TrimSpace(opts.Runner); (r == "" || r == "auto") && seats.Doer != "" {
		opts.Runner = seats.Doer
	}
	if opts.Master != "glm" || opts.Runner != "claude" {
		t.Fatalf("task file overrode the explicit request: %+v", opts)
	}
}

// A run whose two seats hold the same runner is a misconfiguration, not a split.
func TestAutorunRejectsSameRunnerInBothSeats(t *testing.T) {
	workDir := t.TempDir()
	taskPath := filepath.Join(workDir, "task.md")
	if err := os.WriteFile(taskPath, []byte("# Task\n"), 0644); err != nil {
		t.Fatal(err)
	}
	original := autorunExec
	defer func() { autorunExec = original }()
	autorunExec = func(_ context.Context, _ string, _ []string, _ string) autorunCommandResult {
		return autorunCommandResult{}
	}
	_, err := executeAutorun(context.Background(), autorunOptions{
		TaskPath: taskPath, Gate: "go build ./...", WorkDir: workDir,
		Runner: "codex", Master: "codex",
	})
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("same runner in both seats must be refused, got: %v", err)
	}
}

// The master plans; the doer implements what it planned. If the instruction does
// not reach the doer's prompt, the split is theatre — the doer just re-plans and
// the master's tokens bought nothing.
func TestAutorunPlanInstructionReachesTheDoer(t *testing.T) {
	originalKick, originalExec := autorunKick, autorunExec
	defer func() { autorunKick, autorunExec = originalKick, originalExec }()

	workDir := t.TempDir()
	progressPath := filepath.Join(workDir, "progress.md")
	const instruction = "Add a disk floor to ops_diskguard.go; keep the 5 GB constant."

	var doerPrompt string
	autorunKick = func(_ context.Context, _ autorunOptions, runner RunnerConfig, prompt string, _ time.Duration) autorunCommandResult {
		if runner.RunnerID == "opencode" { // master seat — any runner may hold it
			return autorunCommandResult{Output: instruction}
		}
		doerPrompt = prompt
		return autorunCommandResult{Output: "done"}
	}
	autorunExec = func(_ context.Context, _ string, _ []string, _ string) autorunCommandResult {
		return autorunCommandResult{} // clean worktree: the master did not edit
	}

	opts := autorunOptions{TaskPath: filepath.Join(workDir, "task.md"), WorkDir: workDir}
	got, err := autorunPlan(context.Background(), opts, GetRunnerConfig("opencode"), "# Task", "", "", progressPath, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != instruction {
		t.Fatalf("instruction = %q", got)
	}

	// The sync channel: the instruction is in the progress file BEFORE the doer
	// runs, so a run that dies mid-iteration still shows what was asked.
	note, err := os.ReadFile(progressPath)
	if err != nil || !strings.Contains(string(note), "MASTER INSTRUCTION (iteration 3, runner `opencode`)") || !strings.Contains(string(note), instruction) {
		t.Fatalf("instruction not recorded for the doer to read: %v %q", err, note)
	}

	doerPrompt = autorunDoerContext("# Task", "", "", got, 3)
	if !strings.Contains(doerPrompt, instruction) {
		t.Fatalf("doer prompt does not carry the master's instruction: %q", doerPrompt)
	}
}

// The master is told not to edit. This is what makes that true: a stray master
// edit would otherwise land inside the doer's diff seconds later and be
// committed as the doer's work, passing or failing the gate on its behalf.
func TestAutorunPlanRollsBackAMasterThatEdited(t *testing.T) {
	originalKick, originalExec := autorunKick, autorunExec
	defer func() { autorunKick, autorunExec = originalKick, originalExec }()

	workDir := t.TempDir()
	progressPath := filepath.Join(workDir, "progress.md")

	autorunKick = func(_ context.Context, _ autorunOptions, _ RunnerConfig, _ string, _ time.Duration) autorunCommandResult {
		return autorunCommandResult{Output: "Edit ops_diskguard.go."}
	}
	var stashed bool
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		if name == "git" && len(args) > 0 {
			switch args[0] {
			case "status":
				return autorunCommandResult{Output: " M desktop/agent/autorun.go\x00"}
			case "stash":
				stashed = true
			}
		}
		return autorunCommandResult{}
	}

	opts := autorunOptions{TaskPath: filepath.Join(workDir, "task.md"), WorkDir: workDir}
	instruction, err := autorunPlan(context.Background(), opts, GetRunnerConfig("claude"), "# Task", "", "", progressPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !stashed {
		t.Fatal("a master that edited the worktree was not rolled back; its edit would land in the doer's diff")
	}
	if instruction == "" {
		t.Fatal("the plan is still usable after rolling back the master's edit")
	}
	note, _ := os.ReadFile(progressPath)
	if !strings.Contains(string(note), "planning seat does not implement") {
		t.Fatalf("the rollback must be recorded, not silent: %q", note)
	}
}

// An empty plan means the master said nothing usable. Kicking the doer with it
// would spend the doer's tokens on an unconstrained "do whatever" turn.
func TestAutorunPlanRefusesEmptyInstruction(t *testing.T) {
	originalKick, originalExec := autorunKick, autorunExec
	defer func() { autorunKick, autorunExec = originalKick, originalExec }()

	autorunKick = func(_ context.Context, _ autorunOptions, _ RunnerConfig, _ string, _ time.Duration) autorunCommandResult {
		return autorunCommandResult{Output: "   \n  \n"}
	}
	autorunExec = func(_ context.Context, _ string, _ []string, _ string) autorunCommandResult {
		return autorunCommandResult{}
	}
	workDir := t.TempDir()
	_, err := autorunPlan(context.Background(), autorunOptions{WorkDir: workDir}, GetRunnerConfig("codex"), "# Task", "", "", filepath.Join(workDir, "p.md"), 1)
	if err == nil || !strings.Contains(err.Error(), "empty plan") {
		t.Fatalf("empty instruction must not reach the doer, got: %v", err)
	}
}

// A failed master must not be reported as "the doer failed". Same run, very
// different diagnosis.
func TestAutorunPlanNamesTheMasterOnFailure(t *testing.T) {
	originalKick, originalExec := autorunKick, autorunExec
	defer func() { autorunKick, autorunExec = originalKick, originalExec }()

	autorunKick = func(_ context.Context, _ autorunOptions, _ RunnerConfig, _ string, _ time.Duration) autorunCommandResult {
		return autorunCommandResult{Output: "OAuth session expired", Err: fmt.Errorf("exit 1")}
	}
	autorunExec = func(_ context.Context, _ string, _ []string, _ string) autorunCommandResult {
		return autorunCommandResult{}
	}
	workDir := t.TempDir()
	_, err := autorunPlan(context.Background(), autorunOptions{WorkDir: workDir}, GetRunnerConfig("glm"), "# Task", "", "", filepath.Join(workDir, "p.md"), 5)
	if err == nil || !strings.Contains(err.Error(), "master glm") || !strings.Contains(err.Error(), "OAuth session expired") {
		t.Fatalf("a failed master must name itself and carry its output: %v", err)
	}
}

// The planning prompt must forbid editing, and must not name a runner: the seats
// carry the behavior, and any runner can hold either one.
func TestAutorunMasterPromptIsReadOnlyAndRunnerAgnostic(t *testing.T) {
	lower := strings.ToLower(autorunMasterPromptPreamble)
	if !strings.Contains(lower, "do not edit") {
		t.Fatalf("planning prompt must forbid editing: %q", autorunMasterPromptPreamble)
	}
	if !strings.Contains(lower, "developer who owns this repository") {
		t.Fatalf("planning prompt must establish developer ownership like the doer's does: %q", autorunMasterPromptPreamble)
	}
	for _, id := range supportedRunnerIDs {
		if strings.Contains(lower, strings.ToLower(id)) {
			t.Fatalf("planning prompt names runner %q; seats must stay runner-agnostic: %q", id, autorunMasterPromptPreamble)
		}
	}
}

// `yaver autorun --machine` reads the session ID out of autorun_start's reply so
// it can tell the operator how to poll and stop the remote run. autorun_start
// returns the view AS initial, while autorun_status wraps it in {"sessions":[…]}
// — reading the wrong shape reports success with an empty ID and strands a loop
// nobody can stop. This pins the envelope the CLI parses.
func TestRemoteAutorunStartRepliesWithAnUnwrappedSessionView(t *testing.T) {
	body, err := json.Marshal(OpsResult{OK: true, Initial: autorunSessionView{ID: "autorun-42", Master: "claude", ActiveRunner: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		OK      bool               `json:"ok"`
		Initial autorunSessionView `json:"initial"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.OK || parsed.Initial.ID != "autorun-42" {
		t.Fatalf("the CLI cannot read the session ID out of autorun_start's reply: %s", body)
	}
	// The seats must survive the wire, or a remote two-seat run is
	// indistinguishable from a single-runner one.
	if parsed.Initial.Master != "claude" || parsed.Initial.ActiveRunner != "codex" {
		t.Fatalf("seats lost in transport: %+v", parsed.Initial)
	}
}

// The final commit is the run's record. "codex" alone does not say whether it
// planned the work or only typed it.
func TestAutorunFinalCommitBodyNamesBothSeats(t *testing.T) {
	opts := autorunOptions{TaskPath: "/repo/task.md", Gate: "go test ./...", WorkDir: "/repo"}
	summary := autorunRunSummary{Iterations: 2, Commits: 1, FinishReason: autorunReasonConverged, Master: "claude"}
	body := autorunFinalCommitBody(opts, "codex", summary, nil)
	if !strings.Contains(body, "Master: claude") || !strings.Contains(body, "Runner: codex (doer") {
		t.Fatalf("final commit must name both seats: %q", body)
	}
	// A single-runner run has no master and must not grow a phantom seat.
	solo := autorunFinalCommitBody(opts, "codex", autorunRunSummary{FinishReason: autorunReasonDone}, nil)
	if strings.Contains(solo, "Master:") {
		t.Fatalf("single-runner run must not report a master: %q", solo)
	}
}
