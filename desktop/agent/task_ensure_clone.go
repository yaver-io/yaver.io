package main

// task_ensure_clone.go — the runner-box half of the runner/render machine
// split (docs/architecture/RUNNER_RENDER_SPLIT.md, P3c).
//
// When the user's machine-roles config makes THIS box the AI runner for a
// project whose source lives elsewhere (workspace=runner-clone), the creating
// surface passes the project's git identity (gitRemote/gitBranch) with the
// task. If the task's workDir does not exist here, we materialize this box's
// own clone BEFORE spawning the runner — asynchronously, streamed into the
// task output, so the surface gets its taskId immediately and the user
// watches the clone narrate itself instead of staring at a spinner.
//
// After a COMPLETED task, the autoPush policy from the same config decides
// how the runner's commits converge back through git (the split's spine —
// the render box pre-build-pulls on its next build, devserver_pull.go):
//   "always" — commit anything dirty, then push.
//   "ask"    — commit, emit a push_pending event + a line naming the exact
//              command; the surface offers the push. (Additive: surfaces
//              that don't know the event just show the line.)
//   "never"  — commit only; the line says how to sync manually.
//
// Both paths are owner-only because task creation itself accepts only the
// machine owner's authenticated requests.

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	taskCloneTimeout    = 15 * time.Minute
	taskPushTimeout     = 3 * time.Minute
	taskGitProbeTimeout = 30 * time.Second
)

type taskClonePlan struct {
	Dest   string
	Remote string
	Branch string
}

// validGitRemote accepts the transports a surface legitimately hands us and
// nothing that could smuggle a flag or a local path into `git clone`.
func validGitRemote(remote string) bool {
	r := strings.TrimSpace(remote)
	if r == "" || strings.HasPrefix(r, "-") {
		return false
	}
	if strings.HasPrefix(r, "https://") || strings.HasPrefix(r, "ssh://") || strings.HasPrefix(r, "git://") {
		return true
	}
	// scp-like: git@host:path — require user@host: with no spaces.
	if i := strings.Index(r, "@"); i > 0 && strings.Contains(r[i:], ":") && !strings.ContainsAny(r, " \t") {
		return true
	}
	return false
}

// sanitizeRemoteForLog strips any userinfo/token from a remote before it
// reaches a log line. (Remotes never go to Convex; this is for local logs.)
func sanitizeRemoteForLog(remote string) string {
	r := strings.TrimSpace(remote)
	if i := strings.Index(r, "://"); i >= 0 {
		rest := r[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			return r[:i+3] + "…@" + rest[at+1:]
		}
	}
	return r
}

// emitTaskLine appends one narration line to the task transcript and the live
// stream (non-blocking, mirrors the PendingFollowUps queued-note pattern).
func (tm *TaskManager) emitTaskLine(task *Task, line string) {
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	tm.mu.Lock()
	task.Output += line
	if task.outputCh != nil {
		select {
		case task.outputCh <- line:
		default:
		}
	}
	tm.persistAsync()
	tm.mu.Unlock()
}

// pullBeforeSpawn fast-forwards an EXISTING runner clone before the runner
// starts — the other half of the git spine: commits pushed from the render
// box (or anywhere) must be present before the task begins, exactly as the
// render box pre-build-pulls the runner's pushes (devserver_pull.go).
// Bounded, narrated, and non-fatal: a divergence or offline remote emits a
// NAMED line and the task proceeds on the local tree (the runner CLI can
// resolve; blocking the task on advisory sync would put sync in the
// critical path).
func (tm *TaskManager) pullBeforeSpawn(task *Task) {
	dir := tm.effectiveTaskWorkDir(task)
	if strings.TrimSpace(dir) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	probe.WaitDelay = 5 * time.Second
	if err := probe.Run(); err != nil {
		return // not a git tree — nothing to sync
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "pull", "--ff-only")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	summary := strings.TrimSpace(string(out))
	switch {
	case err != nil:
		// Still non-fatal (advisory sync must never gate the task), but the line
		// now NAMES the cause and carries the command that repairs it. A stale
		// remote URL after an org move produced a bare "exit status 128" here
		// and the runner silently edited an unknown-age tree on every task
		// (2026-08-02). See task_git_pull_failure.go.
		tm.emitTaskLine(task, describeGitPullFailure(fmt.Sprintf("%v", err), summary))
	case strings.Contains(summary, "Already up to date"):
		// Quiet: an up-to-date tree needs no narration.
	default:
		tm.emitTaskLine(task, "[yaver] pre-task git pull: "+taskGitFirstLine(summary))
	}
}

