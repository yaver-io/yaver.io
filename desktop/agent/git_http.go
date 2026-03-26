package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	osexec "os/exec"
	"strconv"
	"strings"
	"time"
)

// GitStatus represents the status of a git repository.
type GitStatus struct {
	Branch    string    `json:"branch"`
	Ahead     int       `json:"ahead"`
	Behind    int       `json:"behind"`
	Clean     bool      `json:"clean"`
	Staged    []GitFile `json:"staged"`
	Modified  []GitFile `json:"modified"`
	Untracked []GitFile `json:"untracked"`
}

// GitFile represents a file in a git status.
type GitFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "modified", "deleted", "renamed"
}

// GitCommit represents a commit in the log.
type GitCommit struct {
	Hash         string `json:"hash"`
	ShortHash    string `json:"shortHash"`
	Message      string `json:"message"`
	Author       string `json:"author"`
	Date         string `json:"date"`
	FilesChanged int    `json:"filesChanged"`
}

const gitCmdTimeout = 30 * time.Second

// runGit runs a git command in the given directory with a timeout.
func runGit(workDir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()

	cmd := osexec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func getGitWorkDir(r *http.Request, taskMgr *TaskManager) string {
	workDir := r.URL.Query().Get("workDir")
	if workDir == "" && taskMgr != nil {
		workDir = taskMgr.workDir
	}
	return workDir
}

// handleGitStatus handles GET /git/status.
func (s *HTTPServer) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	status := GitStatus{}

	// Get current branch
	if out, err := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		status.Branch = out
	}

	// Get ahead/behind
	if out, err := runGit(workDir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		parts := strings.Fields(out)
		if len(parts) == 2 {
			status.Ahead, _ = strconv.Atoi(parts[0])
			status.Behind, _ = strconv.Atoi(parts[1])
		}
	}

	// Get porcelain status
	if out, err := runGit(workDir, "status", "--porcelain"); err == nil {
		if out == "" {
			status.Clean = true
		} else {
			for _, line := range strings.Split(out, "\n") {
				if len(line) < 4 {
					continue
				}
				x := line[0]  // index status
				y := line[1]  // worktree status
				path := strings.TrimSpace(line[3:])

				// Handle renames — "R  old -> new"
				if strings.Contains(path, " -> ") {
					parts := strings.SplitN(path, " -> ", 2)
					if len(parts) == 2 {
						path = parts[1]
					}
				}

				file := GitFile{Path: path}

				// Staged changes (index)
				switch x {
				case 'A':
					file.Status = "added"
					status.Staged = append(status.Staged, file)
				case 'M':
					file.Status = "modified"
					status.Staged = append(status.Staged, file)
				case 'D':
					file.Status = "deleted"
					status.Staged = append(status.Staged, file)
				case 'R':
					file.Status = "renamed"
					status.Staged = append(status.Staged, file)
				}

				// Working tree changes
				switch y {
				case 'M':
					file.Status = "modified"
					status.Modified = append(status.Modified, file)
				case 'D':
					file.Status = "deleted"
					status.Modified = append(status.Modified, file)
				}

				// Untracked
				if x == '?' && y == '?' {
					file.Status = "added"
					status.Untracked = append(status.Untracked, file)
				}
			}
		}
	}

	jsonReply(w, http.StatusOK, status)
}

// handleGitLog handles GET /git/log.
func (s *HTTPServer) handleGitLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "20"
	}

	// Format: hash|shortHash|author|date|message
	out, err := runGit(workDir, "log", "--format=%H|%h|%an|%aI|%s", "-n", limit)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git log failed: " + out})
		return
	}

	var commits []GitCommit
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		commit := GitCommit{
			Hash:      parts[0],
			ShortHash: parts[1],
			Author:    parts[2],
			Date:      parts[3],
			Message:   parts[4],
		}

		// Get files changed count
		if fcOut, err := runGit(workDir, "diff-tree", "--no-commit-id", "--name-only", "-r", commit.Hash); err == nil {
			lines := strings.Split(strings.TrimSpace(fcOut), "\n")
			if len(lines) > 0 && lines[0] != "" {
				commit.FilesChanged = len(lines)
			}
		}

		commits = append(commits, commit)
	}

	jsonReply(w, http.StatusOK, commits)
}

