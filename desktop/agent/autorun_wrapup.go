package main

// Wrap-up ("toparla") — never lose a run's work when you stop it.
//
// Why this exists. On 2026-07-18 two autoruns were running on a remote Mac
// mini. `ops autorun_stop_all` reported **zero sessions** and stopped nothing:
// both had been launched as raw tmux by another session, so the in-process
// autorunSessionManager had never heard of them. The only way to stop them was
// `tmux kill-session`, which would have thrown away every uncommitted edit the
// converged run had produced — 16 files that turned out to build clean.
//
// Two lessons, both encoded here:
//
//  1. DISCOVERY MUST NOT RELY ON THE MANAGER'S MEMORY. A run you did not start
//     is still a run. discoverAutorunTmuxSessions finds loops by their tmux
//     session names, so a daemon restart, a second session, or a hand-rolled
//     `tmux new-session` cannot make a live loop invisible.
//
//  2. STOPPING IS NOT KILLING. Wrap up first: commit what the run left behind
//     onto its own branch and push it, THEN stop. The work survives on a branch
//     for a human to review, and nothing lands on main unreviewed.

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// autorunTmuxPrefixes are the session-name shapes we treat as autorun loops.
// `yaver autorun --tmux` uses the first; humans and sibling sessions have used
// the others. Matching on prefix rather than an exact name is deliberate — the
// point is to find loops nobody registered.
var autorunTmuxPrefixes = []string{"yaver-autorun-", "autorun-"}

// autorunTmuxSuffixes catches the reverse convention (`wake-autorun`).
var autorunTmuxSuffixes = []string{"-autorun"}

// WrapupResult is what happened to one run's working tree.
type WrapupResult struct {
	Session   string `json:"session,omitempty"`
	WorkDir   string `json:"workDir"`
	Branch    string `json:"branch,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Pushed    bool   `json:"pushed"`
	FileCount int    `json:"fileCount"`
	// Clean means there was nothing to save. Not an error — the common case for
	// a run that already committed its own work.
	Clean bool   `json:"clean"`
	Error string `json:"error,omitempty"`
}

// AutorunTmuxSession is a live loop found by name, whether or not the session
// manager knows about it.
type AutorunTmuxSession struct {
	Name      string `json:"name"`
	WorkDir   string `json:"workDir,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	// Registered is false when this loop is invisible to autorun_stop_all —
	// the exact condition that made two runs unstoppable via ops.
	Registered bool `json:"registered"`
}