// gitProgressPhase parses "Counting objects:  43% (39/91)"-style progress
// lines into (phase, percent). ("", 0) for non-progress lines.
func gitProgressPhase(line string) (string, int) {
	i := strings.Index(line, ":")
	if i <= 0 {
		return "", 0
	}
	rest := line[i+1:]
	p := strings.Index(rest, "%")
	if p < 0 {
		return "", 0
	}
	pctStr := strings.TrimSpace(rest[:p])
	pct := 0
	for _, ch := range pctStr {
		if ch < '0' || ch > '9' {
			return "", 0
		}
		pct = pct*10 + int(ch-'0')
	}
	return strings.TrimSpace(line[:i]), pct
}

func taskGitFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// clonePlanForTask decides whether this task needs a pre-spawn clone.
// nil = spawn normally (the overwhelmingly common case).
func (tm *TaskManager) clonePlanForTask(task *Task) *taskClonePlan {
	if task == nil {
		return nil
	}
	remote := strings.TrimSpace(task.GitRemote)
	if remote == "" {
		return nil
	}
	dest := strings.TrimSpace(task.WorkDir)
	if dest == "" {
		// No explicit workDir: derive the Yaver-managed repositories parent at
		// runtime. An explicit WorkDir still preserves any user hierarchy.
		base := strings.TrimSuffix(filepath.Base(strings.TrimSuffix(remote, "/")), ".git")
		if base == "" || base == "." || base == string(filepath.Separator) {
			return nil
		}
		dest = filepath.Join(ResolveRepositoryParent(""), base)
		task.WorkDir = dest
	}
	if st, err := os.Stat(dest); err == nil && st.IsDir() {
		return nil // already materialized — the normal spawn path handles it
	}
	if !validGitRemote(remote) {
		return nil // refuse quietly here; the spawn will fail with the real workDir error
	}
	return &taskClonePlan{Dest: dest, Remote: remote, Branch: strings.TrimSpace(task.GitBranch)}
}

