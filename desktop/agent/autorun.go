package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const autorunPromptPreamble = `You are a senior engineer working in this repository. Do not ask questions and do not stop for input. Read the codebase first. When a decision arises, choose the most correct, thorough implementation and implement it fully; treat any explicitly recommended option as the answer. Work in small verified increments, respect the task scope and forbidden-command list, and do not commit or push: Yaver owns the build/test gate and only keeps verified commits.`

// autorunFinalCommitMarker is the phrase the last commit of every autorun run
// carries in BOTH its subject and its body. Autorun's per-iteration commits are
// otherwise indistinguishable from a loop that simply stopped emitting them, so
// this marker is what lets a reader — or autorun_status — tell a run that ended
// from one that is merely quiet.
const autorunFinalCommitMarker = "final autorun commit"

// Why an autorun run ended. Recorded in the final commit, the progress handoff,
// and the MCP session view, so a converged run is never mistaken for one the
// gate blocked or the operator stopped.
const (
	autorunReasonDone      = "task marked DONE"
	autorunReasonConverged = "converged: runner stopped making changes"
	autorunReasonMaxIters  = "reached --max-iters"
	autorunReasonGate      = "gate failed"
	autorunReasonRunner    = "runner failed"
	autorunReasonScope     = "scope violation"
	autorunReasonStopped   = "stopped by operator"
)

// autorunRunSummary is what a finished run reports to its caller: the CLI prints
// it, and the session manager surfaces it over MCP.
type autorunRunSummary struct {
	Iterations   int    `json:"iterations"`
	Commits      int    `json:"commits"`
	FinishReason string `json:"finishReason,omitempty"`
	FinalCommit  string `json:"finalCommit,omitempty"`
	FinalSubject string `json:"finalCommitSubject,omitempty"`
}

func autorunTaskName(taskPath string) string {
	name := strings.TrimSuffix(filepath.Base(taskPath), filepath.Ext(taskPath))
	if strings.TrimSpace(name) == "" {
		return "autorun"
	}
	return name
}

func autorunFinalCommitSubject(taskPath, reason string) string {
	return fmt.Sprintf("autorun: %s for %s (%s)", autorunFinalCommitMarker, autorunTaskName(taskPath), reason)
}

func autorunFinalCommitBody(opts autorunOptions, runnerID string, summary autorunRunSummary, runErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This is the %s for task %s. No further autorun commits will follow for this run.\n\n",
		autorunFinalCommitMarker, autorunTaskName(opts.TaskPath))
	fmt.Fprintf(&b, "Finish reason: %s\n", summary.FinishReason)
	fmt.Fprintf(&b, "Iterations run: %d\n", summary.Iterations)
	fmt.Fprintf(&b, "Verified commits kept: %d\n", summary.Commits)
	fmt.Fprintf(&b, "Runner: %s\n", runnerID)
	fmt.Fprintf(&b, "Gate: %s\n", opts.Gate)
	if runErr != nil {
		fmt.Fprintf(&b, "\nThe run ended on an error. Its code changes were not kept; they are preserved in a diagnostic git stash:\n%s\n", runErr)
	}
	return b.String()
}

type autorunOptions struct {
	TaskPath string
	Runner   string
	Interval time.Duration
	MaxIters int
	Gate     string
	Push     bool
	Scopes   []string
	WorkDir  string
}

type autorunCommandResult struct {
	Output string
	Err    error
}

type autorunExecFunc func(context.Context, string, []string, string) autorunCommandResult

var autorunExec autorunExecFunc = runAutorunCommand

func runAutorunCommand(ctx context.Context, name string, args []string, dir string) autorunCommandResult {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return autorunCommandResult{Output: output.String(), Err: err}
}

func selectAutorunRunner(workDir, requested string) (RunnerConfig, error) {
	requested = normalizeRunnerID(strings.TrimSpace(requested))
	if requested != "" && requested != "auto" {
		if !IsSupportedRunner(requested) {
			return RunnerConfig{}, fmt.Errorf("unsupported runner %q", requested)
		}
		runner := GetRunnerConfig(requested)
		if err := CheckRunnerReady(runner, workDir); err != nil {
			return RunnerConfig{}, fmt.Errorf("runner %s is not ready: %w", requested, err)
		}
		return runner, nil
	}

	var failures []string
	for _, id := range supportedRunnerIDs {
		runner := GetRunnerConfig(id)
		if err := CheckRunnerReady(runner, workDir); err == nil {
			return runner, nil
		} else {
			failures = append(failures, id+": "+err.Error())
		}
	}
	return RunnerConfig{}, fmt.Errorf("no authenticated runner is ready (%s)", strings.Join(failures, "; "))
}

