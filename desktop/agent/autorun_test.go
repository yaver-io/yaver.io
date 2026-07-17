package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAutorunRunnerArgsAlwaysAutoApproves(t *testing.T) {
	tests := []struct{ id, want string }{
		{"claude", "--dangerously-skip-permissions"},
		{"codex", "--dangerously-bypass-approvals-and-sandbox"},
		{"opencode", "--dangerously-skip-permissions"},
		{"glm", "--dangerously-skip-permissions"},
	}
	for _, tt := range tests {
		args := autorunRunnerArgs(GetRunnerConfig(tt.id), "do work")
		if !strings.Contains(strings.Join(args, " "), tt.want) {
			t.Errorf("%s args %q missing %q", tt.id, args, tt.want)
		}
		foundPrompt := false
		for _, arg := range args {
			if arg == "do work" {
				foundPrompt = true
				break
			}
		}
		if !foundPrompt {
			t.Errorf("%s prompt was not passed as one argument: %q", tt.id, args)
		}
	}
}

func TestAutorunPromptDoesNotAdvertiseUnattendedMode(t *testing.T) {
	lower := strings.ToLower(autorunPromptPreamble)
	if strings.Contains(lower, "auto mode") || strings.Contains(lower, "unattended") {
		t.Fatalf("prompt uses framing known to make runners hedge: %q", autorunPromptPreamble)
	}
	if !strings.Contains(lower, "do not ask questions") || !strings.Contains(lower, "most correct") {
		t.Fatalf("prompt is missing never-block guidance: %q", autorunPromptPreamble)
	}
}

// A runner that can't tell whose machine it is on treats a task arriving through
// tooling as someone else's workload and stalls on authorization instead of
// writing code. The preamble answers that up front; this pins the answer.
func TestAutorunPromptEstablishesDeveloperOwnership(t *testing.T) {
	lower := strings.ToLower(autorunPromptPreamble)
	for _, want := range []string{
		"developer who owns it",    // who is asking
		"belong to that developer", // whose machine and repo
		"his own local tooling",    // what Yaver is in this picture
		"pooled",                   // what this explicitly is not
	} {
		if !strings.Contains(lower, want) {
			t.Fatalf("prompt no longer establishes developer ownership (missing %q): %q", want, autorunPromptPreamble)
		}
	}
}

func TestRollbackAutorunChangesUsesDiagnosticStash(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	var gotName string
	var gotArgs []string
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return autorunCommandResult{}
	}
	if _, err := rollbackAutorunChanges(context.Background(), "/repo", 7); err != nil {
		t.Fatal(err)
	}
	if gotName != "git" || len(gotArgs) < 4 || gotArgs[0] != "stash" || gotArgs[1] != "push" || gotArgs[2] != "--include-untracked" {
		t.Fatalf("rollback command = %s %q", gotName, gotArgs)
	}
}

func TestAutorunReleasesSlot(t *testing.T) {
	if !autorunReleasesSlot(autorunReasonConverged) {
		t.Fatal("converged run should release its slot")
	}
	if !autorunReleasesSlot(autorunReasonDone) {
		t.Fatal("DONE run should release its slot")
	}
	for _, reason := range []string{autorunReasonGate, autorunReasonRunner, autorunReasonScope, autorunReasonStopped, autorunReasonMaxIters} {
		if autorunReleasesSlot(reason) {
			t.Fatalf("%q should keep its slot for restart", reason)
		}
	}
}

func TestValidateAutorunShellCommand(t *testing.T) {
	for _, command := range []string{"rm -rf build", "git reset --hard HEAD", "git push --force", "npm publish"} {
		if validateAutorunShellCommand(command) == nil {
			t.Errorf("expected %q to be rejected", command)
		}
	}
	if err := validateAutorunShellCommand("go build ./... && go test ./..."); err != nil {
		t.Fatalf("safe gate rejected: %v", err)
	}
}

func TestValidateAutorunScope(t *testing.T) {
	workDir := "/repo"
	progress := "/repo/docs/handoff/task-progress.md"
	if err := validateAutorunScope([]string{"desktop/agent/autorun.go", "docs/handoff/task-progress.md"}, []string{"desktop/agent/autorun*.go"}, progress, workDir); err != nil {
		t.Fatalf("allowed scope rejected: %v", err)
	}
	if err := validateAutorunScope([]string{"mobile/App.tsx"}, []string{"desktop/agent/**"}, progress, workDir); err == nil {
		t.Fatal("out-of-scope path accepted")
	}
}

