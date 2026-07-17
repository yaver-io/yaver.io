package main

import (
	"context"
	"encoding/json"
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
	runner := fs.String("runner", "auto", "the doer: auto, claude, codex, opencode, or glm")
	master := fs.String("master", "", "optional planning runner: reads the repo and writes each iteration's instruction, never edits. Any runner --runner accepts, and must differ from it.")
	interval := fs.Duration("interval", 5*time.Minute, "delay between kicks")
	maxIters := fs.Int("max-iters", 0, "maximum kicks (0 = until DONE/converged)")
	gate := fs.String("gate", "", "required build/test command")
	push := fs.Bool("push", false, "push gate-verified commits")
	tmux := fs.Bool("tmux", false, "drive the runner as an interactive TUI in tmux (forced on for claude)")
	goal := fs.String("goal", "", "completion condition for the runner's own /goal loop (claude/glm only)")
	machine := fs.String("machine", "", "run the loop on a remote device (deviceId, name, or alias such as primary) instead of this machine")
	var scopes autorunScopes
	fs.Var(&scopes, "scope", "allowed repository glob (repeatable)")
	if err := fs.Parse(args); err != nil {
		return
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "autorun:", err)
		return
	}
	// A remote loop must OUTLIVE this command — the whole point of running it
	// on another box is that closing the laptop does not end it. So --machine
	// does not stream a loop back over the wire; it hands the run to the remote
	// daemon, which owns it exactly as it owns a locally-started one, and
	// returns the session ID to poll with `yaver ops autorun_status`.
	if m := strings.TrimSpace(*machine); m != "" {
		if err := dispatchRemoteAutorun(m, *task, *runner, *master, *gate, *interval, *maxIters, *push, scopes); err != nil {
			fmt.Fprintln(os.Stderr, "autorun:", err)
			os.Exit(1)
		}
		return
	}
	opts := autorunOptions{TaskPath: *task, Runner: *runner, Master: *master, Interval: *interval, MaxIters: *maxIters, Gate: *gate, Push: *push, Scopes: scopes, WorkDir: workDir, Tmux: *tmux, Goal: *goal}
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

