package main

// git_commit_push.go — POST /git/commit-push.
//
// Deterministic "stage everything, commit, push, fast-forward-rebase
// if needed" flow exposed for the Webview "Commit & Push" button.
// Mirrors what a careful human does:
//
//   1. git add -A
//   2. git commit -m <message> (no-op if nothing staged)
//   3. git push
//   4. if push rejected (non-fast-forward):
//        git fetch
//        git rebase origin/<branch>
//        if rebase clean: git push
//        if rebase has conflicts: git rebase --abort, return
//          requiresAgent=true + the list of conflicted files so the
//          caller can hand off to a coding agent.
//
// Why a single endpoint and not three calls (/git/commit + /git/push +
// /git/rebase) from the web side: the rebase-on-reject path needs to
// hold the working tree mid-flight. Doing that in one server-side
// transaction avoids the "user clicked something else between push
// and rebase" race.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type commitPushRequest struct {
	WorkDir string `json:"workDir,omitempty"`
	Message string `json:"message,omitempty"`
	// AllowAutoRebase: when true, push rejection triggers fetch+rebase
	// against origin/<branch>. Default true. Set false to surface a raw
	// non-fast-forward error to the caller.
	AllowAutoRebase *bool `json:"allowAutoRebase,omitempty"`
}

type commitPushResponse struct {
	OK             bool     `json:"ok"`
	Branch         string   `json:"branch,omitempty"`
	Hash           string   `json:"hash,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	Pushed         bool     `json:"pushed,omitempty"`
	NothingToCommit bool    `json:"nothingToCommit,omitempty"`
	Rebased        bool     `json:"rebased,omitempty"`
	// RequiresAgent: true when a rebase was needed but introduced merge
	// conflicts. The deterministic path can't resolve those — caller
	// should re-run the same intent through a coding agent (createTask).
	RequiresAgent bool     `json:"requiresAgent,omitempty"`
	Conflicts     []string `json:"conflicts,omitempty"`
	Error         string   `json:"error,omitempty"`
	Output        string   `json:"output,omitempty"`
}

func (s *HTTPServer) handleGitCommitPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	var req commitPushRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
	}

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = getGitWorkDir(r, s.taskMgr)
	}
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	allowRebase := true
	if req.AllowAutoRebase != nil {
		allowRebase = *req.AllowAutoRebase
	}

	resp := commitPushResponse{}

	branch, err := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		resp.Error = "not a git repo (or git failed): " + branch
		jsonReply(w, http.StatusInternalServerError, resp)
		return
	}
	resp.Branch = branch

	// Stage everything tracked + untracked. The web "Commit & Push" intent
	// is "ship what I'm looking at" — partial staging belongs in the Git
	// tab, not the inline button.
	if out, err := runGit(workDir, "add", "-A"); err != nil {
		resp.Error = "git add failed"
		resp.Output = out
		jsonReply(w, http.StatusInternalServerError, resp)
		return
	}
	resp.Actions = append(resp.Actions, "add -A")

	// commit. If the index is clean (only push pending), skip.
	staged, _ := runGit(workDir, "diff", "--cached", "--name-only")
	if strings.TrimSpace(staged) == "" {
		resp.NothingToCommit = true
	} else {
		message := strings.TrimSpace(req.Message)
		if message == "" {
			n := len(strings.Split(strings.TrimSpace(staged), "\n"))
			message = fmt.Sprintf("yaver: auto-commit · %d file%s · %s", n, plural(n), time.Now().Format("15:04:05"))
		}
		if out, err := runGit(workDir, "commit", "-m", message); err != nil {
			resp.Error = "git commit failed"
			resp.Output = out
			jsonReply(w, http.StatusInternalServerError, resp)
			return
		}
		resp.Actions = append(resp.Actions, "commit")
		hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")
		resp.Hash = hash
	}

	// First push attempt.
	pushOut, pushErr := runGit(workDir, "push")
	if pushErr == nil {
		resp.Actions = append(resp.Actions, "push")
		resp.Pushed = true
		resp.OK = true
		jsonReply(w, http.StatusOK, resp)
		return
	}

	// Push failed. Detect "no upstream" and retry with --set-upstream.
	if strings.Contains(pushOut, "no upstream") || strings.Contains(pushOut, "set-upstream") {
		out, err := runGit(workDir, "push", "--set-upstream", "origin", branch)
		if err == nil {
			resp.Actions = append(resp.Actions, "push --set-upstream")
			resp.Pushed = true
			resp.OK = true
			resp.Output = out
			jsonReply(w, http.StatusOK, resp)
			return
		}
		pushOut = out
		pushErr = err
	}

	// Non-fast-forward → fetch + rebase + push, if allowed.
	if allowRebase && (strings.Contains(pushOut, "rejected") || strings.Contains(pushOut, "non-fast-forward") || strings.Contains(pushOut, "fetch first")) {
		if out, err := runGit(workDir, "fetch", "origin", branch); err != nil {
			resp.Error = "git fetch failed"
			resp.Output = out
			jsonReply(w, http.StatusInternalServerError, resp)
			return
		}
		resp.Actions = append(resp.Actions, "fetch")

		rebaseOut, rebaseErr := runGit(workDir, "rebase", "origin/"+branch)
		if rebaseErr != nil {
			// Conflict — abort to leave the tree clean and signal that
			// only a coding agent can resolve this.
			conflicts := parseConflictedFiles(workDir)
			_, _ = runGit(workDir, "rebase", "--abort")
			resp.Error = "rebase produced merge conflicts"
			resp.Output = rebaseOut
			resp.RequiresAgent = true
			resp.Conflicts = conflicts
			resp.Actions = append(resp.Actions, "rebase --abort")
			jsonReply(w, http.StatusConflict, resp)
			return
		}
		resp.Actions = append(resp.Actions, "rebase origin/"+branch)
		resp.Rebased = true

		out2, err2 := runGit(workDir, "push")
		if err2 != nil {
			resp.Error = "git push (post-rebase) failed"
			resp.Output = out2
			jsonReply(w, http.StatusInternalServerError, resp)
			return
		}
		resp.Actions = append(resp.Actions, "push")
		resp.Pushed = true
		resp.OK = true
		jsonReply(w, http.StatusOK, resp)
		return
	}

	resp.Error = "git push failed"
	resp.Output = pushOut
	jsonReply(w, http.StatusInternalServerError, resp)
}

func parseConflictedFiles(workDir string) []string {
	out, err := runGit(workDir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
