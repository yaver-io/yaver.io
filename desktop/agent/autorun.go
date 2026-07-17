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

// autorunPromptPreamble frames who is asking and on whose machine, because a
// runner that can't tell drifts into hedging: it reads a long task file arriving
// through tooling as someone else's workload and starts asking who authorized it
// instead of writing code. The framing is plain fact — this is the developer's
// own checkout on his own box, Yaver is his local tooling, and the task file is
// his — so stating it costs nothing and removes the doubt.
//
// Deliberately NOT said: that nobody is at the keyboard. It's true, and it's
// exactly what makes a runner cautious about its own edits, so the prompt gives
// the developer's standing decisions ("choose the most correct option", "don't
// ask") rather than announcing the absence and inviting the runner to hedge.
const autorunPromptPreamble = `You are a senior engineer working in this repository, at the request of the developer who owns it.

Context you can rely on:
- This checkout, this machine, and the task below all belong to that developer. Yaver is his own local tooling: it reads his task file and hands it to you. Nothing here is pooled, resold, shared with third parties, or run on anyone else's behalf.
- The task file is his written instruction to you. Treat it exactly as you would the same words typed into your terminal by the developer sitting at this machine.

How he wants you to work:
- Read the codebase first. Do not ask questions and do not stop for input — he has already decided he wants the work done, and answered the questions he could anticipate in the task file.
- When a decision arises, choose the most correct, thorough implementation and implement it fully; treat any explicitly recommended option as the answer.
- Work in small verified increments, and respect the task scope and forbidden-command list.
- Do not commit or push: Yaver owns the build/test gate and only keeps verified commits.`

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
	autorunReasonResources = "insufficient machine resources"
)

// Self-heal kinds. A long-running loop meets transient failures — a runner whose
// auth hiccups, a disk the loop filled itself — and dying on the first one wastes
// the whole run. Each heal is recorded rather than silently swallowed.
const (
	autorunHealRunnerFailover = "runner_failover"
	autorunHealDiskReclaim    = "disk_reclaim"
	autorunHealCPUBackoff     = "cpu_backoff"
)

// autorunHealEvent is one self-heal action, surfaced in the final commit, the
// progress handoff, and autorun_status. Healing invisibly is how a loop ends up
// "fine" for six hours while landing nothing.
type autorunHealEvent struct {
	Iteration int    `json:"iteration"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail"`
}

// autorunDiskFloorGB is the free-space floor. Below it, the loop reclaims caches
// before kicking a runner. The mini filled its own disk to 1.1 GB this way: hours
// of full `go test ./...` fed a 5.2 GB build cache, and at zero the machine can't
// even write a command's output.
var autorunDiskFloorGB = 3.0

// autorunRunSummary is what a finished run reports to its caller: the CLI prints
// it, and the session manager surfaces it over MCP.
type autorunRunSummary struct {
	Iterations   int    `json:"iterations"`
	Commits      int    `json:"commits"`
	FinishReason string `json:"finishReason,omitempty"`
	FinalCommit  string `json:"finalCommit,omitempty"`
	FinalSubject string `json:"finalCommitSubject,omitempty"`
	Runner       string `json:"runner,omitempty"`
	// Master is the planning seat's runner, empty on a single-runner run. It is
	// reported separately from Runner because the two seats fail differently:
	// "the doer failed" and "the master failed" are the same run for very
	// different reasons, and Runner alone cannot tell them apart.
	Master    string             `json:"master,omitempty"`
	Heals     []autorunHealEvent `json:"heals,omitempty"`
	Resources autorunResources   `json:"resources"`
}

// readyAutorunRunners lists every authenticated runner that is ready, skipping
// any already known to have failed this run. Order follows supportedRunnerIDs so
// failover is deterministic.
func readyAutorunRunners(workDir string, exclude map[string]bool) []RunnerConfig {
	var ready []RunnerConfig
	for _, id := range supportedRunnerIDs {
		if exclude[id] {
			continue
		}
		runner := GetRunnerConfig(id)
		if err := CheckRunnerReady(runner, workDir); err == nil {
			ready = append(ready, runner)
		}
	}
	return ready
}

