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
	tmux := fs.Bool("tmux", false, "drive the runner as an interactive TUI in tmux (forced on for claude)")
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
	opts := autorunOptions{TaskPath: *task, Runner: *runner, Interval: *interval, MaxIters: *maxIters, Gate: *gate, Push: *push, Scopes: scopes, WorkDir: workDir, Tmux: *tmux}
	summary, err := executeAutorun(context.Background(), opts)
	if summary.FinishReason != "" {
		fmt.Printf("autorun: %s after %d iteration(s), %d verified commit(s)\n", summary.FinishReason, summary.Iterations, summary.Commits)
	}
	if summary.FinalCommit != "" {
		fmt.Printf("autorun: %s %s\n", autorunFinalCommitMarker, summary.FinalCommit)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "autorun:", err)
	}
}

func executeAutorun(ctx context.Context, opts autorunOptions) (autorunRunSummary, error) {
	var summary autorunRunSummary
	if strings.TrimSpace(opts.TaskPath) == "" || strings.TrimSpace(opts.Gate) == "" {
		return summary, fmt.Errorf("--task and --gate are required")
	}
	if opts.Interval < 0 || opts.MaxIters < 0 {
		return summary, fmt.Errorf("--interval and --max-iters must not be negative")
	}
	if err := validateAutorunShellCommand(opts.Gate); err != nil {
		return summary, fmt.Errorf("unsafe gate: %w", err)
	}
	taskPath, err := filepath.Abs(opts.TaskPath)
	if err != nil {
		return summary, err
	}
	opts.TaskPath = taskPath
	taskBytes, err := os.ReadFile(taskPath)
	if err != nil {
		return summary, fmt.Errorf("read task: %w", err)
	}
	progressPath := autorunProgressPath(taskPath, opts.WorkDir)
	initial, err := autorunGitChanges(ctx, opts.WorkDir)
	if err != nil {
		return summary, err
	}
	if len(initial) > 0 {
		return summary, fmt.Errorf("worktree must be clean before autorun; found: %s", strings.Join(initial, ", "))
	}
	runner, err := selectAutorunRunner(opts.WorkDir, opts.Runner)
	if err != nil {
		return summary, err
	}
	if pull := autorunExec(ctx, "git", []string{"pull", "--ff-only"}, opts.WorkDir); pull.Err != nil {
		return summary, fmt.Errorf("git pull --ff-only: %w: %s", pull.Err, strings.TrimSpace(pull.Output))
	}

	reason, runErr := autorunLoop(ctx, opts, runner, string(taskBytes), progressPath, &summary)
	summary.FinishReason = reason

	// A run that found the task already DONE did no work; minting a commit for
	// it would spam history on every re-kick.
	if reason == autorunReasonDone && summary.Iterations == 0 {
		return summary, runErr
	}
	if finalErr := finalizeAutorun(ctx, opts, runner.RunnerID, progressPath, &summary, runErr); finalErr != nil {
		if runErr != nil {
			return summary, fmt.Errorf("%w (recording the final autorun commit also failed: %v)", runErr, finalErr)
		}
		return summary, finalErr
	}
	return summary, runErr
}

// finalizeAutorun closes out a run by committing the terminal progress note as
// the run's explicitly-marked final commit. The note is docs-only, so it is
// committed even when the gate blocked the run's code — otherwise a blocked run
// leaves the note uncommitted and the NEXT run refuses to start on a dirty
// worktree, which is how this loop stranded itself before.
func finalizeAutorun(ctx context.Context, opts autorunOptions, runnerID, progressPath string, summary *autorunRunSummary, runErr error) error {
	// A stopped run's ctx is already cancelled, which would kill every git
	// command below. The final commit is the whole point of stopping cleanly,
	// so it gets a fresh deadline while keeping the request's values.
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
	}
	subject := autorunFinalCommitSubject(opts.TaskPath, summary.FinishReason)
	body := autorunFinalCommitBody(opts, runnerID, *summary, runErr)
	if err := appendAutorunProgress(progressPath, subject+"\n\n"+body); err != nil {
		return fmt.Errorf("write final progress note: %w", err)
	}
	rel := filepath.ToSlash(mustRel(opts.WorkDir, progressPath))
	if add := autorunExec(ctx, "git", []string{"add", "--", rel}, opts.WorkDir); add.Err != nil {
		return fmt.Errorf("git add final progress note: %w: %s", add.Err, strings.TrimSpace(add.Output))
	}
	if commit := autorunExec(ctx, "git", []string{"commit", "-S", "-m", subject, "-m", body}, opts.WorkDir); commit.Err != nil {
		return fmt.Errorf("final signed commit: %w: %s", commit.Err, strings.TrimSpace(commit.Output))
	}
	summary.FinalSubject = subject
	if head := autorunExec(ctx, "git", []string{"rev-parse", "HEAD"}, opts.WorkDir); head.Err == nil {
		summary.FinalCommit = strings.TrimSpace(head.Output)
	}
	if opts.Push {
		if pushResult := autorunExec(ctx, "git", []string{"push"}, opts.WorkDir); pushResult.Err != nil {
			return fmt.Errorf("push final commit: %w: %s", pushResult.Err, strings.TrimSpace(pushResult.Output))
		}
	}
	return nil
}

