package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func graphNodeWorktreePath(runID, nodeID string) (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "graphs", strings.ToLower(runID), strings.ToLower(nodeID), "worktree"), nil
}

func currentGitBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ensureGraphNodeWorktree(ctx context.Context, srcRepo, runID, nodeID string) (string, error) {
	if srcRepo == "" {
		return "", fmt.Errorf("source repo required")
	}
	if !looksLikeGitRepo(srcRepo) {
		return "", fmt.Errorf("source %s is not a git repo", srcRepo)
	}
	wtPath, err := graphNodeWorktreePath(runID, nodeID)
	if err != nil {
		return "", err
	}
	branch := currentGitBranch(srcRepo)
	if branch == "" {
		branch = "HEAD"
	}
	registered := false
	if out, listErr := exec.Command("git", "-C", srcRepo, "worktree", "list", "--porcelain").Output(); listErr == nil {
		registered = strings.Contains(string(out), wtPath)
	}
	if !registered {
		_ = os.RemoveAll(wtPath)
		if err := os.MkdirAll(filepath.Dir(wtPath), 0o700); err != nil {
			return "", fmt.Errorf("create graph worktree parent: %w", err)
		}
		_ = exec.CommandContext(ctx, "git", "-C", srcRepo, "fetch", "origin").Run()
		args := []string{"-C", srcRepo, "worktree", "add", "--detach", wtPath}
		if branch != "HEAD" {
			args = append(args, branch)
		}
		if out, addErr := exec.CommandContext(ctx, "git", args...).CombinedOutput(); addErr != nil {
			return "", fmt.Errorf("git worktree add %s: %v (%s)", wtPath, addErr, strings.TrimSpace(string(out)))
		}
		return wtPath, nil
	}
	_ = exec.CommandContext(ctx, "git", "-C", wtPath, "reset", "--hard").Run()
	_ = exec.CommandContext(ctx, "git", "-C", wtPath, "clean", "-fd").Run()
	_ = exec.CommandContext(ctx, "git", "-C", wtPath, "fetch", "origin").Run()
	if branch != "HEAD" {
		target := "origin/" + branch
		if err := exec.CommandContext(ctx, "git", "-C", wtPath, "reset", "--hard", target).Run(); err != nil {
			_ = exec.CommandContext(ctx, "git", "-C", wtPath, "reset", "--hard", branch).Run()
		}
	}
	return wtPath, nil
}

func buildGraphSliceContract(runID string, spec AgentGraphNodeSpec, placement *AgentNodePlacement, effectiveWorkDir, isolationMode string) *TaskSliceContract {
	remote, branch, commit := getGitInfo(spec.WorkDir)
	contract := &TaskSliceContract{
		RunID:            runID,
		NodeID:           spec.ID,
		SourceWorkDir:    spec.WorkDir,
		EffectiveWorkDir: effectiveWorkDir,
		GitRemote:        remote,
		GitBranch:        branch,
		GitCommit:        commit,
		IsolationMode:    isolationMode,
	}
	if placement != nil {
		contract.DeviceID = placement.DeviceID
		contract.DeviceName = placement.DeviceName
	}
	return contract
}

func prepareGraphNodeSlice(ctx context.Context, runID string, spec AgentGraphNodeSpec, placement *AgentNodePlacement) (string, *TaskSliceContract, error) {
	workDir := spec.WorkDir
	isolationMode := "shared-workdir"
	isLocal := placement == nil || placement.DeviceID == "" || placement.DeviceID == "local"
	if isLocal && looksLikeGitRepo(spec.WorkDir) {
		wtPath, err := ensureGraphNodeWorktree(ctx, spec.WorkDir, runID, spec.ID)
		if err != nil {
			return "", nil, err
		}
		workDir = wtPath
		isolationMode = "git-worktree"
	} else if !isLocal {
		// The source host's absolute path will not exist on a remote
		// machine; sending it would either fail or silently cwd into an
		// unrelated directory if a same-named path happened to exist.
		// Return empty so the remote falls back to its configured
		// workdir, and keep the source path in contract.SourceWorkDir
		// for the runner prompt.
		workDir = ""
		if looksLikeGitRepo(spec.WorkDir) {
			isolationMode = "remote-repo-contract"
		} else {
			isolationMode = "remote-default-workdir"
		}
	}
	return workDir, buildGraphSliceContract(runID, spec, placement, workDir, isolationMode), nil
}
