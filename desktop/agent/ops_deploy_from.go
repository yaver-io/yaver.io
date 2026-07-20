package main

// ops_deploy_from.go — "deploy from the mac mini", as one verb.
//
// WHY THIS EXISTS. The standing deploy path is: commit → push → the deploy box
// pulls → the deploy box deploys. Every step of that was being retyped from
// memory, and on 2026-07-19/20 each retype found a new way to be wrong:
//
//   - deploying from a shared dev checkout picked up a SIBLING SESSION's
//     uncommitted work and failed the build, while main itself was healthy,
//   - the deploy box was never told to pull, so it shipped an old tree,
//   - the box could not fast-forward because the previous deploy had dirtied
//     it (CFBundleVersion, package-lock.json),
//   - and a run that failed halfway still reported "deployed".
//
// A runbook a human re-derives each time is not a path, it is a hazard. This
// verb IS the path: one call, fixed order, and it refuses rather than improvise.
//
// TWO VERBS, on purpose:
//
//   deploy_from_machine — runs where YOU are. Syncs git (commit named paths,
//                         rebase onto origin/main, push), then asks the target
//                         box to deploy. It is the orchestrator because only
//                         the machine holding your work can push it.
//   deploy_run          — runs ON the deploy box. Pulls, then runs
//                         scripts/mini-deploy.sh sequentially. Routed there by
//                         the ops layer's own `machine` parameter.
//
// WHAT IT WILL NOT DO. It never commits a path you did not name. The tree this
// runs in is shared with other sessions, and `git commit -a` there sweeps a
// sibling's half-finished work into your release — that has already happened
// in this repo. Dirty files outside commitPaths abort the run with the list.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// deployCloneDir is the pull-only checkout the deploy box ships from. Never a
// dev tree: see the sibling-session incident in the header.
func deployCloneDir() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_DEPLOY_CLONE")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "yaver-deploy-runner"
	}
	return filepath.Join(home, "Workspace", "yaver-deploy-runner")
}

func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// deploySyncGit performs the caller-side half: commit the named paths, rebase
// onto origin/main, push. Returns a human-readable step log.
//
// Order matters and is not negotiable. Commit before rebase, or the rebase
// refuses on a dirty tree. Rebase before push, or the push is rejected and the
// caller "fixes" it with force — which is how shared history gets destroyed.
func deploySyncGit(ctx context.Context, repo string, commitPaths []string, message string) ([]string, error) {
	var log []string

	// Refuse on unexpected dirt BEFORE touching anything. A deploy that
	// silently includes, or silently drops, a sibling's work is worse than one
	// that does not start.
	status, err := gitOut(ctx, repo, "status", "--porcelain")
	if err != nil {
		return log, fmt.Errorf("git status: %w", err)
	}
	named := map[string]bool{}
	for _, p := range commitPaths {
		named[strings.TrimSpace(p)] = true
	}
	var unexpected []string
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// "XY path" — take everything past the 3-char status prefix.
		path := strings.TrimSpace(line[min(3, len(line)):])
		if !named[path] {
			unexpected = append(unexpected, path)
		}
	}
	if len(unexpected) > 0 && len(commitPaths) > 0 {
		return log, fmt.Errorf(
			"refusing to deploy: %d file(s) are modified but were not named in commitPaths — "+
				"this checkout is shared with other sessions and committing them would sweep work that is not yours: %s",
			len(unexpected), strings.Join(unexpected, ", "))
	}

	if len(commitPaths) > 0 {
		msg := strings.TrimSpace(message)
		if msg == "" {
			return log, fmt.Errorf("commitPaths given without commitMessage — an unexplained commit in the history is worse than no commit")
		}
		// Pathspec commit, never -a. The index is shared and goes stale
		// between two consecutive commands in this repo.
		args := append([]string{"commit", "-m", msg, "--"}, commitPaths...)
		out, err := gitOut(ctx, repo, args...)
		if err != nil && !strings.Contains(out, "nothing to commit") {
			return log, fmt.Errorf("git commit: %s", out)
		}
		log = append(log, "committed: "+strings.Join(commitPaths, ", "))
	}

	remote := "github"
	if out, err := gitOut(ctx, repo, "remote"); err == nil && out != "" && !strings.Contains(out, "github") {
		remote = strings.Fields(out)[0]
	}

	if out, err := gitOut(ctx, repo, "fetch", remote, "main"); err != nil {
		return log, fmt.Errorf("git fetch: %s", out)
	}
	log = append(log, "fetched "+remote+"/main")

	// Rebase only when we have actually diverged. An unconditional rebase
	// rewrites local commits for no reason and turns a clean fast-forward into
	// a conflict surface.
	behind, _ := gitOut(ctx, repo, "rev-list", "--count", "HEAD.."+remote+"/main")
	ahead, _ := gitOut(ctx, repo, "rev-list", "--count", remote+"/main..HEAD")
	switch {
	case behind != "0" && ahead != "0":
		out, err := gitOut(ctx, repo, "pull", "--rebase", remote, "main")
		if err != nil {
			return log, fmt.Errorf("git pull --rebase (diverged by %s/%s): %s — resolve by hand, this verb will not force", ahead, behind, out)
		}
		log = append(log, fmt.Sprintf("rebased %s local commit(s) onto %s/main", ahead, remote))
	case behind != "0":
		if out, err := gitOut(ctx, repo, "merge", "--ff-only", remote+"/main"); err != nil {
			return log, fmt.Errorf("git merge --ff-only: %s", out)
		}
		log = append(log, "fast-forwarded to "+remote+"/main")
	default:
		log = append(log, "already in sync with "+remote+"/main")
	}

	if ahead, _ := gitOut(ctx, repo, "rev-list", "--count", remote+"/main..HEAD"); ahead != "0" {
		if out, err := gitOut(ctx, repo, "push", remote, "HEAD:main"); err != nil {
			return log, fmt.Errorf("git push: %s", out)
		}
		log = append(log, "pushed "+ahead+" commit(s) to "+remote+"/main")
	} else {
		log = append(log, "nothing to push")
	}
	return log, nil
}

