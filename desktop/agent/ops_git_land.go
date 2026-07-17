package main

// ops_git_land.go — the graceful land, as an ops verb.
//
// `autorunLandOntoMain` (autorun.go) already knows how to put a branch on main
// without losing a race: serialize, fetch, pull --rebase, ff-only merge, push,
// retry. But it is reachable only from inside an autorun loop. Anything else
// driving a box — an agent over MCP, `yaver ops`, a phone — had to open-code
// fetch/rebase/push and re-invent the race handling, or give up and open a PR.
//
// These verbs expose the same discipline to every caller:
//
//   git_land        — put a branch onto the base branch and push, race-safe.
//   git_land_state  — commit/push awareness: what is unlanded, unpushed, or
//                     mid-rebase right now.
//
// They are ops verbs, not MCP tools, so they inherit `machine` routing (land on
// another box), the `ops` grand-tool, and `yaver ops <verb>` for free.
//
// Why this is not just `git_rebase` + `git_merge` + a push verb: the ordering
// and the retry ARE the feature. A caller that rebases, then merges, then races
// a sibling's push and gives up has done the dangerous 90%. Landing is one
// atomic intent and belongs behind one verb.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// opsGitLandMu is this process's landing queue. It mirrors autorunLandMu's
// reasoning: concurrent landers on one machine should QUEUE, not race. Two
// loops pushing main at the same instant both fetch, both rebase, and one loses
// on a non-fast-forward — recoverable, but it burns an attempt and reads as a
// flake. Serializing makes the retry path the exception rather than the norm.
//
// Deliberately separate from autorunLandMu: this lock is held across a network
// push, and an ops caller must not be able to stall an autorun's landing (or
// vice versa) by holding a lock the other side is waiting on. They contend on
// the remote instead, which is what the retry loop is for.
var opsGitLandMu sync.Mutex

const opsGitLandAttempts = 4

type gitLandPayload struct {
	Dir    string `json:"dir,omitempty"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Remote string `json:"remote,omitempty"`
	Push   *bool  `json:"push,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
}

type gitLandStatePayload struct {
	Dir    string `json:"dir,omitempty"`
	Base   string `json:"base,omitempty"`
	Remote string `json:"remote,omitempty"`
}

// resolveLandRemote picks the remote to land against.
//
// autorunLandOntoMain hardcodes "origin", which is a latent break: this repo
// has ONE remote and it is named `github` (branch.main.remote=github). Clones
// that carry an `origin` alias work by luck; a clone without one fails at the
// fetch with a message about a remote the user never configured. Resolve it
// from git's own config the way git itself would, and only then fall back.
func resolveLandRemote(ctx context.Context, dir, explicit, base string) (string, error) {
	if r := strings.TrimSpace(explicit); r != "" {
		if !gitRemoteExists(ctx, dir, r) {
			return "", fmt.Errorf("remote %q is not configured in %s", r, dir)
		}
		return r, nil
	}
	// What git would do for `git push` on that branch.
	if c := runGitVerb(ctx, dir, "config", "--get", "branch."+base+".remote"); c.Err == nil {
		if r := strings.TrimSpace(c.Stdout); r != "" && gitRemoteExists(ctx, dir, r) {
			return r, nil
		}
	}
	for _, cand := range []string{"github", "origin"} {
		if gitRemoteExists(ctx, dir, cand) {
			return cand, nil
		}
	}
	list := runGitVerb(ctx, dir, "remote")
	have := strings.Join(strings.Fields(strings.TrimSpace(list.Stdout)), ", ")
	if have == "" {
		have = "none"
	}
	return "", fmt.Errorf("no landing remote: branch.%s.remote is unset and neither 'github' nor 'origin' exists (configured: %s)", base, have)
}

func gitRemoteExists(ctx context.Context, dir, name string) bool {
	res := runGitVerb(ctx, dir, "remote", "get-url", name)
	return res.Err == nil && strings.TrimSpace(res.Stdout) != ""
}

