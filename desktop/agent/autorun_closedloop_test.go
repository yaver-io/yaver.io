package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A closed-loop test for autorun: a REAL git repo with a real origin, real
// signed commits, a real shell gate, and real scope validation. The only faked
// piece is the runner itself — everything autorun does around it runs for real.
//
// The unit tests around this file each pin one decision. This pins that the
// decisions compose: that a two-seat loop actually converts a task file into
// verified, pushed, signed commits and a marked final commit. Every bug this
// loop has had in production — the stranded dirty worktree, the missing final
// commit, the silent degrade to one runner — was a composition bug that passed
// every unit test.

// autorunTestRepo is a real git repo wired to a real bare origin, with SSH
// commit signing configured against a throwaway key. Autorun signs every commit
// (-S) and pushes; a fixture that fakes either would not exercise them.
type autorunTestRepo struct {
	dir    string
	origin string
	t      *testing.T
}

func newAutorunTestRepo(t *testing.T) *autorunTestRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	dir := filepath.Join(root, "work")
	r := &autorunTestRepo{dir: dir, origin: origin, t: t}

	r.run(root, "git", "init", "--bare", "--initial-branch=main", origin)
	r.run(root, "git", "clone", origin, dir)

	key := filepath.Join(root, "signing-key")
	r.run(root, "ssh-keygen", "-t", "ed25519", "-N", "", "-C", "autorun-test", "-f", key)
	for _, cfg := range [][]string{
		{"user.name", "Autorun Test"},
		{"user.email", "autorun@test.invalid"},
		{"gpg.format", "ssh"},
		{"user.signingkey", key},
		{"commit.gpgsign", "false"},
	} {
		r.run(dir, "git", "config", cfg[0], cfg[1])
	}

	r.write("README.md", "# fixture\n")
	r.run(dir, "git", "add", "-A")
	r.run(dir, "git", "commit", "-S", "-m", "initial")
	r.run(dir, "git", "push", "-u", "origin", "main")
	return r
}

func (r *autorunTestRepo) run(dir, name string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	// A developer's real git config (signing keys, hooks, templates) must not
	// leak into the fixture and make the test pass or fail for their reasons.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (r *autorunTestRepo) write(rel, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *autorunTestRepo) log() string {
	return r.run(r.dir, "git", "log", "--oneline")
}

