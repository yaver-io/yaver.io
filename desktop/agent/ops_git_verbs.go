package main

// ops_git_verbs.go — the ops-verb surface for git read AND write verbs.
//
// This file exists because the old git MCP tools (git_stash,
// git_log_advanced, git_branches, git_reflog, git_blame_file) were
// plain MCP tools keyed on `directory` and nothing else: they could
// not reach a second machine. The ops framework already solved that —
// OpsContext carries Machine (local | auto | deviceId) and routes
// every verb through it for free. These verbs inherit that routing,
// so an agent driving a remote box can diff, stash, commit, rebase,
// and merge there exactly as it would locally.
//
// SAFETY CONTRACT (born from a real incident on 2026-07-17):
// Two boxes were running parallel sessions in one checkout. A bare
// `git commit -a` swept nine files into the wrong commit. Every write
// verb here stages EXPLICIT paths only — never `git add -A`, never
// `git commit -a`, never `git stash` without paths. The verbs that
// take `paths` reject an empty list rather than silently broadening.
//
// PUSH IS DELIBERATELY NOT HERE. git_push_creds (mcp_tools.go) and the
// ops verb git_push (ops_git.go) handle credential distribution; the
// act of pushing is a separate blast-radius decision and gets its own
// verb. Do not add git_push here.
//
// This file does NOT touch ops_git.go — that file is the Phase-D3
// credential-push security contract (owned-only, self-excluded, tokens
// never reach Convex). These verbs are a sibling, not a rewrite.

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

// gitVerbTimeout caps every git invocation. Git is fast on local
// repos but a status on a monorepo over a slow filesystem can stall;
// we'd rather return a typed timeout than hang the ops dispatcher.
const gitVerbTimeout = 60 * time.Second

// gitBin resolves the git binary the same way tmux does — through
// DiscoverBinary, so we exec an absolute path rather than trusting
// $PATH. This matters because the agent augments $PATH at startup (see
// augmentAgentPATH in main.go), but a subprocess inherits the augmented
// env only if we pass it down; resolving to an absolute path is the
// robust choice either way.
func gitBin() string {
	return DiscoverBinary("git")
}

// gitCmdName returns the argv[0] for exec, falling back to the bare
// name so the caller still sees the familiar "executable file not
// found" error rather than trying to exec "".
func gitCmdName() string {
	if p := gitBin(); p != "" {
		return p
	}
	return "git"
}

// gitVerbResult is the stdout/stderr/exit from a single git invocation.
type gitVerbResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// runGitVerb executes git with the given args in dir, resolving the
// binary via DiscoverBinary. It uses exec.Command (arg array, not
// shell) so a caller-supplied ref name like "--upload-pack=…" can never
// escape into the shell — it just becomes a bad ref and git rejects it.
//
// The dir is set via cmd.Dir, which means git inherits the working
// directory naturally; we don't prepend `-C dir` because the two are
// equivalent for our purposes and cmd.Dir is the more common idiom.
func runGitVerb(ctx context.Context, dir string, args ...string) gitVerbResult {
	if dir == "" {
		dir = "."
	}
	cctx, cancel := context.WithTimeout(ctx, gitVerbTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, gitCmdName(), args...)
	cmd.Dir = dir
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	// Inherit the environment so GPG (for -S signing), SSH agent, and
	// the augmented $PATH all reach the subprocess.
	cmd.Env = os.Environ()

	err := cmd.Run()
	res := gitVerbResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
		res.Err = err
	} else if err != nil {
		res.Err = err
		// Non-exit error (timeout, exec failure). Leave ExitCode 0 — the
		// presence of Err is the signal; callers check Err first.
	} else {
		res.ExitCode = 0
	}
	return res
}

// gitVerbPayload is the common optional working directory. Every git
// verb accepts `dir` (default: the agent's project dir / cwd).
type gitVerbPayload struct {
	Dir string `json:"dir,omitempty"`
}

// resolveGitDir resolves the dir to an absolute path, defaulting to
// the agent's cwd when empty. Returns an error if the directory does
// not exist (so the caller gets a clear "bad_payload" instead of a
// git error about a missing repo).
func resolveGitDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("could not resolve working directory: %w", err)
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("could not resolve absolute path %q: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("directory %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", abs)
	}
	return abs, nil
}