func autorunRunnerArgs(runner RunnerConfig, prompt string) []string {
	args := make([]string, 0, len(runner.Args)+2)
	if strings.TrimSpace(runner.Model) != "" {
		args = append(args, "--model", runner.Model)
	}
	for _, arg := range runner.Args {
		// Autorun is unattended. Its safety boundary is the scope and gate,
		// not a runner sandbox or an approval prompt. The general Codex
		// adapter uses --full-auto, so strengthen it only for this surface.
		if normalizeRunnerID(runner.RunnerID) == "codex" && arg == "--full-auto" {
			args = append(args, "--dangerously-bypass-approvals-and-sandbox")
			continue
		}
		if arg == "{prompt}" {
			args = append(args, prompt)
		} else {
			args = append(args, arg)
		}
	}
	return args
}

// rollbackAutorunChanges returns a failed kick to a clean worktree while
// retaining its complete diff in a named stash for diagnosis. A stash is
// deliberately used instead of deleting untracked files or a hard reset.
func rollbackAutorunChanges(ctx context.Context, workDir string, iteration int) error {
	message := fmt.Sprintf("yaver-autorun-failed-iteration-%d-%d", iteration, time.Now().UTC().Unix())
	result := autorunExec(ctx, "git", []string{"stash", "push", "--include-untracked", "--message", message}, workDir)
	if result.Err != nil {
		return fmt.Errorf("preserve failed changes in git stash: %w: %s", result.Err, strings.TrimSpace(result.Output))
	}
	return nil
}

func validateAutorunShellCommand(command string) error {
	lower := strings.ToLower(command)
	for _, forbidden := range []string{
		"rm -rf", "git clean", "git reset --hard", "git rebase", "git push --force",
		"git push -f", "git tag", "yaver deploy", "npm publish", "fastlane", "xcodebuild -exportarchive",
	} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("command contains forbidden operation %q", forbidden)
		}
	}
	return nil
}

func autorunProgressPath(taskPath, workDir string) string {
	base := strings.TrimSuffix(filepath.Base(taskPath), filepath.Ext(taskPath))
	base = strings.TrimSpace(base)
	if base == "" {
		base = "autorun"
	}
	return filepath.Join(workDir, "docs", "handoff", base+"-progress.md")
}

func autorunGitChanges(ctx context.Context, workDir string) ([]string, error) {
	result := autorunExec(ctx, "git", []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}, workDir)
	if result.Err != nil {
		return nil, fmt.Errorf("git status: %w: %s", result.Err, strings.TrimSpace(result.Output))
	}
	var paths []string
	parts := strings.Split(result.Output, "\x00")
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		path := entry[3:]
		if entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C' {
			i++ // porcelain -z emits the source path as the next field
		}
		paths = append(paths, filepath.ToSlash(path))
	}
	sort.Strings(paths)
	return paths, nil
}

func autorunPathAllowed(path string, scopes []string, progressPath, workDir string) bool {
	relProgress, _ := filepath.Rel(workDir, progressPath)
	if filepath.ToSlash(path) == filepath.ToSlash(relProgress) {
		return true
	}
	for _, scope := range scopes {
		scope = filepath.ToSlash(strings.TrimSpace(scope))
		if scope == "" {
			continue
		}
		if ok, _ := filepath.Match(scope, path); ok {
			return true
		}
		prefix := strings.TrimSuffix(scope, "/**")
		if prefix != scope && (path == prefix || strings.HasPrefix(path, prefix+"/")) {
			return true
		}
	}
	return false
}

func validateAutorunScope(paths, scopes []string, progressPath, workDir string) error {
	if len(scopes) == 0 {
		return errors.New("at least one --scope is required; autorun will not run without an explicit allowlist")
	}
	var outside []string
	for _, path := range paths {
		if !autorunPathAllowed(path, scopes, progressPath, workDir) {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return fmt.Errorf("changes outside autorun scope: %s", strings.Join(outside, ", "))
	}
	return nil
}

func appendAutorunProgress(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if info, statErr := f.Stat(); statErr == nil && info.Size() == 0 {
		if _, err = fmt.Fprintln(f, "# Yaver autorun progress"); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(f, "## %s\n\n%s\n\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(body))
	return err
}

func autorunContext(task, progress, log string, iteration int) string {
	return fmt.Sprintf("%s\n\nTASK MARKDOWN:\n%s\n\nCURRENT STATE (iteration %d):\nRecent git log:\n%s\n\nProgress handoff:\n%s", autorunPromptPreamble, task, iteration, log, progress)
}