// The whole point, end to end: a task file plus two seats becomes verified,
// signed, pushed commits — and the loop stops on its own.
func TestAutorunClosedLoopMasterPlansDoerImplementsGateVerifies(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	repo := newAutorunTestRepo(t)
	repo.write("tasks/widget.md", "---\nmaster: opencode\ndoer: codex\n---\n\n# Task\n\nCreate src/widget.txt containing the word `widget`.\n")
	repo.run(repo.dir, "git", "add", "-A")
	repo.run(repo.dir, "git", "commit", "-S", "-m", "add task")
	repo.run(repo.dir, "git", "push")

	originalKick := autorunKick
	defer func() { autorunKick = originalKick }()

	var seats []string
	autorunKick = func(_ context.Context, opts autorunOptions, runner RunnerConfig, prompt string, _ time.Duration) autorunCommandResult {
		switch runner.RunnerID {
		case "opencode": // the master seat, as the task file asked
			seats = append(seats, "master:"+runner.RunnerID)
			return autorunCommandResult{Output: "Create src/widget.txt with the single word: widget"}
		case "codex": // the doer seat
			seats = append(seats, "doer:"+runner.RunnerID)
			// A real doer reads its instruction; assert it actually arrived
			// rather than trusting the seat wiring.
			if !strings.Contains(prompt, "Create src/widget.txt") {
				t.Errorf("doer was kicked without the master's instruction: %q", prompt)
			}
			// Implement once, then go quiet so the loop converges — the same
			// shape as a real doer that has finished the task.
			if _, err := os.Stat(filepath.Join(opts.WorkDir, "src", "widget.txt")); os.IsNotExist(err) {
				repo.write("src/widget.txt", "widget\n")
			}
			return autorunCommandResult{Output: "done"}
		}
		t.Fatalf("unexpected runner in the loop: %q", runner.RunnerID)
		return autorunCommandResult{}
	}

	// A real shell gate against the real worktree.
	opts := autorunOptions{
		TaskPath: filepath.Join(repo.dir, "tasks", "widget.md"),
		WorkDir:  repo.dir,
		Gate:     "grep -q widget src/widget.txt",
		Scopes:   []string{"src/**", "docs/**"},
		Interval: 0,
		MaxIters: 4,
		Push:     true,
	}
	summary, err := executeAutorun(context.Background(), opts)
	if err != nil {
		t.Fatalf("closed loop failed: %v (reason %q)", err, summary.FinishReason)
	}

	// It stopped because the doer went quiet, not because it ran out of road.
	if summary.FinishReason != autorunReasonConverged {
		t.Fatalf("finish reason = %q, want %q", summary.FinishReason, autorunReasonConverged)
	}
	// Both seats were used, and the task file — not a flag — chose them.
	if summary.Master != "opencode" || summary.Runner != "codex" {
		t.Fatalf("seats = master:%q doer:%q; the task file's front matter was ignored", summary.Master, summary.Runner)
	}
	if len(seats) < 2 || seats[0] != "master:opencode" || seats[1] != "doer:codex" {
		t.Fatalf("the master must plan before the doer implements: %v", seats)
	}
	if summary.Commits != 1 {
		t.Fatalf("verified commits = %d, want 1", summary.Commits)
	}

	// The work is really in the repo, really committed, and really pushed.
	if body, err := os.ReadFile(filepath.Join(repo.dir, "src", "widget.txt")); err != nil || !strings.Contains(string(body), "widget") {
		t.Fatalf("the doer's work is not in the worktree: %v %q", err, body)
	}
	if status := repo.run(repo.dir, "git", "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("loop left the worktree dirty, which strands the NEXT run: %q", status)
	}
	if log := repo.log(); !strings.Contains(log, "autorun: verified iteration 1") {
		t.Fatalf("no verified iteration commit: %q", log)
	}
	if summary.FinalCommit == "" || !strings.Contains(summary.FinalSubject, autorunFinalCommitMarker) {
		t.Fatalf("run ended without a marked final commit: %+v", summary)
	}
	// Signed for real — `-S` against a real key, not a stubbed git.
	if sig := repo.run(repo.dir, "git", "log", "-1", "--pretty=%GT"); strings.TrimSpace(sig) == "" {
		t.Fatal("final commit carries no signature trailer")
	}
	// --push means the origin has it, not just the local branch.
	if remote := repo.run(repo.dir, "git", "log", "-1", "--oneline", "origin/main"); !strings.Contains(remote, autorunFinalCommitMarker) {
		t.Fatalf("final commit was not pushed to origin: %q", remote)
	}

	// The two seats' conversation is on disk and committed — this is the sync
	// channel, and it is what a human reads afterwards to see what happened.
	progress, err := os.ReadFile(autorunProgressPath(opts.TaskPath, repo.dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MASTER INSTRUCTION", "DOER REPORT", "Create src/widget.txt", "gate passed"} {
		if !strings.Contains(string(progress), want) {
			t.Fatalf("progress handoff missing %q:\n%s", want, progress)
		}
	}
}

// A runner with nothing left to do must END the run, not be kicked until
// --max-iters. This regressed silently for as long as the loop has existed:
// autorun's own no-op note dirtied the worktree, the next iteration read that
// note as the runner's work, gated it, committed it, and reset the counter — so
// two consecutive no-ops never happened. A finished task kept paying for runner
// turns and minted a commit of note-churn every other pass.
func TestAutorunClosedLoopConvergesWhenTheRunnerHasNothingToDo(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	repo := newAutorunTestRepo(t)
	repo.write("tasks/widget.md", "# Task\n\nAlready done.\n")
	repo.run(repo.dir, "git", "add", "-A")
	repo.run(repo.dir, "git", "commit", "-S", "-m", "add task")
	repo.run(repo.dir, "git", "push")

	originalKick := autorunKick
	defer func() { autorunKick = originalKick }()
	kicks := 0
	autorunKick = func(_ context.Context, _ autorunOptions, _ RunnerConfig, _ string, _ time.Duration) autorunCommandResult {
		kicks++
		return autorunCommandResult{Output: "nothing left to do"} // never edits
	}

	opts := autorunOptions{
		TaskPath: filepath.Join(repo.dir, "tasks", "widget.md"),
		WorkDir:  repo.dir,
		Gate:     "true",
		Scopes:   []string{"src/**", "docs/**"},
		MaxIters: 6,
	}
	summary, err := executeAutorun(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if summary.FinishReason != autorunReasonConverged {
		t.Fatalf("finish reason = %q, want %q — an idle runner was kicked to the iteration cap", summary.FinishReason, autorunReasonConverged)
	}
	// Two no-ops is the convergence rule; anything more is tokens burned on a
	// task that was already finished.
	if kicks != 2 {
		t.Fatalf("runner was kicked %d times; convergence should stop it after 2 consecutive no-ops", kicks)
	}
	// A runner that changed nothing must not produce commits. Progress-note
	// churn is not work, and the final commit is recorded by finalizeAutorun.
	if summary.Commits != 0 {
		t.Fatalf("a runner that edited nothing produced %d verified commit(s) of note-churn", summary.Commits)
	}
	if !strings.Contains(repo.log(), autorunFinalCommitMarker) {
		t.Fatalf("converged run did not record its final commit: %q", repo.log())
	}
}

// The gate and the commit must still see the progress note; only the no-op
// decision ignores it. Filtering it everywhere would drop the run's own record.
func TestAutorunRunnerWorkFiltersOnlyTheProgressNote(t *testing.T) {
	progressPath := "/repo/docs/handoff/task-progress.md"
	changes := []string{"docs/handoff/task-progress.md", "src/widget.go"}
	work := autorunRunnerWork(changes, progressPath, "/repo")
	if len(work) != 1 || work[0] != "src/widget.go" {
		t.Fatalf("runner work = %q; want only the runner's own edit", work)
	}
	if got := autorunRunnerWork([]string{"docs/handoff/task-progress.md"}, progressPath, "/repo"); len(got) != 0 {
		t.Fatalf("a lone progress note is not runner work: %q", got)
	}
}

// A doer whose work fails the gate must leave NOTHING behind: no commit, no
// dirty worktree. A dirty worktree is not cosmetic — the next run refuses to
// start on one, which is how this loop stranded itself for hours in production.
func TestAutorunClosedLoopGateFailureLeavesRepoClean(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	repo := newAutorunTestRepo(t)
	repo.write("tasks/widget.md", "# Task\n\nCreate src/widget.txt.\n")
	repo.run(repo.dir, "git", "add", "-A")
	repo.run(repo.dir, "git", "commit", "-S", "-m", "add task")
	repo.run(repo.dir, "git", "push")
	before := strings.TrimSpace(repo.run(repo.dir, "git", "rev-parse", "HEAD"))

	originalKick := autorunKick
	defer func() { autorunKick = originalKick }()
	autorunKick = func(_ context.Context, _ autorunOptions, _ RunnerConfig, _ string, _ time.Duration) autorunCommandResult {
		repo.write("src/widget.txt", "WRONG CONTENT\n") // will fail the gate
		return autorunCommandResult{Output: "done"}
	}

	opts := autorunOptions{
		TaskPath: filepath.Join(repo.dir, "tasks", "widget.md"),
		WorkDir:  repo.dir,
		Gate:     "grep -q widget src/widget.txt",
		Scopes:   []string{"src/**", "docs/**"},
		MaxIters: 1,
	}
	summary, err := executeAutorun(context.Background(), opts)
	if err == nil {
		t.Fatal("a gate failure must be reported, not swallowed")
	}
	if summary.FinishReason != autorunReasonGate {
		t.Fatalf("finish reason = %q, want %q", summary.FinishReason, autorunReasonGate)
	}
	if summary.Commits != 0 {
		t.Fatalf("gate-failed work was committed anyway: %d commit(s)", summary.Commits)
	}
	if status := repo.run(repo.dir, "git", "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatalf("gate failure left the worktree dirty; the next run cannot start: %q", status)
	}
	// The rejected work is recoverable, not destroyed.
	if stash := repo.run(repo.dir, "git", "stash", "list"); !strings.Contains(stash, "autorun") {
		t.Fatalf("the doer's rejected work was not parked in a diagnostic stash: %q", stash)
	}
	// The run still ends with its marked final commit — that is what tells a
	// reader the loop stopped rather than went quiet.
	if summary.FinalCommit == "" {
		t.Fatal("a gate-blocked run must still record its final commit")
	}
	if after := strings.TrimSpace(repo.run(repo.dir, "git", "rev-parse", "HEAD")); after == before {
		t.Fatal("no final commit landed at all")
	}
	if log := repo.log(); strings.Contains(log, "verified iteration") {
		t.Fatalf("gate-failed iteration must not appear as verified: %q", log)
	}
}

// A doer that edits outside --scope is rolled back and the run stops. Scope is
// the operator's blast radius on a machine they are not watching.
func TestAutorunClosedLoopScopeViolationStopsTheRun(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	repo := newAutorunTestRepo(t)
	repo.write("tasks/widget.md", "# Task\n")
	repo.run(repo.dir, "git", "add", "-A")
	repo.run(repo.dir, "git", "commit", "-S", "-m", "add task")
	repo.run(repo.dir, "git", "push")

	originalKick := autorunKick
	defer func() { autorunKick = originalKick }()
	autorunKick = func(_ context.Context, _ autorunOptions, _ RunnerConfig, _ string, _ time.Duration) autorunCommandResult {
		repo.write("src/widget.txt", "widget\n")    // in scope
		repo.write("secrets/prod.env", "TOKEN=1\n") // NOT in scope
		return autorunCommandResult{Output: "done"}
	}

	opts := autorunOptions{
		TaskPath: filepath.Join(repo.dir, "tasks", "widget.md"),
		WorkDir:  repo.dir,
		Gate:     "true",
		Scopes:   []string{"src/**", "docs/**"},
		MaxIters: 1,
	}
	summary, err := executeAutorun(context.Background(), opts)
	if err == nil || summary.FinishReason != autorunReasonScope {
		t.Fatalf("out-of-scope edit was accepted: %v (reason %q)", err, summary.FinishReason)
	}
	if _, statErr := os.Stat(filepath.Join(repo.dir, "secrets", "prod.env")); !os.IsNotExist(statErr) {
		t.Fatal("the out-of-scope file is still in the worktree; the rollback did not happen")
	}
	if summary.Commits != 0 {
		t.Fatalf("out-of-scope work was committed: %d", summary.Commits)
	}
}
