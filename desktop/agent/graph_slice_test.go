package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGraphSliceRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test User"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{{"git", "add", "README.md"}, {"git", "commit", "-m", "init"}} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s failed: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return repo
}

func TestPrepareGraphNodeSliceLocalGitRepoUsesWorktree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGraphSliceRepo(t)

	spec := AgentGraphNodeSpec{ID: "chat", WorkDir: repo}
	workDir, contract, err := prepareGraphNodeSlice(context.Background(), "run123", spec, &AgentNodePlacement{DeviceID: "local"})
	if err != nil {
		t.Fatalf("prepareGraphNodeSlice() error = %v", err)
	}
	if workDir == repo {
		t.Fatalf("expected isolated worktree, got source repo %s", workDir)
	}
	if contract == nil || contract.IsolationMode != "git-worktree" {
		t.Fatalf("unexpected contract: %+v", contract)
	}
	if contract.EffectiveWorkDir != workDir {
		t.Fatalf("effective work dir mismatch: %+v", contract)
	}
}

func TestPrepareGraphNodeSliceRemoteGitRepoClearsWorkDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initGraphSliceRepo(t)

	spec := AgentGraphNodeSpec{ID: "chat", WorkDir: repo}
	workDir, contract, err := prepareGraphNodeSlice(context.Background(), "run123", spec, &AgentNodePlacement{DeviceID: "remote-box", DeviceName: "remote-box"})
	if err != nil {
		t.Fatalf("prepareGraphNodeSlice() error = %v", err)
	}
	if workDir != "" {
		t.Fatalf("expected empty workDir for remote placement (remote host doesn't have the source path), got %s", workDir)
	}
	if contract == nil || contract.IsolationMode != "remote-repo-contract" {
		t.Fatalf("unexpected contract: %+v", contract)
	}
	if contract.SourceWorkDir != repo {
		t.Fatalf("expected contract.SourceWorkDir to preserve the source repo for prompt context, got %q", contract.SourceWorkDir)
	}
	if contract.EffectiveWorkDir != "" {
		t.Fatalf("expected contract.EffectiveWorkDir empty so remote chooses its own, got %q", contract.EffectiveWorkDir)
	}
}

func TestPrepareGraphNodeSliceRemoteNonGitUsesDefaultContract(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	nonGit := t.TempDir()

	spec := AgentGraphNodeSpec{ID: "chat", WorkDir: nonGit}
	workDir, contract, err := prepareGraphNodeSlice(context.Background(), "run123", spec, &AgentNodePlacement{DeviceID: "remote-box"})
	if err != nil {
		t.Fatalf("prepareGraphNodeSlice() error = %v", err)
	}
	if workDir != "" {
		t.Fatalf("expected empty workDir for remote placement, got %s", workDir)
	}
	if contract == nil || contract.IsolationMode != "remote-default-workdir" {
		t.Fatalf("unexpected contract: %+v", contract)
	}
}