// handleGitDiff handles GET /git/diff.
func (s *HTTPServer) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	file := r.URL.Query().Get("file")

	// Get both staged and unstaged diff
	args := []string{"diff", "HEAD"}
	if file != "" {
		args = append(args, "--", file)
	}

	out, err := runGit(workDir, args...)
	if err != nil {
		// Fallback: try without HEAD (for repos with no commits)
		args = []string{"diff"}
		if file != "" {
			args = append(args, "--", file)
		}
		out, err = runGit(workDir, args...)
		if err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git diff failed"})
			return
		}
	}

	jsonReply(w, http.StatusOK, map[string]string{"diff": out})
}

// handleGitBranches handles GET /git/branches.
func (s *HTTPServer) handleGitBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	out, err := runGit(workDir, "branch", "-a", "--format=%(refname:short)|%(HEAD)")
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git branch failed: " + out})
		return
	}

	type BranchInfo struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
	}

	var branches []BranchInfo
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		name := parts[0]
		current := len(parts) > 1 && strings.TrimSpace(parts[1]) == "*"
		branches = append(branches, BranchInfo{Name: name, Current: current})
	}

	jsonReply(w, http.StatusOK, branches)
}

// handleGitStash handles POST /git/stash.
func (s *HTTPServer) handleGitStash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	out, err := runGit(workDir, "stash", "push", "-m", fmt.Sprintf("yaver-stash-%d", time.Now().Unix()))
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git stash failed: " + out})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "message": out})
}

// handleGitStashPop handles POST /git/stash-pop.
func (s *HTTPServer) handleGitStashPop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	out, err := runGit(workDir, "stash", "pop")
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git stash pop failed: " + out})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "message": out})
}

// handleGitCheckout handles POST /git/checkout.
func (s *HTTPServer) handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	var req struct {
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Branch == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing branch"})
		return
	}

	out, err := runGit(workDir, "checkout", req.Branch)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git checkout failed: " + out})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "branch": req.Branch})
}

// handleGitCommit handles POST /git/commit.
func (s *HTTPServer) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	var req struct {
		Message string   `json:"message"`
		Files   []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Message == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing message"})
		return
	}

	// Stage files
	if len(req.Files) > 0 {
		args := append([]string{"add"}, req.Files...)
		if out, err := runGit(workDir, args...); err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git add failed: " + out})
			return
		}
	} else {
		// Stage all changes
		if out, err := runGit(workDir, "add", "-A"); err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git add failed: " + out})
			return
		}
	}

	out, err := runGit(workDir, "commit", "-m", req.Message)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git commit failed: " + out})
		return
	}

	// Get the new commit hash
	hash, _ := runGit(workDir, "rev-parse", "--short", "HEAD")

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "hash": hash, "message": out})
}

// handleGitPush handles POST /git/push.
func (s *HTTPServer) handleGitPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	out, err := runGit(workDir, "push")
	if err != nil {
		// Try push with --set-upstream
		branch, _ := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")
		out, err = runGit(workDir, "push", "--set-upstream", "origin", branch)
		if err != nil {
			jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git push failed: " + out})
			return
		}
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "message": out})
}

// handleGitPull handles POST /git/pull.
func (s *HTTPServer) handleGitPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	out, err := runGit(workDir, "pull")
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git pull failed: " + out})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "message": out})
}

// handleGitRevert handles POST /git/revert.
func (s *HTTPServer) handleGitRevert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	workDir := getGitWorkDir(r, s.taskMgr)
	if workDir == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing workDir"})
		return
	}

	var req struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Hash == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "missing hash"})
		return
	}

	out, err := runGit(workDir, "revert", "--no-edit", req.Hash)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "git revert failed: " + out})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true", "message": out})
}