// runMiniDeploy executes the sequential deploy script in the deploy clone.
// Deliberately shells out to scripts/mini-deploy.sh rather than reimplementing
// it: that script carries every preflight the box has been taught the hard way
// (both keychains, the python that actually has google-auth, the Podfile a
// clone lacks, the PATH a non-interactive ssh does not get). Two copies of that
// knowledge would drift, and the copy that drifts is the one that false-greens.
func runMiniDeploy(ctx context.Context, targets []string, dryRun bool) (string, error) {
	dir := deployCloneDir()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return "", fmt.Errorf("no deploy clone at %s — create it once with: git clone git@github.com:yaver-io/yaver.io.git %s", dir, dir)
	}
	script := filepath.Join(dir, "scripts", "mini-deploy.sh")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("%s missing — the deploy clone is on a commit older than the script", script)
	}
	args := append([]string{}, targets...)
	if dryRun {
		args = append(args, "--check")
	}
	cmd := exec.CommandContext(ctx, script, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func opsDeployRunHandler(c OpsContext, payload json.RawMessage) OpsResult {
	ctx := c.Ctx
	var p struct {
		Only   []string `json:"only"`
		DryRun bool     `json:"dryRun"`
	}
	_ = json.Unmarshal(payload, &p)

	// A full mobile archive is ~30 minutes; the default ops budget is far
	// shorter, and a deploy killed mid-archive leaves a half-uploaded build.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Minute)
	defer cancel()

	out, err := runMiniDeploy(runCtx, p.Only, p.DryRun)
	tail := out
	if len(tail) > 8000 {
		tail = tail[len(tail)-8000:]
	}
	if err != nil {
		return OpsResult{OK: false, Code: "deploy_failed", Error: err.Error(),
			Initial: map[string]interface{}{"clone": deployCloneDir(), "output": tail}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"clone": deployCloneDir(), "output": tail}}
}

func opsDeployFromMachineHandler(c OpsContext, payload json.RawMessage) OpsResult {
	ctx := c.Ctx
	var p struct {
		Machine       string   `json:"machine"`
		Only          []string `json:"only"`
		DryRun        bool     `json:"dryRun"`
		CommitPaths   []string `json:"commitPaths"`
		CommitMessage string   `json:"commitMessage"`
		SkipSync      bool     `json:"skipSync"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Machine) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "machine is required — name the box that should deploy (deviceId, alias, or 'primary')"}
	}

	steps := []string{}
	if !p.SkipSync {
		repo, err := os.Getwd()
		if err != nil {
			return OpsResult{OK: false, Code: "no_repo", Error: err.Error()}
		}
		if root, err := gitOut(ctx, repo, "rev-parse", "--show-toplevel"); err == nil && root != "" {
			repo = root
		}
		log, err := deploySyncGit(ctx, repo, p.CommitPaths, p.CommitMessage)
		steps = append(steps, log...)
		if err != nil {
			return OpsResult{OK: false, Code: "sync_failed", Error: err.Error(),
				Initial: map[string]interface{}{"steps": steps}}
		}
	} else {
		steps = append(steps, "sync skipped (skipSync=true)")
	}

	// Hand off to the box. It pulls what we just pushed and deploys from a
	// clone nobody edits.
	body := map[string]interface{}{
		"verb":    "deploy_run",
		"payload": map[string]interface{}{"only": p.Only, "dryRun": p.DryRun},
	}
	var remote struct {
		OK      bool                   `json:"ok"`
		Code    string                 `json:"code"`
		Error   string                 `json:"error"`
		Initial map[string]interface{} `json:"initial"`
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 95*time.Minute)
	defer cancel()
	if err := remoteAgentJSONForDevice(callCtx, p.Machine, "POST", "/ops", body, &remote); err != nil {
		return OpsResult{OK: false, Code: "remote_unreachable",
			Error:   fmt.Sprintf("synced git, but could not reach %s to deploy: %v", p.Machine, err),
			Initial: map[string]interface{}{"steps": steps}}
	}
	steps = append(steps, "dispatched deploy_run to "+p.Machine)
	if !remote.OK {
		return OpsResult{OK: false, Code: "deploy_failed",
			Error:   fmt.Sprintf("%s failed to deploy (%s): %s", p.Machine, remote.Code, remote.Error),
			Initial: map[string]interface{}{"steps": steps, "remote": remote.Initial}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"machine": p.Machine, "steps": steps, "remote": remote.Initial,
	}}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "deploy_from_machine",
		Description: "THE deploy path: sync git here, then deploy from a box. " +
			"Commits the paths you name (never others — this checkout is shared), rebases onto origin/main only if diverged, " +
			"pushes, then tells `machine` to pull and run every deploy target sequentially. " +
			"Say 'deploy from the mac mini' and this is the one call. " +
			"Beta/internal channels only — never App Store or Play production. " +
			"dryRun runs the target's preflight (which probes the REAL operation: signs with both keychains, imports google-auth, asks npm whoami) and deploys nothing.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"machine"},
			"properties": map[string]interface{}{
				"machine":       map[string]interface{}{"type": "string", "description": "Box that deploys: deviceId, alias, name, or 'primary'."},
				"only":          map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Subset of targets: convex, web, testflight, playstore. Omit for all ready ones."},
				"dryRun":        map[string]interface{}{"type": "boolean", "description": "Preflight only — deploy nothing."},
				"commitPaths":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Paths to commit before pushing. Anything modified but unnamed aborts the run."},
				"commitMessage": map[string]interface{}{"type": "string", "description": "Required when commitPaths is set."},
				"skipSync":      map[string]interface{}{"type": "boolean", "description": "Deploy what is already on origin/main without touching git here."},
			},
		},
		Handler: opsDeployFromMachineHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "deploy_run",
		Description: "Run the sequential deploy in THIS box's pull-only clone (scripts/mini-deploy.sh): pull, preflight every target by attempting the real operation, then deploy the green ones one at a time. " +
			"Usually reached via deploy_from_machine rather than called directly.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"only":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"dryRun": map[string]interface{}{"type": "boolean"},
			},
		},
		Handler: opsDeployRunHandler,
	})
}
