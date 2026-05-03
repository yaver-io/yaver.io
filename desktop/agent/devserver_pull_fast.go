package main

// devserver_pull_fast.go — synchronous fast-path for the pre-build
// `git pull` decision.
//
// Today (devserver_pull.go) every build delegates the pull decision to
// a coding agent (Claude / Codex / OpenCode) with a 30 s timeout. For a
// 5–10 s mobile dev loop that's an absurd tax — and it requires an
// authenticated runner to do anything at all. The fast path here makes
// the decision in ~50 ms using `git rev-parse` + `git status` + a
// small rules table. Falls back to the existing agent delegation only
// when the state is genuinely ambiguous (merge in progress, mid-rebase,
// etc.).
//
// Mode-aware behavior (drives the autodev/loop/vibe loop):
//
//   YAVER_AUTOPUBLISH=1 + dirty tree + active coding agent
//     → commit working changes, `git pull --rebase`, `git push`.
//       Agent's edits land on the remote without manual intervention.
//
//   no autopublish + dirty tree + active coding agent
//     → `git pull --rebase --autostash`. Agent's edits stay on top of
//       remote work, but we don't publish — user does the push.
//
//   no autopublish + dirty tree + no agent
//     → skip. Preserve whatever the user is editing by hand.
//
//   clean + behind upstream
//     → `git pull --ff-only`. Always safe.
//
//   clean + up to date OR ahead
//     → skip.
//
//   merge/rebase in progress, no upstream, detached HEAD, etc.
//     → delegate to the coding agent (existing flow).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// preBuildPullAction is the verdict from decidePullBeforeBuild.
type preBuildPullAction string

const (
	pullActionSkip            preBuildPullAction = "skip"
	pullActionFFOnly          preBuildPullAction = "ff-only"
	pullActionRebaseAutostash preBuildPullAction = "rebase-autostash"
	pullActionRebasePublish   preBuildPullAction = "rebase-publish"
	pullActionDelegate        preBuildPullAction = "delegate"
)

type preBuildPullDecision struct {
	Action preBuildPullAction
	Reason string
}

// decidePullBeforeBuild walks the rules table and returns one of the
// five actions. Pure function over (workDir state, hasActiveAgent,
// autoPublish) — easy to test, easy to reason about.
func decidePullBeforeBuild(workDir string, hasActiveAgent, autoPublish bool) preBuildPullDecision {
	if out, err := runGit(workDir, "rev-parse", "--is-inside-work-tree"); err != nil ||
		strings.TrimSpace(out) != "true" {
		return preBuildPullDecision{pullActionSkip, "not a git worktree"}
	}
	if _, err := runGit(workDir, "rev-parse", "--abbrev-ref", "@{upstream}"); err != nil {
		return preBuildPullDecision{pullActionSkip, "no upstream tracking branch"}
	}
	// Detached HEAD has no branch context to push/rebase against.
	if branch, _ := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(branch) == "HEAD" {
		return preBuildPullDecision{pullActionSkip, "detached HEAD"}
	}
	if isMergeInProgress(workDir) || isRebaseInProgress(workDir) {
		return preBuildPullDecision{pullActionDelegate, "merge or rebase in progress; needs human/agent decision"}
	}

	porcelain, _ := runGit(workDir, "status", "--porcelain")
	isClean := strings.TrimSpace(porcelain) == ""

	ahead, behind, err := countAheadBehind(workDir)
	if err != nil {
		return preBuildPullDecision{pullActionDelegate, fmt.Sprintf("ahead/behind compare failed: %v", err)}
	}

	if isClean {
		// Order matters: behind takes priority (always safe to ff),
		// then ahead (skip — never auto-pull when local has work the
		// remote doesn't), then in-sync.
		if behind > 0 {
			return preBuildPullDecision{pullActionFFOnly,
				fmt.Sprintf("clean, %d commits behind upstream — fast-forward safe", behind)}
		}
		if ahead > 0 {
			return preBuildPullDecision{pullActionSkip,
				fmt.Sprintf("clean but %d commits ahead of upstream — not auto-pulling", ahead)}
		}
		return preBuildPullDecision{pullActionSkip, "clean tree, up to date with upstream"}
	}

	// Dirty tree below.
	if hasActiveAgent && autoPublish {
		return preBuildPullDecision{pullActionRebasePublish,
			"dirty tree + active coding agent + YAVER_AUTOPUBLISH=1 — committing checkpoint, rebasing, pushing"}
	}
	if hasActiveAgent {
		return preBuildPullDecision{pullActionRebaseAutostash,
			"dirty tree + active coding agent — rebasing with autostash to preserve agent edits"}
	}
	return preBuildPullDecision{pullActionSkip,
		"dirty tree + no active coding agent — skipping pull to preserve local edits"}
}