// dispatchRemoteAutorun hands an autorun to another device's daemon via the ops
// layer, which already knows how to resolve a machine and forward a verb to it.
// The remote daemon runs the verb it would have run for itself, so the two seats,
// the gate and the scope behave identically there — this is transport, not a
// second implementation of the loop.
//
// The task path is deliberately passed through untouched: it is resolved on the
// REMOTE box, against the remote checkout. Making it absolute here would bake
// this laptop's home directory into a path that machine has never had.
func dispatchRemoteAutorun(machine, task, runner, master, gate string, interval time.Duration, maxIters int, push bool, scopes []string) error {
	if strings.TrimSpace(task) == "" || strings.TrimSpace(gate) == "" {
		return fmt.Errorf("--task and --gate are required")
	}
	if len(scopes) == 0 {
		return fmt.Errorf("--scope is required for a remote run: the loop edits a checkout you are not watching")
	}
	payload := map[string]interface{}{
		"task": task, "runner": runner, "gate": gate,
		"interval": interval.String(), "scopes": scopes, "push": push,
	}
	if strings.TrimSpace(master) != "" {
		payload["master"] = master
	}
	if maxIters > 0 {
		payload["maxIters"] = maxIters
	}
	body, err := json.Marshal(map[string]interface{}{"machine": machine, "verb": "autorun_start", "payload": payload})
	if err != nil {
		return err
	}
	token, err := opsLoadToken()
	if err != nil {
		return err
	}
	res, status := opsLocalRequest(context.Background(), "POST", "/ops", token, body)
	if status >= 400 {
		return fmt.Errorf("ops autorun_start on %s: HTTP %d: %s", machine, status, strings.TrimSpace(string(res)))
	}
	// autorun_start returns the session view AS initial, not wrapped in a
	// session/sessions envelope — autorun_status is the verb that wraps.
	var parsed struct {
		OK      bool               `json:"ok"`
		Code    string             `json:"code"`
		Error   string             `json:"error"`
		Initial autorunSessionView `json:"initial"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return fmt.Errorf("ops autorun_start on %s returned an unreadable body: %s", machine, strings.TrimSpace(string(res)))
	}
	if !parsed.OK {
		return fmt.Errorf("ops autorun_start on %s failed (%s): %s", machine, parsed.Code, parsed.Error)
	}
	if parsed.Initial.ID == "" {
		return fmt.Errorf("ops autorun_start on %s reported success without a session ID; the run cannot be polled or stopped: %s", machine, strings.TrimSpace(string(res)))
	}
	session := parsed.Initial
	fmt.Printf("autorun: started on %s as %s\n", machine, session.ID)
	fmt.Printf("autorun: it now owns this run — closing this terminal will not stop it.\n")
	fmt.Printf("autorun: yaver ops autorun_status --machine=%s --payload='{\"id\":\"%s\"}'\n", machine, session.ID)
	fmt.Printf("autorun: yaver ops autorun_stop   --machine=%s --payload='{\"id\":\"%s\"}'\n", machine, session.ID)
	return nil
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
	// A task file may name its own seats. The caller's explicit choice wins.
	seats := autorunSeatsFromTask(string(taskBytes))
	if strings.TrimSpace(opts.Master) == "" {
		opts.Master = seats.Master
	}
	if r := strings.TrimSpace(opts.Runner); (r == "" || r == "auto") && seats.Doer != "" {
		opts.Runner = seats.Doer
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
	// The master seat is optional and resolved the same way as the doer: any
	// registry runner, validated and readiness-checked identically. Resolving it
	// here means a run with an unauthenticated master fails before the first
	// kick rather than mid-iteration.
	var master RunnerConfig
	if strings.TrimSpace(opts.Master) != "" {
		if master, err = selectAutorunRunner(opts.WorkDir, opts.Master); err != nil {
			return summary, fmt.Errorf("master runner: %w", err)
		}
		if normalizeRunnerID(master.RunnerID) == normalizeRunnerID(runner.RunnerID) {
			return summary, fmt.Errorf("master and doer are both %q; the split exists to put two different runners in the two seats", master.RunnerID)
		}
	}
	if pull := autorunExec(ctx, "git", []string{"pull", "--ff-only"}, opts.WorkDir); pull.Err != nil {
		return summary, fmt.Errorf("git pull --ff-only: %w: %s", pull.Err, strings.TrimSpace(pull.Output))
	}

	reason, runErr := autorunLoop(ctx, opts, runner, master, string(taskBytes), progressPath, &summary)
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
func autorunLoop(ctx context.Context, opts autorunOptions, runner, master RunnerConfig, task, progressPath string, summary *autorunRunSummary) (string, error) {
	taskPath := opts.TaskPath
	noops := 0
	failedRunners := map[string]bool{}
	summary.Runner = runner.RunnerID
	summary.Master = master.RunnerID
	for iteration := 1; opts.MaxIters == 0 || iteration <= opts.MaxIters; iteration++ {
		logResult := autorunExec(ctx, "git", []string{"log", "--oneline", "-10"}, opts.WorkDir)
		progressBytes, _ := os.ReadFile(progressPath)
		if autorunMarksDone(string(progressBytes)) {
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

		// The planning seat, when the operator asked for one. Its instruction
		// becomes the doer's prompt; both sides land in the progress file, which
		// is the only channel between them and is what makes the pairing legible
		// afterwards.
		prompt := autorunContext(task, string(progressBytes), logResult.Output, iteration)
		if master.RunnerID != "" {
			instruction, masterErr := autorunPlan(ctx, opts, master, task, string(progressBytes), logResult.Output, progressPath, iteration)
			if masterErr != nil {
				return autorunReasonRunner, masterErr
			}
			// The length guard was doing all the real work here — the substring
			// test alone ends the run on any instruction that merely mentions the
			// marker. Say what is meant: the master signals completion by
			// answering DONE and nothing else.
			if autorunMarksDone(instruction) && len(strings.TrimSpace(instruction)) < 16 {
				return autorunReasonDone, nil
			}
			prompt = autorunDoerContext(task, string(progressBytes), logResult.Output, instruction, iteration)
		}
		result := autorunKick(ctx, opts, runner, prompt, autorunKickTimeout)
		if master.RunnerID != "" {
			_ = appendAutorunProgress(progressPath, fmt.Sprintf("DOER REPORT (iteration %d, runner `%s`):\n\n```text\n%s\n```", iteration, runner.RunnerID, strings.TrimSpace(autorunTailLines(result.Output, 40))))
		}
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
		if len(autorunRunnerWork(changes, progressPath, opts.WorkDir)) == 0 {
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

// autorunPlan runs the master's planning turn and returns the instruction for the
// doer. It records the instruction in the progress file before the doer runs, so
// a run that dies mid-iteration still shows what was asked for — and so
// autorun_status reads as a conversation between the two seats.
//
// The master is told not to touch the worktree; this verifies it. A master edit
// would otherwise be invisible: the doer works the same tree seconds later, so
// the stray change lands inside the doer's diff, passes or fails the gate on the
// doer's behalf, and gets committed as the doer's work. Rolling it back here is
// what keeps "the master does not edit" true rather than aspirational.
func autorunPlan(ctx context.Context, opts autorunOptions, master RunnerConfig, task, progress, log, progressPath string, iteration int) (string, error) {
	result := autorunKick(ctx, opts, master, autorunMasterContext(task, progress, log, iteration), autorunKickTimeout)
	if dirty, err := autorunGitChanges(ctx, opts.WorkDir); err == nil && len(dirty) > 0 {
		parked, rollbackErr := rollbackAutorunChanges(ctx, opts.WorkDir, iteration)
		if rollbackErr != nil {
			return "", fmt.Errorf("master %s edited the worktree while planning iteration %d (%s) and rollback failed: %w", master.RunnerID, iteration, strings.Join(dirty, ", "), rollbackErr)
		}
		_ = appendAutorunProgress(progressPath, fmt.Sprintf("Iteration %d: master `%s` edited the worktree while planning (`%s`). The planning seat does not implement; its changes were removed and preserved in a diagnostic git stash (`%s`). The doer implements from the instruction below.", iteration, master.RunnerID, strings.Join(dirty, "`, `"), parked))
	}
	if result.Err != nil {
		return "", fmt.Errorf("master %s failed to plan iteration %d: %w: %s", master.RunnerID, iteration, result.Err, strings.TrimSpace(autorunTailLines(result.Output, 40)))
	}
	// A runner with no credentials exits 0 and returns its sign-in splash. That
	// is non-empty, so it would pass the guard below and reach the doer as its
	// instruction. Diagnose it as the auth failure it is.
	if autorunTurnIsSignInChrome(result.Output) {
		return "", fmt.Errorf("master %s is not signed in for iteration %d: its turn returned the runner's sign-in screen, not an instruction — run `yaver primary auth %s`", master.RunnerID, iteration, master.RunnerID)
	}
	instruction := autorunMasterInstruction(result.Output)
	if instruction == "" {
		return "", fmt.Errorf("master %s produced no instruction for iteration %d; refusing to kick the doer with an empty plan", master.RunnerID, iteration)
	}
	_ = appendAutorunProgress(progressPath, fmt.Sprintf("MASTER INSTRUCTION (iteration %d, runner `%s`):\n\n%s", iteration, master.RunnerID, instruction))
	return instruction, nil
}

func mustRel(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
