package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type autorunScopes []string

func (s *autorunScopes) String() string { return strings.Join(*s, ",") }
func (s *autorunScopes) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runAutorun(args []string) {
	fs := flag.NewFlagSet("autorun", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	task := fs.String("task", "", "task Markdown file")
	runner := fs.String("runner", "auto", "auto, claude, codex, opencode, or glm")
	interval := fs.Duration("interval", 5*time.Minute, "delay between kicks")
	maxIters := fs.Int("max-iters", 0, "maximum kicks (0 = until DONE/converged)")
	gate := fs.String("gate", "", "required build/test command")
	push := fs.Bool("push", false, "push gate-verified commits")
	machine := fs.String("machine", "", "remote machine (not available in this increment)")
	var scopes autorunScopes
	fs.Var(&scopes, "scope", "allowed repository glob (repeatable)")
	if err := fs.Parse(args); err != nil {
		return
	}
	if strings.TrimSpace(*machine) != "" {
		fmt.Fprintln(os.Stderr, "autorun: --machine is not available yet; refusing to run locally when remote persistence was requested")
		return
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "autorun:", err)
		return
	}
	opts := autorunOptions{TaskPath: *task, Runner: *runner, Interval: *interval, MaxIters: *maxIters, Gate: *gate, Push: *push, Scopes: scopes, WorkDir: workDir}
	if err := executeAutorun(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "autorun:", err)
	}
}

func executeAutorun(ctx context.Context, opts autorunOptions) error {
	if strings.TrimSpace(opts.TaskPath) == "" || strings.TrimSpace(opts.Gate) == "" {
		return fmt.Errorf("--task and --gate are required")
	}
	if opts.Interval < 0 || opts.MaxIters < 0 {
		return fmt.Errorf("--interval and --max-iters must not be negative")
	}
	if err := validateAutorunShellCommand(opts.Gate); err != nil {
		return fmt.Errorf("unsafe gate: %w", err)
	}
	taskPath, err := filepath.Abs(opts.TaskPath)
	if err != nil {
		return err
	}
	taskBytes, err := os.ReadFile(taskPath)
	if err != nil {
		return fmt.Errorf("read task: %w", err)
	}
	progressPath := autorunProgressPath(taskPath, opts.WorkDir)
	initial, err := autorunGitChanges(ctx, opts.WorkDir)
	if err != nil {
		return err
	}
	if len(initial) > 0 {
		return fmt.Errorf("worktree must be clean before autorun; found: %s", strings.Join(initial, ", "))
	}
	runner, err := selectAutorunRunner(opts.WorkDir, opts.Runner)
	if err != nil {
		return err
	}
	if pull := autorunExec(ctx, "git", []string{"pull", "--ff-only"}, opts.WorkDir); pull.Err != nil {
		return fmt.Errorf("git pull --ff-only: %w: %s", pull.Err, strings.TrimSpace(pull.Output))
	}
	noops := 0
	for iteration := 1; opts.MaxIters == 0 || iteration <= opts.MaxIters; iteration++ {
		logResult := autorunExec(ctx, "git", []string{"log", "--oneline", "-10"}, opts.WorkDir)
		progressBytes, _ := os.ReadFile(progressPath)
		if strings.Contains(string(progressBytes), "DONE") {
			return nil
		}
		prompt := autorunContext(string(taskBytes), string(progressBytes), logResult.Output, iteration)
		result := autorunExec(ctx, resolveRunnerBinary(runner.Command), autorunRunnerArgs(runner, prompt), opts.WorkDir)
		changes, statusErr := autorunGitChanges(ctx, opts.WorkDir)
		if statusErr != nil {
			return statusErr
		}
		if err := validateAutorunScope(changes, opts.Scopes, progressPath, opts.WorkDir); err != nil {
			if rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration); rollbackErr != nil {
				return fmt.Errorf("iteration %d violated scope (%v) and rollback failed: %w", iteration, err, rollbackErr)
			}
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: SCOPE FAILED. Runner changes were removed from the worktree and preserved in a diagnostic git stash.\n\n%s", iteration, err))
			return fmt.Errorf("iteration %d violated scope; changes were preserved in a diagnostic git stash: %w", iteration, err)
		}
		if result.Err != nil {
			if len(changes) > 0 {
				if rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration); rollbackErr != nil {
					return fmt.Errorf("runner %s failed in iteration %d and rollback failed: %w", runner.RunnerID, iteration, rollbackErr)
				}
			}
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: runner `%s` failed. Any changes were removed from the worktree and preserved in a diagnostic git stash.\n\n```text\n%s\n```", iteration, runner.RunnerID, strings.TrimSpace(result.Output)))
			return fmt.Errorf("runner %s failed in iteration %d: %w", runner.RunnerID, iteration, result.Err)
		}
		if len(changes) == 0 {
			noops++
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: runner `%s` made no changes (%d consecutive no-op).", iteration, runner.RunnerID, noops))
			if noops >= 2 {
				return nil
			}
		} else {
			noops = 0
			gateResult := autorunExec(ctx, "sh", []string{"-lc", opts.Gate}, opts.WorkDir)
			if gateResult.Err != nil {
				if rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration); rollbackErr != nil {
					return fmt.Errorf("gate failed and rollback failed: %w", rollbackErr)
				}
				_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: GATE FAILED (`%s`). Changes were removed from the worktree and preserved in a diagnostic git stash.\n\n```text\n%s\n```", iteration, opts.Gate, strings.TrimSpace(gateResult.Output)))
				return fmt.Errorf("gate failed; changes were not committed and were preserved in a diagnostic git stash: %w", gateResult.Err)
			}
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: gate passed (`%s`) with runner `%s`.\n\nChanged: `%s`", iteration, opts.Gate, runner.RunnerID, strings.Join(changes, "`, `")))
			if add := autorunExec(ctx, "git", append([]string{"add", "--"}, append(changes, filepath.ToSlash(mustRel(opts.WorkDir, progressPath)))...), opts.WorkDir); add.Err != nil {
				return fmt.Errorf("git add: %w: %s", add.Err, strings.TrimSpace(add.Output))
			}
			message := fmt.Sprintf("autorun: verified iteration %d for %s", iteration, strings.TrimSuffix(filepath.Base(taskPath), filepath.Ext(taskPath)))
			if commit := autorunExec(ctx, "git", []string{"commit", "-S", "-m", message}, opts.WorkDir); commit.Err != nil {
				return fmt.Errorf("signed commit: %w: %s", commit.Err, strings.TrimSpace(commit.Output))
			}
			if opts.Push {
				if pushResult := autorunExec(ctx, "git", []string{"push"}, opts.WorkDir); pushResult.Err != nil {
					return fmt.Errorf("push: %w: %s", pushResult.Err, strings.TrimSpace(pushResult.Output))
				}
			}
		}
		if opts.Interval > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.Interval):
			}
		}
	}
	return nil
}

func mustRel(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