// runCloneThenStart clones the project into place, narrating progress into
// the task stream, then hands the task to the normal spawn path. Runs in its
// own goroutine — the creating HTTP request has already returned the taskId.
func (tm *TaskManager) runCloneThenStart(task *Task, plan *taskClonePlan) {
	started := time.Now()
	tm.emitTaskLine(task, fmt.Sprintf("[yaver] project not on this machine yet — cloning %s%s into %s",
		sanitizeRemoteForLog(plan.Remote),
		map[bool]string{true: " (branch " + plan.Branch + ")", false: ""}[plan.Branch != ""],
		plan.Dest))

	if err := os.MkdirAll(filepath.Dir(plan.Dest), 0o755); err != nil {
		tm.failTaskNamed(task, fmt.Sprintf("Could not prepare the project on this machine: %v", err))
		return
	}

	// Clone into a temp sibling and RENAME on success. Measured on the real
	// fleet 2026-07-27 (task 2141464a): the box died mid-clone and left a
	// partial tree at the destination — which clonePlanForTask would then
	// treat as "already materialized" and hand to the runner as a poisoned
	// workDir. The rename makes landing atomic: dest either doesn't exist
	// or is a complete clone.
	tmpDest := plan.Dest + ".yaver-clone-tmp"
	_ = os.RemoveAll(tmpDest) // stale leftover from a previous interrupted run

	ctx, cancel := context.WithTimeout(context.Background(), taskCloneTimeout)
	defer cancel()
	args := []string{"clone", "--progress"}
	if plan.Branch != "" {
		args = append(args, "--branch", plan.Branch)
	}
	args = append(args, "--", plan.Remote, tmpDest)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 10 * time.Second // a context kill must not hang on a held pipe
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		tm.failTaskNamed(task, fmt.Sprintf("Could not start git clone on this machine: %v — is git installed here?", err))
		return
	}
	// Coalesce progress: git rewrites "Counting objects: 43% ..." per tick,
	// and emitting every tick flooded the transcript with ~one line per
	// percent (measured on the same task). Emit phase changes and 25%
	// buckets only; non-progress lines pass through untouched.
	lastPhase, lastBucket := "", -1
	emitProgress := func(line string) {
		phase, pct := gitProgressPhase(line)
		if phase == "" {
			tm.emitTaskLine(task, "[clone] "+line)
			return
		}
		bucket := pct / 25
		if phase != lastPhase || bucket != lastBucket {
			lastPhase, lastBucket = phase, bucket
			tm.emitTaskLine(task, "[clone] "+line)
		}
	}
	stream := func(r interface{ Read([]byte) (int, error) }) {
		if r == nil {
			return
		}
		sc := bufio.NewScanner(r)
		sc.Split(scanGitProgress)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" {
				emitProgress(line)
			}
		}
	}
	go stream(stdout)
	stream(stderr)
	if err := cmd.Wait(); err != nil {
		_ = os.RemoveAll(tmpDest)
		tm.failTaskNamed(task, fmt.Sprintf(
			"git clone failed after %s: %v. Check this machine's git access to the remote (SSH key / token), or pick a runner machine that already has the source. Remote: %s",
			time.Since(started).Round(time.Second), err, sanitizeRemoteForLog(plan.Remote)))
		return
	}
	if err := os.Rename(tmpDest, plan.Dest); err != nil {
		_ = os.RemoveAll(tmpDest)
		tm.failTaskNamed(task, fmt.Sprintf("Clone finished but could not land at %s: %v", plan.Dest, err))
		return
	}
	tm.emitTaskLine(task, fmt.Sprintf("[yaver] clone complete in %s — starting %s", time.Since(started).Round(time.Second), task.runner.Name))

	if err := tm.startProcess(task); err != nil {
		tm.failTaskNamed(task, fmt.Sprintf("Could not start %s after clone: %v", task.runner.Name, err))
		return
	}
	log.Printf("[task %s] %s started after ensure-clone (%s)", task.ID, task.runner.Name, plan.Dest)
}