// autorunLoop runs the kick/gate/commit cycle and reports why it ended. Every
// exit returns a reason so executeAutorun can mark exactly one final commit.
func autorunLoop(ctx context.Context, opts autorunOptions, runner RunnerConfig, task, progressPath string, summary *autorunRunSummary) (string, error) {
	taskPath := opts.TaskPath
	noops := 0
	failedRunners := map[string]bool{}
	summary.Runner = runner.RunnerID
	for iteration := 1; opts.MaxIters == 0 || iteration <= opts.MaxIters; iteration++ {
		logResult := autorunExec(ctx, "git", []string{"log", "--oneline", "-10"}, opts.WorkDir)
		progressBytes, _ := os.ReadFile(progressPath)
		if strings.Contains(string(progressBytes), "DONE") {
			return autorunReasonDone, nil
		}
		summary.Iterations = iteration

		// Measure BEFORE spending. This loop is what exhausts the machine, so
		// it checks disk, RAM and CPU load ahead of every kick rather than
		// discovering exhaustion halfway through a runner turn.
		res := probeAutorunResources(ctx, opts.WorkDir)
		summary.Resources = res

		if res.TotalRAMGB > 0 && res.TotalRAMGB < autorunMinRAMGB {
			return autorunReasonResources, fmt.Errorf("machine has %.1f GB RAM (floor %.1f GB); build/test toolchains will be OOM-killed rather than fail cleanly", res.TotalRAMGB, autorunMinRAMGB)
		}

		// Self-heal: this loop generates the very cache that fills the disk.
		// Reclaim what we generated before asking for more.
		if res.FreeDiskGB < autorunDiskFloorGB {
			note := reclaimAutorunDisk(ctx, opts.WorkDir)
			summary.Heals = append(summary.Heals, autorunHealEvent{Iteration: iteration, Kind: autorunHealDiskReclaim, Detail: note})
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: SELF-HEAL disk below %.1f GB — %s", iteration, autorunDiskFloorGB, note))
			if after, ok := autorunFreeDiskGB(opts.WorkDir); ok && after < autorunDiskFloorGB {
				return autorunReasonResources, fmt.Errorf("only %.1f GB free after reclaiming caches (floor %.1f GB); refusing to kick a runner that cannot finish — %s", after, autorunDiskFloorGB, res.Summary())
			}
			res = probeAutorunResources(ctx, opts.WorkDir)
			summary.Resources = res
		}

		// CPU saturation is advisory: something else compiling is a reason to
		// wait, not to fail the run. Back off one interval and re-measure.
		if res.Saturated() {
			detail := fmt.Sprintf("load %.2f/core exceeds %.1f — waiting one interval before kicking (%s)", res.LoadPerCPU, autorunCPUBackoffPerCore, res.Summary())
			summary.Heals = append(summary.Heals, autorunHealEvent{Iteration: iteration, Kind: autorunHealCPUBackoff, Detail: detail})
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: SELF-HEAL %s", iteration, detail))
			select {
			case <-ctx.Done():
				return autorunReasonStopped, ctx.Err()
			case <-time.After(opts.Interval):
			}
		}

		prompt := autorunContext(task, string(progressBytes), logResult.Output, iteration)
		result := autorunKick(ctx, opts, runner, prompt, autorunKickTimeout)
		changes, statusErr := autorunGitChanges(ctx, opts.WorkDir)
		if statusErr != nil {
			return autorunReasonRunner, statusErr
		}
		if err := validateAutorunScope(changes, opts.Scopes, progressPath, opts.WorkDir); err != nil {
			if _, rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration); rollbackErr != nil {
				return autorunReasonScope, fmt.Errorf("iteration %d violated scope (%v) and rollback failed: %w", iteration, err, rollbackErr)
			}
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: SCOPE FAILED. Runner changes were removed from the worktree and preserved in a diagnostic git stash.\n\n%s", iteration, err))
			return autorunReasonScope, fmt.Errorf("iteration %d violated scope; changes were preserved in a diagnostic git stash: %w", iteration, err)
		}
		if result.Err != nil {
			stashRef := ""
			if len(changes) > 0 {
				parked, rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration)
				if rollbackErr != nil {
					return autorunReasonRunner, fmt.Errorf("runner %s failed in iteration %d and rollback failed: %w", runner.RunnerID, iteration, rollbackErr)
				}
				stashRef = parked
			}
			// Hand the dead runner's context to its successor rather than
			// dropping it: the tail of what it said, and where its half-done
			// work is parked. The next kick's prompt includes this handoff, so
			// the new runner resumes the thread instead of restarting cold.
			handoff := fmt.Sprintf("Iteration %d: runner `%s` failed. Its changes were removed from the worktree.", iteration, runner.RunnerID)
			if stashRef != "" {
				handoff += fmt.Sprintf("\n\nIts work is preserved and RECOVERABLE — to continue from where it stopped:\n```sh\ngit stash apply \"stash^{/%s}\"\n```", stashRef)
			}
			handoff += fmt.Sprintf("\n\nWhat it reported before failing:\n```text\n%s\n```", strings.TrimSpace(autorunTailLines(result.Output, 60)))
			_ = appendAutorunProgress(progressPath, handoff)

			// Self-heal: one runner's bad day must not end the run. A ready
			// runner sat unused for six hours while this loop died on claude's
			// headless-auth failure — fail over instead of giving up.
			failedRunners[runner.RunnerID] = true
			alternates := readyAutorunRunners(opts.WorkDir, failedRunners)
			if len(alternates) == 0 {
				return autorunReasonRunner, fmt.Errorf("runner %s failed in iteration %d and no other runner is ready: %w", runner.RunnerID, iteration, result.Err)
			}
			detail := fmt.Sprintf("runner %s failed (%v); failing over to %s", runner.RunnerID, result.Err, alternates[0].RunnerID)
			summary.Heals = append(summary.Heals, autorunHealEvent{Iteration: iteration, Kind: autorunHealRunnerFailover, Detail: detail})
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: SELF-HEAL %s", iteration, detail))
			runner = alternates[0]
			summary.Runner = runner.RunnerID
			continue
		}
		if len(changes) == 0 {
			noops++
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: runner `%s` made no changes (%d consecutive no-op).", iteration, runner.RunnerID, noops))
			if noops >= 2 {
				return autorunReasonConverged, nil
			}
		} else {
			noops = 0
			gateResult := autorunExec(ctx, "sh", []string{"-lc", opts.Gate}, opts.WorkDir)
			if gateResult.Err != nil {
				if _, rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration); rollbackErr != nil {
					return autorunReasonGate, fmt.Errorf("gate failed and rollback failed: %w", rollbackErr)
				}
				_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: GATE FAILED (`%s`). Changes were removed from the worktree and preserved in a diagnostic git stash.\n\n```text\n%s\n```", iteration, opts.Gate, strings.TrimSpace(gateResult.Output)))
				return autorunReasonGate, fmt.Errorf("gate failed; changes were not committed and were preserved in a diagnostic git stash: %w", gateResult.Err)
			}
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: gate passed (`%s`) with runner `%s`.\n\nChanged: `%s`", iteration, opts.Gate, runner.RunnerID, strings.Join(changes, "`, `")))
			if add := autorunExec(ctx, "git", append([]string{"add", "--"}, append(changes, filepath.ToSlash(mustRel(opts.WorkDir, progressPath)))...), opts.WorkDir); add.Err != nil {
				return autorunReasonGate, fmt.Errorf("git add: %w: %s", add.Err, strings.TrimSpace(add.Output))
			}
			message := fmt.Sprintf("autorun: verified iteration %d for %s", iteration, autorunTaskName(taskPath))
			if commit := autorunExec(ctx, "git", []string{"commit", "-S", "-m", message}, opts.WorkDir); commit.Err != nil {
				return autorunReasonGate, fmt.Errorf("signed commit: %w: %s", commit.Err, strings.TrimSpace(commit.Output))
			}
			summary.Commits++
			if opts.Push {
				if pushResult := autorunExec(ctx, "git", []string{"push"}, opts.WorkDir); pushResult.Err != nil {
					return autorunReasonGate, fmt.Errorf("push: %w: %s", pushResult.Err, strings.TrimSpace(pushResult.Output))
				}
			}
		}
		if opts.Interval > 0 {
			select {
			case <-ctx.Done():
				return autorunReasonStopped, ctx.Err()
			case <-time.After(opts.Interval):
			}
		}
	}
	return autorunReasonMaxIters, nil
}

func mustRel(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
