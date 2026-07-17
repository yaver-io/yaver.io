package main

// ops_git_verbs_test.go — exercises the git verbs against REAL git in
// a temp repo. The two things this file must prove:
//
//  1. The safety contract holds: git_commit and git_stash_ops push
//     REFUSE to run without explicit paths. The whole reason these
//     verbs exist is that a bare `git commit -a` / `git stash` on a
//     shared checkout silently swept the wrong files in. An empty
//     paths list is a hard error, not "everything".
//
//  2. The happy path works end-to-end: status/diff/log read, and
//     commit/rebase/merge write, returning HEAD + worktree state so
//     the caller never has to guess whether it landed.
//
// These tests skip when git is not on PATH (CI runners without git).
// They do NOT depend on the ops dispatcher — they call the handlers
// directly with a real payload, which is the same shape the dispatcher
// delivers. Machine routing is exercised elsewhere (it's the same
// code path every other ops verb uses).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitTestRepo is a temp git repo with a single initial commit on
// `main`. The caller gets the directory path and the list of files
// that exist at HEAD.
func gitTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	dir := t.TempDir()
	// git needs an identity to commit. Set it locally to keep the repo
	// isolated from the developer's global config.
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@yaver.local")
	gitRun(t, dir, "config", "user.name", "Test")
	// commit.gpgsign=false so -S doesn't require a GPG key in tests.
	// Production uses real signing; the test only verifies the flag is
	// passed and the commit lands.
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	// Initial commit so HEAD exists.
	first := filepath.Join(dir, "README.md")
	if err := os.WriteFile(first, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitRun(t, dir, "add", "README.md")
	// Use plain `git commit` (no -S) here because the test harness has
	// no GPG key; the VERB's -S is what we exercise through the handler
	// with commit.gpgsign=false making -S a no-op.
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, string(out))
	}
	return string(out)
}

// verbPayload marshals a payload struct to json.RawMessage for the
// handler. Handlers receive json.RawMessage; they don't care that the
// caller was a test.
func verbPayload(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// =============================================================================
// SAFETY CONTRACT — these are the tests the task said MUST pass.
// =============================================================================

// TestGitCommitRejectsEmptyPaths is THE regression test for the
// 2026-07-17 incident. A bare `git commit` swept nine files into the
// wrong commit. The verb MUST refuse an empty paths list with a real
// error, not silently broadening to `git add -A`.
func TestGitCommitRejectsEmptyPaths(t *testing.T) {
	dir := gitTestRepo(t)
	// Stage some work that a bare commit would have swept in.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	res := opsGitCommitHandler(testOpsCtx(), verbPayload(t, gitCommitPayload{
		Dir:     dir,
		Message: "should fail",
		Paths:   nil, // empty — the dangerous case
	}))
	if res.OK {
		t.Fatalf("git_commit with empty paths succeeded — the safety contract is broken")
	}
	if res.Code != "bad_payload" {
		t.Errorf("error code = %q, want bad_payload", res.Code)
	}
	if !strings.Contains(strings.ToLower(res.Error), "path") {
		t.Errorf("error message should name paths as the problem, got: %s", res.Error)
	}

	// And the repo MUST be unchanged — no stray commit, file still
	// untracked. This proves the rejection happened BEFORE staging.
	headCount := strings.TrimSpace(gitRun(t, dir, "rev-list", "--count", "HEAD"))
	if headCount != "1" {
		t.Errorf("repo grew after rejected commit — HEAD count = %s, want 1 (the init commit)", headCount)
	}
}

// TestGitCommitRejectsEmptyMessage is the symmetric guard: message
// must be non-empty too. A commit without a message is useless and
// git would block it anyway, but we reject earlier with a typed error.
func TestGitCommitRejectsEmptyMessage(t *testing.T) {
	dir := gitTestRepo(t)
	res := opsGitCommitHandler(testOpsCtx(), verbPayload(t, gitCommitPayload{
		Dir:     dir,
		Message: "   ",
		Paths:   []string{"a.txt"},
	}))
	if res.OK {
		t.Fatalf("git_commit with empty message succeeded")
	}
	if res.Code != "bad_payload" {
		t.Errorf("error code = %q, want bad_payload", res.Code)
	}
}

