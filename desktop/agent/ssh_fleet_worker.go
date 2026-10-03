package main

// ssh_fleet_worker.go is the deliberately narrow, authless worker lane.
//
// Authority comes from an operator-configured SSH target and the remote OS
// account's authorized_keys policy. The worker never registers with Convex,
// opens a Yaver listener, or accepts a bearer token. The master invokes one
// hidden CLI verb over SSH; prompts travel on stdin and runner credentials stay
// in the remote account. This is not a relay/auth bypass: it is local SSH
// configuration, usable only by a master that already has OS-level access.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const sshFleetDevicePrefix = "ssh:"

type sshFleetProbeCacheEntry struct {
	at  time.Time
	row MachineInfo
}

var sshFleetProbeCache sync.Map

type sshFleetRunRequest struct {
	Title         string             `json:"title"`
	Prompt        string             `json:"prompt"`
	Runner        string             `json:"runner"`
	Model         string             `json:"model,omitempty"`
	WorkDir       string             `json:"workDir"`
	SliceContract *TaskSliceContract `json:"sliceContract,omitempty"`
}

type sshFleetRunResponse struct {
	OK         bool       `json:"ok"`
	Status     TaskStatus `json:"status,omitempty"`
	ResultText string     `json:"resultText,omitempty"`
	Output     string     `json:"output,omitempty"`
	BaseCommit string     `json:"baseCommit,omitempty"`
	Patch      string     `json:"patch,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func sshFleetDeviceID(name string) string {
	return sshFleetDevicePrefix + strings.ToLower(strings.TrimSpace(name))
}

func sshFleetTargetName(deviceID string) (string, bool) {
	deviceID = strings.TrimSpace(deviceID)
	if !strings.HasPrefix(strings.ToLower(deviceID), sshFleetDevicePrefix) {
		return "", false
	}
	name := strings.TrimSpace(deviceID[len(sshFleetDevicePrefix):])
	return name, name != ""
}

func lookupSSHFleetTarget(deviceID string) (*SSHTarget, error) {
	name, ok := sshFleetTargetName(deviceID)
	if !ok {
		return nil, fmt.Errorf("%q is not an SSH worker id", deviceID)
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil, fmt.Errorf("load local SSH worker configuration: %w", err)
	}
	target := lookupSSHTarget(cfg, name)
	if target == nil || !target.FleetWorker {
		return nil, fmt.Errorf("SSH worker %q is not configured; run `yaver ssh add %s user@host --worker --work-dir /absolute/project/path`", name, name)
	}
	copyTarget := *target
	return &copyTarget, nil
}

func sshFleetCommand(ctx context.Context, target *SSHTarget, stdin []byte, subcommand string) ([]byte, []byte, error) {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, nil, fmt.Errorf("ssh is not installed: %w", err)
	}
	argv := sshArgsFor(target, sshPath, []string{"yaver", "__ssh-worker", subcommand})
	sshIndex := 0
	for i, arg := range argv {
		if arg == sshPath {
			sshIndex = i
			break
		}
	}
	options := []string{"-o", "ConnectTimeout=5", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2"}
	if strings.TrimSpace(target.Password) == "" {
		options = append(options, "-o", "BatchMode=yes")
	}
	argv = append(argv[:sshIndex+1], append(options, argv[sshIndex+1:]...)...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = strings.NewReader(string(stdin))
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return []byte(stdout.String()), []byte(stderr.String()), err
}

func probeSSHFleetWorker(ctx context.Context, target SSHTarget) MachineInfo {
	cacheKey := strings.Join([]string{target.Name, target.Host, target.User, target.WorkDir, target.Runner, target.Model}, "\x00")
	if cached, ok := sshFleetProbeCache.Load(cacheKey); ok {
		entry := cached.(sshFleetProbeCacheEntry)
		if time.Since(entry.at) < 15*time.Second {
			return entry.row
		}
	}
	row := MachineInfo{
		DeviceID:          sshFleetDeviceID(target.Name),
		Name:              target.Name,
		Platform:          "SSH worker",
		Provider:          "ssh",
		ConnectionKind:    "ssh",
		FleetWorker:       true,
		YaverAuthRequired: false,
		CurrentWorkDir:    strings.TrimSpace(target.WorkDir),
		PreferredRunner:   firstNonEmpty(normalizeRunnerID(target.Runner), "opencode"),
		PreferredModel:    strings.TrimSpace(target.Model),
	}
	remember := func(result MachineInfo) MachineInfo {
		sshFleetProbeCache.Store(cacheKey, sshFleetProbeCacheEntry{at: time.Now(), row: result})
		return result
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, stderr, err := sshFleetCommand(probeCtx, &target, nil, "probe")
	if err != nil {
		row.StatusReason = "SSH worker probe failed: " + firstNonEmpty(firstErrLine(string(stderr)), err.Error())
		row.Remedy = "Verify `yaver ssh " + target.Name + " -- true`, then install/update yaver-cli on that machine. Yaver account sign-in is not required."
		return remember(row)
	}
	var remote MachineInfo
	if err := json.Unmarshal(out, &remote); err != nil {
		row.StatusReason = "SSH worker returned an invalid capability receipt"
		row.Remedy = "Update yaver-cli on " + target.Name + " and retry the probe."
		return remember(row)
	}
	remote.DeviceID = row.DeviceID
	remote.Name = target.Name
	remote.IsLocal = false
	remote.IsOnline = true
	remote.Provider = "ssh"
	remote.ConnectionKind = "ssh"
	remote.FleetWorker = true
	remote.YaverAuthRequired = false
	remote.CurrentWorkDir = strings.TrimSpace(target.WorkDir)
	remote.PreferredRunner = row.PreferredRunner
	remote.PreferredModel = row.PreferredModel
	return remember(remote)
}

func executeSSHFleetRun(ctx context.Context, target *SSHTarget, req sshFleetRunRequest) (sshFleetRunResponse, error) {
	if strings.TrimSpace(req.WorkDir) == "" {
		req.WorkDir = strings.TrimSpace(target.WorkDir)
	}
	if strings.TrimSpace(req.Runner) == "" {
		req.Runner = firstNonEmpty(normalizeRunnerID(target.Runner), "opencode")
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = strings.TrimSpace(target.Model)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return sshFleetRunResponse{}, err
	}
	out, stderr, err := sshFleetCommand(ctx, target, body, "run")
	var resp sshFleetRunResponse
	decodeErr := json.Unmarshal(out, &resp)
	if err != nil && decodeErr != nil {
		return sshFleetRunResponse{}, fmt.Errorf("SSH worker %s failed: %s", target.Name, firstNonEmpty(firstErrLine(string(stderr)), err.Error()))
	}
	if decodeErr != nil {
		return sshFleetRunResponse{}, fmt.Errorf("SSH worker %s returned invalid JSON: %w", target.Name, decodeErr)
	}
	if !resp.OK {
		return resp, fmt.Errorf("SSH worker %s: %s", target.Name, firstNonEmpty(resp.Error, "task failed"))
	}
	return resp, nil
}

func runSSHFleetWorker(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "__ssh-worker: expected probe or run")
		return 2
	}
	switch args[0] {
	case "probe":
		row := selfMachine(context.Background())
		row.DeviceID = "ssh-worker"
		row.IsLocal = false
		row.ConnectionKind = "ssh"
		row.FleetWorker = true
		row.YaverAuthRequired = false
		return writeSSHFleetJSON(row)
	case "run":
		return runSSHFleetWorkerTask()
	default:
		fmt.Fprintln(os.Stderr, "__ssh-worker: unsupported operation")
		return 2
	}
}

func runSSHFleetWorkerTask() int {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: "read request: " + err.Error()})
	}
	var req sshFleetRunRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: "invalid request JSON"})
	}
	workDir := expandHome(strings.TrimSpace(req.WorkDir))
	if !filepath.IsAbs(workDir) {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: "workDir must be an absolute path on the SSH worker"})
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: "workDir is unavailable on the SSH worker: " + workDir})
	}
	baseCommit := ""
	if req.SliceContract != nil && looksLikeGitRepo(workDir) {
		baseCommit = strings.TrimSpace(req.SliceContract.GitCommit)
		if baseCommit == "" {
			return writeSSHFleetJSON(sshFleetRunResponse{Error: "the controller did not provide a Git base commit; refresh the graph from a Git worktree"})
		}
		if err := requireGitCommit(workDir, baseCommit); err != nil {
			return writeSSHFleetJSON(sshFleetRunResponse{Error: err.Error()})
		}
		isolated, err := ensureGraphNodeWorktree(context.Background(), workDir, req.SliceContract.RunID, req.SliceContract.NodeID, false)
		if err != nil {
			return writeSSHFleetJSON(sshFleetRunResponse{Error: "create isolated SSH worker worktree: " + err.Error()})
		}
		if out, err := exec.Command("git", "-C", isolated, "reset", "--hard", baseCommit).CombinedOutput(); err != nil {
			return writeSSHFleetJSON(sshFleetRunResponse{Error: "align SSH worker to controller base: " + firstNonEmpty(firstErrLine(string(out)), err.Error())})
		}
		workDir = isolated
	}
	runnerID := normalizeRunnerID(firstNonEmpty(req.Runner, "opencode"))
	if !IsSupportedRunner(runnerID) {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: "unsupported runner: " + runnerID})
	}
	runner := GetRunnerConfig(runnerID)
	if err := CheckRunnerReady(runner, workDir); err != nil {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: fmt.Sprintf("runner %s is not ready on this SSH worker: %v", runnerID, err)})
	}
	tm := NewTaskManager(workDir, nil, runner)
	task, err := tm.CreateTaskWithOptions(
		firstNonEmpty(strings.TrimSpace(req.Title), "SSH fleet task"),
		strings.TrimSpace(req.Prompt), req.Model, "ssh-fleet", runnerID, "", nil,
		TaskCreateOptions{
			WorkDir:           workDir,
			InitialUserPrompt: strings.TrimSpace(req.Prompt),
			PromptText:        strings.TrimSpace(req.Prompt),
			SliceContract:     req.SliceContract,
			IncludeYaverMcp:   false,
		},
	)
	if err != nil {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: err.Error()})
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopped)
	select {
	case <-task.doneCh:
	case <-stopped:
		_ = tm.StopTask(task.ID)
		return writeSSHFleetJSON(sshFleetRunResponse{Status: TaskStatusStopped, Error: "SSH worker task was interrupted"})
	}
	final, ok := tm.GetTask(task.ID)
	if !ok {
		return writeSSHFleetJSON(sshFleetRunResponse{Error: "SSH worker task disappeared"})
	}
	resp := sshFleetRunResponse{
		OK:         final.Status == TaskStatusFinished || final.Status == TaskStatusReview,
		Status:     final.Status,
		ResultText: strings.TrimSpace(final.ResultText),
		Output:     tailString(strings.TrimSpace(final.Output), 64<<10),
		BaseCommit: baseCommit,
	}
	if baseCommit != "" {
		patch, err := gitPatchFromBase(workDir, baseCommit, 8<<20)
		if err != nil {
			return writeSSHFleetJSON(sshFleetRunResponse{Status: final.Status, Error: err.Error()})
		}
		resp.Patch = patch
	}
	if !resp.OK {
		resp.Error = firstNonEmpty(resp.ResultText, resp.Output, "runner task failed")
	}
	return writeSSHFleetJSON(resp)
}

func requireGitCommit(repo, commit string) error {
	if err := exec.Command("git", "-C", repo, "cat-file", "-e", commit+"^{commit}").Run(); err == nil {
		return nil
	}
	_ = exec.Command("git", "-C", repo, "fetch", "origin", commit).Run()
	if err := exec.Command("git", "-C", repo, "cat-file", "-e", commit+"^{commit}").Run(); err != nil {
		return fmt.Errorf("controller Git base %s is unavailable on the SSH worker; fetch/synchronize the configured repository and retry", shortGitID(commit))
	}
	return nil
}

func gitPatchFromBase(workDir, baseCommit string, maxBytes int) (string, error) {
	// Intent-to-add makes new, non-ignored files visible to `git diff` without
	// committing or publishing anything from the worker.
	if out, err := exec.Command("git", "-C", workDir, "add", "-N", "--", ".").CombinedOutput(); err != nil {
		return "", fmt.Errorf("inventory SSH worker changes: %s", firstNonEmpty(firstErrLine(string(out)), err.Error()))
	}
	out, err := exec.Command("git", "-C", workDir, "diff", "--binary", "--no-ext-diff", baseCommit, "--").Output()
	if err != nil {
		return "", fmt.Errorf("produce SSH worker patch: %w", err)
	}
	if len(out) > maxBytes {
		return "", fmt.Errorf("SSH worker patch is %d bytes (limit %d); split the task into a smaller slice", len(out), maxBytes)
	}
	return string(out), nil
}

func shortGitID(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func materializeSSHFleetPatch(ctx context.Context, workDir, runID, workspaceGroup, baseCommit, patch string) (string, error) {
	if strings.TrimSpace(patch) == "" {
		return "", nil
	}
	if !looksLikeGitRepo(workDir) {
		return "", fmt.Errorf("cannot materialize SSH worker patch: controller workdir is not a Git repository")
	}
	_, _, controllerCommit := getGitInfo(workDir)
	if strings.TrimSpace(controllerCommit) != strings.TrimSpace(baseCommit) {
		return "", fmt.Errorf("SSH worker patch base %s differs from controller base %s; refresh/rebase before applying", shortGitID(baseCommit), shortGitID(controllerCommit))
	}
	group := firstNonEmpty(strings.TrimSpace(workspaceGroup), "ssh-worker")
	localWorktree, err := ensureGraphNodeWorktree(ctx, workDir, runID, group, true)
	if err != nil {
		return "", fmt.Errorf("prepare controller integration worktree: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", localWorktree, "apply", "--whitespace=nowarn", "-")
	cmd.Stdin = strings.NewReader(patch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("apply SSH worker patch: %s", firstNonEmpty(firstErrLine(string(out)), err.Error()))
	}
	return localWorktree, nil
}

func writeSSHFleetJSON(value any) int {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if resp, ok := value.(sshFleetRunResponse); ok && !resp.OK {
		return 1
	}
	return 0
}

func tailString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[len(value)-maxBytes:]
}
