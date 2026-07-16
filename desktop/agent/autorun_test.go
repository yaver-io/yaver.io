package main

import (
	"context"
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
	if err := rollbackAutorunChanges(context.Background(), "/repo", 7); err != nil {
		t.Fatal(err)
	}
	if gotName != "git" || len(gotArgs) < 4 || gotArgs[0] != "stash" || gotArgs[1] != "push" || gotArgs[2] != "--include-untracked" {
		t.Fatalf("rollback command = %s %q", gotName, gotArgs)
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