// executePullDecision performs the action returned by decidePullBeforeBuild
// and returns a one-line summary plus error. Caller is expected to log
// + emit the summary on the dev-server progress stream.
func executePullDecision(workDir string, d preBuildPullDecision) (string, error) {
	switch d.Action {
	case pullActionSkip, pullActionDelegate:
		return d.Reason, nil
	case pullActionFFOnly:
		out, err := runGit(workDir, "pull", "--ff-only")
		if err != nil {
			return fmt.Sprintf("git pull --ff-only failed: %s", strings.TrimSpace(out)), err
		}
		return "git pull --ff-only succeeded", nil
	case pullActionRebaseAutostash:
		out, err := runGit(workDir, "pull", "--rebase", "--autostash")
		if err != nil {
			return fmt.Sprintf("git pull --rebase --autostash failed: %s", strings.TrimSpace(out)), err
		}
		return "git pull --rebase --autostash succeeded", nil
	case pullActionRebasePublish:
		// 1. Commit current working changes as a checkpoint.
		//    `git add -A` includes new untracked files; agent's edits
		//    sometimes create files we want under version control.
		if out, err := runGit(workDir, "add", "-A"); err != nil {
			return fmt.Sprintf("git add -A failed: %s", strings.TrimSpace(out)), err
		}
		commitMsg := fmt.Sprintf("autodev checkpoint %s", time.Now().UTC().Format(time.RFC3339))
		if out, err := runGit(workDir, "commit", "-m", commitMsg); err != nil {
			// "nothing to commit" is a benign race — `git status` saw
			// dirt, then something else cleaned it. Keep going to the
			// rebase + push.
			if !strings.Contains(strings.ToLower(out), "nothing to commit") {
				return fmt.Sprintf("git commit failed: %s", strings.TrimSpace(out)), err
			}
		}
		// 2. Rebase onto upstream.
		if out, err := runGit(workDir, "pull", "--rebase"); err != nil {
			return fmt.Sprintf("git pull --rebase failed: %s", strings.TrimSpace(out)), err
		}
		// 3. Publish.
		if out, err := runGit(workDir, "push"); err != nil {
			return fmt.Sprintf("git push failed: %s", strings.TrimSpace(out)), err
		}
		return "checkpoint committed, rebased, pushed", nil
	}
	return "", nil
}

// pullAutoPublishEnabled is the env-flag gate for the rebase + push
// branch. Default OFF. Enable per-session with YAVER_AUTOPUBLISH=1
// (typically set by `yaver autodev` / `yaver loop` / `yaver vibe` when
// they spawn the agent process). Don't set this in interactive
// sessions — `yaver code` users expect to control commits + pushes
// themselves.
func pullAutoPublishEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_AUTOPUBLISH"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// hasActiveCodingAgent returns true when a runner with auth is present
// — same predicate the existing agent-delegation path uses, just split
// out so the fast path can call it without the task-creation side
// effects.
func hasActiveCodingAgent(s *HTTPServer, workDir string) bool {
	if s == nil {
		return false
	}
	defaultRunnerID := ""
	if s.taskMgr != nil && s.taskMgr.runner.RunnerID != "" {
		defaultRunnerID = s.taskMgr.runner.RunnerID
	}
	rows := collectHotReloadPullRunnerRows(workDir)
	return chooseHotReloadPullRunner(defaultRunnerID, rows) != ""
}

// countAheadBehind returns how many commits HEAD is ahead of @{upstream}
// and how many it's behind. Both zero means in-sync. err non-nil means
// the comparison itself failed (no upstream, shallow clone, etc.) and
// the caller should default to "delegate / skip".
func countAheadBehind(workDir string) (ahead, behind int, err error) {
	out, err := runGit(workDir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) >= 2 {
		ahead, _ = strconv.Atoi(parts[0])
		behind, _ = strconv.Atoi(parts[1])
	}
	return ahead, behind, nil
}

func isMergeInProgress(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".git", "MERGE_HEAD"))
	return err == nil
}

func isRebaseInProgress(workDir string) bool {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(workDir, ".git", name)); err == nil {
			return true
		}
	}
	return false
}