// scanGitProgress splits on \n OR \r so `git clone --progress` percentage
// updates (carriage-return rewrites) stream as lines instead of buffering.
func scanGitProgress(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// failTaskNamed marks the task failed with a readable cause on every channel
// a surface reads (mirrors the start-failure block in CreateTaskWithOptions).
func (tm *TaskManager) failTaskNamed(task *Task, msg string) {
	log.Printf("[task %s] %s", task.ID, msg)
	now := time.Now()
	tm.mu.Lock()
	task.Status = TaskStatusFailed
	task.Output += msg + "\n"
	task.ResultText = msg
	task.FinishedAt = &now
	tm.persistAsync()
	tm.mu.Unlock()
	func() {
		defer func() { _ = recover() }()
		if task.outputCh != nil {
			select {
			case task.outputCh <- msg + "\n":
			default:
			}
			close(task.outputCh)
		}
	}()
	tm.fireTaskDone(task)
}

// autoPushAfterTask converges a completed runner-clone task back through git
// per the task's autoPush policy. Called from fireTaskDone on completion.
//
// Note on `git add -A`: this tree is the task's OWN clone on the runner box
// (workspace=runner-clone) — the whole diff is this task's work product, so
// a full add is the correct converge unit here. (The repo-development rule
// about pathspec-only commits protects SHARED interactive checkouts; this
// is not one.)
func (tm *TaskManager) autoPushAfterTask(task *Task) {
	policy := strings.TrimSpace(strings.ToLower(task.AutoPush))
	if policy == "" {
		return
	}
	dir := tm.effectiveTaskWorkDir(task)
	if strings.TrimSpace(dir) == "" {
		return
	}
	git := func(timeout time.Duration, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.WaitDelay = 5 * time.Second
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	if _, err := git(taskGitProbeTimeout, "rev-parse", "--is-inside-work-tree"); err != nil {
		return // not a git tree — nothing to converge
	}
	dirty, _ := git(taskGitProbeTimeout, "status", "--porcelain")
	committed := false
	if dirty != "" {
		commitArgs := []string{}
		// A fresh clone box may have no git identity; commits made by the
		// runner get an honest machine identity rather than failing.
		if email, _ := git(taskGitProbeTimeout, "config", "user.email"); email == "" {
			commitArgs = append(commitArgs, "-c", "user.name=Yaver Runner", "-c", "user.email=runner@yaver.io")
		}
		if _, err := git(taskGitProbeTimeout, append(commitArgs, "add", "-A")...); err != nil {
			tm.emitTaskLine(task, fmt.Sprintf("[yaver] auto-push: git add failed: %v", err))
			return
		}
		msg := fmt.Sprintf("yaver: %s (task %s)", strings.TrimSpace(task.Title), task.ID)
		if out, err := git(taskGitProbeTimeout, append(commitArgs, "commit", "-m", msg)...); err != nil {
			tm.emitTaskLine(task, fmt.Sprintf("[yaver] auto-push: commit failed: %v — %s", err, out))
			return
		}
		committed = true
		// Stamp the commit EVIDENCE onto the task (sha + shortstat +
		// branch) — the proof package (task_proof.go) renders these as
		// facts, separate from whatever the runner claimed. Free here:
		// the commit just happened in this tree.
		sha, _ := git(taskGitProbeTimeout, "rev-parse", "HEAD")
		subject, _ := git(taskGitProbeTimeout, "log", "-1", "--pretty=%s")
		branch, _ := git(taskGitProbeTimeout, "rev-parse", "--abbrev-ref", "HEAD")
		shortstat, _ := git(taskGitProbeTimeout, "diff", "--shortstat", "HEAD~1..HEAD")
		if sha != "" {
			tm.SetTaskCommitEvidence(task.ID, sha, subject, branch, shortstat)
		}
	}

	ahead := "0"
	if out, err := git(taskGitProbeTimeout, "rev-list", "--count", "@{u}..HEAD"); err == nil {
		ahead = out
	} else if committed {
		ahead = "1" // no upstream yet — there is definitely something to push
	}
	if !committed && (ahead == "0" || ahead == "") {
		return // clean and converged — stay quiet
	}

	switch policy {
	case "always":
		out, err := git(taskPushTimeout, "push")
		if err != nil && strings.Contains(out, "no upstream") {
			out, err = git(taskPushTimeout, "push", "-u", "origin", "HEAD")
		}
		if err != nil {
			tm.emitTaskLine(task, fmt.Sprintf(
				"[yaver] auto-push failed: %v — %s. Fix this machine's push access (SSH key / token) or push manually: git -C %s push", err, out, dir))
			return
		}
		tm.emitTaskLine(task, "[yaver] auto-push: changes committed and pushed — the render machine picks them up on its next build.")
	case "ask":
		tm.emitTaskEventForPush(task, dir)
		tm.emitTaskLine(task, fmt.Sprintf(
			"[yaver] changes committed on this machine (%s ahead). Your push policy is ASK — push with: git -C %s push", ahead, dir))
	default: // "never"
		tm.emitTaskLine(task, fmt.Sprintf(
			"[yaver] changes committed on this machine (%s ahead). Push policy is NEVER — sync manually when ready: git -C %s push", ahead, dir))
	}
}

// emitTaskEventForPush sends the structured push_pending event so surfaces
// can render a Push button next to the completed task. Additive — clients
// ignore unknown event types (see the eventCh contract on Task).
func (tm *TaskManager) emitTaskEventForPush(task *Task, dir string) {
	defer func() { _ = recover() }()
	if task.eventCh == nil {
		return
	}
	select {
	case task.eventCh <- map[string]interface{}{
		"type":    "push_pending",
		"taskId":  task.ID,
		"workDir": dir,
	}:
	default:
	}
}