func gitCurrentBranch(ctx context.Context, dir string) string {
	res := runGitVerb(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if res.Err != nil {
		return ""
	}
	b := strings.TrimSpace(res.Stdout)
	if b == "HEAD" {
		return "" // detached
	}
	return b
}

// gitCountRange returns how many commits are in `from..to`.
func gitCountRange(ctx context.Context, dir, from, to string) int {
	res := runGitVerb(ctx, dir, "rev-list", "--count", from+".."+to)
	if res.Err != nil {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(res.Stdout), "%d", &n); err != nil {
		return 0
	}
	return n
}

func gitConflictedPaths(ctx context.Context, dir string) []string {
	out := []string{}
	c := runGitVerb(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	if c.Err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(c.Stdout), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_land",
		Description: "Put a branch onto the base branch (default main) and push it, race-safe: serialize, fetch, pull --rebase, merge --ff-only, push, and retry when a sibling lands first. This is the ONE verb for 'get my work onto main' — it is not git_rebase + git_merge + push, because the ordering and the retry are the point. Never opens a pull request. A rebase that hits conflicts is aborted so the checkout is never left mid-rebase, and the conflicted paths are reported. Set push:false to merge locally only; dryRun:true reports what would land without touching the remote.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dir":    map[string]interface{}{"type": "string", "description": "Repo dir. Defaults to the agent's project dir."},
				"branch": map[string]interface{}{"type": "string", "description": "Branch to land. Defaults to the current branch; ignored when it already equals base (then this just syncs and pushes base)."},
				"base":   map[string]interface{}{"type": "string", "description": "Branch to land onto. Default: main."},
				"remote": map[string]interface{}{"type": "string", "description": "Remote to land against. Default: branch.<base>.remote, else github, else origin."},
				"push":   map[string]interface{}{"type": "boolean", "description": "Push base after merging. Default true. false = merge locally, leave the remote alone."},
				"dryRun": map[string]interface{}{"type": "boolean", "description": "Report what would land (counts + remote + base) and change nothing."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitLandHandler,
	})

	registerOpsVerb(opsVerbSpec{
		Name:        "git_land_state",
		Description: "Commit/push awareness for a repo: current branch, HEAD, how many commits are unlanded (not yet on base) and unpushed (on local base but not the remote), whether the worktree is dirty, and whether a rebase or merge is in progress. Answers 'did my work actually get out?' without issuing four separate git verbs — an autorun that finished is not the same as an autorun whose commits reached the remote.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dir":    map[string]interface{}{"type": "string"},
				"base":   map[string]interface{}{"type": "string", "description": "Base branch to measure against. Default: main."},
				"remote": map[string]interface{}{"type": "string", "description": "Remote to measure against. Default: branch.<base>.remote, else github, else origin."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitLandStateHandler,
	})
}

// autorunLandingState is the commit/push awareness an autorun view carries.
//
// A run that says "finished" is not a run whose work got out. `commits: 3` and
// a finalCommit are both true of a loop that committed locally and never
// pushed, of one whose land lost every race, and of one that landed cleanly —
// three very different states a surface must not paint identically. This is the
// difference, read off git rather than inferred from the loop's own bookkeeping
// (the loop is exactly the thing that might be wrong).
type autorunLandingState struct {
	Base     string `json:"base"`
	Remote   string `json:"remote,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	Unlanded int    `json:"unlanded"`
	Unpushed int    `json:"unpushed"`
	Clean    bool   `json:"clean"`
	Rebasing bool   `json:"rebasing,omitempty"`
	// Landed is true when nothing is left to land or push — the only honest
	// "your work is on the remote" signal.
	Landed bool   `json:"landed"`
	Error  string `json:"error,omitempty"`
}

// autorunLandingSnapshot reads landing state for an autorun's dir. Best-effort
// and offline: it does NOT fetch. Status is polled by UIs, and a network round
// trip per poll per session would make the fleet hammer the forge; a stale
// remote ref can under-report Unpushed, which is why Error is surfaced rather
// than swallowed and why git_land_state (which does fetch) exists for the
// authoritative answer.
func autorunLandingSnapshot(dir, base string) *autorunLandingState {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if strings.TrimSpace(base) == "" {
		base = "main"
	}
	resolved, err := resolveGitDir(dir)
	if err != nil {
		return &autorunLandingState{Base: base, Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()

	st := &autorunLandingState{
		Base:     base,
		Branch:   gitCurrentBranch(ctx, resolved),
		Unlanded: gitCountRange(ctx, resolved, base, "HEAD"),
		Rebasing: gitDirHasFile(resolved, "rebase-merge") || gitDirHasFile(resolved, "rebase-apply"),
	}
	ws := readWorktreeState(ctx, resolved)
	st.Head, st.Clean = ws.Head, ws.Clean

	if remote, rerr := resolveLandRemote(ctx, resolved, "", base); rerr == nil {
		st.Remote = remote
		st.Unpushed = gitCountRange(ctx, resolved, remote+"/"+base, base)
	} else {
		st.Error = rerr.Error()
	}
	st.Landed = st.Unlanded == 0 && st.Unpushed == 0 && st.Error == ""
	return st
}

func opsGitLandStateHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitLandStatePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	base := strings.TrimSpace(p.Base)
	if base == "" {
		base = "main"
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()

	remote, rerr := resolveLandRemote(ctx, dir, p.Remote, base)
	if rerr != nil {
		return OpsResult{OK: false, Code: "no_remote", Error: rerr.Error()}
	}
	// Measure against the remote as it is NOW, not a stale ref: "unpushed: 0"
	// read off a week-old remote-tracking ref is a lie the caller will act on.
	if f := runGitVerb(ctx, dir, "fetch", remote, base); f.Err != nil {
		return OpsResult{OK: false, Code: "fetch_failed", Error: fmt.Sprintf("git fetch %s %s: %s", remote, base, strings.TrimSpace(f.Stderr))}
	}

	state := readWorktreeState(ctx, dir)
	branch := gitCurrentBranch(ctx, dir)
	remoteBase := remote + "/" + base

	out := map[string]interface{}{
		"branch":     branch,
		"head":       state.Head,
		"base":       base,
		"remote":     remote,
		"clean":      state.Clean,
		"rebasing":   gitDirHasFile(dir, "rebase-merge") || gitDirHasFile(dir, "rebase-apply"),
		"merging":    gitDirHasFile(dir, "MERGE_HEAD"),
		"unlanded":   gitCountRange(ctx, dir, base, "HEAD"),
		"unpushed":   gitCountRange(ctx, dir, remoteBase, base),
		"baseBehind": gitCountRange(ctx, dir, base, remoteBase),
	}
	if c := gitConflictedPaths(ctx, dir); len(c) > 0 {
		out["conflicts"] = c
	}
	return OpsResult{OK: true, Initial: out}
}

// gitDirHasFile reports whether a marker exists under .git/. Cheap, and the
// only reliable way to see "a rebase is half-done" — status output varies by
// git version and locale, the marker files do not.
func gitDirHasFile(dir, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()
	res := runGitVerb(ctx, dir, "rev-parse", "--git-path", name)
	if res.Err != nil {
		return false
	}
	p := strings.TrimSpace(res.Stdout)
	if p == "" {
		return false
	}
	return pathExists(p) || pathExists(dir+"/"+p)
}

func opsGitLandHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitLandPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	base := strings.TrimSpace(p.Base)
	if base == "" {
		base = "main"
	}
	push := true
	if p.Push != nil {
		push = *p.Push
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*gitVerbTimeout)
	defer cancel()

	branch := strings.TrimSpace(p.Branch)
	if branch == "" {
		branch = gitCurrentBranch(ctx, dir)
	}
	if branch == "" {
		return OpsResult{OK: false, Code: "detached_head", Error: "cannot land from a detached HEAD: pass branch, or check out the branch you mean to land"}
	}

	// A dirty tree cannot be rebased. Say so before touching the remote rather
	// than failing halfway with git's own less obvious wording.
	if st := readWorktreeState(ctx, dir); !st.Clean {
		return OpsResult{OK: false, Code: "dirty_worktree", Error: fmt.Sprintf("worktree has uncommitted changes (%d file(s)) — commit them (git_commit with explicit paths) or stash before landing", len(st.Files)), Initial: map[string]interface{}{"status": st}}
	}

	remote, rerr := resolveLandRemote(ctx, dir, p.Remote, base)
	if rerr != nil {
		return OpsResult{OK: false, Code: "no_remote", Error: rerr.Error()}
	}

	if p.DryRun {
		if f := runGitVerb(ctx, dir, "fetch", remote, base); f.Err != nil {
			return OpsResult{OK: false, Code: "fetch_failed", Error: fmt.Sprintf("git fetch %s %s: %s", remote, base, strings.TrimSpace(f.Stderr))}
		}
		// What would actually reach the remote. When branch == base (the common
		// "I committed straight onto main" case) `base..branch` is empty by
		// definition, so counting that would report 0 with work plainly waiting.
		// The honest number is what the remote does not have yet.
		wouldLand := gitCountRange(ctx, dir, base, branch)
		if branch == base {
			wouldLand = gitCountRange(ctx, dir, remote+"/"+base, base)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"dryRun":     true,
			"branch":     branch,
			"base":       base,
			"remote":     remote,
			"wouldLand":  wouldLand,
			"baseBehind": gitCountRange(ctx, dir, base, remote+"/"+base),
			"push":       push,
		}}
	}

	// Local-only: no remote to race for.
	if !push {
		if err := landMergeFFOnly(ctx, dir, base, branch); err != nil {
			return *err
		}
		state := readWorktreeState(ctx, dir)
		return OpsResult{OK: true, Initial: map[string]interface{}{"landed": true, "pushed": false, "branch": branch, "base": base, "head": state.Head, "attempts": 1}}
	}

	opsGitLandMu.Lock()
	defer opsGitLandMu.Unlock()

	var lastErr string
	for attempt := 1; attempt <= opsGitLandAttempts; attempt++ {
		if ctx.Err() != nil {
			return OpsResult{OK: false, Code: "cancelled", Error: "landing cancelled: " + ctx.Err().Error()}
		}
		if f := runGitVerb(ctx, dir, "fetch", remote, base); f.Err != nil {
			return OpsResult{OK: false, Code: "fetch_failed", Error: fmt.Sprintf("git fetch %s %s: %s", remote, base, strings.TrimSpace(f.Stderr))}
		}

		// Take whatever landed while we waited. --rebase, not --ff-only: on a
		// retry our own merge is already on local base, so ff-only would refuse.
		if co := runGitVerb(ctx, dir, "checkout", base); co.Err != nil {
			return OpsResult{OK: false, Code: "checkout_failed", Error: fmt.Sprintf("git checkout %s: %s", base, strings.TrimSpace(co.Stderr))}
		}
		if sync := runGitVerb(ctx, dir, "pull", "--rebase", remote, base); sync.Err != nil {
			// Never leave the clone mid-rebase — that strands it for every
			// future caller, not just this one.
			conflicts := gitConflictedPaths(ctx, dir)
			runGitVerb(ctx, dir, "rebase", "--abort")
			out := map[string]interface{}{"stderr": strings.TrimSpace(sync.Stderr), "aborted": true}
			if len(conflicts) > 0 {
				out["conflicts"] = conflicts
				return OpsResult{OK: false, Code: "rebase_conflicts", Error: fmt.Sprintf("rebase onto %s/%s hit %d conflicted path(s), aborted cleanly: %s", remote, base, len(conflicts), strings.Join(conflicts, ", ")), Initial: out}
			}
			return OpsResult{OK: false, Code: "rebase_failed", Error: fmt.Sprintf("git pull --rebase %s %s: %s", remote, base, strings.TrimSpace(sync.Stderr)), Initial: out}
		}

		if branch != base {
			if err := landMergeFFOnly(ctx, dir, base, branch); err != nil {
				return *err
			}
		}

		pushRes := runGitVerb(ctx, dir, "push", remote, base)
		if pushRes.Err == nil {
			state := readWorktreeState(ctx, dir)
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"landed": true, "pushed": true, "branch": branch, "base": base,
				"remote": remote, "head": state.Head, "attempts": attempt,
			}}
		}
		// Lost the race: someone pushed between our fetch and our push. Loop.
		lastErr = strings.TrimSpace(pushRes.Stderr)
	}
	return OpsResult{OK: false, Code: "land_race_lost", Error: fmt.Sprintf("could not land onto %s after %d attempts — the remote kept moving. Last push error: %s", base, opsGitLandAttempts, lastErr)}
}

// landMergeFFOnly folds branch into base, fast-forward only. Idempotent across
// retries: once merged it reports "Already up to date". A non-ff here means the
// branch genuinely diverged and a human should look — we do NOT create a merge
// commit behind the caller's back.
func landMergeFFOnly(ctx context.Context, dir, base, branch string) *OpsResult {
	if co := runGitVerb(ctx, dir, "checkout", base); co.Err != nil {
		return &OpsResult{OK: false, Code: "checkout_failed", Error: fmt.Sprintf("git checkout %s: %s", base, strings.TrimSpace(co.Stderr))}
	}
	m := runGitVerb(ctx, dir, "merge", "--ff-only", branch)
	if m.Err != nil {
		return &OpsResult{OK: false, Code: "not_fast_forward", Error: fmt.Sprintf("git merge --ff-only %s into %s: %s — the branch diverged from %s; rebase it first (git_rebase onto:%s)", branch, base, strings.TrimSpace(m.Stderr), base, base)}
	}
	return nil
}