// reclaimAutorunDisk frees the caches a build/test loop generates. It only ever
// touches caches — never the repo, never a path built from an unvalidated
// variable. Returns a human-readable note on what it did.
func reclaimAutorunDisk(ctx context.Context, workDir string) string {
	before, _ := autorunFreeDiskGB(workDir)
	// The go build cache is the dominant consumer for this loop; clearing it
	// costs one slow rebuild and buys back GBs.
	result := autorunExec(ctx, "go", []string{"clean", "-cache"}, workDir)
	after, _ := autorunFreeDiskGB(workDir)
	note := fmt.Sprintf("go clean -cache: %.1f GB free -> %.1f GB free", before, after)
	if result.Err != nil {
		note += fmt.Sprintf(" (go clean reported: %v)", result.Err)
	}
	return note
}

func autorunFreeDiskGB(dir string) (float64, bool) {
	_, free, ok := statfsGB(dir)
	return free, ok
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
	if summary.Master != "" {
		// Name the seats, not just the runners: six months on, "codex" alone
		// does not say whether it planned this work or only typed it.
		fmt.Fprintf(&b, "Runner: %s (doer — implemented each iteration)\n", runnerID)
		fmt.Fprintf(&b, "Master: %s (planned each iteration; did not edit)\n", summary.Master)
	} else {
		fmt.Fprintf(&b, "Runner: %s\n", runnerID)
	}
	fmt.Fprintf(&b, "Gate: %s\n", opts.Gate)
	fmt.Fprintf(&b, "Machine at finish: %s\n", summary.Resources.Summary())
	if len(summary.Heals) > 0 {
		fmt.Fprintf(&b, "\nSelf-healed %d time(s) during this run:\n", len(summary.Heals))
		for _, h := range summary.Heals {
			fmt.Fprintf(&b, "- iteration %d [%s] %s\n", h.Iteration, h.Kind, h.Detail)
		}
	}
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
	// Tmux drives the runner as an interactive TUI in tmux instead of headless
	// `-p`. Forced on for claude, whose `-p` path fails auth even when the TUI
	// on the same box is signed in.
	Tmux bool
	// Goal is an optional completion condition handed to the runner's OWN
	// `/goal` loop. claude re-enters itself after each turn until a judge model
	// agrees the condition holds. Empty = rely on autorun's own termination
	// signals (DONE / converged / --max-iters). claude-family only.
	Goal string
	// Master is an optional SECOND runner that plans each iteration but never
	// edits: it reads the repo and writes the instruction that Runner (the doer)
	// implements. Empty = single-runner, where the doer plans for itself.
	//
	// The split is about token economics. The doer's context grows with the
	// files it edits; the master's stays small because it only ever emits a
	// short instruction. So the expensive subscription can hold whichever seat
	// the operator wants it in.
	//
	// Which runner plays which role is entirely the operator's call — any
	// registry runner can be master, any can be doer, and nothing here treats a
	// particular one as the natural planner. The roles carry the behavior; the
	// runners are interchangeable.
	Master string
}

// autorunSeats is the runner assignment a task file asks for in its front
// matter, so the choice travels with the task instead of living in whatever
// command happened to start it:
//
//	---
//	master: opencode
//	doer: codex
//	---
//
// Both keys are optional; `runner:` is accepted as a synonym for `doer:`. An
// explicit flag or ops argument always wins, since that is the operator speaking
// now versus the file speaking whenever it was written.
//
// This reads front matter, NOT prose. "use codex as the doer" written in a
// paragraph does nothing — recognizing that reliably takes a model, and a regex
// that half-recognizes it would silently run the wrong pairing while looking like
// it understood. The task file's own body reaches the master anyway, which is the
// place where free-form intent already works.
type autorunSeats struct {
	Master string
	Doer   string
}

// autorunSeatsFromTask parses the front-matter seat assignment. Unknown keys and
// malformed headers are ignored: a task file is a document first, and a typo in
// it must not stop a run the operator fully specified on the command line.
func autorunSeatsFromTask(task string) autorunSeats {
	var seats autorunSeats
	lines := strings.Split(task, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return seats
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "master":
			seats.Master = value
		case "doer", "runner":
			seats.Doer = value
		}
	}
	return seats
}