func looksLikeAutorunSession(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, p := range autorunTmuxPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	for _, s := range autorunTmuxSuffixes {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// discoverAutorunTmuxSessions lists autorun-shaped tmux sessions and the
// directory each is working in. `registeredDirs` (keyed by workDir) marks
// whether the in-process manager also knows it.
func discoverAutorunTmuxSessions(registeredDirs map[string]bool) ([]AutorunTmuxSession, error) {
	out, err := exec.Command(tmuxCmdName(), "list-sessions", "-F",
		"#{session_name}|#{session_path}|#{session_created}").CombinedOutput()
	if err != nil {
		txt := string(out)
		// No server and no sessions are both "nothing running", not failures.
		if strings.Contains(txt, "no server running") || strings.Contains(txt, "no sessions") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(txt))
	}
	var found []AutorunTmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "|")
		name := strings.TrimSpace(parts[0])
		if !looksLikeAutorunSession(name) {
			continue
		}
		s := AutorunTmuxSession{Name: name}
		if len(parts) > 1 {
			s.WorkDir = strings.TrimSpace(parts[1])
			s.Registered = registeredDirs[s.WorkDir]
		}
		if len(parts) > 2 {
			if secs, convErr := time.ParseDuration(strings.TrimSpace(parts[2]) + "s"); convErr == nil {
				s.CreatedAt = time.Unix(int64(secs.Seconds()), 0).UTC().Format(time.RFC3339)
			}
		}
		found = append(found, s)
	}
	return found, nil
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// wrapupWorkDir commits whatever a run left uncommitted onto a dedicated branch
// and pushes it.
//
// It NEVER pushes to main. An autorun's leftovers are unreviewed by
// construction, and this function is most often called while tearing the run
// down — the worst possible moment to land code on a shared branch. The branch
// name carries the run and the date so a human can find it later.
func wrapupWorkDir(dir string, sessionName string, push bool) WrapupResult {
	res := WrapupResult{Session: sessionName, WorkDir: dir}

	if _, err := gitIn(dir, "rev-parse", "--git-dir"); err != nil {
		res.Error = "not a git repository"
		return res
	}

	status, err := gitIn(dir, "status", "--porcelain")
	if err != nil {
		res.Error = fmt.Sprintf("git status: %v", err)
		return res
	}
	if strings.TrimSpace(status) == "" {
		res.Clean = true
		return res
	}

	// Collect paths explicitly. `git add -A` is banned in this repo: the index
	// is shared between concurrent sessions and a blanket add sweeps a
	// sibling's staged files into your commit.
	var paths []string
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		// Rename entries read "old -> new"; keep the destination.
		if idx := strings.Index(p, " -> "); idx >= 0 {
			p = p[idx+4:]
		}
		p = strings.Trim(p, "\"")
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		res.Clean = true
		return res
	}
	res.FileCount = len(paths)

	base := sessionName
	if base == "" {
		base = filepath.Base(dir)
	}
	branch := fmt.Sprintf("autorun/wrapup/%s-%s", sanitizeBranchSegment(base), time.Now().UTC().Format("20060102-150405"))

	if out, err := gitIn(dir, "checkout", "-b", branch); err != nil {
		res.Error = fmt.Sprintf("git checkout -b %s: %v: %s", branch, err, out)
		return res
	}
	res.Branch = branch

	addArgs := append([]string{"add", "--"}, paths...)
	if out, err := gitIn(dir, addArgs...); err != nil {
		res.Error = fmt.Sprintf("git add: %v: %s", err, out)
		return res
	}

	msg := fmt.Sprintf(`autorun(wrapup): preserve uncommitted work from %s

Gathered automatically when the run was stopped. NOT reviewed and NOT merged —
this branch exists so a tmux kill cannot destroy a run's output.

%d file(s) from %s.`, base, len(paths), dir)

	commitArgs := append([]string{"commit", "-q", "-m", msg, "--"}, paths...)
	if out, err := gitIn(dir, commitArgs...); err != nil {
		res.Error = fmt.Sprintf("git commit: %v: %s", err, out)
		return res
	}
	if sha, shaErr := gitIn(dir, "rev-parse", "--short", "HEAD"); shaErr == nil {
		res.Commit = sha
	}

	if push {
		remote := autorunPushRemote(dir)
		if out, err := gitIn(dir, "push", remote, "HEAD:refs/heads/"+branch); err != nil {
			// A failed push is not a failed wrap-up — the commit is safe on disk.
			res.Error = fmt.Sprintf("committed locally but push failed: %v: %s", err, out)
			return res
		}
		res.Pushed = true
	}
	return res
}

// autorunPushRemote picks the remote to push a wrap-up branch to. This repo
// calls its remote `github`; fall back to `origin` elsewhere.
func autorunPushRemote(dir string) string {
	if out, err := gitIn(dir, "remote"); err == nil {
		for _, r := range strings.Fields(out) {
			if r == "github" {
				return "github"
			}
		}
	}
	return "origin"
}

func sanitizeBranchSegment(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "run"
	}
	return out
}

// registeredAutorunSessionNames reports which working directories the
// in-process manager is driving, so discovery can flag the loops it does not
// know. Keyed by workDir rather than tmux name because autorunSession records
// the directory, not the session — and the directory is the thing that
// actually collides.
//
// A loop the manager has never heard of is exactly the case that made
// `ops autorun_stop_all` return "0 stopped" while two loops were running.
func registeredAutorunSessionNames() map[string]bool {
	dirs := make(map[string]bool)
	autorunSessions.mu.Lock()
	defer autorunSessions.mu.Unlock()
	for _, s := range autorunSessions.sessions {
		if s == nil {
			continue
		}
		if d := strings.TrimSpace(s.WorkDir); d != "" {
			dirs[d] = true
		}
	}
	return dirs
}