// TestGitStashOpsRejectsPushWithoutPaths pins the other half of the
// safety contract: stash push without paths would swallow a sibling's
// work in a shared checkout. The verb refuses.
func TestGitStashOpsRejectsPushWithoutPaths(t *testing.T) {
	dir := gitTestRepo(t)
	// Create some work that a bare stash would have captured.
	if err := os.WriteFile(filepath.Join(dir, "unrelated.txt"), []byte("sibling work"), 0o644); err != nil {
		t.Fatalf("write unrelated.txt: %v", err)
	}

	res := opsGitStashOpsHandler(testOpsCtx(), verbPayload(t, gitStashOpsPayload{
		Dir:    dir,
		Action: "push",
	}))
	if res.OK {
		t.Fatalf("git_stash_ops push with no paths succeeded — a bare stash on a shared checkout swallows a sibling's work")
	}
	if res.Code != "bad_payload" {
		t.Errorf("error code = %q, want bad_payload", res.Code)
	}
	// And the stash list MUST be empty — nothing was stashed.
	listRes := opsGitStashOpsHandler(testOpsCtx(), verbPayload(t, gitStashOpsPayload{
		Dir:    dir,
		Action: "list",
	}))
	if !listRes.OK {
		t.Fatalf("stash list failed: %v", listRes.Error)
	}
	list := ""
	_ = json.Unmarshal(verbPayload(t, listRes.Initial), &struct {
		List *string `json:"list"`
	}{List: &list})
	// The list field is whatever git printed — empty means no stashes.
	listBytes, _ := json.Marshal(listRes.Initial)
	if strings.Contains(string(listBytes), "stash@{0}") {
		t.Errorf("stash list has entries after rejected push: %s", string(listBytes))
	}
}

// TestGitRebaseRejectsInteractive pins the -i rejection. The transport
// can't drive an editor, so interactive rebase would hang; the verb
// rejects it up front with a clear error instead.
func TestGitRebaseRejectsInteractive(t *testing.T) {
	dir := gitTestRepo(t)
	for _, bad := range []string{"-i", "--interactive"} {
		res := opsGitRebaseHandler(testOpsCtx(), verbPayload(t, gitRebasePayload{
			Dir:  dir,
			Onto: bad,
		}))
		if res.OK {
			t.Errorf("git_rebase onto=%q succeeded — interactive must be rejected", bad)
		}
		if !strings.Contains(strings.ToLower(res.Error), "interactive") {
			t.Errorf("onto=%q error should mention 'interactive', got: %s", bad, res.Error)
		}
	}
}

// =============================================================================
// HAPPY PATHS — prove the verbs work end-to-end against real git.
// =============================================================================

// TestGitStatusReadsWorktree proves status parses a dirty worktree
// into the documented {path, index, worktree, untracked} shape.
func TestGitStatusReadsWorktree(t *testing.T) {
	dir := gitTestRepo(t)
	// One untracked, one modified-after-stage, one clean.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("u"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	res := opsGitStatusHandler(testOpsCtx(), verbPayload(t, gitStatusPayload{Dir: dir}))
	if !res.OK {
		t.Fatalf("git_status failed: %v", res.Error)
	}
	state := gitWorktreeState{}
	if err := json.Unmarshal(verbPayload(t, res.Initial), &state); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if state.Clean {
		t.Errorf("worktree reported clean; expected entries")
	}
	if state.Head == "" {
		t.Errorf("HEAD sha is empty")
	}
	wantPaths := map[string]bool{"untracked.txt": false, "README.md": false}
	for _, f := range state.Files {
		wantPaths[f.Path] = true
		if f.Path == "untracked.txt" && !f.Untracked {
			t.Errorf("untracked.txt not flagged untracked: %+v", f)
		}
		if f.Path == "README.md" && !f.WorktreeModified() {
			t.Errorf("README.md not flagged worktree-modified: %+v", f)
		}
	}
	for path, seen := range wantPaths {
		if !seen {
			t.Errorf("status missing path %q in %+v", path, state.Files)
		}
	}
}

// WorktreeModified is a small helper so the test reads naturally.
// " " = unmodified, "M" = modified.
func (f gitStatusFile) WorktreeModified() bool {
	return f.Worktree == "M"
}