// autorunMarksDone reports whether text carries the DONE marker — a line that is
// nothing but DONE.
//
// This MUST NOT be a substring test. Both prompts tell the runner to "Say DONE,
// alone", and a runner discussing that contract writes the word in prose all the
// time. The progress file is worse than free text: appendAutorunProgress writes
// the doer's own transcript into it every iteration, so a substring test hands
// the runner a loaded gun pointed at the loop.
//
// It fired. A doer wrote "I did not run the full project gate, so this is not
// marked `DONE`" — a sentence whose whole purpose is to deny completion — and the
// next iteration read the substring and ended the run as complete, mid-task,
// after one iteration of a six-part job. The runner said not done; autorun heard
// DONE. Same shape as autorunTurnIsSignInChrome's bug: free text matched loosely.
//
// Markdown decoration is stripped because a runner writing a marker on its own
// line reaches for `DONE`, **DONE**, or "- DONE" without meaning anything by it.
func autorunMarksDone(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "`*_#>-\t ")) == "DONE" {
			return true
		}
	}
	return false
}

// autorunSlotKey is an agent's STABLE address: task + seat. Unlike the session
// ID — a timestamp, unique to one run — this is the same string every time the
// same work runs on the same machine, which is what lets a UI give an agent a
// fixed home instead of a row that moves.
//
// The session ID cannot do this job. `autorun-<UnixNano>` is new on every start,
// so a client keying off it sees a brand-new agent after every restart and has
// nowhere stable to put it. Sorting by StartedAt then moves every agent whenever
// any of them changes — which is precisely the "list that reorders under your
// eyes" that fixed slots exist to kill.
//
// Machine is deliberately NOT part of the key: a daemon only ever reports its
// own sessions, so the machine is implied by who answered. Clients that merge
// several machines' sessions qualify with the deviceId they asked.
//
// The shape mirrors the tmux session name (yaver-autorun-<task>-<runner>), which
// is the same identity from the other end — one agent, one address, both places.
func autorunSlotKey(taskPath, seat string) string {
	seat = normalizeRunnerID(strings.TrimSpace(seat))
	if seat == "" {
		seat = "auto"
	}
	return autorunTaskName(taskPath) + ":" + seat
}

// autorunRunsClaudeBinary reports whether the runner drives the `claude` binary.
// Both "claude" and "glm" do — glm is the same binary pointed at z.ai's
// Anthropic-compatible endpoint — so binary-level features like `/goal` apply to
// both, and to neither codex nor opencode.
func autorunRunsClaudeBinary(runner RunnerConfig) bool {
	switch normalizeRunnerID(runner.RunnerID) {
	case "claude", "glm":
		return true
	}
	return false
}

// autorunUsesTmux reports whether this runner must be driven as a TUI. claude's
// headless `-p` reports "OAuth session expired" while its TUI works, so driving
// it any other way cannot succeed.
func autorunUsesTmux(opts autorunOptions, runner RunnerConfig) bool {
	return opts.Tmux || normalizeRunnerID(runner.RunnerID) == "claude"
}

// autorunKick runs one iteration against a runner, via tmux PTY when that
// runner needs it and headless otherwise. Either path runs subscription-only:
// the metered API keys are stripped from the runner's environment first.
//
// A var, like autorunExec, so tests can drive the loop's seat logic without a
// real runner on the machine.
var autorunKick = func(ctx context.Context, opts autorunOptions, runner RunnerConfig, prompt string, timeout time.Duration) autorunCommandResult {
	if !autorunUsesTmux(opts, runner) {
		return autorunExecRunner(ctx, runner, resolveRunnerBinary(runner.Command), autorunRunnerArgs(runner, prompt), opts.WorkDir)
	}
	if !autorunTmuxAvailable(ctx, opts.WorkDir) {
		return autorunCommandResult{Err: fmt.Errorf("runner %s must be driven as a TUI but tmux is not installed", runner.RunnerID)}
	}
	session := autorunTmuxSessionName(opts.TaskPath, runner.RunnerID)
	created, err := ensureAutorunTmuxSession(ctx, session, runner, opts.WorkDir)
	if err != nil {
		return autorunCommandResult{Err: err}
	}
	// A /goal belongs to the SESSION, not to a turn: claude keeps re-entering
	// itself until the condition holds, so re-arming it every iteration would
	// be noise. Send it only on the turn that created the TUI — which also
	// covers the self-heal recreate above, since a fresh session has no goal.
	if created {
		if res := autorunTmuxSetGoal(ctx, session, opts.Goal, runner, opts.WorkDir); res.Err != nil {
			return res
		}
	}
	return autorunTmuxKick(ctx, session, prompt, opts.WorkDir, timeout)
}

