package main

// Tests for devserver_pull_fast.go's rules table. Real git repos
// throughout — the failure modes we care about (porcelain output
// idiosyncrasies, ahead/behind edge cases, mid-rebase markers) only
// reproduce against actual git, not a mock.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	return dir
}

func cloneAndConfig(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "clone", "-q", remote, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
		// Default branch may be `master` on older git; force it to a known name.
		{"checkout", "-q", "-B", "main"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDecidePullBeforeBuild_NotARepo(t *testing.T) {
	dir := t.TempDir()
	d := decidePullBeforeBuild(dir, false, false)
	if d.Action != pullActionSkip {
		t.Errorf("expected skip for non-repo, got %+v", d)
	}
	if !strings.Contains(d.Reason, "git worktree") {
		t.Errorf("reason should mention git worktree; got %q", d.Reason)
	}
}

func TestDecidePullBeforeBuild_NoUpstream(t *testing.T) {
	// Plain init, no upstream configured.
	dir := initBundleCacheGitRepo(t)
	gitCommitFile(t, dir, "src/index.js", "x", "init")
	d := decidePullBeforeBuild(dir, false, false)
	if d.Action != pullActionSkip {
		t.Errorf("expected skip with no upstream, got %+v", d)
	}
	if !strings.Contains(d.Reason, "upstream") {
		t.Errorf("reason should mention upstream; got %q", d.Reason)
	}
}

func TestDecidePullBeforeBuild_CleanUpToDate(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")

	d := decidePullBeforeBuild(clone, false, false)
	if d.Action != pullActionSkip {
		t.Errorf("clean+up-to-date should skip, got %+v", d)
	}
	if !strings.Contains(d.Reason, "up to date") {
		t.Errorf("reason should mention up to date; got %q", d.Reason)
	}
}

func TestDecidePullBeforeBuild_CleanBehindUpstream(t *testing.T) {
	remote := initBareRemote(t)
	a := cloneAndConfig(t, remote)
	gitCommitFile(t, a, "src/index.js", "x", "init")
	gitInDir(t, a, "push", "-q", "-u", "origin", "main")

	// Second clone "b" — push from a, b will be behind.
	b := cloneAndConfig(t, remote)
	gitInDir(t, b, "branch", "--set-upstream-to=origin/main", "main")
	gitCommitFile(t, a, "src/feature.js", "y", "feature in a")
	gitInDir(t, a, "push", "-q")
	gitInDir(t, b, "fetch", "-q")

	d := decidePullBeforeBuild(b, false, false)
	if d.Action != pullActionFFOnly {
		t.Errorf("clean+behind should ff-only, got %+v", d)
	}
}

func TestDecidePullBeforeBuild_CleanAheadSkips(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	// Local-only commit — ahead by 1, not pushed.
	gitCommitFile(t, clone, "src/feature.js", "y", "ahead")

	d := decidePullBeforeBuild(clone, false, false)
	if d.Action != pullActionSkip {
		t.Errorf("clean+ahead should skip, got %+v", d)
	}
	if !strings.Contains(d.Reason, "ahead") {
		t.Errorf("reason should mention ahead; got %q", d.Reason)
	}
}

func TestDecidePullBeforeBuild_DirtyNoAgent(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	// Dirty: untracked file.
	if err := os.WriteFile(filepath.Join(clone, "src/scratch.js"), []byte("scratch"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	d := decidePullBeforeBuild(clone, false, false)
	if d.Action != pullActionSkip {
		t.Errorf("dirty+no-agent should skip, got %+v", d)
	}
	if !strings.Contains(d.Reason, "preserve local edits") {
		t.Errorf("reason should mention preserving local edits; got %q", d.Reason)
	}
}

func TestDecidePullBeforeBuild_DirtyWithAgentRebaseAutostash(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	if err := os.WriteFile(filepath.Join(clone, "src/scratch.js"), []byte("scratch"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	d := decidePullBeforeBuild(clone, true /*hasAgent*/, false /*no autopublish*/)
	if d.Action != pullActionRebaseAutostash {
		t.Errorf("dirty+agent+no-publish should rebase-autostash, got %+v", d)
	}
}

func TestDecidePullBeforeBuild_DirtyWithAgentAndAutoPublish(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	if err := os.WriteFile(filepath.Join(clone, "src/scratch.js"), []byte("scratch"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	d := decidePullBeforeBuild(clone, true /*hasAgent*/, true /*autopublish*/)
	if d.Action != pullActionRebasePublish {
		t.Errorf("dirty+agent+autopublish should rebase-publish, got %+v", d)
	}
}

func TestDecidePullBeforeBuild_MergeInProgressDelegates(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	// Fake mid-merge state by touching .git/MERGE_HEAD
	mergeHead := filepath.Join(clone, ".git", "MERGE_HEAD")
	if err := os.WriteFile(mergeHead, []byte("dead"), 0o644); err != nil {
		t.Fatalf("touch MERGE_HEAD: %v", err)
	}

	d := decidePullBeforeBuild(clone, false, false)
	if d.Action != pullActionDelegate {
		t.Errorf("mid-merge should delegate, got %+v", d)
	}
}

func TestDecidePullBeforeBuild_DetachedHEAD(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	first := gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitCommitFile(t, clone, "src/two.js", "y", "second")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	// Detach HEAD by checking out a commit SHA directly.
	gitInDir(t, clone, "checkout", "-q", first)

	d := decidePullBeforeBuild(clone, false, false)
	if d.Action != pullActionSkip {
		t.Errorf("detached HEAD should skip, got %+v", d)
	}
}

func TestExecutePullDecision_FFOnlyMovesHead(t *testing.T) {
	remote := initBareRemote(t)
	a := cloneAndConfig(t, remote)
	gitCommitFile(t, a, "src/index.js", "x", "init")
	gitInDir(t, a, "push", "-q", "-u", "origin", "main")

	b := cloneAndConfig(t, remote)
	gitInDir(t, b, "branch", "--set-upstream-to=origin/main", "main")
	bBefore := gitInDir(t, b, "rev-parse", "HEAD")

	// Push another commit from a so b is behind.
	newSHA := gitCommitFile(t, a, "src/feature.js", "y", "feature in a")
	gitInDir(t, a, "push", "-q")

	d := preBuildPullDecision{Action: pullActionFFOnly, Reason: "test"}
	summary, err := executePullDecision(b, d)
	if err != nil {
		t.Fatalf("executePullDecision returned error: %v summary=%s", err, summary)
	}

	bAfter := gitInDir(t, b, "rev-parse", "HEAD")
	if bAfter == bBefore {
		t.Errorf("HEAD should have moved after ff-only pull; before=%s after=%s", bBefore, bAfter)
	}
	if bAfter != newSHA {
		t.Errorf("HEAD should match remote head; want %s got %s", newSHA, bAfter)
	}
}

func TestExecutePullDecision_RebasePublishCommitsAndPushes(t *testing.T) {
	remote := initBareRemote(t)
	clone := cloneAndConfig(t, remote)
	gitCommitFile(t, clone, "src/index.js", "x", "init")
	gitInDir(t, clone, "push", "-q", "-u", "origin", "main")
	// Dirt: agent's untracked checkpoint.
	if err := os.WriteFile(filepath.Join(clone, "src/scratch.js"), []byte("scratch"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	d := preBuildPullDecision{Action: pullActionRebasePublish, Reason: "test"}
	summary, err := executePullDecision(clone, d)
	if err != nil {
		t.Fatalf("executePullDecision returned error: %v summary=%s", err, summary)
	}
	if !strings.Contains(summary, "pushed") {
		t.Errorf("summary should mention push; got %q", summary)
	}

	// Verify remote actually has the new file by cloning fresh.
	verify := cloneAndConfig(t, remote)
	if _, err := os.Stat(filepath.Join(verify, "src/scratch.js")); err != nil {
		t.Errorf("expected pushed scratch.js to land on remote; got err %v", err)
	}
}