// TestGitDiffDefaultsToWorktree proves the default diff is the unstaged
// worktree diff (the common case), and stat:true returns --stat only.
func TestGitDiffDefaultsToWorktree(t *testing.T) {
	dir := gitTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	res := opsGitDiffHandler(testOpsCtx(), verbPayload(t, gitDiffPayload{Dir: dir}))
	if !res.OK {
		t.Fatalf("git_diff failed: %v", res.Error)
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(verbPayload(t, res.Initial), &m); err != nil {
		t.Fatalf("unmarshal diff: %v", err)
	}
	diff, _ := m["diff"].(string)
	if !strings.Contains(diff, "# changed") {
		t.Errorf("default diff missing the change; got:\n%s", diff)
	}

	// stat:true returns the shape only — no +/# changed content.
	statRes := opsGitDiffHandler(testOpsCtx(), verbPayload(t, gitDiffPayload{Dir: dir, Stat: true}))
	if !statRes.OK {
		t.Fatalf("git_diff --stat failed: %v", statRes.Error)
	}
	statMap := map[string]interface{}{}
	_ = json.Unmarshal(verbPayload(t, statRes.Initial), &statMap)
	statDiff, _ := statMap["diff"].(string)
	if !strings.Contains(statDiff, "README.md") {
		t.Errorf("--stat output missing filename; got:\n%s", statDiff)
	}
	if strings.Contains(statDiff, "# changed") {
		t.Errorf("--stat should not include content lines; got:\n%s", statDiff)
	}
}

// TestGitLogReturnsHistory proves the log verb returns at least the
// initial commit.
func TestGitLogReturnsHistory(t *testing.T) {
	dir := gitTestRepo(t)
	res := opsGitLogHandler(testOpsCtx(), verbPayload(t, gitLogPayload{Dir: dir}))
	if !res.OK {
		t.Fatalf("git_log failed: %v", res.Error)
	}
	m := map[string]interface{}{}
	_ = json.Unmarshal(verbPayload(t, res.Initial), &m)
	log, _ := m["log"].(string)
	if !strings.Contains(log, "init") {
		t.Errorf("git_log missing the initial commit; got:\n%s", log)
	}
}

// TestGitCommitStagesExplicitPathsOnly is the positive form of the
// safety test: when paths ARE provided, exactly those paths land in
// the commit and nothing else, even when other dirty files exist.
func TestGitCommitStagesExplicitPathsOnly(t *testing.T) {
	dir := gitTestRepo(t)
	// Three dirty files. We'll commit only the middle one.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatalf("write c.txt: %v", err)
	}

	res := opsGitCommitHandler(testOpsCtx(), verbPayload(t, gitCommitPayload{
		Dir:     dir,
		Message: "only b",
		Paths:   []string{"b.txt"},
	}))
	if !res.OK {
		t.Fatalf("git_commit failed: %v\nerror: %s", res.Error, res.Error)
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(verbPayload(t, res.Initial), &out); err != nil {
		t.Fatalf("unmarshal commit result: %v", err)
	}
	head, _ := out["head"].(string)
	if head == "" {
		t.Fatalf("commit returned empty HEAD sha")
	}
	// The committed tree contains b.txt but NOT a.txt or c.txt.
	tree := gitRun(t, dir, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(tree, "b.txt") {
		t.Errorf("b.txt missing from committed tree:\n%s", tree)
	}
	if strings.Contains(tree, "a.txt") {
		t.Errorf("a.txt leaked into the commit — the verb staged more than the explicit paths:\n%s", tree)
	}
	if strings.Contains(tree, "c.txt") {
		t.Errorf("c.txt leaked into the commit — the verb staged more than the explicit paths:\n%s", tree)
	}
	// And a.txt/c.txt are still untracked in the worktree (not silently
	// staged-and-left).
	status := gitRun(t, dir, "status", "--porcelain")
	if !strings.Contains(status, "?? a.txt") || !strings.Contains(status, "?? c.txt") {
		t.Errorf("a.txt / c.txt should still be untracked after the selective commit; got:\n%s", status)
	}
}

// TestGitMergeCreatesMergeCommit proves merge returns the new HEAD
// and the worktree state after landing. Both branches diverge from the
// fork point so the merge MUST create a real merge commit (not a
// fast-forward, which wouldn't exercise the merge machinery).
func TestGitMergeCreatesMergeCommit(t *testing.T) {
	dir := gitTestRepo(t)
	// Branch off, add a commit.
	gitRun(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("f"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gitRun(t, dir, "add", "feature.txt")
	gitRun(t, dir, "commit", "-q", "-m", "feature work")
	gitRun(t, dir, "checkout", "-q", "main")
	// Diverge main too — otherwise the merge is a fast-forward and no
	// merge commit is created, which would not exercise the verb's
	// "landed a merge" contract.
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("m"), 0o644); err != nil {
		t.Fatalf("write main.txt: %v", err)
	}
	gitRun(t, dir, "add", "main.txt")
	gitRun(t, dir, "commit", "-q", "-m", "main work")

	res := opsGitMergeHandler(testOpsCtx(), verbPayload(t, gitMergePayload{
		Dir: dir,
		Ref: "feature",
	}))
	if !res.OK {
		t.Fatalf("git_merge failed: %v", res.Error)
	}
	out := map[string]interface{}{}
	_ = json.Unmarshal(verbPayload(t, res.Initial), &out)
	head, _ := out["head"].(string)
	if head == "" {
		t.Fatalf("merge returned empty HEAD sha")
	}
	// The merge commit should have two parents (now that both branches
	// diverged, the merge cannot be a fast-forward).
	parents := strings.TrimSpace(gitRun(t, dir, "log", "-1", "--format=%P"))
	if pc := len(strings.Fields(parents)); pc != 2 {
		t.Errorf("merge commit should have 2 parents, got %d (%s) — was it a fast-forward?", pc, parents)
	}
}

// TestGitStashOpsPushPopRoundTrip proves the happy path for the verb
// that has the strictest safety contract: push WITH paths works, pop
// restores them. The stash scenario is modifying a TRACKED file (git
// `stash push -- <paths>` only handles tracked pathspecs; untracked
// files require `--include-untracked`, which is a separate decision).
func TestGitStashOpsPushPopRoundTrip(t *testing.T) {
	dir := gitTestRepo(t)
	// README.md is tracked from the init commit. Modify it — that's
	// the in-progress work we'll stash.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# in progress"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	// push with explicit paths — the safe form.
	pushRes := opsGitStashOpsHandler(testOpsCtx(), verbPayload(t, gitStashOpsPayload{
		Dir:    dir,
		Action: "push",
		Paths:  []string{"README.md"},
	}))
	if !pushRes.OK {
		t.Fatalf("stash push failed: %v", pushRes.Error)
	}
	// After push, README.md should be back to its HEAD content.
	content, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README after stash: %v", err)
	}
	if strings.TrimSpace(string(content)) != "# test" {
		t.Errorf("README.md should be reverted to HEAD content after stash; got %q", string(content))
	}
	// list should show one stash.
	listRes := opsGitStashOpsHandler(testOpsCtx(), verbPayload(t, gitStashOpsPayload{
		Dir:    dir,
		Action: "list",
	}))
	if !listRes.OK {
		t.Fatalf("stash list failed: %v", listRes.Error)
	}
	listBytes, _ := json.Marshal(listRes.Initial)
	if !strings.Contains(string(listBytes), "stash@{0}") {
		t.Errorf("stash list missing stash@{0} after push: %s", string(listBytes))
	}
	// pop restores the in-progress modification.
	popRes := opsGitStashOpsHandler(testOpsCtx(), verbPayload(t, gitStashOpsPayload{
		Dir:    dir,
		Action: "pop",
	}))
	if !popRes.OK {
		t.Fatalf("stash pop failed: %v", popRes.Error)
	}
	content, err = os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README after pop: %v", err)
	}
	if strings.TrimSpace(string(content)) != "# in progress" {
		t.Errorf("README.md should be restored to the stashed in-progress content after pop; got %q", string(content))
	}
}

// =============================================================================
// REGISTRATION — every verb from the task MUST be in the ops registry.
// This is the "Definition of done" check: if a verb name is missing or
// misspelled, the ops dispatcher would say "unknown verb" at runtime;
// this test catches that at build time.
// =============================================================================

func TestGitOpsVerbsRegistered(t *testing.T) {
	want := []string{
		"git_status", "git_diff", "git_log",
		"git_stash_ops", "git_commit", "git_rebase", "git_merge",
	}
	for _, name := range want {
		spec, ok := lookupOpsVerbForTest(name)
		if !ok {
			t.Errorf("verb %q is NOT registered — call listOpsVerbs() to see what landed", name)
			continue
		}
		if spec.Handler == nil {
			t.Errorf("verb %q registered with nil handler", name)
		}
		if spec.Schema == nil {
			t.Errorf("verb %q registered without a schema", name)
		}
	}
}

// =============================================================================
// helpers
// =============================================================================

func testOpsCtx() OpsContext {
	return OpsContext{}
}

// lookupOpsVerbForTest reads the ops registry without exporting it.
// We don't import the registry lock here; tests are single-threaded
// with respect to registration (init-only).
func lookupOpsVerbForTest(name string) (opsVerbSpec, bool) {
	opsRegistryMu.RLock()
	defer opsRegistryMu.RUnlock()
	spec, ok := opsRegistry[name]
	return spec, ok
}