func TestAutorunFinalCommitIsMarkedInSubjectAndBody(t *testing.T) {
	opts := autorunOptions{TaskPath: "/repo/tasks/yaver-video-task.md", Gate: "go test ./...", WorkDir: "/repo"}
	summary := autorunRunSummary{Iterations: 4, Commits: 2, FinishReason: autorunReasonConverged}

	subject := autorunFinalCommitSubject(opts.TaskPath, summary.FinishReason)
	if !strings.Contains(subject, autorunFinalCommitMarker) {
		t.Fatalf("subject must name it the final commit: %q", subject)
	}
	if !strings.Contains(subject, "yaver-video-task") || !strings.Contains(subject, autorunReasonConverged) {
		t.Fatalf("subject must carry task and reason: %q", subject)
	}

	body := autorunFinalCommitBody(opts, "codex", summary, nil)
	if !strings.Contains(body, autorunFinalCommitMarker) {
		t.Fatalf("body must name it the final commit: %q", body)
	}
	for _, want := range []string{autorunReasonConverged, "Iterations run: 4", "Verified commits kept: 2", "codex", "go test ./..."} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %q", want, body)
		}
	}
}

func TestAutorunFinalCommitBodyExplainsFailure(t *testing.T) {
	opts := autorunOptions{TaskPath: "/repo/task.md", Gate: "go test ./...", WorkDir: "/repo"}
	summary := autorunRunSummary{Iterations: 1, FinishReason: autorunReasonGate}
	body := autorunFinalCommitBody(opts, "codex", summary, errors.New("gate failed; changes preserved in a diagnostic git stash"))
	if !strings.Contains(body, "diagnostic git stash") || !strings.Contains(body, autorunReasonGate) {
		t.Fatalf("a blocked run's final commit must explain itself: %q", body)
	}
}

func TestFinalizeAutorunCommitsMarkedFinalCommitAndReportsSHA(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	workDir := t.TempDir()
	progressPath := filepath.Join(workDir, "docs", "handoff", "task-progress.md")

	var commitArgs []string
	var pushed bool
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		if name == "git" && len(args) > 0 {
			switch args[0] {
			case "commit":
				commitArgs = append([]string(nil), args...)
			case "rev-parse":
				return autorunCommandResult{Output: "abc1234def\n"}
			case "push":
				pushed = true
			}
		}
		return autorunCommandResult{}
	}

	opts := autorunOptions{TaskPath: filepath.Join(workDir, "task.md"), Gate: "go build ./...", WorkDir: workDir, Push: true}
	summary := autorunRunSummary{Iterations: 3, Commits: 1, FinishReason: autorunReasonMaxIters}
	if err := finalizeAutorun(context.Background(), opts, "codex", progressPath, &summary, nil); err != nil {
		t.Fatal(err)
	}

	if summary.FinalCommit != "abc1234def" {
		t.Fatalf("final commit SHA not reported: %q", summary.FinalCommit)
	}
	if !strings.Contains(summary.FinalSubject, autorunFinalCommitMarker) {
		t.Fatalf("summary subject not marked final: %q", summary.FinalSubject)
	}
	joined := strings.Join(commitArgs, " ")
	if !strings.Contains(joined, "-S") {
		t.Fatalf("final commit must be signed: %q", commitArgs)
	}
	if strings.Count(joined, autorunFinalCommitMarker) < 2 {
		t.Fatalf("marker must appear in BOTH subject and body: %q", commitArgs)
	}
	if !pushed {
		t.Fatal("final commit was not pushed despite --push")
	}
	note, err := os.ReadFile(progressPath)
	if err != nil || !strings.Contains(string(note), autorunFinalCommitMarker) {
		t.Fatalf("progress handoff missing the final note: %v %q", err, note)
	}
}

// A stopped run's context is already cancelled; the final commit is the whole
// point of stopping cleanly, so it must still be recorded.
func TestFinalizeAutorunRecordsFinalCommitAfterCancellation(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	workDir := t.TempDir()
	var sawLiveContext bool
	autorunExec = func(ctx context.Context, name string, args []string, _ string) autorunCommandResult {
		if ctx.Err() != nil {
			return autorunCommandResult{Err: ctx.Err()}
		}
		sawLiveContext = true
		if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
			return autorunCommandResult{Output: "deadbeef\n"}
		}
		return autorunCommandResult{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := autorunOptions{TaskPath: filepath.Join(workDir, "task.md"), Gate: "go build ./...", WorkDir: workDir}
	summary := autorunRunSummary{Iterations: 2, FinishReason: autorunReasonStopped}
	if err := finalizeAutorun(ctx, opts, "codex", filepath.Join(workDir, "p.md"), &summary, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if !sawLiveContext || summary.FinalCommit != "deadbeef" {
		t.Fatalf("cancelled run failed to record its final commit: %q", summary.FinalCommit)
	}
}

func TestAutorunReleaseWorkspaceLandsMainAndCleansUp(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	var calls []string
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "git" && len(args) >= 5 && args[0] == "-C" && args[2] == "status" {
			return autorunCommandResult{}
		}
		if name == "git" && len(args) >= 5 && args[0] == "-C" && args[2] == "branch" && args[3] == "--show-current" {
			return autorunCommandResult{Output: "feature\n"}
		}
		if name == "git" && len(args) >= 5 && args[0] == "-C" && args[2] == "push" && args[3] == "origin" && args[4] == "--delete" {
			return autorunCommandResult{Output: "remote ref does not exist"}
		}
		return autorunCommandResult{}
	}
	ws := autorunWorkspace{Slot: "task:codex", Branch: "autorun/task/codex", SourceWorkDir: "/repo", WorkDir: "/slot"}
	if err := autorunReleaseWorkspace(context.Background(), ws, true, true); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{
		"git -C /repo status --porcelain",
		"git -C /repo branch --show-current",
		"git -C /repo checkout main",
		"git -C /repo fetch origin",
		"git -C /repo pull --ff-only origin main",
		"git -C /repo merge --ff-only autorun/task/codex",
		"git -C /repo push origin main",
		"git -C /repo worktree remove --force /slot",
		"git -C /repo branch -D autorun/task/codex",
		"git -C /repo push origin --delete autorun/task/codex",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("release sequence missing %q:\n%s", want, joined)
		}
	}
}