type autorunCommandResult struct {
	Output string
	Err    error
}

type autorunExecFunc func(context.Context, string, []string, string) autorunCommandResult

var autorunExec autorunExecFunc = runAutorunCommand

func runAutorunCommand(ctx context.Context, name string, args []string, dir string) autorunCommandResult {
	return runAutorunCommandEnv(ctx, name, args, dir, os.Environ())
}

func runAutorunCommandEnv(ctx context.Context, name string, args []string, dir string, env []string) autorunCommandResult {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
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
func rollbackAutorunChanges(ctx context.Context, workDir string, iteration int) (string, error) {
	message := fmt.Sprintf("yaver-autorun-failed-iteration-%d-%d", iteration, time.Now().UTC().Unix())
	result := autorunExec(ctx, "git", []string{"stash", "push", "--include-untracked", "--message", message}, workDir)
	if result.Err != nil {
		return "", fmt.Errorf("preserve failed changes in git stash: %w: %s", result.Err, strings.TrimSpace(result.Output))
	}
	return message, nil
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

// autorunRunnerWork filters autorun's OWN bookkeeping out of a dirty worktree,
// leaving only what the runner actually did. Only the no-op/convergence decision
// uses it — the gate and the commit still take every change, progress note
// included.
//
// Without this the loop cannot converge, and provably did not: autorun appends a
// note to the progress file on a no-op iteration, which leaves the worktree
// dirty, which makes the NEXT iteration read its own note as "the runner did
// work" — so it gates, commits the note, and resets the counter. Two consecutive
// no-ops never happen, `converged` never fires, and a finished task keeps kicking
// a runner until --max-iters, minting a commit of pure note-churn every other
// pass. Measured on a fixture: a runner that edited nothing ran all 6 iterations
// and produced 3 commits.
func autorunRunnerWork(changes []string, progressPath, workDir string) []string {
	progressRel := filepath.ToSlash(mustRel(workDir, progressPath))
	work := make([]string, 0, len(changes))
	for _, change := range changes {
		if filepath.ToSlash(change) != progressRel {
			work = append(work, change)
		}
	}
	return work
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

// autorunMasterPromptPreamble drives the planning seat. It says "do not edit"
// three different ways because a coding runner's whole reflex is to reach for the
// editor, and an edit here is worse than useless: the doer is about to work the
// same tree, so the master's stray edit lands in the doer's diff and gets
// attributed to it. autorunLoop enforces this rather than trusting it — but a
// runner that never edits is cheaper than one rolled back every iteration.
const autorunMasterPromptPreamble = `You are the technical lead for this iteration, working at the request of the developer who owns this repository and this machine. Yaver is his own local tooling: it reads his task file and hands it to you. Nothing here is pooled, resold, or run on anyone else's behalf.

You do NOT write code this iteration. Another engineer — the doer — implements what you decide, in this same worktree, immediately after you answer. Your entire output is the instruction they will follow.

Your job:
- Read the codebase, the task file, the git log, and the progress handoff below. Find the single most valuable next increment.
- Do NOT edit, create, or delete any file. Do NOT run the gate. Do NOT commit. Read-only tools only — leave the worktree exactly as you found it.
- Answer with the instruction itself and nothing else: no preamble, no "here is the plan", no restating the task.

What the instruction must contain:
- ONE increment, small enough to verify in a single pass. Not a roadmap.
- The specific files and functions to change, named by path, since the doer starts cold and has not read what you just read.
- The approach to take, and any decision you already made for them — if a choice arises, make it here rather than leaving it open.
- How they will know it worked (the behavior to check, or the test to add).
- Say DONE, alone, only when the task file's work is fully complete and verified in the git log.`

// autorunMasterContext prompts the planning seat. The doer's report from the last
// iteration reaches it through the progress handoff — that file is the whole sync
// channel between the two seats, which is why both roles write to it.
func autorunMasterContext(task, progress, log string, iteration int) string {
	return fmt.Sprintf("%s\n\nTASK MARKDOWN:\n%s\n\nCURRENT STATE (iteration %d):\nRecent git log:\n%s\n\nProgress handoff (includes what the doer reported last iteration):\n%s", autorunMasterPromptPreamble, task, iteration, log, progress)
}

// autorunDoerContext prompts the implementing seat with the master's instruction.
// The task file rides along because the instruction is deliberately narrow: it
// says what to do now, and the doer still needs the scope and constraints the
// task file sets around it.
func autorunDoerContext(task, progress, log, instruction string, iteration int) string {
	return fmt.Sprintf("%s\n\nYOUR INSTRUCTION FOR THIS ITERATION — from the technical lead on this task, who has just read the codebase. Implement exactly this, and nothing beyond it:\n%s\n\nTASK MARKDOWN (the overall task this increment serves; use it for scope and constraints, not as your instruction):\n%s\n\nCURRENT STATE (iteration %d):\nRecent git log:\n%s\n\nProgress handoff:\n%s",
		autorunPromptPreamble, strings.TrimSpace(instruction), task, iteration, log, progress)
}

// autorunMasterInstruction extracts the instruction from a master turn. A TUI
// capture carries the runner's chrome around the answer, so an empty result means
// the master said nothing usable — the loop treats that as a runner failure
// rather than kicking the doer with an empty instruction.
func autorunMasterInstruction(output string) string {
	return strings.TrimSpace(output)
}

// autorunSignInMarkers are phrases a runner prints when it needs credentials.
// They are matched only against a turn that ALSO carries the runner's TUI
// chrome (see autorunTurnIsSignInChrome) — on their own they are perfectly
// ordinary words for an instruction to use ("show 'Not logged in' when the
// session expires"), and a false positive here would kill a healthy loop.
var autorunSignInMarkers = []string{
	"Not logged in",
	"Run /login",
	"Please run /login",
	"Invalid API key",
	"credit balance is too low",
}

// autorunTUIChromeMarkers are fragments unique to a runner's splash//chrome —
// the Claude Code wordmark and the permission-mode footer. Real prose does not
// contain these glyphs, so they are what distinguishes "this turn is a
// screenshot of a terminal" from "this turn is an answer".
var autorunTUIChromeMarkers = []string{
	"▐▛███▜▌",               // Claude Code wordmark, line 1
	"▝▜█████▛▘",             // wordmark, line 2
	"bypass permissions on", // permission-mode footer
}

// autorunTurnIsSignInChrome reports whether a runner turn is the runner's
// sign-in screen rather than an answer.
//
// Why this exists: a TUI turn is a pane capture, so an unauthenticated runner
// exits 0 and returns its splash screen — non-empty text that looks like output
// but contains no instruction. That sails past an `instruction == ""` guard, and
// the loop then hands the doer a banner as its work order. Observed in the wild
// (2026-07-17): a master with no credentials produced "Claude Code v2.1.211 …
// Not logged in · Run /login … ⏵⏵ bypass permissions on", which was passed to
// the doer verbatim as "YOUR INSTRUCTION FOR THIS ITERATION".
//
// Both halves are required — chrome AND a sign-in phrase — so that an
// instruction merely discussing login stays usable.
func autorunTurnIsSignInChrome(output string) bool {
	hasChrome := false
	for _, m := range autorunTUIChromeMarkers {
		if strings.Contains(output, m) {
			hasChrome = true
			break
		}
	}
	if !hasChrome {
		return false
	}
	for _, m := range autorunSignInMarkers {
		if strings.Contains(output, m) {
			return true
		}
	}
	return false
}

// autorunKickTimeout bounds one runner turn. A TUI turn can be long (a real fix
// with tests), but it must not hang the loop forever.
const autorunKickTimeout = 30 * time.Minute

// autorunTailLines returns the last n lines of a runner's output. A failed
// runner's full transcript can be megabytes; its successor needs the end — where
// the error is — not the whole thing.
func autorunTailLines(output string, n int) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// autorunExecRunner runs a runner with the metered API keys stripped from its
// environment, so a subscription runner cannot silently fall back to per-token
// billing in an unattended loop.
func autorunExecRunner(ctx context.Context, runner RunnerConfig, name string, args []string, dir string) autorunCommandResult {
	env, stripped := sanitizeRunnerEnv(os.Environ(), runner.RunnerID)
	res := runAutorunCommandEnv(ctx, name, args, dir, env)
	if len(stripped) > 0 {
		res.Output = fmt.Sprintf("[yaver] subscription-only: stripped %s from %s's environment\n%s",
			strings.Join(stripped, ", "), runner.RunnerID, res.Output)
	}
	return res
}