// gitHeadSha returns the current HEAD sha for dir, or "" if it can't
// (not a repo, detached with no commits, etc.).
func gitHeadSha(ctx context.Context, dir string) string {
	res := runGitVerb(ctx, dir, "rev-parse", "HEAD")
	if res.Err != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// gitBranchName returns the current branch name for dir, or "HEAD"
// (detached) on failure. Used in status output so a caller sees which
// branch a write verb landed on.
func gitBranchName(ctx context.Context, dir string) string {
	res := runGitVerb(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if res.Err != nil {
		return ""
	}
	name := strings.TrimSpace(res.Stdout)
	if name == "" {
		return ""
	}
	return name
}

// gitStatusFile is one entry from `git status --porcelain=v1`.
//
// Index and Worktree hold the two-character porcelain codes (X = index,
// Y = worktree). Untracked files come back as Index="?" Worktree="?".
// The raw porcelain codes are preserved verbatim so a caller can branch
// on them without us translating-and-losing information; the boolean
// convenience fields handle the common cases.
type gitStatusFile struct {
	Path      string `json:"path"`
	Index     string `json:"index"`     // porcelain X code, 1 char (or "?")
	Worktree  string `json:"worktree"`  // porcelain Y code, 1 char (or "?")
	Untracked bool   `json:"untracked"`
	Staged    bool   `json:"staged"`    // X is one of A/M/D/R/C
	Renamed   bool   `json:"renamed"`   // X is R or C
	OldPath   string `json:"oldPath,omitempty"` // present on rename/copy
}

// gitWorktreeState is the post-verb snapshot every write verb returns.
// It is deliberately the same shape as git_status's `files` so a
// caller writes one parser, not two.
type gitWorktreeState struct {
	Branch string           `json:"branch,omitempty"`
	Head   string           `json:"head"`
	Clean  bool             `json:"clean"`
	Files  []gitStatusFile  `json:"files"`
	// Ahead/Behind vs upstream — best-effort, omitted when no upstream.
	Ahead  int `json:"ahead,omitempty"`
	Behind int `json:"behind,omitempty"`
}

// readWorktreeState builds the post-verb snapshot. It's the tail of
// every write verb: HEAD sha + porcelain status + branch, so the
// caller never has to issue a separate status to know it landed.
func readWorktreeState(ctx context.Context, dir string) gitWorktreeState {
	state := gitWorktreeState{
		Head:   gitHeadSha(ctx, dir),
		Branch: gitBranchName(ctx, dir),
	}
	res := runGitVerb(ctx, dir, "status", "--porcelain=v1")
	if res.Err != nil {
		// Not a repo or git missing — leave Clean=false, Files empty.
		return state
	}
	state.Files = parsePorcelainV1(res.Stdout)
	state.Clean = len(state.Files) == 0

	// Ahead/behind — only meaningful when an upstream exists.
	if ab := runGitVerb(ctx, dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); ab.Err == nil {
		parts := strings.Fields(strings.TrimSpace(ab.Stdout))
		if len(parts) == 2 {
			fmt.Sscanf(parts[0], "%d", &state.Ahead)
			fmt.Sscanf(parts[1], "%d", &state.Behind)
		}
	}
	return state
}

// parsePorcelainV1 turns `git status --porcelain=v1` output into a
// list of file entries. It handles the rename arrow ("R  old -> new")
// and the untracked pair ("??"). It does NOT handle quoted paths
// (git's default quoting for paths with spaces); callers needing that
// fidelity should use porcelain -z which this verb doesn't expose.
func parsePorcelainV1(output string) []gitStatusFile {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	files := make([]gitStatusFile, 0, len(lines))
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		x := line[0]
		y := line[1]
		// Porcelain v1 format: XY<space>path  (or XY<space>old -> new)
		raw := line[3:]
		entry := gitStatusFile{
			Index:    string(x),
			Worktree: string(y),
		}
		if arrow := strings.Index(raw, " -> "); arrow >= 0 {
			entry.OldPath = raw[:arrow]
			entry.Path = raw[arrow+4:]
			entry.Renamed = true
		} else {
			entry.Path = raw
		}
		// Strip surrounding double-quotes that git adds to paths with
		// special characters under core.quotePath=true (the default).
		// A path that genuinely starts and ends with a quote char is
		// pathological and rare enough that this is the right trade.
		if len(entry.Path) >= 2 && entry.Path[0] == '"' && entry.Path[len(entry.Path)-1] == '"' {
			entry.Path = entry.Path[1 : len(entry.Path)-1]
		}
		if x == '?' && y == '?' {
			entry.Untracked = true
		}
		switch x {
		case 'A', 'M', 'D', 'R', 'C':
			entry.Staged = true
		}
		files = append(files, entry)
	}
	return files
}

// =============================================================================
// READ VERBS
// =============================================================================

type gitStatusPayload struct {
	Dir string `json:"dir,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_status",
		Description: "Porcelain v1 working-tree status, parsed into per-file {path, index, worktree, untracked} entries plus branch and ahead/behind. Read-only. Machine-routable via ops.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dir": map[string]interface{}{"type": "string", "description": "Working directory. Defaults to the agent's CWD."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitStatusHandler,
	})
}

func opsGitStatusHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitStatusPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()
	state := readWorktreeState(ctx, dir)
	return OpsResult{OK: true, Initial: state}
}

type gitDiffPayload struct {
	Dir    string   `json:"dir,omitempty"`
	Ref    string   `json:"ref,omitempty"`    // diff against this ref (default: working tree)
	Staged bool     `json:"staged,omitempty"` // --staged (diff cached vs HEAD); overrides Ref
	Paths  []string `json:"paths,omitempty"`  // limit to these paths
	Stat   bool     `json:"stat,omitempty"`   // --stat only (shape, not content)
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_diff",
		Description: "The real git diff. Defaults to the unstaged worktree diff; pass staged:true for cached-vs-HEAD, or ref:<ref> for working-tree-vs-ref. stat:true returns --stat only (the shape of the change) because a full diff of a big change is enormous and the caller usually wants the shape first.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dir":    map[string]interface{}{"type": "string"},
				"ref":    map[string]interface{}{"type": "string", "description": "Diff working tree against this ref. Ignored when staged:true."},
				"staged": map[string]interface{}{"type": "boolean", "description": "Diff cached (staged) changes vs HEAD. Mutually exclusive with ref."},
				"paths":  map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"stat":   map[string]interface{}{"type": "boolean", "description": "Return --stat only."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitDiffHandler,
	})
}

func opsGitDiffHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitDiffPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	args := []string{"diff"}
	if p.Staged {
		args = append(args, "--staged")
	} else if ref := strings.TrimSpace(p.Ref); ref != "" {
		// Append the ref as a positional; we'll add `-- <paths>` after.
		// Using it bare is safe because exec.Command doesn't invoke a
		// shell — a ref named "--output=evil" is just a bad ref to git.
		args = append(args, ref)
	}
	if p.Stat {
		args = append(args, "--stat")
	}
	if len(p.Paths) > 0 {
		args = append(args, "--")
		args = append(args, p.Paths...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()
	res := runGitVerb(ctx, dir, args...)
	if res.Err != nil {
		return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git diff exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: map[string]interface{}{"stdout": res.Stdout, "stderr": res.Stderr, "exitCode": res.ExitCode}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"diff": res.Stdout, "stat": p.Stat}}
}

type gitLogPayload struct {
	Dir   string   `json:"dir,omitempty"`
	Limit int      `json:"limit,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_log",
		Description: "Thin git log. Defaults to oneline, -20. Pass limit + paths for a focused view. This verb intentionally does NOT duplicate git_log_advanced (the MCP tool) — that tool covers author/since/until filters; this one is the machine-routable quick form via ops.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dir":   map[string]interface{}{"type": "string"},
				"limit": map[string]interface{}{"type": "integer", "description": "Max commits to return (default 20)."},
				"paths": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Limit to commits touching these paths."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitLogHandler,
	})
}

func opsGitLogHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitLogPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	args := []string{"log", "--oneline", fmt.Sprintf("-%d", limit)}
	if len(p.Paths) > 0 {
		args = append(args, "--")
		args = append(args, p.Paths...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()
	res := runGitVerb(ctx, dir, args...)
	if res.Err != nil {
		return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git log exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: map[string]interface{}{"stdout": res.Stdout, "stderr": res.Stderr, "exitCode": res.ExitCode}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"log": res.Stdout}}
}

// =============================================================================
// WRITE VERBS — every one returns {head, status} (HEAD sha + worktree
// state) so the caller never has to guess whether it landed.
// =============================================================================

type gitStashOpsPayload struct {
	Dir     string   `json:"dir,omitempty"`
	Action  string   `json:"action"` // list | push | pop | apply | drop
	Message string   `json:"message,omitempty"`
	Paths   []string `json:"paths,omitempty"` // REQUIRED for push — see safety contract
	Index   int      `json:"index,omitempty"` // stash@{N} for pop/apply/drop
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_stash_ops",
		Description: "Stash control that CANNOT swallow the whole tree by accident. push REQUIRES explicit paths — a bare `git stash` on a shared checkout eats a sibling's live work (the 2026-07-17 incident). list/pop/apply/drop take an optional index (default 0). Every action returns the resulting HEAD sha + worktree state.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"action"},
			"properties": map[string]interface{}{
				"dir":     map[string]interface{}{"type": "string"},
				"action":  map[string]interface{}{"type": "string", "enum": []string{"list", "push", "pop", "apply", "drop"}},
				"message": map[string]interface{}{"type": "string", "description": "Stash message (push only)."},
				"paths":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Explicit paths to stash (REQUIRED for push — refuses to stash the whole tree without them)."},
				"index":   map[string]interface{}{"type": "integer", "description": "Stash index for pop/apply/drop (default 0)."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitStashOpsHandler,
	})
}

func opsGitStashOpsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitStashOpsPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()

	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "list":
		res := runGitVerb(ctx, dir, "stash", "list")
		if res.Err != nil {
			return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git stash list exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: map[string]interface{}{"stdout": res.Stdout, "stderr": res.Stderr}}
		}
		// list doesn't change the worktree, so return HEAD + list.
		return OpsResult{OK: true, Initial: map[string]interface{}{"list": res.Stdout, "head": gitHeadSha(ctx, dir)}}

	case "push":
		// SAFETY: refuse to stash without explicit paths. The incident
		// was a bare `git stash` silently capturing a sibling's edits
		// in a shared checkout. If the caller wants everything, they say
		// so by naming the paths — there's no implicit wildcard here.
		if len(p.Paths) == 0 {
			return OpsResult{OK: false, Code: "bad_payload", Error: "git_stash_ops push requires explicit paths — a bare stash on a shared checkout swallows a sibling's work. Pass the files you want stashed."}
		}
		args := []string{"stash", "push"}
		if msg := strings.TrimSpace(p.Message); msg != "" {
			args = append(args, "-m", msg)
		}
		args = append(args, "--")
		args = append(args, p.Paths...)
		res := runGitVerb(ctx, dir, args...)
		if res.Err != nil {
			return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git stash push exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: map[string]interface{}{"stdout": res.Stdout, "stderr": res.Stderr, "status": readWorktreeState(ctx, dir)}}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"output": res.Stdout, "status": readWorktreeState(ctx, dir)}}

	case "pop", "apply", "drop":
		target := fmt.Sprintf("stash@{%d}", p.Index)
		res := runGitVerb(ctx, dir, "stash", strings.ToLower(strings.TrimSpace(p.Action)), target)
		if res.Err != nil {
			return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git stash %s exited %d: %s", p.Action, res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: map[string]interface{}{"stdout": res.Stdout, "stderr": res.Stderr, "status": readWorktreeState(ctx, dir)}}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"output": res.Stdout, "status": readWorktreeState(ctx, dir)}}
	}
	return OpsResult{OK: false, Code: "bad_payload", Error: "unknown action: " + p.Action + " (expected list|push|pop|apply|drop)"}
}

type gitCommitPayload struct {
	Dir    string   `json:"dir,omitempty"`
	Message string  `json:"message"`
	Paths  []string `json:"paths"` // REQUIRED, non-empty
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_commit",
		Description: "Stage EXPLICIT paths and commit them, signed. paths is required and non-empty — an empty list is rejected with a real error, NOT silently broadened to `git add -A` (the 2026-07-17 incident: a bare commit swept nine files into the wrong commit). Commits with -S because this repo signs. Returns the new HEAD sha + worktree state.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"message", "paths"},
			"properties": map[string]interface{}{
				"dir":     map[string]interface{}{"type": "string"},
				"message": map[string]interface{}{"type": "string", "description": "Commit message. Required."},
				"paths":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "minItems": 1, "description": "Explicit paths to stage. REQUIRED and non-empty — the verb refuses to commit without them."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitCommitHandler,
	})
}

func opsGitCommitHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitCommitPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	if strings.TrimSpace(p.Message) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "git_commit requires a non-empty message"}
	}
	// SAFETY CONTRACT: paths required and non-empty. The whole reason
	// this verb exists is that `git commit -a` / `git add -A` silently
	// swept unrelated work into a commit. An empty paths list is NOT
	// "commit nothing" (useless) and is NOT "commit everything"
	// (dangerous) — it's a hard error, named so the caller knows why.
	if len(p.Paths) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "git_commit requires explicit paths (non-empty). A bare commit on a shared checkout sweeps unrelated work into the wrong commit — name the files you want committed."}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()

	// Stage explicit paths. `-- ` separates paths from pathspecs so a
	// file literally named "--amend" can't masquerade as a flag.
	addArgs := append([]string{"add", "--"}, p.Paths...)
	addRes := runGitVerb(ctx, dir, addArgs...)
	if addRes.Err != nil {
		return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git add exited %d: %s", addRes.ExitCode, strings.TrimSpace(addRes.Stderr)), Initial: map[string]interface{}{"stdout": addRes.Stdout, "stderr": addRes.Stderr, "status": readWorktreeState(ctx, dir)}}
	}

	// Commit signed. -S forces signing even if commit.gpgsign is unset;
	// this repo signs, and a signed history is the invariant we keep.
	commitRes := runGitVerb(ctx, dir, "commit", "-S", "-m", p.Message)
	if commitRes.Err != nil {
		return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git commit exited %d: %s", commitRes.ExitCode, strings.TrimSpace(commitRes.Stderr)), Initial: map[string]interface{}{"stdout": commitRes.Stdout, "stderr": commitRes.Stderr, "status": readWorktreeState(ctx, dir)}}
	}

	state := readWorktreeState(ctx, dir)
	return OpsResult{OK: true, Initial: map[string]interface{}{"head": state.Head, "status": state, "output": strings.TrimSpace(commitRes.Stdout)}}
}

type gitRebasePayload struct {
	Dir       string `json:"dir,omitempty"`
	Onto      string `json:"onto,omitempty"`      // target branch/tip to rebase onto
	Upstream  string `json:"upstream,omitempty"`  // upstream ref (with onto, produces `rebase <upstream> <onto>`)
	Abort     bool   `json:"abort,omitempty"`     // git rebase --abort
	Continue  bool   `json:"continue,omitempty"`  // git rebase --continue
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_rebase",
		Description: "Non-interactive rebase. Interactive (-i) is impossible over this transport and is rejected with a clear error. abort/continue resolve an in-progress rebase. On failure (merge conflict), the conflicted paths are reported so a remote caller knows what to fix rather than seeing only 'exit 1'.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"dir":      map[string]interface{}{"type": "string"},
				"onto":     map[string]interface{}{"type": "string", "description": "Target tip to rebase onto."},
				"upstream": map[string]interface{}{"type": "string", "description": "Upstream ref; with onto, produces `rebase <upstream> <onto>`."},
				"abort":    map[string]interface{}{"type": "boolean", "description": "Abort an in-progress rebase."},
				"continue": map[string]interface{}{"type": "boolean", "description": "Continue an in-progress rebase after resolving conflicts."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitRebaseHandler,
	})
}

func opsGitRebaseHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitRebasePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()

	// Reject interactive. Our transport can't drive an editor, and a
	// rebase that tries to open one hangs until the timeout. The args
	// below are arg-array passed (no shell), so `-i` only arrives if a
	// caller explicitly packages it into onto/upstream — this check is
	// belt-and-suspenders so the error is clear rather than a hang.
	for _, s := range []string{p.Onto, p.Upstream} {
		if strings.TrimSpace(s) == "-i" || strings.Contains(s, " -i ") || s == "--interactive" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "interactive rebase (-i) is not supported over this transport — the editor cannot be driven remotely. Use non-interactive rebase, or run -i locally."}
		}
	}

	var args []string
	switch {
	case p.Abort:
		args = []string{"rebase", "--abort"}
	case p.Continue:
		args = []string{"rebase", "--continue"}
	default:
		if strings.TrimSpace(p.Onto) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "git_rebase requires onto (or abort:true / continue:true)"}
		}
		args = []string{"rebase"}
		if up := strings.TrimSpace(p.Upstream); up != "" {
			args = append(args, up)
		}
		args = append(args, strings.TrimSpace(p.Onto))
	}

	res := runGitVerb(ctx, dir, args...)
	state := readWorktreeState(ctx, dir)
	if res.Err != nil {
		// Report the conflicted paths so the caller knows what to fix.
		// `git diff --name-only --diff-filter=U` lists unmerged paths.
		conflicts := []string{}
		if c := runGitVerb(ctx, dir, "diff", "--name-only", "--diff-filter=U"); c.Err == nil {
			for _, line := range strings.Split(strings.TrimSpace(c.Stdout), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					conflicts = append(conflicts, line)
				}
			}
		}
		out := map[string]interface{}{
			"stdout":   res.Stdout,
			"stderr":   res.Stderr,
			"exitCode": res.ExitCode,
			"status":   state,
		}
		if len(conflicts) > 0 {
			out["conflicts"] = conflicts
			return OpsResult{OK: false, Code: "rebase_conflicts", Error: fmt.Sprintf("rebase stopped with %d conflicted path(s): %s", len(conflicts), strings.Join(conflicts, ", ")), Initial: out}
		}
		return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git rebase exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: out}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"output": strings.TrimSpace(res.Stdout), "head": state.Head, "status": state}}
}

type gitMergePayload struct {
	Dir   string `json:"dir,omitempty"`
	Ref   string `json:"ref"`
	Abort bool   `json:"abort,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_merge",
		Description: "Merge a ref with --no-edit. Never --squash by default (a squash merge rewrites history shape and is a separate decision). abort cancels an in-progress merge. On conflict, the conflicted paths are reported. Returns the resulting HEAD sha + worktree state.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"ref"},
			"properties": map[string]interface{}{
				"dir":   map[string]interface{}{"type": "string"},
				"ref":   map[string]interface{}{"type": "string", "description": "Ref (branch/tag/sha) to merge. Required unless abort:true."},
				"abort": map[string]interface{}{"type": "boolean", "description": "Abort an in-progress merge (ref is ignored)."},
			},
			"additionalProperties": false,
		},
		Handler: opsGitMergeHandler,
	})
}

func opsGitMergeHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p gitMergePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
	}
	dir, derr := resolveGitDir(p.Dir)
	if derr != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: derr.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitVerbTimeout)
	defer cancel()

	var args []string
	if p.Abort {
		args = []string{"merge", "--abort"}
	} else {
		if strings.TrimSpace(p.Ref) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "git_merge requires ref (or abort:true)"}
		}
		// --no-edit: don't open an editor for the merge commit message.
		// No --squash: see verb description.
		args = []string{"merge", "--no-edit", strings.TrimSpace(p.Ref)}
	}

	res := runGitVerb(ctx, dir, args...)
	state := readWorktreeState(ctx, dir)
	if res.Err != nil {
		conflicts := []string{}
		if c := runGitVerb(ctx, dir, "diff", "--name-only", "--diff-filter=U"); c.Err == nil {
			for _, line := range strings.Split(strings.TrimSpace(c.Stdout), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					conflicts = append(conflicts, line)
				}
			}
		}
		out := map[string]interface{}{
			"stdout":   res.Stdout,
			"stderr":   res.Stderr,
			"exitCode": res.ExitCode,
			"status":   state,
		}
		if len(conflicts) > 0 {
			out["conflicts"] = conflicts
			return OpsResult{OK: false, Code: "merge_conflicts", Error: fmt.Sprintf("merge stopped with %d conflicted path(s): %s", len(conflicts), strings.Join(conflicts, ", ")), Initial: out}
		}
		return OpsResult{OK: false, Code: "git_failed", Error: fmt.Sprintf("git merge exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr)), Initial: out}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"output": strings.TrimSpace(res.Stdout), "head": state.Head, "status": state}}
}