func TestAutorunReleaseWorkspaceDeletesEmptySlotWithoutLanding(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	var calls []string
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return autorunCommandResult{}
	}
	ws := autorunWorkspace{Slot: "task:codex", Branch: "autorun/task/codex", SourceWorkDir: "/repo", WorkDir: "/slot"}
	if err := autorunReleaseWorkspace(context.Background(), ws, false, false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, " merge ") || strings.Contains(joined, " push ") || strings.Contains(joined, " checkout ") {
		t.Fatalf("empty slot cleanup should not try to land or push anything:\n%s", joined)
	}
	for _, want := range []string{
		"git -C /repo worktree remove --force /slot",
		"git -C /repo branch -D autorun/task/codex",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("cleanup missing %q:\n%s", want, joined)
		}
	}
}

func TestAutorunGitChangesParsesRename(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	autorunExec = func(_ context.Context, name string, args []string, dir string) autorunCommandResult {
		return autorunCommandResult{Output: " M desktop/agent/autorun.go\x00R  docs/new.md\x00docs/old.md\x00?? docs/handoff/progress.md\x00"}
	}
	got, err := autorunGitChanges(context.Background(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"desktop/agent/autorun.go", "docs/handoff/progress.md", "docs/new.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

// The heal that would have saved 2026-07-16: claude died on headless-auth while
// codex sat ready and unused. Failover must be recorded, not silent.
func TestReadyAutorunRunnersSkipsFailedRunner(t *testing.T) {
	all := readyAutorunRunners(t.TempDir(), nil)
	if len(all) == 0 {
		t.Skip("no runner authenticated on this machine")
	}
	excluded := readyAutorunRunners(t.TempDir(), map[string]bool{all[0].RunnerID: true})
	for _, r := range excluded {
		if r.RunnerID == all[0].RunnerID {
			t.Fatalf("failed runner %q was offered again for failover", r.RunnerID)
		}
	}
	if len(excluded) != len(all)-1 {
		t.Fatalf("expected exactly one runner excluded: %d vs %d", len(excluded), len(all))
	}
}

func TestAutorunFinalCommitBodyRecordsHeals(t *testing.T) {
	opts := autorunOptions{TaskPath: "/repo/task.md", Gate: "go test ./...", WorkDir: "/repo"}
	summary := autorunRunSummary{
		Iterations: 3, Commits: 1, FinishReason: autorunReasonConverged,
		Heals: []autorunHealEvent{
			{Iteration: 1, Kind: autorunHealRunnerFailover, Detail: "runner claude failed; failing over to codex"},
			{Iteration: 2, Kind: autorunHealDiskReclaim, Detail: "go clean -cache: 1.1 GB free -> 6.5 GB free"},
		},
	}
	body := autorunFinalCommitBody(opts, "codex", summary, nil)
	if !strings.Contains(body, "Self-healed 2 time(s)") {
		t.Fatalf("heal count missing: %q", body)
	}
	for _, want := range []string{autorunHealRunnerFailover, autorunHealDiskReclaim, "failing over to codex", "6.5 GB free"} {
		if !strings.Contains(body, want) {
			t.Fatalf("heal detail %q missing from final commit: %q", want, body)
		}
	}
}

func TestReclaimAutorunDiskOnlyTouchesCaches(t *testing.T) {
	original := autorunExec
	defer func() { autorunExec = original }()
	var ran [][]string
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		ran = append(ran, append([]string{name}, args...))
		return autorunCommandResult{}
	}
	note := reclaimAutorunDisk(context.Background(), t.TempDir())
	if len(ran) != 1 || ran[0][0] != "go" || ran[0][1] != "clean" || ran[0][2] != "-cache" {
		t.Fatalf("reclaim must only clean caches, ran: %v", ran)
	}
	if !strings.Contains(note, "GB free") {
		t.Fatalf("reclaim must report the space delta: %q", note)
	}
}
